/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"encoding/asn1"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	session2 "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	view3 "github.com/LFDT-Panurus/panurus/token/services/utils/view"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/endpoint"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type RecipientData = token.RecipientData

type MultisigRecipientData struct {
	RecipientData *token.RecipientData
	Nodes         []view.Identity
	Recipients    []token.Identity
}

// PolicyRecipientData carries the composite policy identity back to each co-owner.
type PolicyRecipientData struct {
	RecipientData *token.RecipientData
	Nodes         []view.Identity
	Recipients    []token.Identity
	Policy        string
}

type ExchangeRecipientRequest struct {
	TMSID         token.TMSID
	WalletID      []byte
	RecipientData *RecipientData
	Nonce         []byte
	Signature     []byte
}

// ExchangeRecipientResponse carries the responder's identity material together
// with a key-ownership attestation: a signature over the request-bound
// attestation message (see buildAttestationMessage).
type ExchangeRecipientResponse struct {
	RecipientData *RecipientData
	Signature     []byte
}

// Bytes serializes the ExchangeRecipientRequest to bytes.
func (r *ExchangeRecipientRequest) Bytes() ([]byte, error) {
	return Marshal(r)
}

// FromBytes deserializes the ExchangeRecipientRequest from bytes.
func (r *ExchangeRecipientRequest) FromBytes(raw []byte) error {
	return Unmarshal(raw, r)
}

type RecipientRequest struct {
	TMSID         token.TMSID
	WalletID      []byte
	RecipientData *RecipientData
	MultiSig      bool
	// Policy, when non-empty, signals that the initiator will follow up with a PolicyRecipientData message.
	Policy string
	Nonce  []byte
}

// RecipientResponse is the response for single-recipient identity requests.
// On the echo path (initiator supplied RecipientData), RecipientData is nil
// and the initiator uses its own copy; on the fresh path RecipientData carries
// the full identity material. Signature covers the request-bound attestation
// message (see buildAttestationMessage).
type RecipientResponse struct {
	RecipientData *RecipientData
	Signature     []byte
}

// Bytes serializes the RecipientRequest to bytes.
func (r *RecipientRequest) Bytes() ([]byte, error) {
	return Marshal(r)
}

// FromBytes deserializes the RecipientRequest from bytes.
func (r *RecipientRequest) FromBytes(raw []byte) error {
	return Unmarshal(raw, r)
}

type Recipient struct {
	Identity      view.Identity
	WalletID      string
	RecipientData *RecipientData
}

type Recipients []Recipient

// Identities extracts and returns all recipient identities from the Recipients slice.
func (r Recipients) Identities() []view.Identity {
	ids := make([]view.Identity, len(r))
	for i, recipient := range r {
		ids[i] = recipient.Identity
	}

	return ids
}

type RequestRecipientIdentityView struct {
	TMSID      token.TMSID
	Recipients Recipients
	// Policy, when non-empty, causes the collected identities to be wrapped
	// as a PolicyIdentity instead of a MultiIdentity.
	Policy string
}

// RequestRecipientIdentity executes the RequestRecipientIdentityView.
// The sender contacts the recipient's FSC node identified via the passed view identity.
// The sender gets back the identity the recipient wants to use to assign ownership of tokens.
func RequestRecipientIdentity(context view.Context, recipient view.Identity, opts ...token.ServiceOption) (token.Identity, error) {
	options, err := CompileServiceOptions(opts...)
	if err != nil {
		return nil, err
	}
	pseudonymBoxed, err := view3.RunViewWithTimeout(
		context,
		&RequestRecipientIdentityView{
			TMSID: options.TMSID(),
			Recipients: []Recipient{
				{
					Identity:      recipient,
					RecipientData: getRecipientData(options),
					WalletID:      getRecipientWalletID(options),
				},
			},
		},
		options.Duration,
	)
	if err != nil {
		return nil, err
	}

	return pseudonymBoxed.(view.Identity), nil
}

// RequestMultisigIdentity collects the recipient identities from all the passed identities.
// It merges them into a single multisig identity and distributes it to all the participants.
func RequestMultisigIdentity(context view.Context, ids []view.Identity, opts ...token.ServiceOption) (token.Identity, error) {
	options, err := CompileServiceOptions(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed compiling service options")
	}
	recipients := make([]Recipient, len(ids))
	for i, id := range ids {
		recipients[i] = Recipient{
			Identity:      id,
			RecipientData: getRecipientData(options),
		}
	}
	pseudonymBoxed, err := view3.RunViewWithTimeout(
		context,
		&RequestRecipientIdentityView{
			TMSID:      options.TMSID(),
			Recipients: recipients,
		},
		options.Duration,
	)
	if err != nil {
		return nil, err
	}

	return pseudonymBoxed.(view.Identity), nil
}

