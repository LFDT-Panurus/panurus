/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package token provides the Request type for building and managing token transactions.
// A Request assembles token actions (issue, transfer, redeem) and their metadata,
// handles serialization, validation, and signature collection. It supports both
// fungible and non-fungible tokens with privacy-preserving features.
package token

import (
	"context"
	"maps"
	"slices"

	"github.com/LFDT-Panurus/panurus/token/core/common/meta"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/services/logging"
	"go.uber.org/zap/zapcore"
)

const (
	// TransferMetadataPrefix is the prefix for the metadata of a transfer action
	TransferMetadataPrefix = meta.TransferMetadataPrefix
	// IssueMetadataPrefix is the prefix for the metadata of an issue action
	IssueMetadataPrefix = meta.IssueMetadataPrefix
	// PublicMetadataPrefix is the prefix for the metadata that will be published on the ledger without further validation
	PublicMetadataPrefix = meta.PublicMetadataPrefix
)

// ActionMetadata models the action metadata as a map from string to byte array
type ActionMetadata = map[string][]byte

// Binder binds ephemeral identities to long-term identities for privacy-preserving transactions.
type Binder interface {
	Bind(ctx context.Context, longTerm Identity, ephemeral ...Identity) error
}

type (
	// TokensUpgradeChallenge is the challenge the issuer generates to make sure the client is not cheating
	TokensUpgradeChallenge = driver.TokensUpgradeChallenge
	// TokensUpgradeProof is the proof generated with the respect to a given challenge to prove the validity of the tokens to be upgrade
	TokensUpgradeProof = driver.TokensUpgradeProof
	// RequestAnchor models the anchor of a token request
	RequestAnchor = driver.TokenRequestAnchor
)

// RecipientData contains information about the identity of a token owner
type RecipientData = driver.RecipientData

// IssueOptions contains optional parameters for token issuance operations.
type IssueOptions struct {
	// Attributes is a container of generic options that might be driver specific
	Attributes map[string]any
}

// compileIssueOptions aggregates multiple IssueOption functions into a single IssueOptions struct.
func compileIssueOptions(opts ...IssueOption) (*IssueOptions, error) {
	txOptions := &IssueOptions{}
	for _, opt := range opts {
		if err := opt(txOptions); err != nil {
			return nil, err
		}
	}

	return txOptions, nil
}

// IssueOption is a function that modifies IssueOptions.
type IssueOption func(*IssueOptions) error

// WithIssueAttribute adds a custom attribute to an issue operation.
func WithIssueAttribute(attr string, value any) IssueOption {
	return func(o *IssueOptions) error {
		if o.Attributes == nil {
			o.Attributes = map[string]any{}
		}
		o.Attributes[attr] = value

		return nil
	}
}

// WithIssueMetadata adds metadata to an issue action (automatically prefixed).
func WithIssueMetadata(key string, value []byte) IssueOption {
	return WithIssueAttribute(IssueMetadataPrefix+key, value)
}

// TransferOptions contains optional parameters for token transfer operations.
type TransferOptions struct {
	// Attributes is a container of generic options that might be driver specific
	Attributes map[string]any
	// Selector is the custom token selector to use. If nil, the default will be used.
	Selector Selector
	// TokenIDs to transfer. If empty, the tokens will be selected.
	TokenIDs []*token.ID
	// RestRecipientIdentity is the recipient to which the transfer's leftover (change) amount is
	// assigned when the selected inputs exceed the requested output sum. If nil, the change is
	// assigned to the sender wallet's default recipient identity. Set via WithRestRecipientIdentity.
	RestRecipientIdentity *RecipientData
}

// CompileTransferOptions aggregates multiple TransferOption functions into a single TransferOptions struct.
func CompileTransferOptions(opts ...TransferOption) (*TransferOptions, error) {
	txOptions := &TransferOptions{}
	for _, opt := range opts {
		if err := opt(txOptions); err != nil {
			return nil, err
		}
	}

	return txOptions, nil
}

// TransferOption is a function that modifies TransferOptions.
type TransferOption func(*TransferOptions) error

// WithTokenSelector sets the passed token selector
func WithTokenSelector(selector Selector) TransferOption {
	return func(o *TransferOptions) error {
		o.Selector = selector

		return nil
	}
}

// WithTransferMetadata adds metadata to a transfer action (automatically prefixed).
func WithTransferMetadata(key string, value []byte) TransferOption {
	return WithTransferAttribute(TransferMetadataPrefix+key, value)
}

// WithPublicTransferMetadata adds any data to the public ledger that may be relevant to the application.
// It is also made available to the participants as part of the TransactionRecord.
// The transaction fails if the key already exists on the ledger. The value is not validated.
func WithPublicTransferMetadata(key string, value []byte) TransferOption {
	return WithTransferMetadata(PublicMetadataPrefix+key, value)
}

// WithPublicIssueMetadata adds any data to the public ledger that may be relevant to the application.
// It is also made available to the participants as part of the TransactionRecord.
// The transaction fails if the key already exists on the ledger. The value is not validated.
func WithPublicIssueMetadata(key string, value []byte) IssueOption {
	return WithIssueMetadata(PublicMetadataPrefix+key, value)
}

// WithTokenIDs sets the tokens ids to transfer
func WithTokenIDs(ids ...*token.ID) TransferOption {
	return func(o *TransferOptions) error {
		o.TokenIDs = ids

		return nil
	}
}

// WithTransferAttribute adds a custom attribute to a transfer operation.
func WithTransferAttribute(attr string, value any) TransferOption {
	return func(o *TransferOptions) error {
		if o.Attributes == nil {
			o.Attributes = make(map[string]any)
		}
		o.Attributes[attr] = value

		return nil
	}
}

// WithRestRecipientIdentity sets the recipient data to be used to assign any rest left during a transfer operation
func WithRestRecipientIdentity(recipientData *RecipientData) TransferOption {
	return func(o *TransferOptions) error {
		o.RestRecipientIdentity = recipientData

		return nil
	}
}

// AuditRecord models the audit record returned by the audit command
// It contains the token request's anchor, inputs (with Type and Quantity), and outputs
type AuditRecord struct {
	// Anchor is used to bind the Actions to a given Transaction
	Anchor RequestAnchor
	// Inputs represent the input tokens of the transaction
	Inputs *InputStream
	// Outputs represent the output tokens of the transaction
	Outputs *OutputStream
	// Attributes are metadata which are stored on the public ledger as part of the transaction Actions.
	Attributes map[string][]byte
}

// Issue contains information about an issue operation.
// In particular, it carries the identities of the issuer and the receivers
type Issue struct {
	// Issuer is the issuer of the tokens
	Issuer Identity
	// Receivers is the list of identities of the receivers
	Receivers []Identity
	// ExtraSigners is the list of extra identities that must sign the token request to make it valid.
	// This field is to be used by the token drivers to list any additional identities that must
	// sign the token request.
	ExtraSigners []Identity
}

// Transfer contains information about a transfer operation.
// In particular, it carries the identities of the senders and the receivers
type Transfer struct {
	// Senders is the list of identities of the senders
	Senders []Identity
	// Receivers is the list of identities of the receivers
	Receivers []Identity
	// ExtraSigners is the list of extra identities that must sign the token request to make it valid.
	// This field is to be used by the token drivers to list any additional identities that must
	// sign the token request.
	ExtraSigners []Identity
	// Issuer
	Issuer Identity
}

// SignerWithAction associates a signer identity with its action ID.
// This is used to preserve the action context during signature collection.
type SignerWithAction struct {
	Signer   Identity
	ActionID uint32
}

// Request aggregates token operations that must be performed atomically.
// Operations are represented in a backend agnostic way but driver specific.
type Request struct {
	// Anchor is used to bind the Actions to a given Transaction
	Anchor driver.TokenRequestAnchor
	// Actions contains the token operations.
	Actions *driver.TokenRequest
	// Metadata contains the actions' metadata used to unscramble the content of the actions, if the
	// underlying token driver requires that
	Metadata *driver.TokenRequestMetadata
	// TokenService this request refers to
	TokenService *ManagementService `json:"-"`
}

// NewRequest creates a new empty request for the given token service and anchor
func NewRequest(tokenService *ManagementService, anchor RequestAnchor) *Request {
	return &Request{
		Anchor:       anchor,
		Actions:      &driver.TokenRequest{},
		Metadata:     &driver.TokenRequestMetadata{},
		TokenService: tokenService,
	}
}

// NewRequestFromBytes creates a new request from the given anchor, and whose actions and metadata
// are unmarshalled from the given bytes
func NewRequestFromBytes(tokenService *ManagementService, anchor RequestAnchor, actions []byte, trmRaw []byte) (*Request, error) {
	tr := &driver.TokenRequest{}
	if err := tr.FromBytes(actions); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling token request [%d]", len(actions))
	}
	trm := &driver.TokenRequestMetadata{}
	if len(trmRaw) != 0 {
		if err := trm.FromBytes(trmRaw); err != nil {
			return nil, errors.Wrapf(err, "failed unmarshalling token request metadata [%d]", len(trmRaw))
		}
	}

	return &Request{
		Anchor:       anchor,
		Actions:      tr,
		Metadata:     trm,
		TokenService: tokenService,
	}, nil
}

