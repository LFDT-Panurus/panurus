/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fsc

import (
	"context"
	"encoding/json"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/services/endorser"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

const (
	TransientTMSIDKey        = "tmsID"
	TransientTokenRequestKey = "token_request"

	ChaincodeVersion = "1.0"
	InvokeFunction   = "invoke"
	SetupFunction    = "setup"
)

// Request models an in-flight request being processed by ResponderView. It carries the
// fields common to every protocol handled by ResponderView plus the fields that are
// specific to a given responderBehaviour; only the fields relevant to the selected
// behaviour are populated.
type Request struct {
	Tx     *endorser.Transaction
	Rws    *fabric.RWSet
	TMSID  token2.TMSID
	Anchor string
	Tms    *token2.ManagementService

	// approval-specific fields, populated by approvalBehaviour
	RequestRaw       []byte
	Actions          []any
	Meta             map[string][]byte
	ApprovalMetadata map[string][]byte
	PublicParamsHash tdriver.PPHash

	// setup-specific fields, populated by setupBehaviour
	PublicParamsRaw []byte
}

// responderBehaviour models the protocol-specific steps of ResponderView. ResponderView
// itself implements the phases that are common to every protocol (receiving the transaction,
// validating the proposal, and endorsing it); a responderBehaviour is selected by chaincode
// function name and supplies the remaining, protocol-specific steps.
type responderBehaviour interface {
	// function returns the chaincode function name this behaviour handles.
	function() string
	// checkTransientCount validates the number of transient fields carried by tx.
	checkTransientCount(tx *endorser.Transaction) error
	// extractTransient extracts the behaviour-specific transient data into request. It runs
	// after the common TMS ID transient has been parsed.
	extractTransient(tx *endorser.Transaction, request *Request) error
	// validate performs behaviour-specific validation of the request, after validateProposal
	// has already validated the proposal itself. It is responsible for looking up the TMS for
	// request.TMSID (via request.Tms) and deciding whether an absent TMS is a failure.
	validate(ctx view.Context, request *Request) error
	// translate writes the behaviour-specific actions into the request's RWSet.
	translate(ctx context.Context, request *Request) error
}

// ResponderView is the responder of the FSC endorsement protocols. It receives a
// transaction, validates the proposal and the transaction content, and endorses it. The
// protocol-specific steps (transient parsing, content validation, and translation into
// RWSet writes) are delegated to a responderBehaviour selected by chaincode function name.
type ResponderView struct {
	endorserService EndorserService
	channelProvider ChannelProvider
	behaviours      map[string]responderBehaviour
}

func newResponderView(
	endorserService EndorserService,
	channelProvider ChannelProvider,
	behaviours ...responderBehaviour,
) *ResponderView {
	m := make(map[string]responderBehaviour, len(behaviours))
	for _, behaviour := range behaviours {
		m[behaviour.function()] = behaviour
	}

	return &ResponderView{
		endorserService: endorserService,
		channelProvider: channelProvider,
		behaviours:      m,
	}
}

// NewResponderView returns a new instance of ResponderView configured to handle both
// protocols supported by this package: token request approval and public parameters
// setup/update. The chaincode function name carried by the incoming transaction selects
// which behaviour handles it.
func NewResponderView(
	keyTranslator translator.KeyTranslator,
	getTranslator TranslatorProviderFunc,
	endorserService EndorserService,
	tokenManagementSystemProvider TokenManagementSystemProvider,
	storageProvider StorageProvider,
	channelProvider ChannelProvider,
	ppValidator PublicParamsValidator,
) *ResponderView {
	return newResponderView(
		endorserService,
		channelProvider,
		&approvalBehaviour{
			keyTranslator:                 keyTranslator,
			getTranslator:                 getTranslator,
			storageProvider:               storageProvider,
			tokenManagementSystemProvider: tokenManagementSystemProvider,
		},
		&setupBehaviour{
			getTranslator:                 getTranslator,
			ppValidator:                   ppValidator,
			tokenManagementSystemProvider: tokenManagementSystemProvider,
		},
	)
}

func (r *ResponderView) Call(context view.Context) (any, error) {
	// receive
	request, behaviour, err := r.receive(context)
	if err != nil {
		return nil, errors.Join(ErrReceivedProposal, err)
	}
	defer request.Rws.Done()

	// validate proposal
	if err := validateProposal(context, r.channelProvider, request.Tx, request.TMSID, request.Anchor); err != nil {
		return nil, errors.Join(ErrValidateProposal, err)
	}

	// validate the request content
	if err := behaviour.validate(context, request); err != nil {
		return nil, errors.Join(ErrValidateProposal, err)
	}

	// endorse
	res, err := r.endorse(context, request, behaviour)
	if err != nil {
		return nil, errors.Join(ErrEndorseProposal, err)
	}

	return res, nil
}

func (r *ResponderView) receive(ctx view.Context) (*Request, responderBehaviour, error) {
	logger.DebugfContext(ctx.Context(), "Waiting for transaction on context [%s]", ctx.ID())
	tx, err := r.endorserService.ReceiveTx(ctx)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "failed to received transaction for approval")
	}
	logger.DebugfContext(ctx.Context(), "Received transaction [%s] for endorsement on context [%s]", tx.ID(), ctx.ID())
	defer logger.DebugfContext(ctx.Context(), "Return endorsement result for TX [%s]", tx.ID())

	// the function name determines which behaviour handles this request; it must be known
	// before the behaviour-specific transient checks below can run
	fn, parms := tx.FunctionAndParameters()
	behaviour, ok := r.behaviours[fn]
	if !ok {
		return nil, nil, errors.Wrapf(ErrInvalidProposal, "invalid function [%s][%v]", fn, r.behaviours)
	}

	// validate transient

	if err := behaviour.checkTransientCount(tx); err != nil {
		return nil, nil, err
	}

	// TMS ID
	var tmsID token2.TMSID
	if err := tx.GetTransientState(TransientTMSIDKey, &tmsID); err != nil {
		return nil, nil, errors.Wrapf(errors.Join(err, ErrInvalidTransient), "empty tms id")
	}
	if len(tmsID.Network) == 0 || len(tmsID.Channel) == 0 || len(tmsID.Namespace) == 0 {
		return nil, nil, errors.Wrapf(errors.Join(err, ErrInvalidTransient), "invalid tms id [%s]", tmsID)
	}
	logger.DebugfContext(ctx.Context(), "evaluate token request on TMS [%s]", tmsID)

	// request anchor
	requestAnchor := tx.ID()

	request := &Request{
		Tx:     tx,
		TMSID:  tmsID,
		Anchor: requestAnchor,
	}

	// behaviour-specific transient fields
	if err := behaviour.extractTransient(tx, request); err != nil {
		return nil, nil, err
	}

	// rws
	rws, err := tx.RWSet()
	if err != nil {
		return nil, nil, errors.Wrapf(errors.Join(ErrInvalidProposal, err), "failed to get rws for tx [%s]", tx.ID())
	}
	defer func() {
		// if an error occurred, then call Done on the rwset
		if rws != nil {
			rws.Done()
		}
	}()

	// the rws must be empty
	if len(rws.Namespaces()) != 0 {
		return nil, nil, errors.Wrapf(ErrInvalidProposal, "non empty namespaces")
	}

	if name, version := tx.Chaincode(); name != tmsID.Namespace || version != ChaincodeVersion {
		return nil, nil, errors.Wrapf(ErrInvalidProposal, "invalid chaincode")
	}
	if len(parms) != 0 {
		return nil, nil, errors.Wrapf(ErrInvalidProposal, "invalid parameters")
	}

	// copy rws to make sure Done is not invoked on it, see defer above
	request.Rws = rws
	rws = nil

	return request, behaviour, nil
}