// RequestPolicyIdentity collects recipient identities from all the passed parties,
// wraps them into a single PolicyIdentity governed by the given boolean policy expression,
// and distributes the composite identity back to all participants.
func RequestPolicyIdentity(context view.Context, policy string, ids []view.Identity, opts ...token.ServiceOption) (token.Identity, error) {
	options, err := CompileServiceOptions(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed compiling service options")
	}
	recipients := make([]Recipient, len(ids))
	for i, id := range ids {
		recipients[i] = Recipient{
			Identity:      id,
			RecipientData: getRecipientData(options),
		}
	}
	pseudonymBoxed, err := view3.RunViewWithTimeout(
		context,
		&RequestRecipientIdentityView{
			TMSID:      options.TMSID(),
			Recipients: recipients,
			Policy:     policy,
		},
		options.Duration,
	)
	if err != nil {
		return nil, err
	}

	return pseudonymBoxed.(view.Identity), nil
}

func (f *RequestRecipientIdentityView) Call(context view.Context) (any, error) {
	results := make([]token.Identity, len(f.Recipients))
	local := make([]bool, len(f.Recipients))
	var err error
	tms, err := token.GetManagementService(context, token.WithTMSID(f.TMSID))
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting token management service [%s]", f.TMSID)
	}
	multiSig := len(f.Recipients) > 1
	for i, recipient := range f.Recipients {
		local[i] = true
		w, err := tms.WalletManager().OwnerWallet(context.Context(), recipient.Identity)
		if err != nil {
			w = nil
		}

		if isSameNode := w != nil; !isSameNode {
			results[i], err = f.callWithRecipientData(context, &recipient, multiSig, f.Policy)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to get recipient identity")
			}
			local[i] = false

			continue
		}

		if isRemoteRecipient := recipient.RecipientData != nil; isRemoteRecipient {
			results[i] = recipient.RecipientData.Identity

			continue
		}
		if w == nil {
			return nil, errors.Errorf("wallet [%s] not found", string(recipient.Identity))
		}
		results[i], err = w.GetRecipientIdentity(context.Context())
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get recipient identity")
		}
	}
	if !multiSig {
		return results[0], nil
	}

	if f.Policy != "" {
		// aggregate the results as a policy identity, then distribute to all participants
		policyIdentity, err := f.aggregateAndDistributePolicy(context, tms, results, local)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to aggregate policy recipient identities")
		}

		return policyIdentity, nil
	}

	// aggregate the results as multisig identity, then distribute the aggregate results to all the participants
	multisigIdentity, err := f.aggregateAndDistribute(context, tms, results, local)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to aggregate recipient identities")
	}

	return multisigIdentity, nil
}

func (f *RequestRecipientIdentityView) callWithRecipientData(context view.Context, recipient *Recipient, multiSig bool, policy string) (token.Identity, error) {
	logger.DebugfContext(context.Context(), "request recipient [%s] is not registered", recipient.Identity)
	session, err := session2.NewTypedSessionToParty(context, recipient.Identity)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get session with [%s]", recipient.Identity)
	}

	wID := []byte(recipient.WalletID)
	if len(wID) == 0 {
		wID = recipient.Identity
	}
	nonce, err := GetRandomNonce()
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate nonce for recipient request")
	}
	recipientRequest := &RecipientRequest{
		TMSID:         f.TMSID,
		WalletID:      wID,
		RecipientData: recipient.RecipientData,
		MultiSig:      multiSig && policy == "",
		Policy:        policy,
		Nonce:         nonce,
	}
	logger.DebugfContext(context.Context(), "Send identity request to %s", wID)
	if err = session.SendTyped(context.Context(), recipientRequest, TypeRecipientRequest); err != nil {
		return nil, errors.Wrapf(err, "failed to send recipient request")
	}

	logger.DebugfContext(context.Context(), "Receive identity response")
	resp := &RecipientResponse{}
	if err = session.ReceiveTypedWithTimeout(TypeRecipientResponse, resp, 10*time.Second); err != nil {
		return nil, errors.Wrapf(err, "failed to receive recipient response")
	}

	// On the echo path the responder sends only a signature; use our own data.
	recipientData := resp.RecipientData
	if recipient.RecipientData != nil {
		recipientData = recipient.RecipientData
	}
	if recipientData == nil {
		return nil, errors.New("responder returned empty recipient data on fresh path")
	}

	tms, err := token.GetManagementService(context, token.WithTMSID(f.TMSID))
	if err != nil {
		return nil, errors.Wrap(err, "failed to get token management service")
	}

	// Verify key-ownership attestation when the responder could sign locally.
	message, err := buildAttestationMessage(recipientRequest.TMSID, recipientRequest.WalletID, recipientData.Identity, recipientRequest.MultiSig, recipientRequest.Policy, recipientRequest.Nonce, session.Info().ID, context.ID())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to build attestation message")
	}
	if err = verifyRecipientAttestation(context.Context(), tms, message, recipientData, resp.Signature, recipient.RecipientData != nil); err != nil {
		return nil, err
	}

	wm := tms.WalletManager()
	logger.DebugfContext(context.Context(), "Register recipient identity")
	if err = wm.RegisterRecipientIdentity(context.Context(), recipientData); err != nil {
		return nil, errors.Wrapf(err, "failed to register recipient identity")
	}

	logger.DebugfContext(context.Context(), "update endpoint resolver for [%s], bind to [%s]", recipientData.Identity, recipient.Identity)
	if err = endpoint.GetService(context).Bind(context.Context(), recipient.Identity, recipientData.Identity); err != nil {
		logger.ErrorfContext(context.Context(), "failed binding [%s] to [%s]: %s", recipientData.Identity, recipient.Identity, err)

		return nil, errors.Wrapf(err, "failed binding [%s] to [%s]", recipientData.Identity, recipient.Identity)
	}

	return recipientData.Identity, nil
}