// NewFullRequestFromBytes creates a new request from the given byte representation
func NewFullRequestFromBytes(tokenService *ManagementService, tr []byte) (*Request, error) {
	request := NewRequest(tokenService, "")
	if err := request.FromBytes(tr); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal request")
	}

	return request, nil
}

// ID returns the anchor of the request
func (r *Request) ID() RequestAnchor {
	return r.Anchor
}

// Issue appends an issue action to the request. The action will be prepared using the provided issuer wallet.
// The action issues to the receiver a token of the passed type and quantity.
// Additional options can be passed to customize the action.
func (r *Request) Issue(ctx context.Context, wallet *IssuerWallet, receiver Identity, typ token.Type, q uint64, opts ...IssueOption) (*IssueAction, error) {
	logger.DebugfContext(ctx, "Start issue")
	logger.DebugfContext(ctx, "Done issue")
	if wallet == nil {
		return nil, errors.Errorf("wallet is nil")
	}
	if typ == "" {
		return nil, errors.Errorf("type is empty")
	}
	if q == 0 {
		return nil, errors.Errorf("q is zero")
	}
	maxTokenValue := r.TokenService.PublicParametersManager().PublicParameters().MaxTokenValue()
	if q > maxTokenValue {
		return nil, errors.Errorf("q is larger than max token value [%d]", maxTokenValue)
	}

	if receiver.IsNone() {
		return nil, errors.Errorf("all recipients should be defined")
	}

	id, err := wallet.GetIssuerIdentity(typ)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting issuer identity for type [%s]", typ)
	}

	opt, err := compileIssueOptions(opts...)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed compiling options [%v]", opts)
	}

	// Compute Issue
	action, metaRaw, err := r.TokenService.tms.IssueService().Issue(
		ctx,
		id,
		typ,
		[]uint64{q},
		[][]byte{receiver},
		&driver.IssueOptions{
			Attributes: opt.Attributes,
		},
	)
	if err != nil {
		return nil, err
	}

	// Append
	actionRaw, err := action.Serialize()
	if err != nil {
		return nil, err
	}
	actionID := uint32(len(r.Actions.Actions)) //nolint:gosec
	r.Actions.Actions = append(r.Actions.Actions, &driver.TypedAction{
		Type: request.ActionType_ACTION_TYPE_ISSUE,
		Raw:  actionRaw,
	})
	r.Metadata.Actions = append(r.Metadata.Actions, &driver.ActionMetadataEntry{
		ActionID:      actionID,
		IssueMetadata: metaRaw,
	})

	return &IssueAction{a: action}, nil
}

// Transfer appends a transfer action to the request. The action will be prepared using the provided owner wallet.
// The action transfers tokens of the passed types to the receivers for the passed quantities.
// In other words, owners[0] will receives values[0], and so on.
// Additional options can be passed to customize the action.
func (r *Request) Transfer(ctx context.Context, wallet *OwnerWallet, typ token.Type, values []uint64, owners []Identity, opts ...TransferOption) (*TransferAction, error) {
	if slices.Contains(values, 0) {
		return nil, errors.Errorf("value is zero")
	}
	opt, err := CompileTransferOptions(opts...)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed compiling options [%v]", opts)
	}
	tokenIDs, outputTokens, err := r.prepareTransfer(ctx, false, wallet, typ, values, owners, opt)
	if err != nil {
		return nil, errors.Wrap(err, "failed preparing transfer")
	}

	r.TokenService.logger.DebugfContext(ctx, "Prepare Transfer Action [id:%s,ins:%d,outs:%d,attr:%d]", r.Anchor, len(tokenIDs), len(outputTokens), len(opt.Attributes))

	ts := r.TokenService.tms.TransferService()

	// Compute transfer
	transfer, transferMetadata, err := ts.Transfer(
		ctx,
		r.Anchor,
		wallet.w,
		tokenIDs,
		outputTokens,
		&driver.TransferOptions{
			Attributes: opt.Attributes,
		},
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed creating transfer action")
	}
	if r.TokenService.logger.IsEnabledFor(zapcore.DebugLevel) {
		// double check
		if err := ts.VerifyTransfer(ctx, transfer, transferMetadata.Outputs); err != nil {
			return nil, errors.Wrap(err, "failed checking generated proof")
		}
	}

	// Append
	raw, err := transfer.Serialize()
	if err != nil {
		return nil, errors.Wrap(err, "failed serializing transfer action")
	}
	actionID := uint32(len(r.Actions.Actions)) //nolint:gosec
	r.Actions.Actions = append(r.Actions.Actions, &driver.TypedAction{
		Type: request.ActionType_ACTION_TYPE_TRANSFER,
		Raw:  raw,
	})
	r.Metadata.Actions = append(r.Metadata.Actions, &driver.ActionMetadataEntry{
		ActionID:         actionID,
		TransferMetadata: transferMetadata,
	})

	return &TransferAction{TransferAction: transfer}, nil
}

// Redeem appends a redeem action to the request. The action will be prepared using the provided owner wallet.
// The action redeems tokens of the passed type for a total amount matching the passed value.
// Additional options can be passed to customize the action.
func (r *Request) Redeem(ctx context.Context, wallet *OwnerWallet, typ token.Type, value uint64, opts ...TransferOption) (*TransferAction, error) {
	opt, err := CompileTransferOptions(opts...)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed compiling options [%v]", opts)
	}
	tokenIDs, outputTokens, err := r.prepareTransfer(ctx, true, wallet, typ, []uint64{value}, []Identity{nil}, opt)
	if err != nil {
		return nil, errors.Wrap(err, "failed preparing transfer")
	}

	r.TokenService.logger.DebugfContext(ctx, "Prepare Redeem Action [ins:%d,outs:%d]", len(tokenIDs), len(outputTokens))

	ts := r.TokenService.tms.TransferService()

	// Compute redeem, it is a transfer with owner set to nil
	transfer, transferMetadata, err := ts.Transfer(
		ctx,
		r.Anchor,
		wallet.w,
		tokenIDs,
		outputTokens,
		&driver.TransferOptions{
			Attributes: opt.Attributes,
		},
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed creating transfer action")
	}

	if r.TokenService.logger.IsEnabledFor(zapcore.DebugLevel) {
		// double check
		if err := ts.VerifyTransfer(ctx, transfer, transferMetadata.Outputs); err != nil {
			return nil, errors.Wrap(err, "failed checking generated proof")
		}
	}

	// Append
	raw, err := transfer.Serialize()
	if err != nil {
		return nil, errors.Wrap(err, "failed serializing transfer action")
	}

	actionID := uint32(len(r.Actions.Actions)) //nolint:gosec
	r.Actions.Actions = append(r.Actions.Actions, &driver.TypedAction{
		Type: request.ActionType_ACTION_TYPE_TRANSFER,
		Raw:  raw,
	})
	r.Metadata.Actions = append(r.Metadata.Actions, &driver.ActionMetadataEntry{
		ActionID:         actionID,
		TransferMetadata: transferMetadata,
	})

	return &TransferAction{transfer}, nil
}

// Upgrade performs an upgrade operation of the passed ledger tokens.
// A proof and its challenge will be used to verify that the request of upgrade is legit.
// If the proof verifies then the passed wallet will be used to issue a new amount of tokens
// matching those whose upgrade has been requested.
func (r *Request) Upgrade(
	ctx context.Context,
	wallet *IssuerWallet,
	receiver Identity,
	challenge TokensUpgradeChallenge,
	tokens []token.LedgerToken,
	proof TokensUpgradeProof,
	opts ...IssueOption,
) (*IssueAction, error) {
	if wallet == nil {
		return nil, errors.Errorf("wallet is nil")
	}
	if len(tokens) == 0 {
		return nil, errors.Errorf("tokens is empty")
	}

	opt, err := compileIssueOptions(opts...)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed compiling options [%v]", opts)
	}

	// Compute Issue
	action, meta, err := r.TokenService.tms.IssueService().Issue(
		ctx,
		nil,
		"",
		nil,
		[][]byte{receiver},
		&driver.IssueOptions{
			Attributes: opt.Attributes,
			TokensUpgradeRequest: &driver.TokenUpgradeRequest{
				Challenge: challenge,
				Tokens:    tokens,
				Proof:     proof,
			},
			Wallet: wallet.w,
		},
	)
	if err != nil {
		return nil, err
	}

	// Append
	raw, err := action.Serialize()
	if err != nil {
		return nil, err
	}
	actionID := uint32(len(r.Actions.Actions)) //nolint:gosec
	r.Actions.Actions = append(r.Actions.Actions, &driver.TypedAction{
		Type: request.ActionType_ACTION_TYPE_ISSUE,
		Raw:  raw,
	})
	r.Metadata.Actions = append(r.Metadata.Actions, &driver.ActionMetadataEntry{
		ActionID:      actionID,
		IssueMetadata: meta,
	})

	return &IssueAction{a: action}, nil
}

