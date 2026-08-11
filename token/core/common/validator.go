/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token/driver"
	request2 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// MetadataCounterID defines the type for metadata counter identifiers.
type MetadataCounterID = string

const (
	// TokenRequestToSign is the attribute ID for the token request to sign.
	TokenRequestToSign driver.ValidationAttributeID = "trs"
	// TokenRequestSignatures is the attribute ID for the token request signatures.
	TokenRequestSignatures driver.ValidationAttributeID = "sigs"
)

var (
	// ErrNoActions is returned when a submitted token request contains no actions.
	ErrNoActions = errors.New("token request has no actions")
	// ErrNilTokenRequest is returned when a nil token request is passed to validation.
	ErrNilTokenRequest = errors.New("token request is nil")
	// ErrNilActionDeserializer is returned when validation has no action deserializer.
	ErrNilActionDeserializer = errors.New("action deserializer is nil")
	// ErrNilAction is returned when an action deserializer returns a nil action.
	ErrNilAction = errors.New("deserialized action is nil")
	// ErrNilSignatureProvider is returned when a validator needs signatures but no provider is available.
	ErrNilSignatureProvider = errors.New("signature provider is nil")
	// ErrActionSignatureIDOutOfRange is returned when an action signature references an action that does not exist.
	ErrActionSignatureIDOutOfRange = errors.New("action signature ID is out of range")
)

// Context contains the context for token request validation.
type Context[P driver.PublicParameters, T driver.Input, TA driver.TransferAction, IA driver.IssueAction, DS driver.Deserializer] struct {
	Logger            logging.Logger
	PP                P
	Anchor            driver.TokenRequestAnchor
	TokenRequest      *driver.TokenRequest
	Deserializer      DS
	SignatureProvider driver.SignatureProvider
	Signatures        [][]byte
	InputTokens       []T
	TransferAction    TA
	IssueAction       IA
	Ledger            driver.Ledger
	MetadataCounter   map[MetadataCounterID]int
	Attributes        driver.ValidationAttributes
}

// CountMetadataKey increments the counter for the passed metadata key.
func (c *Context[P, T, TA, IA, DS]) CountMetadataKey(key string) {
	if c.MetadataCounter == nil {
		c.MetadataCounter = map[MetadataCounterID]int{}
	}
	c.MetadataCounter[key] = c.MetadataCounter[key] + 1
}

// ValidateTransferFunc is a function type for validating transfer actions.
type ValidateTransferFunc[P driver.PublicParameters, T driver.Input, TA driver.TransferAction, IA driver.IssueAction, DS driver.Deserializer] func(c context.Context, ctx *Context[P, T, TA, IA, DS]) error

// ValidateIssueFunc is a function type for validating issue actions.
type ValidateIssueFunc[P driver.PublicParameters, T driver.Input, TA driver.TransferAction, IA driver.IssueAction, DS driver.Deserializer] func(c context.Context, ctx *Context[P, T, TA, IA, DS]) error

// ValidateAuditingFunc is a function type for validating auditing information.
type ValidateAuditingFunc[P driver.PublicParameters, T driver.Input, TA driver.TransferAction, IA driver.IssueAction, DS driver.Deserializer] func(c context.Context, ctx *Context[P, T, TA, IA, DS]) error

// Validator validates token requests.
type Validator[P driver.PublicParameters, T driver.Input, TA driver.TransferAction, IA driver.IssueAction, DS driver.Deserializer] struct {
	Logger             logging.Logger
	PublicParams       P
	Deserializer       DS
	ActionDeserializer driver.ActionDeserializer[TA, IA]

	AuditingValidators []ValidateAuditingFunc[P, T, TA, IA, DS]
	TransferValidators []ValidateTransferFunc[P, T, TA, IA, DS]
	IssueValidators    []ValidateIssueFunc[P, T, TA, IA, DS]

	// Limits bounds the resources spent deserializing and validating untrusted requests, before
	// any cryptographic work is performed. See driver.ResourceLimits.
	Limits driver.ResourceLimits

	// MinProtocolVersion specifies the minimum protocol version required for token requests.
	// If set to 0, no minimum version is enforced (accepts all versions).
	// If set to a specific version (e.g., driver.ProtocolV1), only requests with that version
	// or higher will be accepted, rejecting older protocol versions.
	MinProtocolVersion uint32
}