func (f *RequestRecipientIdentityView) aggregateAndDistribute(context view.Context, tms *token.ManagementService, recipients []token.Identity, local []bool) (token.Identity, error) {
	// prepare identity
	multisigIdentity, err := multisig.WrapIdentities(recipients...)
	if err != nil {
		return nil, errors.Wrap(err, "failed wrapping identities")
	}

	// prepare audit info
	auditInfoForRecipients, err := tms.SigService().GetAuditInfo(context.Context(), recipients...)
	if err != nil {
		return nil, errors.Wrap(err, "failed getting token audit info")
	}
	auditInfo, err := multisig.WrapAuditInfo(auditInfoForRecipients)
	if err != nil {
		return nil, errors.Wrap(err, "failed wrapping audit info")
	}

	// register audit info for the recipient
	recipientData := &token.RecipientData{
		Identity:  multisigIdentity,
		AuditInfo: auditInfo,
	}
	err = tms.WalletManager().RegisterRecipientIdentity(context.Context(), recipientData)
	if err != nil {
		return nil, errors.Wrapf(err, "failed registering recipient identity [%s]", multisigIdentity)
	}

	// distribute recipient identity and its audit info to all the participants
	mrd := &MultisigRecipientData{
		RecipientData: recipientData,
		Nodes:         f.Recipients.Identities(),
		Recipients:    recipients,
	}
	for i, recipient := range f.Recipients {
		if local[i] {
			continue
		}
		session, err := session2.NewTypedSessionForCaller(context, context.Initiator(), recipient.Identity)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get session with [%s]", recipient.Identity)
		}
		err = session.SendTyped(context.Context(), mrd, TypeMultisigRecipientData)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to send recipient request")
		}
	}

	return multisigIdentity, nil
}

func (f *RequestRecipientIdentityView) aggregateAndDistributePolicy(context view.Context, tms *token.ManagementService, recipients []token.Identity, local []bool) (token.Identity, error) {
	// prepare policy identity
	policyIdentity, err := boolpolicy.WrapPolicyIdentity(f.Policy, recipients...)
	if err != nil {
		return nil, errors.Wrap(err, "failed wrapping policy identity")
	}

	// prepare audit info
	auditInfoForRecipients, err := tms.SigService().GetAuditInfo(context.Context(), recipients...)
	if err != nil {
		return nil, errors.Wrap(err, "failed getting token audit info")
	}
	auditInfo, err := boolpolicy.WrapAuditInfo(auditInfoForRecipients)
	if err != nil {
		return nil, errors.Wrap(err, "failed wrapping policy audit info")
	}

	// register audit info for the recipient
	recipientData := &token.RecipientData{
		Identity:  policyIdentity,
		AuditInfo: auditInfo,
	}
	if err = tms.WalletManager().RegisterRecipientIdentity(context.Context(), recipientData); err != nil {
		return nil, errors.Wrapf(err, "failed registering policy recipient identity [%s]", policyIdentity)
	}

	// distribute back to all remote participants
	prd := &PolicyRecipientData{
		RecipientData: recipientData,
		Nodes:         f.Recipients.Identities(),
		Recipients:    recipients,
		Policy:        f.Policy,
	}
	for i, recipient := range f.Recipients {
		if local[i] {
			continue
		}
		s, err := session2.NewTypedSessionForCaller(context, context.Initiator(), recipient.Identity)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get session with [%s]", recipient.Identity)
		}
		if err = s.SendTyped(context.Context(), prd, TypePolicyRecipientData); err != nil {
			return nil, errors.Wrapf(err, "failed to send policy recipient data")
		}
	}

	return policyIdentity, nil
}

type RespondRequestRecipientIdentityView struct {
	Wallet         string
	WalletSelector WalletSelector
}

// RespondRequestRecipientIdentity executes the RespondRequestRecipientIdentityView.
// The recipient sends back the identity to receive ownership of tokens.
// The identity is taken from the default wallet, unless a WalletSelector option
// (see WithWalletSelector) overrides the choice.
// If the wallet is not found, an error is returned.
func RespondRequestRecipientIdentity(context view.Context, opts ...token.ServiceOption) (view.Identity, error) {
	return RespondRequestRecipientIdentityUsingWallet(context, "", opts...)
}

// RespondRequestRecipientIdentityUsingWallet executes the RespondRequestRecipientIdentityView.
// The recipient sends back the identity to receive ownership of tokens.
// The identity is taken from the passed wallet, unless a WalletSelector option
// (see WithWalletSelector) overrides the choice.
// If the wallet is not found, an error is returned.
// If the wallet is the empty string, the identity is taken from the default wallet.
func RespondRequestRecipientIdentityUsingWallet(context view.Context, wallet string, opts ...token.ServiceOption) (view.Identity, error) {
	options, err := CompileServiceOptions(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed compiling service options")
	}
	id, err := context.RunView(&RespondRequestRecipientIdentityView{
		Wallet:         wallet,
		WalletSelector: getWalletSelector(options),
	})
	if err != nil {
		return nil, err
	}

	return id.(view.Identity), nil
}