// Outputs returns all token outputs created by this request's actions.
func (r *Request) Outputs(ctx context.Context) (*OutputStream, error) {
	return r.outputs(ctx, false)
}

// processIssueOutputsAt deserializes and matches the i-th issue action against its metadata, then
// extracts its outputs.
func (r *Request) processIssueOutputsAt(ctx context.Context, meta *Metadata, i int, issue []byte, counter uint64, failOnMissing bool) ([]*Output, uint64, error) {
	is := r.TokenService.tms.IssueService()
	// deserialize action
	issueAction, err := is.DeserializeIssueAction(issue)
	if err != nil {
		return nil, 0, errors.Wrapf(err, "failed deserializing issue action [%d]", i)
	}
	// get metadata for action
	issueMeta, err := meta.Issue(i)
	if err != nil {
		return nil, 0, errors.Wrapf(err, "failed getting issue metadata [%d]", i)
	}
	if err := issueMeta.Match(&IssueAction{a: issueAction}); err != nil {
		return nil, 0, errors.Wrapf(err, "failed matching issue action with its metadata [%d]", i)
	}

	return r.extractIssueOutputs(ctx, i, counter, issueAction, issueMeta, failOnMissing, false)
}

// processTransferOutputsAt deserializes and matches the i-th transfer action against its
// metadata, then extracts its outputs.
func (r *Request) processTransferOutputsAt(ctx context.Context, meta *Metadata, i int, transfer []byte, counter uint64, failOnMissing bool) ([]*Output, uint64, error) {
	ts := r.TokenService.tms.TransferService()
	// deserialize action
	transferAction, err := ts.DeserializeTransferAction(transfer)
	if err != nil {
		return nil, 0, errors.Wrapf(err, "failed deserializing transfer action [%d]", i)
	}
	// get metadata for action
	transferMeta, err := meta.Transfer(i)
	if err != nil {
		return nil, 0, errors.Wrapf(err, "failed getting transfer metadata [%d]", i)
	}
	if err := transferMeta.Match(&TransferAction{transferAction}); err != nil {
		return nil, 0, errors.Wrapf(err, "failed matching transfer action with its metadata [%d]", i)
	}
	if len(transferAction.GetOutputs()) != len(transferMeta.Outputs) {
		return nil, 0, errors.Errorf("failed matching transfer action with its metadata [%d]: invalid metadata", i)
	}

	return r.extractTransferOutputs(ctx, i, counter, transferAction, transferMeta, failOnMissing, false)
}

func (r *Request) outputs(ctx context.Context, failOnMissing bool) (*OutputStream, error) {
	tms := r.TokenService.tms
	pp := tms.PublicParamsManager().PublicParameters()
	if pp == nil {
		return nil, errors.Errorf("public paramenters not set")
	}

	meta, err := r.GetMetadata()
	if err != nil {
		return nil, err
	}
	var outputs []*Output
	counter := uint64(0)
	for i, issue := range r.Actions.GetIssues() {
		extractedOutputs, newCounter, err := r.processIssueOutputsAt(ctx, meta, i, issue, counter, failOnMissing)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, extractedOutputs...)
		counter = newCounter
	}

	for i, transfer := range r.Actions.GetTransfers() {
		extractedOutputs, newCounter, err := r.processTransferOutputsAt(ctx, meta, i, transfer, counter, failOnMissing)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, extractedOutputs...)
		counter = newCounter
	}

	return NewOutputStream(outputs, tms.PublicParamsManager().PublicParameters().Precision()), nil
}

// expandIssueOutputRecipients builds one *Output per recipient of the j-th issue output (already
// deobfuscated as tok/issuer/recipients), validating each recipient against its metadata.
func (r *Request) expandIssueOutputRecipients(
	ctx context.Context,
	i, j int,
	tok *token.Token,
	issuer Identity,
	recipients []Identity,
	q token.Quantity,
	raw []byte,
	format token.Format,
	issueMeta *IssueMetadata,
	counter uint64,
) ([]*Output, error) {
	var outputs []*Output
	for k, recipient := range recipients {
		metaRecipient := issueMeta.Outputs[j].RecipientAt(k)
		if metaRecipient == nil {
			return nil, errors.Errorf("missing recipient metadata for output [%d,%d]", i, j)
		}
		if !recipient.Equal(metaRecipient.Identity) {
			return nil, errors.Errorf("invalid recipient [%d,%d] [%s:%s]", i, j, recipient, metaRecipient.Identity)
		}
		eID, rID, err := r.TokenService.tms.WalletService().GetEIDAndRH(ctx, recipient, metaRecipient.AuditInfo)
		if err != nil {
			return nil, errors.Wrapf(err, "failed getting enrollment id and revocation handle [%d,%d]", i, j)
		}

		outputs = append(outputs, &Output{
			Token:                *tok,
			ActionIndex:          i,
			Index:                counter,
			Owner:                recipient,
			OwnerAuditInfo:       metaRecipient.AuditInfo,
			EnrollmentID:         eID,
			RevocationHandler:    rID,
			Type:                 tok.Type,
			Quantity:             q,
			Issuer:               issuer,
			LedgerOutput:         raw,
			LedgerOutputFormat:   format,
			LedgerOutputMetadata: issueMeta.Outputs[j].OutputMetadata,
		})
	}

	return outputs, nil
}

// extractIssueOutput extracts (as zero or more *Output) the j-th output of issue action i. It
// returns no outputs (and no error) when the output's metadata was filtered out and
// failOnMissing is false.
func (r *Request) extractIssueOutput(
	ctx context.Context,
	i, j int,
	output driver.Output,
	issueAction driver.IssueAction,
	issueMeta *IssueMetadata,
	precision uint64,
	counter uint64,
	failOnMissing, noOutputForRecipient bool,
) ([]*Output, error) {
	if output == nil {
		return nil, errors.Errorf("%d^th output in issue action [%d] is nil", j, i)
	}

	raw, err := output.Serialize()
	if err != nil {
		return nil, errors.Wrapf(err, "failed deserializing issue action output [%d,%d]", i, j)
	}

	// is the j-th meta present? It might have been filtered out
	if issueMeta.IsOutputAbsent(j) {
		r.TokenService.logger.Debugf("Issue Action Output [%d,%d] is absent", i, j)
		if failOnMissing {
			return nil, errors.Errorf("missing token info for output [%d,%d]", i, j)
		}

		return nil, nil
	}

	// is the j-th meta present? Yes
	tok, issuer, recipients, format, err := r.TokenService.tms.TokensService().Deobfuscate(ctx, raw, issueMeta.Outputs[j].OutputMetadata)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting issue action output in the clear [%d,%d]", i, j)
	}
	if !issuer.Equal(issueAction.GetIssuer()) {
		return nil, errors.Errorf("invalid issuer [%d,%d]", i, j)
	}
	if len(recipients) == 0 {
		return nil, errors.Errorf("missing recipients [%d,%d]", i, j)
	}
	q, err := token.ToQuantity(tok.Quantity, precision)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting quantity [%d,%d]", i, j)
	}
	if noOutputForRecipient {
		return []*Output{{
			Token:                *tok,
			ActionIndex:          i,
			Index:                counter,
			Owner:                tok.Owner,
			Type:                 tok.Type,
			Quantity:             q,
			Issuer:               issuer,
			LedgerOutput:         raw,
			LedgerOutputFormat:   format,
			LedgerOutputMetadata: issueMeta.Outputs[j].OutputMetadata,
		}}, nil
	}

	return r.expandIssueOutputRecipients(ctx, i, j, tok, issuer, recipients, q, raw, format, issueMeta, counter)
}

func (r *Request) extractIssueOutputs(ctx context.Context, i int, counter uint64, issueAction driver.IssueAction, issueMeta *IssueMetadata, failOnMissing, noOutputForRecipient bool) ([]*Output, uint64, error) {
	if len(issueAction.GetOutputs()) != len(issueMeta.Outputs) {
		return nil, 0, errors.Errorf("failed matching issue action with its metadata [%d]: invalid metadata, the number of outputs does not match", i)
	}
	// extract outputs for this action
	pp := r.TokenService.tms.PublicParamsManager().PublicParameters()
	if pp == nil {
		return nil, 0, errors.Errorf("public paramenters not set")
	}
	precision := pp.Precision()
	var outputs []*Output
	for j, output := range issueAction.GetOutputs() {
		produced, err := r.extractIssueOutput(ctx, i, j, output, issueAction, issueMeta, precision, counter, failOnMissing, noOutputForRecipient)
		if err != nil {
			return nil, 0, err
		}
		outputs = append(outputs, produced...)
		counter++
	}

	return outputs, counter, nil
}