// NewValidator returns a new Validator instance for the passed arguments.
func NewValidator[P driver.PublicParameters, T driver.Input, TA driver.TransferAction, IA driver.IssueAction, DS driver.Deserializer](
	Logger logging.Logger,
	publicParams P,
	deserializer DS,
	limits driver.ResourceLimits,
	actionDeserializer driver.ActionDeserializer[TA, IA],
	transferValidators []ValidateTransferFunc[P, T, TA, IA, DS],
	issueValidators []ValidateIssueFunc[P, T, TA, IA, DS],
	auditingValidators []ValidateAuditingFunc[P, T, TA, IA, DS],
) *Validator[P, T, TA, IA, DS] {
	return &Validator[P, T, TA, IA, DS]{
		Logger:             Logger,
		PublicParams:       publicParams,
		Deserializer:       deserializer,
		ActionDeserializer: actionDeserializer,
		Limits:             limits,
		TransferValidators: transferValidators,
		IssueValidators:    issueValidators,
		AuditingValidators: auditingValidators,
	}
}

// SetMinProtocolVersion configures the minimum protocol version that this validator will accept.
// Token requests with a protocol version below this minimum will be rejected during validation.
// Setting this to 0 (default) accepts all protocol versions.
func (v *Validator[P, T, TA, IA, DS]) SetMinProtocolVersion(version uint32) {
	v.MinProtocolVersion = version
}

// VerifyTokenRequestFromRaw verifies a token request from its raw representation.
func (v *Validator[P, T, TA, IA, DS]) VerifyTokenRequestFromRaw(ctx context.Context, getState driver.GetStateFnc, anchor driver.TokenRequestAnchor, raw []byte) ([]any, driver.ValidationAttributes, error) {
	logger.DebugfContext(ctx, "Verify token request from raw")
	if len(raw) == 0 {
		return nil, nil, errors.New("empty token request")
	}
	if err := v.CheckRawRequestSize(raw); err != nil {
		return nil, nil, err
	}
	tr := &driver.TokenRequest{}
	err := tr.FromBytes(raw)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to unmarshal token request")
	}

	// Validate protocol version
	if tr.Version == 0 {
		return nil, nil, driver.ErrInvalidVersion
	}

	// Enforce minimum protocol version if configured
	if v.MinProtocolVersion > 0 && tr.Version < v.MinProtocolVersion {
		return nil, nil, errors.Wrapf(
			driver.ErrVersionBelowMinimum,
			"got version %d, minimum required is %d",
			tr.Version,
			v.MinProtocolVersion,
		)
	}
	if len(tr.Actions) == 0 {
		return nil, nil, ErrNoActions
	}
	if err := v.CheckRequestLimits(tr); err != nil {
		return nil, nil, err
	}

	// Prepare message expected to be signed
	signed, err := tr.MarshalToMessageToSign([]byte(anchor))
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to marshal signed token request")
	}
	auditorSignatures := make([][]byte, 0, len(tr.Signatures))
	actionSignatures := make([][]byte, 0, len(tr.Signatures))
	actionSignaturesByID := make(map[uint32][][]byte)
	for _, sig := range tr.Signatures {
		if sig == nil {
			continue
		}
		if sig.Auditor != nil {
			auditorSignatures = append(auditorSignatures, sig.Auditor.Signature)

			continue
		}
		if sig.Action != nil {
			if uint64(sig.Action.ActionID) >= uint64(len(tr.Actions)) {
				return nil, nil, errors.Wrapf(
					ErrActionSignatureIDOutOfRange,
					"action signature ID [%d], number of actions [%d]",
					sig.Action.ActionID,
					len(tr.Actions),
				)
			}
			actionSignatures = append(actionSignatures, sig.Action.Signature)
			actionSignaturesByID[sig.Action.ActionID] = append(actionSignaturesByID[sig.Action.ActionID], sig.Action.Signature)
		}
	}
	// Merge signatures with auditor signatures first
	signatures := make([][]byte, 0, len(auditorSignatures)+len(actionSignatures))
	signatures = append(signatures, auditorSignatures...)
	signatures = append(signatures, actionSignatures...)

	attributes := make(driver.ValidationAttributes)
	attributes[TokenRequestToSign] = signed
	attributes[TokenRequestSignatures], err = json.Marshal(signatures)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to marshal token request signatures")
	}

	ledger := NewBackend(v.Logger, getState, signed, nil)
	auditorProvider := NewBackend(v.Logger, nil, signed, auditorSignatures)
	actionProviders := make(map[uint32]*Backend, len(tr.Actions))
	for i := range tr.Actions {
		actionID := uint32(i) // #nosec G115 -- bounded by the in-memory slice length
		actionProviders[actionID] = NewBackend(v.Logger, nil, signed, actionSignaturesByID[actionID])
	}

	return v.verifyTokenRequestWithScopedSignatures(ctx, ledger, auditorProvider, actionProviders, anchor, tr, attributes)
}