func (s *RespondRequestRecipientIdentityView) Call(context view.Context) (any, error) {
	session := session2.NewTypedSessionFromContext(context)
	recipientRequest := &RecipientRequest{}
	if err := session.ReceiveTyped(TypeRecipientRequest, recipientRequest); err != nil {
		return nil, errors.Wrapf(err, "failed to receive recipient request")
	}
	if len(recipientRequest.Nonce) == 0 {
		return nil, errors.New("recipient request missing nonce")
	}

	wallet := s.Wallet
	if len(wallet) == 0 && len(recipientRequest.WalletID) != 0 {
		wallet = string(recipientRequest.WalletID)
	}
	if s.WalletSelector != nil {
		selected, err := s.WalletSelector(recipientRequest, wallet)
		if err != nil {
			return nil, errors.Wrapf(err, "failed selecting wallet")
		}
		wallet = selected
	}
	logger.DebugfContext(context.Context(), "Respond request recipient identity using wallet [%s]", wallet)
	tms, err := token.GetManagementService(context, token.WithTMSID(recipientRequest.TMSID))
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting token management service [%s]", recipientRequest.TMSID)
	}
	w, err := tms.WalletManager().OwnerWallet(context.Context(), wallet)
	if err != nil {
		return nil, errors.Wrapf(err, "wallet [%s:%s] not found", wallet, recipientRequest.TMSID)
	}

	var recipientData *RecipientData
	var recipientIdentity view.Identity
	isEcho := false
	if recipientRequest.RecipientData != nil {
		recipientData = recipientRequest.RecipientData
		recipientIdentity = recipientData.Identity
		if !w.Contains(context.Context(), recipientIdentity) {
			return nil, errors.Errorf("cannot find identity [%s] in wallet [%s:%s]", recipientIdentity, wallet, recipientRequest.TMSID)
		}
		isEcho = true
	} else {
		logger.DebugfContext(context.Context(), "generate_identity")
		recipientData, err = w.GetRecipientData(context.Context())
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get recipient identity")
		}
		recipientIdentity = recipientData.Identity
	}

	// Sign the request-bound attestation to prove key ownership when the key is local.
	message, err := buildAttestationMessage(recipientRequest.TMSID, recipientRequest.WalletID, recipientIdentity, recipientRequest.MultiSig, recipientRequest.Policy, recipientRequest.Nonce, session.Info().ID, context.ID())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to build attestation message")
	}
	sig, err := signRecipientAttestation(context.Context(), w, message, recipientIdentity, !isEcho)
	if err != nil {
		return nil, err
	}

	// Bind before send
	resolver := endpoint.GetService(context)
	logger.DebugfContext(context.Context(), "bind me [%s] to [%s]", context.Me(), recipientIdentity)
	if err = resolver.Bind(context.Context(), context.Me(), recipientIdentity); err != nil {
		return nil, errors.Wrapf(err, "failed to bind me to recipient identity")
	}

	// Build response: slim ack on echo path, full data on fresh path
	resp := &RecipientResponse{Signature: sig}
	if !isEcho {
		resp.RecipientData = recipientData
	}

	logger.DebugfContext(context.Context(), "Send recipient identity response to %s", session.Info().Caller)
	if err = session.SendTyped(context.Context(), resp, TypeRecipientResponse); err != nil {
		return nil, errors.Wrapf(err, "failed to send recipient response")
	}

	if err = s.handleComposite(context, session.Session(), tms, recipientRequest, recipientIdentity); err != nil {
		return nil, errors.Wrapf(err, "failed to handle composite identity")
	}

	return recipientIdentity, nil
}

// handleComposite dispatches to the appropriate composite-identity handler.
func (s *RespondRequestRecipientIdentityView) handleComposite(
	context view.Context,
	session view.Session,
	tms *token.ManagementService,
	recipientRequest *RecipientRequest,
	recipientIdentity token.Identity,
) error {
	if recipientRequest.Policy != "" {
		return s.handlePolicy(context, session, tms, recipientRequest, recipientIdentity)
	}

	return s.handleMultisig(context, session, tms, recipientRequest, recipientIdentity)
}