// validateProposal performs the common proposal-level validation shared by all responders in this
// package: it checks that the creator identity is present and known to the network via MSP, that
// ACL checks pass, and that the proposal signature verifies against the claimed creator.
func validateProposal(ctx view.Context, channelProvider ChannelProvider, tx *endorser.Transaction, tmsID token2.TMSID, anchor string) error {
	logger.DebugfContext(ctx.Context(), "Validate proposal for TX [%s]", anchor)

	// Get the signed proposal from the underlying Fabric transaction
	signedProposal := tx.Transaction.SignedProposal()

	// Get the proposal
	proposal := tx.Transaction.Proposal()

	// The creator identity must be present
	creator := tx.Transaction.Creator()
	if len(creator) == 0 {
		return errors.Errorf("creator is empty for tx [%s]", anchor)
	}

	// The proposal bytes and signature must be present
	proposalBytes := signedProposal.ProposalBytes()
	if len(proposalBytes) == 0 {
		return errors.Errorf("proposal bytes are empty for tx [%s]", anchor)
	}
	signature := signedProposal.Signature()
	if len(signature) == 0 {
		return errors.Errorf("proposal signature is empty for tx [%s]", anchor)
	}

	// The proposal header and payload should be present
	if len(proposal.Header()) == 0 {
		return errors.Errorf("proposal header is empty for tx [%s]", anchor)
	}
	if len(proposal.Payload()) == 0 {
		return errors.Errorf("proposal payload is empty for tx [%s]", anchor)
	}

	// Verify the creator is known to the network via MSP.
	// In the Fabric protocol, the endorser is responsible for checking that the proposal signer
	// is recognized by at least one MSP in the channel configuration.
	mspManager, err := channelProvider.GetMSPManager(tmsID.Network, tmsID.Channel)
	if err != nil {
		return errors.Wrapf(err, "failed to get MSP manager for tx [%s]", anchor)
	}
	if err := mspManager.IsValid(creator); err != nil {
		return errors.Wrapf(err, "creator identity is not valid for tx [%s]", anchor)
	}

	acl, err := channelProvider.GetACLProvider(tmsID.Network, tmsID.Channel)
	if err != nil {
		return errors.Wrapf(err, "failed to get ACL provider for tx [%s]", anchor)
	}
	if err := acl.CheckACL(tx.SignedProposal()); err != nil {
		return errors.Wrapf(err, "failed to check ACL for tx [%s]", anchor)
	}

	// Verify the proposal signature using the creator's verifier.
	// This ensures the proposal was indeed signed by the claimed creator.
	verifier, err := mspManager.GetVerifier(creator)
	if err != nil {
		return errors.Wrapf(err, "failed to get verifier for creator for tx [%s]", anchor)
	}
	if err := verifier.Verify(proposalBytes, signature); err != nil {
		return errors.Wrapf(err, "proposal signature verification failed for tx [%s]", anchor)
	}

	logger.DebugfContext(ctx.Context(), "Proposal validated successfully for TX [%s]", anchor)

	return nil
}