// deobfuscatedTransferOutput holds the cleartext form of a single transfer output, deobfuscated
// against its metadata.
type deobfuscatedTransferOutput struct {
	tok                *token.Token
	issuer             Identity
	recipients         []Identity
	ledgerOutput       []byte
	ledgerOutputFormat token.Format
	q                  token.Quantity
}

// deobfuscateTransferOutputAt serializes and deobfuscates the j-th transfer output. absent is
// true (with a nil result and error) when the output's metadata was filtered out and
// failOnMissing is false — the caller should skip it.
func (r *Request) deobfuscateTransferOutputAt(
	ctx context.Context,
	i, j int,
	output driver.Output,
	transferAction driver.TransferAction,
	transferMeta *TransferMetadata,
	precision uint64,
	failOnMissing bool,
) (result *deobfuscatedTransferOutput, absent bool, err error) {
	if output == nil {
		return nil, false, errors.Errorf("%d^th output in transfer action [%d] is nil", j, i)
	}
	ledgerOutput, err := output.Serialize()
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed deserializing transfer action output [%d,%d]", i, j)
	}
	// is the j-th meta present? It might have been filtered out
	if transferMeta.IsOutputAbsent(j) || len(transferMeta.Outputs[j].OutputMetadata) == 0 {
		r.TokenService.logger.Debugf("Transfer Action Output [%d,%d] is absent", i, j)
		if failOnMissing {
			return nil, false, errors.Errorf("missing token info for output [%d,%d]", i, j)
		}

		return nil, true, nil
	}
	// is the j-th meta present? Yes
	tok, issuer, recipients, ledgerOutputFormat, err := r.TokenService.tms.TokensService().Deobfuscate(ctx, ledgerOutput, transferMeta.Outputs[j].OutputMetadata)
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed getting transfer action output in the clear [%d,%d]", i, j)
	}
	// For a redeem output (empty owner), the per-output metadata does not carry the
	// issuer identity: it is only available at the transfer action level. Recover it
	// so that downstream processing can attribute the redeem to its issuer.
	if len(tok.Owner) == 0 && issuer.IsNone() {
		issuer = transferAction.GetIssuer()
	}
	if len(recipients) == 0 {
		// Add an empty recipient
		recipients = append(recipients, Identity{})
	}
	if len(issuer) == 0 && output.IsRedeem() {
		issuer = transferAction.GetIssuer()
	}
	q, err := token.ToQuantity(tok.Quantity, precision)
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed getting quantity [%d,%d]", i, j)
	}

	return &deobfuscatedTransferOutput{
		tok: tok, issuer: issuer, recipients: recipients,
		ledgerOutput: ledgerOutput, ledgerOutputFormat: ledgerOutputFormat, q: q,
	}, false, nil
}

// buildTransferOutputForAllRecipients builds a single *Output owned by the token's own owner
// (used when noOutputForRecipient is set), after validating every recipient against its metadata.
func buildTransferOutputForAllRecipients(i, j int, d *deobfuscatedTransferOutput, transferMeta *TransferMetadata, counter uint64) (*Output, error) {
	for k, recipient := range d.recipients {
		metaRecipient := transferMeta.Outputs[j].RecipientAt(k)
		if metaRecipient == nil {
			return nil, errors.Errorf("missing recipient metadata for output [%d,%d]", i, j)
		}
		if !recipient.Equal(metaRecipient.Identity) {
			return nil, errors.Errorf("invalid recipient [%d,%d] [%s:%s]", i, j, recipient, metaRecipient.Identity)
		}
	}

	return &Output{
		Token:                *d.tok,
		ActionIndex:          i,
		Index:                counter,
		Owner:                d.tok.Owner,
		OwnerAuditInfo:       transferMeta.Outputs[j].OutputAuditInfo,
		EnrollmentID:         "", // not available here
		RevocationHandler:    "", // not available here
		Type:                 d.tok.Type,
		Quantity:             d.q,
		LedgerOutput:         d.ledgerOutput,
		LedgerOutputFormat:   d.ledgerOutputFormat,
		LedgerOutputMetadata: transferMeta.Outputs[j].OutputMetadata,
		Issuer:               d.issuer,
	}, nil
}

// buildTransferOutputsPerRecipient builds one *Output per recipient of the j-th transfer output.
func (r *Request) buildTransferOutputsPerRecipient(ctx context.Context, i, j int, d *deobfuscatedTransferOutput, transferMeta *TransferMetadata, counter uint64, recipientCounter int) ([]*Output, int, error) {
	var outputs []*Output
	for k, recipient := range d.recipients {
		metaRecipient := transferMeta.Outputs[j].RecipientAt(k)
		if metaRecipient == nil {
			return nil, recipientCounter, errors.Errorf("missing recipient metadata for output [%d,%d]", i, j)
		}
		if !recipient.Equal(metaRecipient.Identity) {
			return nil, recipientCounter, errors.Errorf("invalid recipient [%d,%d] [%s:%s]", i, j, recipient, metaRecipient.Identity)
		}
		var eID string
		var rID string
		var receiverAuditInfo []byte
		var targetLedgerOutput []byte
		if len(d.tok.Owner) != 0 {
			receiverAuditInfo = metaRecipient.AuditInfo
			var err error
			eID, rID, err = r.TokenService.tms.WalletService().GetEIDAndRH(ctx, recipient, receiverAuditInfo)
			if err != nil {
				return nil, recipientCounter, errors.Wrapf(err, "failed getting enrollment id and revocation handle [%d,%d]", i, recipientCounter)
			}
			targetLedgerOutput = d.ledgerOutput
		}
		r.TokenService.logger.Debugf("Transfer Action Output [%d,%d][%s:%d] is present, extract [%s]", i, j, r.Anchor, counter, Hashable(d.ledgerOutput))
		outputs = append(outputs, &Output{
			Token:                *d.tok,
			ActionIndex:          i,
			Index:                counter,
			Owner:                recipient,
			OwnerAuditInfo:       receiverAuditInfo,
			EnrollmentID:         eID,
			RevocationHandler:    rID,
			Type:                 d.tok.Type,
			Quantity:             d.q,
			LedgerOutput:         targetLedgerOutput,
			LedgerOutputFormat:   d.ledgerOutputFormat,
			LedgerOutputMetadata: transferMeta.Outputs[j].OutputMetadata,
			Issuer:               d.issuer,
		})
		recipientCounter++
	}

	return outputs, recipientCounter, nil
}

// extractTransferOutputAt extracts (as zero or more *Output) the j-th output of transfer action
// i, returning the updated recipientCounter.
func (r *Request) extractTransferOutputAt(
	ctx context.Context,
	i, j int,
	output driver.Output,
	transferAction driver.TransferAction,
	transferMeta *TransferMetadata,
	precision uint64,
	counter uint64,
	recipientCounter int,
	failOnMissing, noOutputForRecipient bool,
) ([]*Output, int, error) {
	d, absent, err := r.deobfuscateTransferOutputAt(ctx, i, j, output, transferAction, transferMeta, precision, failOnMissing)
	if err != nil {
		return nil, recipientCounter, err
	}
	if absent {
		return nil, recipientCounter, nil
	}

	if noOutputForRecipient {
		out, err := buildTransferOutputForAllRecipients(i, j, d, transferMeta, counter)
		if err != nil {
			return nil, recipientCounter, err
		}

		return []*Output{out}, recipientCounter, nil
	}

	return r.buildTransferOutputsPerRecipient(ctx, i, j, d, transferMeta, counter, recipientCounter)
}

func (r *Request) extractTransferOutputs(ctx context.Context, i int, counter uint64, transferAction driver.TransferAction, transferMeta *TransferMetadata, failOnMissing, noOutputForRecipient bool) ([]*Output, uint64, error) {
	tms := r.TokenService.tms
	if tms.PublicParamsManager() == nil || tms.PublicParamsManager().PublicParameters() == nil {
		return nil, 0, errors.New("can't get inputs: invalid token service in request")
	}
	precision := tms.PublicParamsManager().PublicParameters().Precision()
	var outputs []*Output
	recipientCounter := 0
	for j, output := range transferAction.GetOutputs() {
		produced, newRecipientCounter, err := r.extractTransferOutputAt(ctx, i, j, output, transferAction, transferMeta, precision, counter, recipientCounter, failOnMissing, noOutputForRecipient)
		if err != nil {
			return nil, 0, err
		}
		recipientCounter = newRecipientCounter
		outputs = append(outputs, produced...)
		counter++
	}

	return outputs, counter, nil
}

// Inputs returns all token inputs consumed by this request's transfer actions.
// Note: Type and Quantity are not included (use AuditRecord for full details).
func (r *Request) Inputs(ctx context.Context) (*InputStream, error) {
	return r.inputs(ctx, false)
}