func (s *RespondRequestRecipientIdentityView) handleMultisig(
	context view.Context,
	session view.Session,
	tms *token.ManagementService,
	recipientRequest *RecipientRequest,
	recipientIdentity token.Identity,
) error {
	if !recipientRequest.MultiSig {
		logger.DebugfContext(context.Context(), "Skip multisig")

		return nil
	}

	jsonSession := session2.NewTypedSession(context, session)

	logger.DebugfContext(context.Context(), "Receive multisig")
	multisigRecipientData := &MultisigRecipientData{}
	err := jsonSession.ReceiveTyped(TypeMultisigRecipientData, multisigRecipientData)
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshal multisig recipient data")
	}
	logger.DebugfContext(context.Context(), "Received multisig")

	multisigIdentities, auditInfos, err := validateMultisigRecipientData(multisigRecipientData, recipientIdentity)
	if err != nil {
		return err
	}

	// register the multisig recipient identity
	wm := tms.WalletManager()
	err = wm.RegisterRecipientIdentity(context.Context(), multisigRecipientData.RecipientData)
	if err != nil {
		return errors.Wrapf(err, "failed to register recipient identity")
	}
	sigService := tms.SigService()
	signer, err := sigService.GetSigner(context.Context(), recipientIdentity)
	if err != nil {
		return err
	}
	logger.DebugfContext(context.Context(), "registering signer for reclaim...")
	if err := sigService.RegisterSigner(
		context.Context(),
		multisigRecipientData.RecipientData.Identity,
		signer,
		nil,
	); err != nil {
		return err
	}

	// register the audit info for each party too
	for i, identity := range multisigIdentities {
		if identity.Equal(recipientIdentity) {
			continue
		}
		err = wm.RegisterRecipientIdentity(context.Context(), &RecipientData{
			Identity:               identity,
			AuditInfo:              auditInfos[i],
			TokenMetadata:          multisigRecipientData.RecipientData.TokenMetadata,
			TokenMetadataAuditInfo: multisigRecipientData.RecipientData.TokenMetadataAuditInfo,
		})
		if err != nil {
			return errors.Wrapf(err, "failed to register recipient identity")
		}
	}

	// Update the Endpoint Resolver
	resolver := endpoint.GetService(context)
	for i, node := range multisigRecipientData.Nodes {
		err = resolver.Bind(context.Context(), node, multisigRecipientData.Recipients[i])
		if err != nil {
			return errors.Wrapf(err, "failed to bind node identity to recipient identity")
		}
	}

	return nil
}

func validateMultisigRecipientData(data *MultisigRecipientData, recipientIdentity token.Identity) ([]token.Identity, [][]byte, error) {
	if data == nil || data.RecipientData == nil {
		return nil, nil, errors.New("invalid multisig recipient data: recipient data is nil")
	}
	identities, ok, err := multisig.Unwrap(data.RecipientData.Identity)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to unwrap multisig identity")
	}
	if !ok {
		return nil, nil, errors.New("invalid multisig recipient data: expected multisig identity")
	}
	ok, auditInfos, err := multisig.UnwrapAuditInfo(data.RecipientData.AuditInfo)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to unwrap multisig audit info")
	}
	if !ok {
		return nil, nil, errors.New("invalid multisig recipient data: expected multisig audit info")
	}
	if err := validateCompositeRecipientData(identities, auditInfos, data.Nodes, data.Recipients, recipientIdentity); err != nil {
		return nil, nil, errors.Wrap(err, "invalid multisig recipient data")
	}

	return identities, auditInfos, nil
}

func (s *RespondRequestRecipientIdentityView) handlePolicy(
	context view.Context,
	session view.Session,
	tms *token.ManagementService,
	recipientRequest *RecipientRequest,
	recipientIdentity token.Identity,
) error {
	jsonSession := session2.NewTypedSession(context, session)

	logger.DebugfContext(context.Context(), "Receive policy recipient data")
	prd := &PolicyRecipientData{}
	if err := jsonSession.ReceiveTyped(TypePolicyRecipientData, prd); err != nil {
		return errors.Wrapf(err, "failed to receive policy recipient data")
	}
	logger.DebugfContext(context.Context(), "Received policy recipient data")

	pi, auditInfos, err := validatePolicyRecipientData(prd, recipientRequest.Policy, recipientIdentity)
	if err != nil {
		return err
	}

	// register the composite policy identity
	wm := tms.WalletManager()
	if err := wm.RegisterRecipientIdentity(context.Context(), prd.RecipientData); err != nil {
		return errors.Wrapf(err, "failed to register policy recipient identity")
	}

	// register the component signer for the composite identity (my slot)
	sigService := tms.SigService()
	signer, err := sigService.GetSigner(context.Context(), recipientIdentity)
	if err != nil {
		return err
	}
	if err := sigService.RegisterSigner(context.Context(), prd.RecipientData.Identity, signer, nil); err != nil {
		return errors.Wrapf(err, "failed to register policy signer")
	}

	// register audit info for each component identity
	for i, raw := range pi.Identities {
		componentID := token.Identity(raw)
		if componentID.Equal(recipientIdentity) {
			continue
		}
		if err := wm.RegisterRecipientIdentity(context.Context(), &RecipientData{
			Identity:               componentID,
			AuditInfo:              auditInfos[i],
			TokenMetadata:          prd.RecipientData.TokenMetadata,
			TokenMetadataAuditInfo: prd.RecipientData.TokenMetadataAuditInfo,
		}); err != nil {
			return errors.Wrapf(err, "failed to register component identity [%d]", i)
		}
	}

	// bind endpoint resolver for all participants
	resolver := endpoint.GetService(context)
	for i, node := range prd.Nodes {
		if err := resolver.Bind(context.Context(), node, prd.Recipients[i]); err != nil {
			return errors.Wrapf(err, "failed to bind node to recipient identity")
		}
	}

	return nil
}