func (r *ResponderView) endorse(ctx view.Context, request *Request, behaviour responderBehaviour) (any, error) {
	// endorse
	logger.DebugfContext(ctx.Context(), "Endorse TX [%s]", request.Anchor)
	endorserID, err := r.endorserService.EndorserID(request.TMSID)
	if err != nil {
		return nil, err
	}

	// write actions into the transaction
	logger.DebugfContext(ctx.Context(), "Translate TX [%s]", request.Anchor)
	if err := behaviour.translate(ctx.Context(), request); err != nil {
		return nil, err
	}

	logger.DebugfContext(ctx.Context(), "Endorse proposal for TX [%s]", request.Anchor)
	endorsementResult, err := r.endorserService.Endorse(ctx, request.Tx, endorserID)
	if err != nil {
		logger.Errorf("failed to respond to endorsement [%s]", err)
	}
	logger.DebugfContext(ctx.Context(), "Finished endorsement on TX [%s]", request.Anchor)

	return endorsementResult, err
}

// approvalBehaviour implements responderBehaviour for the token request approval protocol.
type approvalBehaviour struct {
	keyTranslator                 translator.KeyTranslator
	getTranslator                 TranslatorProviderFunc
	storageProvider               StorageProvider
	tokenManagementSystemProvider TokenManagementSystemProvider
}