func (r *Request) inputs(ctx context.Context, failOnMissing bool) (*InputStream, error) {
	tms := r.TokenService.tms
	if tms.PublicParamsManager() == nil || tms.PublicParamsManager().PublicParameters() == nil {
		return nil, errors.New("can't get inputs: invalid token service in request")
	}
	meta, err := r.GetMetadata()
	if err != nil {
		return nil, err
	}
	var inputs []*Input
	ts := tms.TransferService()
	transfers := r.Actions.GetTransfers()
	for i, transfer := range transfers {
		// deserialize action
		transferAction, err := ts.DeserializeTransferAction(transfer)
		if err != nil {
			return nil, errors.Wrapf(err, "failed deserializing transfer action [%d]", i)
		}
		// get metadata for action
		transferMeta, err := meta.Transfer(i)
		if err != nil {
			return nil, errors.Wrapf(err, "failed getting transfer metadata [%d]", i)
		}
		if err := transferMeta.Match(&TransferAction{transferAction}); err != nil {
			return nil, errors.Wrapf(err, "failed matching transfer action with its metadata [%d]", i)
		}

		// we might not have TokenIDs if they have been filtered
		if len(transferMeta.Inputs) == 0 && failOnMissing {
			return nil, errors.Errorf("missing token ids for transfer [%d]", i)
		}

		extractedInputs, err := r.extractTransferInputs(ctx, i, transferMeta, failOnMissing)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, extractedInputs...)
	}

	return NewInputStream(r.TokenService.Vault().NewQueryEngine(), inputs, tms.PublicParamsManager().PublicParameters().Precision()), nil
}

func (r *Request) extractIssueInputs(actionIndex int, metadata *IssueMetadata) ([]*Input, error) {
	var inputs []*Input
	for j, input := range metadata.Inputs {
		inputs = append(inputs, NewInput(actionIndex, j, input.TokenID))
	}

	return inputs, nil
}

func (r *Request) extractTransferInputs(ctx context.Context, actionIndex int, metadata *TransferMetadata, failOnMissing bool) ([]*Input, error) {
	// Iterate over the metadata.SenderAuditInfos because we know that there will be at least one
	// sender, but it might be that there are not token IDs due to filtering.
	tms := r.TokenService.tms
	var inputs []*Input
	for j, input := range metadata.Inputs {
		// The recipient might be missing because it has been filtered out. Skip in this case
		if metadata.IsInputAbsent(j) {
			if failOnMissing {
				return nil, errors.Errorf("missing receiver for transfer [%d,%d]", actionIndex, j)
			}

			continue
		}

		for _, sender := range input.Senders {
			eID, rID, err := tms.WalletService().GetEIDAndRH(ctx, sender.Identity, sender.AuditInfo)
			if err != nil {
				return nil, errors.Wrapf(err, "failed getting enrollment id and revocation handle [%d,%d]", actionIndex, j)
			}

			in := NewInput(actionIndex, j, metadata.TokenIDAt(j))
			in.Owner = sender.Identity
			in.OwnerAuditInfo = sender.AuditInfo
			in.EnrollmentID = eID
			in.RevocationHandler = rID
			inputs = append(inputs, in)
		}
	}

	return inputs, nil
}

// InputsAndOutputs returns both inputs and outputs along with action metadata.
func (r *Request) InputsAndOutputs(ctx context.Context) (*InputStream, *OutputStream, map[string][]byte, error) {
	return r.inputsAndOutputs(ctx, false, false, false)
}

// InputsAndOutputsNoRecipients returns inputs and outputs without recipient identity information.
func (r *Request) InputsAndOutputsNoRecipients(ctx context.Context) (*InputStream, *OutputStream, error) {
	is, os, _, err := r.inputsAndOutputs(ctx, false, false, true)

	return is, os, err
}

// processIssueInputsAndOutputsAt deserializes and matches the i-th issue action against its
// metadata, optionally verifies it, then extracts its inputs and outputs, merging its metadata
// attributes into attributes.
func (r *Request) processIssueInputsAndOutputsAt(
	ctx context.Context,
	meta *Metadata,
	i int,
	issue []byte,
	counter uint64,
	attributes map[string][]byte,
	failOnMissing, verifyActions, noOutputForRecipient bool,
) ([]*Input, []*Output, uint64, error) {
	issueService := r.TokenService.tms.IssueService()
	issueAction, err := issueService.DeserializeIssueAction(issue)
	if err != nil {
		return nil, nil, 0, errors.Wrapf(err, "failed deserializing issue action [%d]", i)
	}
	maps.Copy(attributes, issueAction.GetMetadata())

	issueMeta, err := meta.Issue(i)
	if err != nil {
		return nil, nil, 0, errors.Wrapf(err, "failed getting issue metadata [%d]", i)
	}
	if err := issueMeta.Match(&IssueAction{a: issueAction}); err != nil {
		return nil, nil, 0, errors.Wrapf(err, "failed matching issue action with its metadata [%d]", i)
	}

	if verifyActions {
		if err := issueService.VerifyIssue(ctx, issueAction, issueMeta.Outputs); err != nil {
			return nil, nil, 0, errors.WithMessagef(err, "failed verifying issue action")
		}
	}

	inputs, err := r.extractIssueInputs(i, issueMeta)
	if err != nil {
		return nil, nil, 0, err
	}

	outputs, newCounter, err := r.extractIssueOutputs(ctx, i, counter, issueAction, issueMeta, failOnMissing, noOutputForRecipient)
	if err != nil {
		return nil, nil, 0, err
	}

	return inputs, outputs, newCounter, nil
}

// processTransferInputsAndOutputsAt deserializes and matches the i-th transfer action against its
// metadata, optionally verifies it, then extracts its inputs and outputs, merging its metadata
// attributes into attributes.
func (r *Request) processTransferInputsAndOutputsAt(
	ctx context.Context,
	meta *Metadata,
	i int,
	transfer []byte,
	counter uint64,
	attributes map[string][]byte,
	failOnMissing, verifyActions, noOutputForRecipient bool,
) ([]*Input, []*Output, uint64, error) {
	ts := r.TokenService.tms.TransferService()
	transferAction, err := ts.DeserializeTransferAction(transfer)
	if err != nil {
		return nil, nil, 0, errors.Wrapf(err, "failed deserializing transfer action [%d]", i)
	}
	maps.Copy(attributes, transferAction.GetMetadata())

	transferMeta, err := meta.Transfer(i)
	if err != nil {
		return nil, nil, 0, errors.Wrapf(err, "failed getting transfer metadata [%d]", i)
	}
	if err := transferMeta.Match(&TransferAction{transferAction}); err != nil {
		return nil, nil, 0, errors.Wrapf(err, "failed matching transfer action with its metadata [%d]", i)
	}
	if verifyActions {
		if err := ts.VerifyTransfer(ctx, transferAction, transferMeta.Outputs); err != nil {
			return nil, nil, 0, errors.WithMessagef(err, "failed verifying transfer action")
		}
	}

	// we might not have TokenIDs if they have been filtered
	if len(transferMeta.Inputs) == 0 && failOnMissing {
		return nil, nil, 0, errors.Errorf("missing token ids for transfer [%d]", i)
	}

	inputs, err := r.extractTransferInputs(ctx, i, transferMeta, failOnMissing)
	if err != nil {
		return nil, nil, 0, err
	}

	outputs, newCounter, err := r.extractTransferOutputs(ctx, i, counter, transferAction, transferMeta, failOnMissing, noOutputForRecipient)
	if err != nil {
		return nil, nil, 0, err
	}

	return inputs, outputs, newCounter, nil
}

func (r *Request) inputsAndOutputs(ctx context.Context, failOnMissing, verifyActions, noOutputForRecipient bool) (*InputStream, *OutputStream, ActionMetadata, error) {
	tms := r.TokenService.tms
	if tms.PublicParamsManager() == nil || tms.PublicParamsManager().PublicParameters() == nil {
		return nil, nil, nil, errors.New("can't get inputs: invalid token service in request")
	}
	meta, err := r.GetMetadata()
	if err != nil {
		return nil, nil, nil, err
	}
	var inputs []*Input
	var outputs []*Output
	attributes := map[string][]byte{}
	counter := uint64(0)

	for i, issue := range r.Actions.GetIssues() {
		extractedInputs, extractedOutputs, newCounter, err := r.processIssueInputsAndOutputsAt(ctx, meta, i, issue, counter, attributes, failOnMissing, verifyActions, noOutputForRecipient)
		if err != nil {
			return nil, nil, nil, err
		}
		inputs = append(inputs, extractedInputs...)
		outputs = append(outputs, extractedOutputs...)
		counter = newCounter
	}

	for i, transfer := range r.Actions.GetTransfers() {
		extractedInputs, extractedOutputs, newCounter, err := r.processTransferInputsAndOutputsAt(ctx, meta, i, transfer, counter, attributes, failOnMissing, verifyActions, noOutputForRecipient)
		if err != nil {
			return nil, nil, nil, err
		}
		inputs = append(inputs, extractedInputs...)
		outputs = append(outputs, extractedOutputs...)
		counter = newCounter
	}

	precision := tms.PublicParamsManager().PublicParameters().Precision()
	inputStream := NewInputStream(r.TokenService.Vault().NewQueryEngine(), inputs, precision)
	os := NewOutputStream(outputs, precision)

	return inputStream, os, attributes, nil
}