func validatePolicyRecipientData(data *PolicyRecipientData, expectedPolicy string, recipientIdentity token.Identity) (*boolpolicy.PolicyIdentity, [][]byte, error) {
	if data == nil || data.RecipientData == nil {
		return nil, nil, errors.New("invalid policy recipient data: recipient data is nil")
	}
	if data.Policy != expectedPolicy {
		return nil, nil, errors.Errorf("invalid policy recipient data: policy mismatch [%s]!=[%s]", data.Policy, expectedPolicy)
	}
	pi, ok, err := boolpolicy.Unwrap(data.RecipientData.Identity)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to unwrap policy identity")
	}
	if !ok {
		return nil, nil, errors.New("invalid policy recipient data: expected policy identity")
	}
	if pi.Policy != expectedPolicy {
		return nil, nil, errors.Errorf("invalid policy recipient data: embedded policy mismatch [%s]!=[%s]", pi.Policy, expectedPolicy)
	}
	_, auditInfos, err := boolpolicy.UnwrapAuditInfo(data.RecipientData.AuditInfo)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to unwrap policy audit info")
	}
	identities := make([]token.Identity, len(pi.Identities))
	for i, raw := range pi.Identities {
		identities[i] = raw
	}
	if err := validateCompositeRecipientData(identities, auditInfos, data.Nodes, data.Recipients, recipientIdentity); err != nil {
		return nil, nil, errors.Wrap(err, "invalid policy recipient data")
	}

	return pi, auditInfos, nil
}

func validateCompositeRecipientData(
	identities []token.Identity,
	auditInfos [][]byte,
	nodes []view.Identity,
	recipients []token.Identity,
	recipientIdentity token.Identity,
) error {
	if len(identities) == 0 {
		return errors.New("composite identity has no components")
	}
	if len(auditInfos) != len(identities) {
		return errors.Errorf("identity/audit-info count mismatch [%d]!=[%d]", len(identities), len(auditInfos))
	}
	if len(nodes) != len(identities) || len(recipients) != len(identities) {
		return errors.Errorf(
			"identity/node/recipient count mismatch [%d]!=[%d]!=[%d]",
			len(identities),
			len(nodes),
			len(recipients),
		)
	}
	foundRecipient := false
	for i, identity := range identities {
		if !identity.Equal(recipients[i]) {
			return errors.Errorf("component/recipient mismatch at index [%d]", i)
		}
		if identity.Equal(recipientIdentity) {
			foundRecipient = true
		}
	}
	if !foundRecipient {
		return errors.New("responder identity is not a component")
	}

	return nil
}

type ExchangeRecipientIdentitiesView struct {
	TMSID  token.TMSID
	Wallet string
	Other  view.Identity
}

// ExchangeRecipientIdentities executes the ExchangeRecipientIdentitiesView using by passed wallet id to
// derive the recipient identity to send to the passed recipient.
// The function returns, the recipient identity of the sender, the recipient identity of the recipient
func ExchangeRecipientIdentities(context view.Context, walletID string, recipient view.Identity, opts ...token.ServiceOption) (view.Identity, view.Identity, error) {
	options, err := CompileServiceOptions(opts...)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed compiling service options")
	}
	ids, err := context.RunView(&ExchangeRecipientIdentitiesView{
		TMSID:  options.TMSID(),
		Wallet: walletID,
		Other:  recipient,
	})
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed running view")
	}

	return ids.([]view.Identity)[0], ids.([]view.Identity)[1], nil
}