// VerifyTokenRequest verifies a token request.
func (v *Validator[P, T, TA, IA, DS]) VerifyTokenRequest(
	ctx context.Context,
	ledger driver.Ledger,
	signatureProvider driver.SignatureProvider,
	anchor driver.TokenRequestAnchor,
	tr *driver.TokenRequest,
	attributes driver.ValidationAttributes,
) ([]any, driver.ValidationAttributes, error) {
	if tr == nil {
		return nil, nil, ErrNilTokenRequest
	}
	if utils.IsNil(v.ActionDeserializer) {
		return nil, nil, ErrNilActionDeserializer
	}
	if err := v.VerifyAuditing(ctx, anchor, tr, ledger, signatureProvider, attributes); err != nil {
		return nil, nil, errors.Wrapf(err, "failed to verify auditor signatures [%s]", anchor)
	}
	actions, err := v.deserializeActionsInRequestOrder(tr)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to unmarshal actions [%s]", anchor)
	}
	res := make([]any, 0, len(actions))
	for _, action := range actions {
		if err := v.verifyDeserializedAction(ctx, anchor, tr, ledger, signatureProvider, action, attributes); err != nil {
			return nil, nil, err
		}
		res = append(res, action.value())
	}

	return res, attributes, nil
}

// UnmarshalActions unmarshals the actions from the passed raw representation of a token request.
func (v *Validator[P, T, TA, IA, DS]) UnmarshalActions(raw []byte) ([]any, error) {
	tr := &driver.TokenRequest{}
	err := tr.FromBytes(raw)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal token request")
	}

	if utils.IsNil(v.ActionDeserializer) {
		return nil, ErrNilActionDeserializer
	}
	actions, err := v.deserializeActionsInRequestOrder(tr)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal actions")
	}
	res := make([]any, 0, len(actions))
	for _, action := range actions {
		res = append(res, action.value())
	}

	return res, nil
}

// VerifyIssue verifies an issue action.
func (v *Validator[P, T, TA, IA, DS]) VerifyIssue(
	ctx context.Context,
	anchor driver.TokenRequestAnchor,
	tokenRequest *driver.TokenRequest,
	action IA,
	ledger driver.Ledger,
	signatureProvider driver.SignatureProvider,
	attributes driver.ValidationAttributes,
) error {
	if utils.IsNil(action) {
		return ErrNilAction
	}
	context := &Context[P, T, TA, IA, DS]{
		Logger:            v.Logger,
		PP:                v.PublicParams,
		Anchor:            anchor,
		TokenRequest:      tokenRequest,
		Deserializer:      v.Deserializer,
		IssueAction:       action,
		Ledger:            ledger,
		SignatureProvider: signatureProvider,
		MetadataCounter:   map[string]int{},
		Attributes:        attributes,
	}
	for _, v := range v.IssueValidators {
		if err := v(ctx, context); err != nil {
			return err
		}
	}

	// Check that all metadata have been validated
	counter := 0
	for k, c := range context.MetadataCounter {
		if c > 1 {
			return errors.Errorf("metadata key [%s] appeared more than one time", k)
		}
		counter += c
	}
	if len(action.GetMetadata()) != counter {
		return errors.Errorf("more metadata than those validated [%d]!=[%d], [%v]!=[%v]", len(action.GetMetadata()), counter, action.GetMetadata(), context.MetadataCounter)
	}

	return nil
}