// IsValid validates the request structure and verifies all actions.
func (r *Request) IsValid(ctx context.Context) error {
	// check request fields
	if r.TokenService == nil {
		return errors.New("invalid token service in request")
	}
	if r.Actions == nil {
		return errors.New("invalid actions in request")
	}
	if r.Metadata == nil {
		return errors.New("invalid metadata in request")
	}

	// check inputs, outputs, and verify actions
	if _, _, _, err := r.inputsAndOutputs(ctx, false, true, false); err != nil {
		return errors.WithMessagef(err, "failed verifying inputs and outputs")
	}

	return nil
}

// MarshalToAudit serializes the request for auditor signatures (excludes metadata).
func (r *Request) MarshalToAudit() ([]byte, error) {
	if r.Actions == nil {
		return nil, errors.Errorf("failed to marshal request in tx [%s] for audit", r.Anchor)
	}

	return r.Actions.MarshalToMessageToSign([]byte(r.Anchor))
}

// MarshalToSign serializes the request for participant signatures.
func (r *Request) MarshalToSign() ([]byte, error) {
	if r.Actions == nil {
		return nil, errors.Errorf("failed to marshal request in tx [%s] for signing", r.Anchor)
	}

	return r.Actions.MarshalToMessageToSign([]byte(r.Anchor))
}

// RequestToBytes serializes only the actions (excludes metadata and anchor).
func (r *Request) RequestToBytes() ([]byte, error) {
	if r.Actions == nil {
		return nil, errors.Errorf("failed to marshal request in tx [%s]", r.Anchor)
	}

	return r.Actions.Bytes()
}

// Bytes serializes the complete request (anchor, actions, and metadata).
func (r *Request) Bytes() ([]byte, error) {
	requestProto, err := r.Actions.ToProtos()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to marshal request in tx [%s]", r.Anchor)
	}
	metadataProto, err := r.Metadata.ToProtos()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to marshal metadata in tx [%s]", r.Anchor)
	}
	requestWithMetadata := &request.TokenRequestWithMetadata{
		Version:  driver.ProtocolV1,
		Anchor:   string(r.Anchor),
		Request:  requestProto,
		Metadata: metadataProto,
	}

	return proto.Marshal(requestWithMetadata)
}

// FromBytes deserializes a request from bytes, replacing current content.
func (r *Request) FromBytes(raw []byte) error {
	requestWithMetadata := &request.TokenRequestWithMetadata{}
	if err := proto.Unmarshal(raw, requestWithMetadata); err != nil {
		return errors.Wrapf(err, "failed unmarshaling request")
	}
	// assert version
	if requestWithMetadata.Version != driver.ProtocolV1 {
		return errors.Errorf("invalid token request with metadata version, expected [%d], got [%d]", driver.ProtocolV1, requestWithMetadata.Version)
	}

	r.Anchor = RequestAnchor(requestWithMetadata.Anchor)
	if requestWithMetadata.Request != nil {
		if err := r.Actions.FromProtos(requestWithMetadata.Request); err != nil {
			return errors.Wrapf(err, "failed unmarshalling actions")
		}
	}
	if requestWithMetadata.Metadata != nil {
		if err := r.Metadata.FromProtos(requestWithMetadata.Metadata); err != nil {
			return errors.Wrapf(err, "failed unmarshalling metadata")
		}
	}

	return nil
}

// AddAuditorSignature appends an auditor's signature to the request.
func (r *Request) AddAuditorSignature(identity Identity, sigma []byte) {
	r.Actions.Signatures = append(r.Actions.Signatures, &driver.RequestSignature{
		Auditor: &driver.AuditorSignature{
			Identity:  identity,
			Signature: sigma,
		},
	})
}

// AppendSignatures appends the action signatures for all issue and transfer
// signers from sigmas, keyed by signer unique ID. Signatures already attached
// to the request, e.g. via AddAuditorSignature, are left in place. Returns
// true if every signer has a signature in sigmas.
func (r *Request) AppendSignatures(sigmas map[string][]byte) bool {
	issueSignersWithActions := r.IssueSignersWithActions()
	transferSignersWithActions := r.TransferSignersWithActions()

	// Combine all signers with their action IDs
	allSignersWithActions := append(issueSignersWithActions, transferSignersWithActions...)

	signatures := make([]*driver.RequestSignature, 0, len(allSignersWithActions))
	all := true

	for i, signerWithAction := range allSignersWithActions {
		signature := &driver.RequestSignature{
			Action: &driver.ActionSignature{
				ActionID: signerWithAction.ActionID,
			},
		}
		if sigma, ok := sigmas[signerWithAction.Signer.UniqueID()]; ok {
			signature.Action.Signature = sigma
			r.TokenService.logger.Debugf("signature [%d] for signer [%s] with actionID [%d] is [%s]",
				i, signerWithAction.Signer, signerWithAction.ActionID, logging.SHA256Base64(sigma))
		} else {
			all = false
			r.TokenService.logger.Debugf("signature [%d] for signer [%s] with actionID [%d] not found",
				i, signerWithAction.Signer, signerWithAction.ActionID)
		}
		signatures = append(signatures, signature)
	}

	r.Actions.Signatures = append(r.Actions.Signatures, signatures...)

	return all
}

// TransferSigners returns all identities that must sign transfer actions.
func (r *Request) TransferSigners() []Identity {
	signers := make([]Identity, 0)
	for _, transfer := range r.Transfers() {
		signers = append(signers, transfer.Senders...)
		if transfer.Issuer != nil { // add also the identity of the issuer, if specified
			signers = append(signers, transfer.Issuer)
		}
		signers = append(signers, transfer.ExtraSigners...)
	}

	return signers
}

// IssueSigners returns all identities that must sign issue actions.
func (r *Request) IssueSigners() []Identity {
	signers := make([]Identity, 0)
	for _, issue := range r.Issues() {
		signers = append(signers, issue.Issuer)
		signers = append(signers, issue.ExtraSigners...)
	}

	return signers
}

// IssueSignersWithActions returns all identities that must sign issue actions along with their action IDs.
// This preserves the action context needed for correct ActionSignature construction.
func (r *Request) IssueSignersWithActions() []SignerWithAction {
	signers := make([]SignerWithAction, 0)
	issues := r.Issues()
	issueIndex := 0

	for _, action := range r.Metadata.Actions {
		if action.IssueMetadata != nil {
			if issueIndex >= len(issues) {
				break
			}
			issue := issues[issueIndex]
			issueIndex++

			// Add issuer
			signers = append(signers, SignerWithAction{
				Signer:   issue.Issuer,
				ActionID: action.ActionID,
			})

			// Add extra signers
			for _, extraSigner := range issue.ExtraSigners {
				signers = append(signers, SignerWithAction{
					Signer:   extraSigner,
					ActionID: action.ActionID,
				})
			}
		}
	}

	return signers
}

// TransferSignersWithActions returns all identities that must sign transfer actions along with their action IDs.
// This preserves the action context needed for correct ActionSignature construction.
func (r *Request) TransferSignersWithActions() []SignerWithAction {
	signers := make([]SignerWithAction, 0)
	transfers := r.Transfers()
	transferIndex := 0

	for _, action := range r.Metadata.Actions {
		if action.TransferMetadata != nil {
			if transferIndex >= len(transfers) {
				break
			}
			transfer := transfers[transferIndex]
			transferIndex++

			// Add senders
			for _, sender := range transfer.Senders {
				signers = append(signers, SignerWithAction{
					Signer:   sender,
					ActionID: action.ActionID,
				})
			}

			// Add issuer if specified
			if transfer.Issuer != nil {
				signers = append(signers, SignerWithAction{
					Signer:   transfer.Issuer,
					ActionID: action.ActionID,
				})
			}

			// Add extra signers
			for _, extraSigner := range transfer.ExtraSigners {
				signers = append(signers, SignerWithAction{
					Signer:   extraSigner,
					ActionID: action.ActionID,
				})
			}
		}
	}

	return signers
}

// SetTokenService assigns the TMS to this request.
func (r *Request) SetTokenService(service *ManagementService) {
	r.TokenService = service
}