func (b *approvalBehaviour) function() string { return InvokeFunction }

func (b *approvalBehaviour) checkTransientCount(tx *endorser.Transaction) error {
	// 2 required (tmsID + token_request) plus 1 optional (approval_metadata)
	if n := len(tx.Transaction.Transient()); n < 2 || n > 3 {
		return errors.Wrapf(ErrInvalidTransient, "invalid number of transient fields, expected 2 or 3, got %d", n)
	}

	return nil
}

func (b *approvalBehaviour) extractTransient(tx *endorser.Transaction, request *Request) error {
	// token request
	requestRaw := tx.GetTransient(TransientTokenRequestKey)
	if len(requestRaw) == 0 {
		return errors.Wrapf(ErrInvalidTransient, "empty token request")
	}

	// approval metadata (optional)
	var approvalMetadata map[string][]byte
	if raw := tx.GetTransient(TransientApprovalMetadataKey); len(raw) > 0 {
		if err := json.Unmarshal(raw, &approvalMetadata); err != nil {
			return errors.Wrapf(ErrInvalidTransient, "failed to unmarshal approval metadata")
		}
	}

	request.RequestRaw = requestRaw
	request.ApprovalMetadata = approvalMetadata

	return nil
}

func (b *approvalBehaviour) validate(context view.Context, request *Request) error {
	logger.DebugfContext(context.Context(), "Validate TX [%s]", request.Anchor)
	defer logger.DebugfContext(context.Context(), "Finished validation of TX [%s]", request.Anchor)

	tms, err := b.tokenManagementSystemProvider.GetManagementService(token2.WithTMSID(request.TMSID))
	if err != nil {
		return errors.Wrapf(err, "no tms found for [%s]", request.TMSID)
	}
	if !tms.ID().Equal(request.TMSID) {
		return errors.Errorf("tms ids do not match, expected [%s], got [%s]", request.TMSID, tms.ID())
	}
	request.Tms = tms
	request.PublicParamsHash = tms.PublicParametersManager().PublicParamsHash()

	getState := func(id token.ID) ([]byte, error) {
		key, err := b.keyTranslator.CreateOutputKey(id.TxId, id.Index)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to create token key for id [%s]", id)
		}

		return request.Rws.GetDirectState(request.TMSID.Namespace, key)
	}

	logger.DebugfContext(context.Context(), "Get validator for TX [%s]", request.Anchor)
	validator, err := request.Tms.Validator()
	if err != nil {
		return errors.WithMessagef(err, "failed to get validator [%s]", request.TMSID)
	}
	logger.DebugfContext(context.Context(), "Unmarshal and verify with metadata for TX [%s]", request.Anchor)
	actions, meta, err := validator.UnmarshallAndVerifyWithMetadata(
		context.Context(),
		token2.NewLedgerFromGetter(getState),
		token2.RequestAnchor(request.Anchor),
		request.RequestRaw,
	)
	if err != nil {
		return errors.WithMessagef(err, "failed to verify token request for [%s]", request.Anchor)
	}
	db, err := b.storageProvider.GetStorage(request.TMSID)
	if err != nil {
		return errors.WithMessagef(err, "failed to retrieve db [%s]", request.TMSID)
	}
	logger.DebugfContext(context.Context(), "Append validation record for TX [%s]", request.Anchor)
	if err := db.AppendValidationRecord(
		context.Context(),
		request.Anchor,
		request.RequestRaw,
		meta,
		request.PublicParamsHash,
	); err != nil {
		return errors.WithMessagef(err, "failed to append metadata for [%s]", request.Anchor)
	}
	request.Actions = actions
	request.Meta = meta

	return nil
}