// VerifyTransfer verifies a transfer action.
func (v *Validator[P, T, TA, IA, DS]) VerifyTransfer(
	ctx context.Context,
	anchor driver.TokenRequestAnchor,
	tokenRequest *driver.TokenRequest,
	action TA,
	ledger driver.Ledger,
	signatureProvider driver.SignatureProvider,
	attributes driver.ValidationAttributes,
) error {
	if utils.IsNil(action) {
		return ErrNilAction
	}
	context := &Context[P, T, TA, IA, DS]{
		Logger:            v.Logger,
		PP:                v.PublicParams,
		Anchor:            anchor,
		TokenRequest:      tokenRequest,
		Deserializer:      v.Deserializer,
		TransferAction:    action,
		Ledger:            ledger,
		SignatureProvider: signatureProvider,
		MetadataCounter:   map[MetadataCounterID]int{},
		Attributes:        attributes,
	}
	for _, v := range v.TransferValidators {
		if err := v(ctx, context); err != nil {
			return err
		}
	}

	// Check that all metadata have been validated
	counter := 0
	for k, c := range context.MetadataCounter {
		if c > 1 {
			return errors.Errorf("metadata key [%s] appeared more than one time", k)
		}
		counter += c
	}
	if len(action.GetMetadata()) != counter {
		return errors.Errorf("more metadata than those validated [%d]!=[%d], [%v]!=[%v]", len(action.GetMetadata()), counter, action.GetMetadata(), context.MetadataCounter)
	}

	return nil
}

// VerifyAuditing verifies the auditing information in a token request.
func (v *Validator[P, T, TA, IA, DS]) VerifyAuditing(
	ctx context.Context,
	anchor driver.TokenRequestAnchor,
	tokenRequest *driver.TokenRequest,
	ledger driver.Ledger,
	signatureProvider driver.SignatureProvider,
	attributes driver.ValidationAttributes,
) error {
	if tokenRequest == nil {
		return ErrNilTokenRequest
	}
	context := &Context[P, T, TA, IA, DS]{
		Logger:            v.Logger,
		PP:                v.PublicParams,
		Anchor:            anchor,
		TokenRequest:      tokenRequest,
		Deserializer:      v.Deserializer,
		Ledger:            ledger,
		SignatureProvider: signatureProvider,
		MetadataCounter:   map[MetadataCounterID]int{},
		Attributes:        attributes,
	}
	for _, v := range v.AuditingValidators {
		if err := v(ctx, context); err != nil {
			return err
		}
	}

	return nil
}

// IsAnyNil returns true if any of the passed arguments is nil.
func IsAnyNil[T any](args ...*T) bool {
	for _, arg := range args {
		if arg == nil {
			return true
		}
	}

	return false
}

type deserializedAction[TA driver.TransferAction, IA driver.IssueAction] struct {
	index    int
	actionID uint32
	typeID   request2.ActionType
	issue    IA
	transfer TA
}

func (a deserializedAction[TA, IA]) value() any {
	if a.typeID == request2.ActionType_ACTION_TYPE_ISSUE {
		return a.issue
	}

	return a.transfer
}