// BindTo binds all external identities (senders/receivers not owned by caller) to the specified identity.
// Used for privacy-preserving transactions to link ephemeral identities.
// bindIfNotMine binds candidate to identity via binder, unless candidate already has a local
// wallet (in which case it's "me" and doesn't need binding).
func (r *Request) bindIfNotMine(ctx context.Context, binder Binder, identity, candidate Identity, logMsg, errMsg string) error {
	if w := r.TokenService.WalletManager().Wallet(ctx, candidate); w != nil {
		// this is me, skip
		return nil
	}
	r.TokenService.logger.DebugfContext(ctx, logMsg, candidate, identity)
	if err := binder.Bind(ctx, identity, candidate); err != nil {
		return errors.Wrap(err, errMsg)
	}

	return nil
}

// bindTransferSenders binds every non-local sender identity in transfer's inputs to identity.
func (r *Request) bindTransferSenders(ctx context.Context, binder Binder, identity Identity, transfer *driver.TransferMetadata) error {
	for _, input := range transfer.Inputs {
		for _, sender := range input.Senders {
			if err := r.bindIfNotMine(ctx, binder, identity, sender.Identity, "bind sender [%s] to [%s]", "failed binding sender identities"); err != nil {
				return err
			}
		}
	}

	return nil
}

// bindTransferExtraSigners binds every non-local extra-signer identity in transfer to identity.
func (r *Request) bindTransferExtraSigners(ctx context.Context, binder Binder, identity Identity, transfer *driver.TransferMetadata) error {
	for _, eid := range transfer.ExtraSigners {
		if err := r.bindIfNotMine(ctx, binder, identity, eid.Identity, "bind extra signer [%s] to [%s]", "failed binding sender identities"); err != nil {
			return err
		}
	}

	return nil
}

// bindTransferReceivers binds every non-local receiver identity in transfer's outputs to
// identity (as a potential future sender).
func (r *Request) bindTransferReceivers(ctx context.Context, binder Binder, identity Identity, transfer *driver.TransferMetadata) error {
	for _, output := range transfer.Outputs {
		for _, receiver := range output.Receivers {
			if err := r.bindIfNotMine(ctx, binder, identity, receiver.Identity, "bind receiver as sender [%s] to [%s]", "failed binding receiver identities"); err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *Request) BindTo(ctx context.Context, binder Binder, identity Identity) error {
	for _, action := range r.Metadata.Actions {
		if action.TransferMetadata == nil {
			continue
		}
		transfer := action.TransferMetadata

		if err := r.bindTransferSenders(ctx, binder, identity, transfer); err != nil {
			return err
		}
		if err := r.bindTransferExtraSigners(ctx, binder, identity, transfer); err != nil {
			return err
		}
		if err := r.bindTransferReceivers(ctx, binder, identity, transfer); err != nil {
			return err
		}
	}

	return nil
}

// Issues returns all issue actions in this request.
func (r *Request) Issues() []*Issue {
	var issues []*Issue
	for _, action := range r.Metadata.Actions {
		if action.IssueMetadata != nil {
			// Convert []AuditableIdentity to []Identity for ExtraSigners
			extraSigners := make([]Identity, len(action.IssueMetadata.ExtraSigners))
			for i, es := range action.IssueMetadata.ExtraSigners {
				extraSigners[i] = es.Identity
			}
			issues = append(issues, &Issue{
				Issuer:       action.IssueMetadata.Issuer.Identity,
				Receivers:    action.IssueMetadata.Receivers(),
				ExtraSigners: extraSigners,
			})
		}
	}

	return issues
}

// Transfers returns all transfer actions in this request.
func (r *Request) Transfers() []*Transfer {
	var transfers []*Transfer
	for _, action := range r.Metadata.Actions {
		if action.TransferMetadata != nil {
			// Convert []AuditableIdentity to []Identity for ExtraSigners
			extraSigners := make([]Identity, len(action.TransferMetadata.ExtraSigners))
			for i, es := range action.TransferMetadata.ExtraSigners {
				extraSigners[i] = es.Identity
			}
			transfers = append(transfers, &Transfer{
				Senders:      action.TransferMetadata.Senders(),
				Receivers:    action.TransferMetadata.Receivers(),
				ExtraSigners: extraSigners,
				Issuer:       action.TransferMetadata.Issuer.Identity,
			})
		}
	}

	return transfers
}

// AuditCheck validates the request and performs auditor-specific checks.
func (r *Request) AuditCheck(ctx context.Context) error {
	r.TokenService.logger.DebugfContext(ctx, "audit check request [%s] on tms [%s]", r.Anchor, r.TokenService.ID())
	if err := r.IsValid(ctx); err != nil {
		return err
	}

	return r.TokenService.tms.AuditorService().AuditorCheck(
		ctx,
		r.Actions,
		r.Metadata,
		r.Anchor,
	)
}

// AuditRecord returns the complete audit record with inputs, outputs, and metadata.
// Includes full token details (type, quantity, owner info) for auditing purposes.
func (r *Request) AuditRecord(ctx context.Context) (*AuditRecord, error) {
	inputs, outputs, attr, err := r.inputsAndOutputs(ctx, true, false, false)
	if err != nil {
		return nil, err
	}

	// load the tokens corresponding to the input token ids
	ids := inputs.IDs()
	toks, err := r.TokenService.Vault().NewQueryEngine().ListAuditTokens(ctx, ids...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed retrieving inputs for auditing")
	}
	if len(ids) != len(toks) {
		return nil, errors.Errorf("retrieved less inputs than those in the transaction [%d][%d]", len(ids), len(toks))
	}

	// populate type and quantity
	pp := r.TokenService.tms.PublicParamsManager().PublicParameters()
	if pp == nil {
		return nil, errors.Errorf("public paramenters not set")
	}
	precision := pp.Precision()
	for i := range ids {
		in := inputs.At(i)
		if toks[i] == nil {
			return nil, errors.Errorf("failed to audit inputs: nil input at [%d]th input", i)
		}
		in.Type = toks[i].Type
		q, err := token.ToQuantity(toks[i].Quantity, precision)
		if err != nil {
			return nil, errors.Wrapf(err, "failed converting quantity [%s]", toks[i].Quantity)
		}
		in.Quantity = q

		// retrieve the owner's audit info
		ownerAuditInfo, err := r.TokenService.tms.WalletService().GetAuditInfo(ctx, toks[i].Owner)
		if err != nil {
			return nil, errors.Wrapf(err, "failed getting audit info for owner [%s]", toks[i].Owner)
		}
		// The wallet service only knows the identities registered on this node
		// and returns nil for the others, so keep the audit info the request
		// metadata already carries when the local lookup finds nothing.
		if len(ownerAuditInfo) != 0 {
			in.OwnerAuditInfo = ownerAuditInfo
		}
	}

	return &AuditRecord{
		Anchor:     r.Anchor,
		Inputs:     inputs,
		Outputs:    outputs,
		Attributes: attr,
	}, nil
}

// ApplicationMetadata retrieves application-specific metadata by key (returns nil if not found).
func (r *Request) ApplicationMetadata(k string) []byte {
	if len(r.Metadata.Application) == 0 {
		return nil
	}

	return r.Metadata.Application[k]
}

// SetApplicationMetadata stores application-specific metadata (format not validated by SDK).
func (r *Request) SetApplicationMetadata(k string, v []byte) {
	if r.Metadata == nil {
		r.Metadata = &driver.TokenRequestMetadata{}
	}
	if len(r.Metadata.Application) == 0 {
		r.Metadata.Application = map[string][]byte{}
	}
	r.Metadata.Application[k] = v
}

// FilterMetadataBy creates a new request with metadata filtered to show only the specified enrollment ID's data.
func (r *Request) FilterMetadataBy(ctx context.Context, eIDs ...string) (*Request, error) {
	meta := &Metadata{
		TokenService:         r.TokenService.tms.TokensService(),
		WalletService:        r.TokenService.tms.WalletService(),
		TokenRequestMetadata: r.Metadata,
		Logger:               r.TokenService.logger,
	}
	filteredMeta, err := meta.FilterBy(ctx, eIDs[0])
	if err != nil {
		return nil, errors.WithMessagef(err, "failed filtering metadata by [%s]", eIDs[0])
	}

	return &Request{
		Anchor:       r.Anchor,
		Actions:      r.Actions,
		Metadata:     filteredMeta.TokenRequestMetadata,
		TokenService: r.TokenService,
	}, nil
}

// GetMetadata returns the request's metadata wrapper with helper methods.
func (r *Request) GetMetadata() (*Metadata, error) {
	return &Metadata{
		TokenService:         r.TokenService.tms.TokensService(),
		WalletService:        r.TokenService.tms.WalletService(),
		TokenRequestMetadata: r.Metadata,
		Logger:               r.TokenService.logger,
	}, nil
}

func (r *Request) AllApplicationMetadata() map[string][]byte { return r.Metadata.Application }

// PublicParamsHash returns the hash of the public parameters used by this request.
func (r *Request) PublicParamsHash() PPHash {
	return r.TokenService.PublicParametersManager().PublicParamsHash()
}

func (r *Request) String() string { return string(r.Anchor) }

func (r *Request) parseInputIDs(ctx context.Context, inputs []*token.ID) ([]*token.ID, token.Quantity, token.Type, error) {
	inputTokens, err := r.TokenService.Vault().NewQueryEngine().GetTokens(ctx, inputs...)
	if err != nil {
		return nil, nil, "", errors.WithMessagef(err, "failed querying tokens ids")
	}
	var typ token.Type
	pp := r.TokenService.tms.PublicParamsManager().PublicParameters()
	if pp == nil {
		return nil, nil, "", errors.Errorf("public parameters not set")
	}
	precision := pp.Precision()
	sum := token.NewZeroQuantity(precision)
	for _, tok := range inputTokens {
		if len(typ) == 0 {
			typ = tok.Type
		}
		if typ != tok.Type {
			return nil, nil, "", errors.WithMessagef(err, "tokens must have the same type [%s]!=[%s]", typ, tok.Type)
		}
		q, err := token.ToQuantity(tok.Quantity, precision)
		if err != nil {
			return nil, nil, "", errors.WithMessagef(err, "failed unmarshalling token quantity [%s]", tok.Quantity)
		}
		sum, err = sum.Add(q)
		if err != nil {
			return nil, nil, "", errors.WithMessagef(err, "failed adding token quantity [%s]", tok.Quantity)
		}
	}

	return inputs, sum, typ, nil
}

// validateTransferOwners checks that every owner is nil for a redeem, or non-nil otherwise.
func validateTransferOwners(owners []Identity, redeem bool) error {
	for _, owner := range owners {
		if redeem {
			if !owner.IsNone() {
				return errors.Errorf("all recipients must be nil")
			}
		} else {
			if owner.IsNone() {
				return errors.Errorf("all recipients should be defined")
			}
		}
	}

	return nil
}

// selectTransferInputs selects input tokens (via transferOpts.Selector, or the default selector
// strategy) covering at least outputSum of tokenType.
func (r *Request) selectTransferInputs(ctx context.Context, transferOpts *TransferOptions, wallet *OwnerWallet, outputSum token.Quantity, tokenType token.Type) ([]*token.ID, token.Quantity, error) {
	selector := transferOpts.Selector
	if selector == nil {
		// resort to default strategy
		sm, err := r.TokenService.SelectorManager()
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed to get selector manager")
		}
		var err2 error
		selector, err2 = sm.NewSelector(string(r.Anchor))
		defer utils.IgnoreErrorWithOneArg(sm.Close, string(r.Anchor))
		if err2 != nil {
			return nil, nil, errors.Wrapf(err2, "failed getting default selector")
		}
	}
	tokenIDs, inputSum, err := selector.Select(ctx, wallet, outputSum.Decimal(), tokenType)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed selecting tokens")
	}

	return tokenIDs, inputSum, nil
}

