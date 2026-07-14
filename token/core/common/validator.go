/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
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

	// Prepare message expected to be signed
	signed, err := tr.MarshalToMessageToSign([]byte(anchor))
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to marshal signed token request")
	}
	auditorSignatures := make([][]byte, 0, len(tr.Signatures))
	actionSignatures := make([][]byte, 0, len(tr.Signatures))
	for _, sig := range tr.Signatures {
		if sig == nil {
			continue
		}
		if sig.Auditor != nil {
			auditorSignatures = append(auditorSignatures, sig.Auditor.Signature)

			continue
		}
		if sig.Action != nil {
			actionSignatures = append(actionSignatures, sig.Action.Signature)
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

	backend := NewBackend(v.Logger, getState, signed, signatures)

	return v.VerifyTokenRequest(ctx, backend, backend, anchor, tr, attributes)
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
	if err := v.VerifyAuditing(ctx, anchor, tr, ledger, signatureProvider, attributes); err != nil {
		return nil, nil, errors.Wrapf(err, "failed to verifier auditor's signature [%s]", anchor)
	}
	ia, ta, err := v.ActionDeserializer.DeserializeActions(tr)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to unmarshal actions [%s]", anchor)
	}
	err = v.verifyIssues(ctx, anchor, tr, ledger, ia, signatureProvider, attributes)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to verify issue actions [%s]", anchor)
	}
	err = v.verifyTransfers(ctx, anchor, tr, ledger, ta, signatureProvider, attributes)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to verify transfer actions [%s]", anchor)
	}

	var actions []any
	for _, action := range ia {
		actions = append(actions, action)
	}
	for _, action := range ta {
		actions = append(actions, action)
	}

	return actions, attributes, nil
}

// UnmarshalActions unmarshals the actions from the passed raw representation of a token request.
func (v *Validator[P, T, TA, IA, DS]) UnmarshalActions(raw []byte) ([]any, error) {
	tr := &driver.TokenRequest{}
	err := tr.FromBytes(raw)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal token request")
	}

	ia, ta, err := v.ActionDeserializer.DeserializeActions(tr)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal actions")
	}
	var res []any
	for _, action := range ia {
		res = append(res, action)
	}
	for _, action := range ta {
		res = append(res, action)
	}

	return res, nil
}

func (v *Validator[P, T, TA, IA, DS]) verifyIssues(
	ctx context.Context,
	anchor driver.TokenRequestAnchor,
	tokenRequest *driver.TokenRequest,
	ledger driver.Ledger,
	issues []IA,
	signatureProvider driver.SignatureProvider,
	attributes driver.ValidationAttributes,
) error {
	for i, issue := range issues {
		if err := v.VerifyIssue(ctx, anchor, tokenRequest, issue, ledger, signatureProvider, attributes); err != nil {
			return errors.Wrapf(err, "failed to verify issue action at [%d]", i)
		}
	}

	return nil
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

func (v *Validator[P, T, TA, IA, DS]) verifyTransfers(
	ctx context.Context,
	anchor driver.TokenRequestAnchor,
	tokenRequest *driver.TokenRequest,
	ledger driver.Ledger,
	transferActions []TA,
	signatureProvider driver.SignatureProvider,
	attributes driver.ValidationAttributes,
) error {
	v.Logger.Debugf("check sender start...")
	defer v.Logger.Debugf("check sender finished.")
	for i, action := range transferActions {
		if err := v.VerifyTransfer(ctx, anchor, tokenRequest, action, ledger, signatureProvider, attributes); err != nil {
			return errors.Wrapf(err, "failed to verify transfer action at [%d]", i)
		}
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