func (v *Validator[P, T, TA, IA, DS]) deserializeActionsInRequestOrder(tr *driver.TokenRequest) ([]deserializedAction[TA, IA], error) {
	if tr == nil {
		return nil, ErrNilTokenRequest
	}
	if utils.IsNil(v.ActionDeserializer) {
		return nil, ErrNilActionDeserializer
	}
	issues, transfers, err := v.ActionDeserializer.DeserializeActions(tr)
	if err != nil {
		return nil, err
	}

	actions := make([]deserializedAction[TA, IA], 0, len(tr.Actions))
	issueIndex := 0
	transferIndex := 0
	for index, typedAction := range tr.Actions {
		if typedAction == nil {
			return nil, errors.Wrapf(ErrNilAction, "action at request index [%d]", index)
		}
		action := deserializedAction[TA, IA]{
			index:    index,
			actionID: uint32(index), // #nosec G115 -- bounded by the in-memory slice length
			typeID:   typedAction.Type,
		}
		switch typedAction.Type {
		case request2.ActionType_ACTION_TYPE_ISSUE:
			if issueIndex >= len(issues) || utils.IsNil(issues[issueIndex]) {
				return nil, errors.Wrapf(ErrNilAction, "issue action at request index [%d]", index)
			}
			action.issue = issues[issueIndex]
			issueIndex++
		case request2.ActionType_ACTION_TYPE_TRANSFER:
			if transferIndex >= len(transfers) || utils.IsNil(transfers[transferIndex]) {
				return nil, errors.Wrapf(ErrNilAction, "transfer action at request index [%d]", index)
			}
			action.transfer = transfers[transferIndex]
			transferIndex++
		default:
			return nil, errors.Errorf("unknown action type [%s] at request index [%d]", typedAction.Type, index)
		}
		actions = append(actions, action)
	}
	if issueIndex != len(issues) || transferIndex != len(transfers) {
		return nil, errors.Errorf(
			"deserialized action count mismatch, issues [%d]!=[%d], transfers [%d]!=[%d]",
			issueIndex,
			len(issues),
			transferIndex,
			len(transfers),
		)
	}

	return actions, nil
}

func (v *Validator[P, T, TA, IA, DS]) verifyDeserializedAction(
	ctx context.Context,
	anchor driver.TokenRequestAnchor,
	tr *driver.TokenRequest,
	ledger driver.Ledger,
	signatureProvider driver.SignatureProvider,
	action deserializedAction[TA, IA],
	attributes driver.ValidationAttributes,
) error {
	switch action.typeID {
	case request2.ActionType_ACTION_TYPE_ISSUE:
		if err := v.VerifyIssue(ctx, anchor, tr, action.issue, ledger, signatureProvider, attributes); err != nil {
			return errors.Wrapf(err, "failed to verify issue action at request index [%d]", action.index)
		}
	case request2.ActionType_ACTION_TYPE_TRANSFER:
		if err := v.VerifyTransfer(ctx, anchor, tr, action.transfer, ledger, signatureProvider, attributes); err != nil {
			return errors.Wrapf(err, "failed to verify transfer action at request index [%d]", action.index)
		}
	default:
		return errors.Errorf("unknown action type [%s] at request index [%d]", action.typeID, action.index)
	}

	return nil
}

func (v *Validator[P, T, TA, IA, DS]) verifyTokenRequestWithScopedSignatures(
	ctx context.Context,
	ledger driver.Ledger,
	auditorProvider *Backend,
	actionProviders map[uint32]*Backend,
	anchor driver.TokenRequestAnchor,
	tr *driver.TokenRequest,
	attributes driver.ValidationAttributes,
) ([]any, driver.ValidationAttributes, error) {
	if err := v.VerifyAuditing(ctx, anchor, tr, ledger, auditorProvider, attributes); err != nil {
		return nil, nil, errors.Wrapf(err, "failed to verify auditor signatures [%s]", anchor)
	}
	if err := auditorProvider.EnsureExhausted(); err != nil {
		return nil, nil, errors.Wrap(err, "failed to consume auditor signatures")
	}
	actions, err := v.deserializeActionsInRequestOrder(tr)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to unmarshal actions [%s]", anchor)
	}
	res := make([]any, 0, len(actions))
	for _, action := range actions {
		provider := actionProviders[action.actionID]
		if provider == nil {
			provider = NewBackend(v.Logger, nil, nil, nil)
		}
		if err := v.verifyDeserializedAction(ctx, anchor, tr, ledger, provider, action, attributes); err != nil {
			return nil, nil, err
		}
		if err := provider.EnsureExhausted(); err != nil {
			return nil, nil, errors.Wrapf(err, "failed to consume signatures for action at request index [%d]", action.index)
		}
		res = append(res, action.value())
	}

	return res, attributes, nil
}