// resolveRestRecipient returns the identity that should receive the leftover ("rest") of the
// inputs over the outputs, registering transferOpts.RestRecipientIdentity with wallet if one was
// explicitly provided.
func resolveRestRecipient(ctx context.Context, wallet *OwnerWallet, transferOpts *TransferOptions) ([]byte, error) {
	if transferOpts.RestRecipientIdentity != nil {
		// register it and us it
		if err := wallet.RegisterRecipient(ctx, transferOpts.RestRecipientIdentity); err != nil {
			return nil, errors.WithMessagef(err, "failed to register recipient identity [%s] for the rest, wallet [%s]", transferOpts.RestRecipientIdentity.Identity, wallet.ID())
		}

		return transferOpts.RestRecipientIdentity.Identity, nil
	}

	restIdentity, err := wallet.GetRecipientIdentity(ctx)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting recipient identity for the rest, wallet [%s]", wallet.ID())
	}

	return restIdentity, nil
}

// addRestOutputIfAny compares inputSum against outputSum and, if the inputs exceed the outputs,
// appends a "rest" output back to a recipient identity for the difference. It errors if the
// outputs exceed the inputs.
func (r *Request) addRestOutputIfAny(ctx context.Context, wallet *OwnerWallet, transferOpts *TransferOptions, tokenType token.Type, inputSum, outputSum token.Quantity, outputTokens []*token.Token) ([]*token.Token, error) {
	switch inputSum.Cmp(outputSum) {
	case 1:
		diff, err := inputSum.Sub(outputSum)
		if err != nil {
			return nil, errors.Wrap(err, "failed computing rest")
		}
		r.TokenService.logger.DebugfContext(ctx, "reassign rest [%s] to sender", diff.Decimal())

		restIdentity, err := resolveRestRecipient(ctx, wallet, transferOpts)
		if err != nil {
			return nil, err
		}

		return append(outputTokens, &token.Token{
			Owner:    restIdentity,
			Type:     tokenType,
			Quantity: diff.Hex(),
		}), nil
	case -1:
		return nil, errors.Errorf("the sum of the outputs is larger then the sum of the inputs [%s][%s]", inputSum.Decimal(), outputSum.Decimal())
	}

	return outputTokens, nil
}

// certifyTransferInputsIfNeeded requests certification for tokenIDs when the public parameters
// require graph hiding.
func (r *Request) certifyTransferInputsIfNeeded(ctx context.Context, tokenIDs []*token.ID) error {
	if !r.TokenService.PublicParametersManager().PublicParameters().GraphHiding() {
		return nil
	}
	r.TokenService.logger.DebugfContext(ctx, "graph hiding enabled, request certification")
	// Check token certification
	cc, err := r.TokenService.CertificationClient(ctx)
	if err != nil {
		return errors.WithMessagef(err, "cannot get certification client")
	}
	if err := cc.RequestCertification(ctx, tokenIDs...); err != nil {
		return errors.WithMessagef(err, "failed certifiying inputs")
	}

	return nil
}

func (r *Request) prepareTransfer(ctx context.Context, redeem bool, wallet *OwnerWallet, tokenType token.Type, values []uint64, owners []Identity, transferOpts *TransferOptions) ([]*token.ID, []*token.Token, error) {
	if err := validateTransferOwners(owners, redeem); err != nil {
		return nil, nil, err
	}
	var tokenIDs []*token.ID
	var inputSum token.Quantity
	var err error

	transferOpts.TokenIDs = r.cleanupInputIDs(transferOpts.TokenIDs)

	// if inputs have been passed, parse and certify them, if needed
	if len(transferOpts.TokenIDs) != 0 {
		tokenIDs, inputSum, tokenType, err = r.parseInputIDs(ctx, transferOpts.TokenIDs)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed parsing passed input tokens")
		}
	}

	if tokenType == "" {
		return nil, nil, errors.Errorf("type is empty")
	}

	// Compute output tokens
	outputTokens, outputSum, err := r.genOutputs(values, owners, tokenType)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "failed to generate outputs")
	}

	// Select input tokens, if not passed as opt
	if len(transferOpts.TokenIDs) == 0 {
		tokenIDs, inputSum, err = r.selectTransferInputs(ctx, transferOpts, wallet, outputSum, tokenType)
		if err != nil {
			return nil, nil, err
		}
	}

	// Is there a rest?
	outputTokens, err = r.addRestOutputIfAny(ctx, wallet, transferOpts, tokenType, inputSum, outputSum, outputTokens)
	if err != nil {
		return nil, nil, err
	}

	if err := r.certifyTransferInputsIfNeeded(ctx, tokenIDs); err != nil {
		return nil, nil, err
	}

	return tokenIDs, outputTokens, nil
}

func (r *Request) genOutputs(values []uint64, owners []Identity, tokenType token.Type) ([]*token.Token, token.Quantity, error) {
	pp := r.TokenService.PublicParametersManager().PublicParameters()
	precision := pp.Precision()
	maxTokenValue := pp.MaxTokenValue()
	maxTokenValueQ, err := token.UInt64ToQuantity(maxTokenValue, precision)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to convert [%d] to quantity of precision [%d]", maxTokenValue, precision)
	}
	outputSum := token.NewZeroQuantity(precision)
	var outputTokens []*token.Token
	for i, value := range values {
		q, err := token.UInt64ToQuantity(value, precision)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed to convert [%d] to quantity of precision [%d]", value, precision)
		}
		if q.Cmp(maxTokenValueQ) == 1 {
			return nil, nil, errors.Errorf("cannot create output with value [%s], max [%s]", q.Decimal(), maxTokenValueQ.Decimal())
		}
		outputSum, err = outputSum.Add(q)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed to add quantity for output %d", i)
		}

		// single output is fine
		outputTokens = append(outputTokens, &token.Token{
			Owner:    owners[i],
			Type:     tokenType,
			Quantity: q.Hex(),
		})
	}

	return outputTokens, outputSum, nil
}

func (r *Request) cleanupInputIDs(ds []*token.ID) []*token.ID {
	newSlice := make([]*token.ID, 0, len(ds))
	for _, item := range ds {
		if item != nil {
			newSlice = append(newSlice, item)
		}
	}

	return newSlice
}