func (f *ExchangeRecipientIdentitiesView) Call(context view.Context) (any, error) {
	ts, err := token.GetManagementService(context, token.WithTMSID(f.TMSID))
	if err != nil {
		return nil, errors.Wrap(err, "failed to get token management service")
	}

	if otherWallet, err := ts.WalletManager().OwnerWallet(context.Context(), f.Other); err == nil {
		other, err := otherWallet.GetRecipientIdentity(context.Context())
		if err != nil {
			return nil, err
		}

		meWallet, err := ts.WalletManager().OwnerWallet(context.Context(), f.Wallet)
		if err != nil {
			return nil, errors.Wrapf(err, "wallet [%s:%s] not found", f.Wallet, f.TMSID)
		}
		me, err := meWallet.GetRecipientIdentity(context.Context())

		if err != nil {
			return nil, err
		}

		return []view.Identity{me, other}, nil
	} else {
		session, err := session2.NewTypedSessionToParty(context, f.Other)
		if err != nil {
			return nil, err
		}

		w, err := ts.WalletManager().OwnerWallet(context.Context(), f.Wallet)
		if err != nil {
			return nil, errors.Wrapf(err, "wallet [%s:%s] not found", f.Wallet, f.TMSID)
		}
		localRecipientData, err := w.GetRecipientData(context.Context())
		if err != nil {
			return nil, errors.WithMessagef(err, "failed getting recipient data, wallet [%s]", w.ID())
		}
		nonce, err := GetRandomNonce()
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate nonce for exchange request")
		}
		request := &ExchangeRecipientRequest{
			TMSID:         f.TMSID,
			WalletID:      f.Other,
			RecipientData: localRecipientData,
			Nonce:         nonce,
		}
		message, err := buildAttestationMessage(request.TMSID, request.WalletID, localRecipientData.Identity, false, "", request.Nonce, session.Info().ID, context.ID())
		if err != nil {
			return nil, errors.Wrap(err, "failed to build exchange initiator attestation message")
		}
		request.Signature, err = signRecipientAttestation(context.Context(), w, message, localRecipientData.Identity, true)
		if err != nil {
			return nil, errors.Wrap(err, "failed to sign exchange initiator attestation")
		}
		if err = session.SendTyped(context.Context(), request, TypeExchangeRecipientRequest); err != nil {
			return nil, err
		}

		resp := &ExchangeRecipientResponse{}
		if err = session.ReceiveTyped(TypeExchangeRecipientResp, resp); err != nil {
			return nil, err
		}
		if resp.RecipientData == nil {
			return nil, errors.New("exchange responder returned empty recipient data")
		}

		// Verify key-ownership attestation
		verifier, err := ts.SigService().OwnerVerifier(context.Context(), resp.RecipientData.Identity)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get verifier for exchange recipient")
		}
		message, err = buildAttestationMessage(request.TMSID, request.WalletID, resp.RecipientData.Identity, false, "", request.Nonce, session.Info().ID, context.ID())
		if err != nil {
			return nil, errors.Wrapf(err, "failed to build attestation message")
		}
		if err = verifier.Verify(message, resp.Signature); err != nil {
			return nil, errors.Wrapf(err, "exchange recipient key-ownership attestation failed")
		}

		if err = ts.WalletManager().RegisterRecipientIdentity(context.Context(), resp.RecipientData); err != nil {
			return nil, err
		}

		logger.DebugfContext(context.Context(), "bind [%s] to other [%s]", resp.RecipientData.Identity, f.Other)
		resolver := endpoint.GetService(context)
		if err = resolver.Bind(context.Context(), f.Other, resp.RecipientData.Identity); err != nil {
			return nil, err
		}

		logger.DebugfContext(context.Context(), "bind me [%s] to [%s]", localRecipientData.Identity, context.Me())
		if err = resolver.Bind(context.Context(), context.Me(), localRecipientData.Identity); err != nil {
			return nil, err
		}

		return []view.Identity{localRecipientData.Identity, resp.RecipientData.Identity}, nil
	}
}

type RespondExchangeRecipientIdentitiesView struct {
	Wallet         string
	WalletSelector ExchangeWalletSelector
}

// RespondExchangeRecipientIdentities executes the RespondExchangeRecipientIdentitiesView.
// The recipient sends back the identity to receive ownership of tokens.
// The identity is taken from the default wallet, unless an ExchangeWalletSelector option
// (see WithExchangeWalletSelector) overrides the choice.
func RespondExchangeRecipientIdentities(context view.Context, opts ...token.ServiceOption) (view.Identity, view.Identity, error) {
	options, err := CompileServiceOptions(opts...)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed compiling service options")
	}
	ids, err := context.RunView(&RespondExchangeRecipientIdentitiesView{
		WalletSelector: getExchangeWalletSelector(options),
	})
	if err != nil {
		return nil, nil, err
	}

	return ids.([]view.Identity)[0], ids.([]view.Identity)[1], nil
}