func (b *approvalBehaviour) translate(ctx context.Context, request *Request) error {
	// prepare the rws as usual
	txID := request.Anchor
	w, err := b.getTranslator(txID, request.TMSID.Namespace, request.Rws)
	if err != nil {
		return errors.Wrapf(err, "failed to get translator for tx [%s]", request.Anchor)
	}
	for _, action := range request.Actions {
		if err := w.Write(ctx, action); err != nil {
			return errors.Wrapf(err, "failed to write token action for tx [%s]", txID)
		}
	}
	if err := w.AddPublicParamsDependency(); err != nil {
		return errors.Wrapf(err, "failed to add public params dependency")
	}
	if _, err := w.CommitTokenRequest(request.Meta[common.TokenRequestToSign], true); err != nil {
		return errors.Wrapf(err, "failed to write token request")
	}

	return nil
}

// setupAction is a minimal translator.SetupAction implementation carrying the raw public
// parameters to be committed to the RWSet.
type setupAction struct {
	PublicParamsRaw []byte
}

// GetSetupParameters returns the raw public parameters carried by this action.
func (a *setupAction) GetSetupParameters() ([]byte, error) {
	return a.PublicParamsRaw, nil
}

// setupBehaviour implements responderBehaviour for the public parameters setup/update protocol.
type setupBehaviour struct {
	getTranslator                 TranslatorProviderFunc
	ppValidator                   PublicParamsValidator
	tokenManagementSystemProvider TokenManagementSystemProvider
}

func (b *setupBehaviour) function() string { return SetupFunction }

func (b *setupBehaviour) checkTransientCount(tx *endorser.Transaction) error {
	// 2 required (tmsID + public_params)
	if n := len(tx.Transaction.Transient()); n != 2 {
		return errors.Wrapf(ErrInvalidTransient, "invalid number of transient fields, expected 2, got %d", n)
	}

	return nil
}

func (b *setupBehaviour) extractTransient(tx *endorser.Transaction, request *Request) error {
	// public parameters
	publicParamsRaw := tx.GetTransient(TransientPublicParamsKey)
	if len(publicParamsRaw) == 0 {
		return errors.Wrapf(ErrInvalidTransient, "empty public params")
	}

	request.PublicParamsRaw = publicParamsRaw

	return nil
}

// validate looks up the TMS for request.TMSID, if any, and checks that it matches the
// requested TMS ID. Validating the submitted public parameters themselves (parsing,
// internal consistency, and driver compatibility against an existing TMS) is left for
// https://github.com/LFDT-Panurus/panurus/issues/1943, since at this point we don't know
// enough to make that call yet.
func (b *setupBehaviour) validate(_ view.Context, request *Request) error {
	tms, err := b.tokenManagementSystemProvider.GetManagementService(token2.WithTMSID(request.TMSID))
	if err != nil {
		if !errors.Is(err, token2.ErrTMSNotFound) {
			return errors.Wrapf(err, "no tms found for [%s]", request.TMSID)
		}
		// no TMS yet for this namespace, this is a first-time initialization
	} else if !tms.ID().Equal(request.TMSID) {
		return errors.Errorf("tms ids do not match, expected [%s], got [%s]", request.TMSID, tms.ID())
	} else {
		request.Tms = tms
	}

	// TODO: need to add validation logic...to be addressed in #1943

	return nil
}

func (b *setupBehaviour) translate(ctx context.Context, request *Request) error {
	w, err := b.getTranslator(request.Anchor, request.TMSID.Namespace, request.Rws)
	if err != nil {
		return errors.Wrapf(err, "failed to get translator for tx [%s]", request.Anchor)
	}
	if err := w.Write(ctx, &setupAction{PublicParamsRaw: request.PublicParamsRaw}); err != nil {
		return errors.Wrapf(err, "failed to write setup action for tx [%s]", request.Anchor)
	}

	return nil
}