func (s *RespondExchangeRecipientIdentitiesView) Call(context view.Context) (any, error) {
	session := session2.NewTypedSessionFromContext(context)

	request := &ExchangeRecipientRequest{}
	if err := session.ReceiveTyped(TypeExchangeRecipientRequest, request); err != nil {
		return nil, err
	}
	if len(request.Nonce) == 0 {
		return nil, errors.New("exchange request missing nonce")
	}
	if request.RecipientData == nil {
		return nil, errors.New("exchange request missing recipient data")
	}
	if len(request.Signature) == 0 {
		return nil, errors.New("exchange request missing initiator signature")
	}

	ts, err := token.GetManagementService(context, token.WithTMSID(request.TMSID))
	if err != nil {
		return nil, errors.Wrap(err, "failed to get token management service")
	}
	other := request.RecipientData.Identity
	message, err := buildAttestationMessage(request.TMSID, request.WalletID, other, false, "", request.Nonce, session.Info().ID, context.ID())
	if err != nil {
		return nil, errors.Wrap(err, "failed to build exchange initiator attestation message")
	}
	if err = verifyRecipientAttestation(context.Context(), ts, message, request.RecipientData, request.Signature, false); err != nil {
		return nil, errors.Wrap(err, "exchange initiator key-ownership attestation failed")
	}
	if err = ts.WalletManager().RegisterRecipientIdentity(context.Context(), &RecipientData{
		Identity:               other,
		AuditInfo:              request.RecipientData.AuditInfo,
		TokenMetadata:          request.RecipientData.TokenMetadata,
		TokenMetadataAuditInfo: request.RecipientData.TokenMetadataAuditInfo,
	}); err != nil {
		return nil, err
	}

	wallet := s.Wallet
	if len(wallet) == 0 && len(request.WalletID) != 0 {
		wallet = string(request.WalletID)
	}
	if s.WalletSelector != nil {
		selected, err := s.WalletSelector(request, wallet)
		if err != nil {
			return nil, errors.Wrapf(err, "failed selecting wallet")
		}
		wallet = selected
	}
	w, err := ts.WalletManager().OwnerWallet(context.Context(), wallet)
	if err != nil {
		return nil, errors.Wrapf(err, "wallet [%s] not found", wallet)
	}

	recipientData, err := w.GetRecipientData(context.Context())
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting recipient data, wallet [%s]", w.ID())
	}

	// Sign the request-bound attestation to prove key ownership when the key is local.
	message, err = buildAttestationMessage(request.TMSID, request.WalletID, recipientData.Identity, false, "", request.Nonce, session.Info().ID, context.ID())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to build attestation message")
	}
	sig, err := signRecipientAttestation(context.Context(), w, message, recipientData.Identity, true)
	if err != nil {
		return nil, err
	}

	// Bind locally before sending
	resolver := endpoint.GetService(context)
	if err = resolver.Bind(context.Context(), context.Me(), recipientData.Identity); err != nil {
		return nil, errors.WithMessagef(err, "failed binding recipient data, wallet [%s]", w.ID())
	}
	if err = resolver.Bind(context.Context(), session.Info().Caller, other); err != nil {
		return nil, errors.WithMessagef(err, "failed binding recipient data, wallet [%s]", w.ID())
	}

	resp := &ExchangeRecipientResponse{
		RecipientData: recipientData,
		Signature:     sig,
	}
	if err = session.SendTyped(context.Context(), resp, TypeExchangeRecipientResp); err != nil {
		return nil, errors.WithMessagef(err, "failed sending recipient data, wallet [%s]", w.ID())
	}

	return []token.Identity{recipientData.Identity, other}, nil
}

// recipientAttestation is the canonical structure a responder signs to prove
// ownership of the recipient private key and to bind that proof to one specific
// protocol run. It captures every field of the originating request, plus the
// session and context identifiers, so a signature can neither be replayed nor
// transplanted onto a different request, session, or context.
//
// The bytes are DER-encoded with encoding/asn1: tag-length-value framing makes
// every field boundary explicit, which removes the concatenation ambiguity that
// a flat nonce||identity message allows (e.g. "AB"||"CD" and "ABC"||"D" yield
// the same bytes). That is the extension/field-boundary attack this guards
// against.
type recipientAttestation struct {
	Network   string `asn1:"utf8"`
	Channel   string `asn1:"utf8"`
	Namespace string `asn1:"utf8"`
	WalletID  []byte
	Identity  []byte
	MultiSig  bool
	Policy    string `asn1:"utf8"`
	Nonce     []byte
	SessionID string `asn1:"utf8"`
	ContextID string `asn1:"utf8"`
}

// buildAttestationMessage assembles the DER-encoded bytes a responder signs (and
// an initiator verifies) for a recipient identity request. Both sides observe
// the same session and context identifiers (carried in the message header), so
// they reconstruct identical bytes independently.
func buildAttestationMessage(tmsID token.TMSID, walletID []byte, identity view.Identity, multiSig bool, policy string, nonce []byte, sessionID, contextID string) ([]byte, error) {
	return asn1.Marshal(recipientAttestation{
		Network:   tmsID.Network,
		Channel:   tmsID.Channel,
		Namespace: tmsID.Namespace,
		WalletID:  walletID,
		Identity:  identity,
		MultiSig:  multiSig,
		Policy:    policy,
		Nonce:     nonce,
		SessionID: sessionID,
		ContextID: contextID,
	})
}

func signRecipientAttestation(ctx context.Context, w *token.OwnerWallet, message []byte, identity view.Identity, freshPath bool) ([]byte, error) {
	if w.Remote() {
		if freshPath {
			return nil, errors.Errorf("remote wallet [%s] cannot attest fresh recipient identity locally", w.ID())
		}

		return nil, nil
	}

	signer, err := w.GetSigner(ctx, identity)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get signer for recipient identity")
	}
	sig, err := signer.Sign(message)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to sign attestation")
	}

	return sig, nil
}

func verifyRecipientAttestation(ctx context.Context, tms *token.ManagementService, message []byte, recipientData *RecipientData, signature []byte, echoPath bool) error {
	if len(signature) == 0 {
		if echoPath {
			// Remote wallets on the responder node cannot sign locally; membership was checked there.
			return nil
		}

		return errors.New("responder returned empty signature on fresh path")
	}

	verifier, err := tms.SigService().OwnerVerifier(ctx, recipientData.Identity)
	if err != nil {
		return errors.Wrapf(err, "failed to get verifier for recipient identity")
	}
	if err = verifier.Verify(message, signature); err != nil {
		return errors.Wrapf(err, "recipient key-ownership attestation failed")
	}

	return nil
}

func getRecipientData(opts *token.ServiceOptions) *RecipientData {
	rdBoxed, ok := opts.Params["RecipientData"]
	if !ok {
		return nil
	}

	return rdBoxed.(*RecipientData)
}
