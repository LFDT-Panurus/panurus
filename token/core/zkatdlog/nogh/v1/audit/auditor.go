/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package audit

import (
	"bytes"
	"context"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.opentelemetry.io/otel/trace"
)

const (
	// tokenTypeNotNeeded is used when creating inspectable tokens for validation
	// where the token type is not required (e.g., for transfer inputs)
	tokenTypeNotNeeded token2.Type = ""
)

// SigningIdentity is an alias for driver.SigningIdentity
//
//go:generate counterfeiter -o mock/signing_identity.go -fake-name SigningIdentity . SigningIdentity
type SigningIdentity = driver.SigningIdentity

// InfoMatcher deserialize audit information
//
//go:generate counterfeiter -o mock/info_matcher.go -fake-name InfoMatcher . InfoMatcher
type InfoMatcher interface {
	MatchIdentity(ctx context.Context, id driver.Identity, ai []byte) error
	Recipients(raw driver.Identity) ([]driver.Identity, error)
}

// InspectableIdentity contains the identity and its corresponding audit info.
type InspectableIdentity struct {
	Identity         driver.Identity
	IdentityFromMeta driver.Identity
	AuditInfo        []byte
}

// InspectableData contains the token data and its opening.
type InspectableData struct {
	Data      *math.G1
	TokenType token2.Type
	Value     *math.Zr
	BF        *math.Zr
}

// InspectableToken contains a zkat token and the information that allows
// an auditor to learn its content.
type InspectableToken struct {
	Identity InspectableIdentity
	Data     InspectableData
}

// NewInspectableToken creates a new InspectableToken.
func NewInspectableToken(
	token *token.Token,
	ownerAuditInfo []byte,
	tokenType token2.Type,
	value *math.Zr,
	bf *math.Zr,
) (*InspectableToken, error) {
	return &InspectableToken{
		Identity: InspectableIdentity{
			Identity:  token.Owner,
			AuditInfo: ownerAuditInfo,
		},
		Data: InspectableData{
			Data:      token.Data,
			TokenType: tokenType,
			Value:     value,
			BF:        bf,
		},
	}, nil
}

// ValidateIssueAuditFunc is a function type for validating issue actions during audit.
type ValidateIssueAuditFunc = common.ValidateIssueAuditFunc[*v1.PublicParams, *issue.Action, *transfer.Action, driver.Deserializer]

// ValidateTransferAuditFunc is a function type for validating transfer actions during audit.
type ValidateTransferAuditFunc = common.ValidateTransferAuditFunc[*v1.PublicParams, *issue.Action, *transfer.Action, driver.Deserializer]

// AuditContext is the context for zkatdlog auditing operations.
type AuditContext = common.AuditContext[*v1.PublicParams, *issue.Action, *transfer.Action, driver.Deserializer]

// ActionDeserializer deserializes zkatdlog actions.
type ActionDeserializer struct{}

// DeserializeActions deserializes issue and transfer actions from a token request.
func (a *ActionDeserializer) DeserializeActions(tr *driver.TokenRequest) ([]*issue.Action, []*transfer.Action, error) {
	issues := tr.GetIssues()
	issueActions := make([]*issue.Action, len(issues))
	for i := range issues {
		ia := &issue.Action{}
		if err := ia.Deserialize(issues[i]); err != nil {
			return nil, nil, err
		}
		issueActions[i] = ia
	}

	transfers := tr.GetTransfers()
	transferActions := make([]*transfer.Action, len(transfers))
	for i := range transfers {
		ta := &transfer.Action{}
		if err := ta.Deserialize(transfers[i]); err != nil {
			return nil, nil, err
		}
		transferActions[i] = ta
	}

	return issueActions, transferActions, nil
}

// Auditor is the generic auditor type for zkatdlog.
type Auditor = common.Auditor[*v1.PublicParams, *issue.Action, *transfer.Action, driver.Deserializer]

// NewAuditor creates a new Auditor for zkatdlog validation.
func NewAuditor(logger logging.Logger, tracer trace.Tracer, infoMatcher InfoMatcher, pp []*math.G1, c *math.Curve, precision uint64, issuers []driver.Identity) *Auditor {
	// Create public params wrapper - we don't need to set all fields, just what's needed
	publicParams := &v1.PublicParams{
		PedersenGenerators: pp,
		QuantityPrecision:  precision,
		IssuerIDs:          issuers,
	}

	issueValidators := []ValidateIssueAuditFunc{
		IssueAuditValidate(infoMatcher, pp, c),
	}

	transferValidators := []ValidateTransferAuditFunc{
		TransferAuditValidate(infoMatcher, pp, c, precision),
	}

	auditor := common.NewAuditor[*v1.PublicParams, *issue.Action, *transfer.Action, driver.Deserializer](
		logger,
		tracer,
		publicParams,
		nil, // deserializer not needed for zkatdlog auditor
		&ActionDeserializer{},
		issueValidators,
		transferValidators,
	)

	return auditor
}

// AuditorWrapper wraps the generic auditor with zkatdlog-specific helper methods.
type AuditorWrapper struct {
	*Auditor
	InfoMatcher    InfoMatcher
	PedersenParams []*math.G1
	Curve          *math.Curve
}

// NewAuditorWrapper creates a wrapper around the auditor with helper methods.
func NewAuditorWrapper(logger logging.Logger, tracer trace.Tracer, infoMatcher InfoMatcher, pp []*math.G1, c *math.Curve, precision uint64, issuers []driver.Identity) *AuditorWrapper {
	return &AuditorWrapper{
		Auditor:        NewAuditor(logger, tracer, infoMatcher, pp, c, precision, issuers),
		InfoMatcher:    infoMatcher,
		PedersenParams: pp,
		Curve:          c,
	}
}

// InspectOutput is a convenience method that wraps the standalone InspectOutput function.
func (a *AuditorWrapper) InspectOutput(ctx context.Context, output *InspectableToken, index int) error {
	return InspectOutput(ctx, a.InfoMatcher, a.PedersenParams, a.Curve, output, index)
}

// InspectIdentity is a convenience method that wraps the standalone InspectIdentity function.
func (a *AuditorWrapper) InspectIdentity(ctx context.Context, matcher InfoMatcher, identity *InspectableIdentity, index int) error {
	return InspectIdentity(ctx, matcher, identity, index)
}

// IssueAuditValidate returns a validation function for issue actions.
func IssueAuditValidate(infoMatcher InfoMatcher, pedersenParams []*math.G1, curve *math.Curve) ValidateIssueAuditFunc {
	return func(ctx context.Context, auditCtx *AuditContext) error {
		// Get the issue action
		action := auditCtx.IssueAction
		if action == nil {
			return errors.Errorf("issue action is nil")
		}

		// Get the metadata for this specific action using ActionIndex
		if auditCtx.ActionIndex >= len(auditCtx.TokenRequestMetadata.Actions) {
			return errors.Errorf("action index %d out of range (have %d actions)", auditCtx.ActionIndex, len(auditCtx.TokenRequestMetadata.Actions))
		}

		actionMeta := auditCtx.TokenRequestMetadata.Actions[auditCtx.ActionIndex]
		if actionMeta.IssueMetadata == nil {
			return errors.Errorf("issue metadata not found at action index %d", auditCtx.ActionIndex)
		}

		metadata := actionMeta.IssueMetadata

		// Use IssueMetadata.Match to validate structural correspondence
		if err := metadata.Match(action); err != nil {
			return errors.Wrapf(err, "issue action does not match metadata")
		}

		// Validate inputs (for token upgrades)
		if err := validateIssueInputs(action.Inputs, metadata.Inputs); err != nil {
			return err
		}

		// Validate outputs
		if err := validateIssueOutputs(ctx, infoMatcher, pedersenParams, curve, action.Outputs, metadata.Outputs); err != nil {
			return err
		}

		// Validate that all inputs and outputs have the same token type
		if err := common.ValidateIssueActionTokenTypes(metadata, auditCtx.AuditTokens); err != nil {
			return errors.Wrapf(err, "token type validation failed for issue action")
		}

		// Validate issuer identity
		if err := validateIssuer(ctx, infoMatcher, action.Issuer, &metadata.Issuer); err != nil {
			return err
		}

		return nil
	}
}

// TransferAuditValidate returns a validation function for transfer actions.
func TransferAuditValidate(infoMatcher InfoMatcher, pedersenParams []*math.G1, curve *math.Curve, precision uint64) ValidateTransferAuditFunc {
	return func(ctx context.Context, auditCtx *AuditContext) error {
		// Get the transfer action
		action := auditCtx.TransferAction
		if action == nil {
			return errors.Errorf("transfer action is nil")
		}

		// Get the metadata for this specific action using ActionIndex
		if auditCtx.ActionIndex >= len(auditCtx.TokenRequestMetadata.Actions) {
			return errors.Errorf("action index %d out of range (have %d actions)", auditCtx.ActionIndex, len(auditCtx.TokenRequestMetadata.Actions))
		}

		actionMeta := auditCtx.TokenRequestMetadata.Actions[auditCtx.ActionIndex]
		if actionMeta.TransferMetadata == nil {
			return errors.Errorf("transfer metadata not found at action index %d", auditCtx.ActionIndex)
		}

		metadata := actionMeta.TransferMetadata

		// Use TransferMetadata.Match to validate structural correspondence
		if err := metadata.Match(action); err != nil {
			return errors.Wrapf(err, "transfer action does not match metadata")
		}

		// Validate inputs
		if err := validateTransferInputs(ctx, infoMatcher, action.Inputs, metadata.Inputs); err != nil {
			return err
		}

		// Validate outputs
		if err := validateTransferOutputs(ctx, infoMatcher, pedersenParams, curve, action.Outputs, metadata.Outputs); err != nil {
			return err
		}

		// Validate that all inputs and outputs have the same token type and sum of values
		if err := common.ValidateTransferActionTokenTypes(metadata, auditCtx.AuditTokens, true, precision); err != nil {
			return errors.Wrapf(err, "token type validation failed for transfer action")
		}

		// For redeems, validate that the issuer who approved the redeem is one of the issuers
		// listed in the public parameters. The issuer is optional: when the public parameters do
		// not list any issuer, redeems can be approved without a signing issuer, and there is
		// nothing to validate here. Issuer identities are not private in this driver, so there is
		// no audit info to inspect: identity membership in the public parameters' issuer list is
		// sufficient.
		if action.IsRedeem() && !metadata.Issuer.Identity.IsNone() {
			if err := validateRedeemIssuer(auditCtx.PP.Issuers(), metadata.Issuer.Identity); err != nil {
				return err
			}
		}

		return nil
	}
}

// validateRedeemIssuer checks that the issuer who approved a redeem is one of the issuers
// listed in the public parameters.
func validateRedeemIssuer(issuers []driver.Identity, issuer driver.Identity) error {
	for _, i := range issuers {
		if i.Equal(issuer) {
			return nil
		}
	}

	return errors.Errorf("issuer [%s] is not a recognized issuer", issuer)
}

// validateIssueInputs validates issue action inputs against metadata.
func validateIssueInputs(inputs []*issue.ActionInput, inputsMetadata []*driver.IssueInputMetadata) error {
	for i, input := range inputs {
		if input == nil {
			return errors.Errorf("input at index [%d] is nil", i)
		}
		if input.Token == nil {
			return errors.Errorf("input token at index [%d] is nil", i)
		}

		inputMetadata := inputsMetadata[i]
		if inputMetadata == nil {
			return errors.Errorf("input metadata at index [%d] is nil", i)
		}

		// Validate TokenID matches the actual input token ID
		if inputMetadata.TokenID != nil {
			if !inputMetadata.TokenID.Equal(input.ID) {
				return errors.Errorf(
					"token ID mismatch at input [%d]: metadata has [%s:%d] but action has [%s:%d]",
					i,
					inputMetadata.TokenID.TxId, inputMetadata.TokenID.Index,
					input.ID.TxId, input.ID.Index,
				)
			}
		}
	}

	return nil
}

// validateIssueOutputs validates issue action outputs against metadata.
func validateIssueOutputs(ctx context.Context, infoMatcher InfoMatcher, pedersenParams []*math.G1, curve *math.Curve, outputs []*token.Token, outputsMetadata []*driver.IssueOutputMetadata) error {
	for i, output := range outputs {
		if output == nil {
			return errors.Errorf("output at index [%d] is nil", i)
		}

		outputMetadata := outputsMetadata[i]
		if outputMetadata == nil {
			return errors.Errorf("output metadata at index [%d] is nil", i)
		}

		// Issue actions cannot redeem tokens
		if output.IsRedeem() {
			return errors.Errorf("issue action cannot redeem tokens")
		}

		// Deserialize token metadata
		tokenMetadata := &token.Metadata{}
		if err := tokenMetadata.Deserialize(outputMetadata.OutputMetadata); err != nil {
			return errors.Wrapf(err, "failed to deserialize token metadata at index [%d]", i)
		}

		// Create inspectable token and validate commitment
		inspectable, err := NewInspectableToken(
			output,
			outputMetadata.OutputAuditInfo,
			tokenMetadata.Type,
			tokenMetadata.Value,
			tokenMetadata.BlindingFactor,
		)
		if err != nil {
			return errors.Wrapf(err, "failed to create inspectable token for output [%d]", i)
		}

		if err := InspectOutput(ctx, infoMatcher, pedersenParams, curve, inspectable, i); err != nil {
			return errors.Wrapf(err, "failed inspecting output [%d]", i)
		}

		// Validate receivers match recipients
		if err := validateOutputReceivers(ctx, infoMatcher, output.Owner, outputMetadata.Receivers, i, true); err != nil {
			return err
		}
	}

	return nil
}

// validateIssuer validates the issuer identity against metadata.
func validateIssuer(ctx context.Context, infoMatcher InfoMatcher, issuer driver.Identity, issuerMetadata *driver.AuditableIdentity) error {
	issuerIdentity := InspectableIdentity{
		Identity:         issuer,
		IdentityFromMeta: issuerMetadata.Identity,
		AuditInfo:        issuerMetadata.AuditInfo,
	}
	if err := InspectIdentity(ctx, infoMatcher, &issuerIdentity, 0); err != nil {
		return errors.Wrapf(err, "failed checking issuer identity")
	}

	return nil
}

// validateTransferInputs validates transfer action inputs against metadata.
func validateTransferInputs(ctx context.Context, infoMatcher InfoMatcher, inputs []*transfer.ActionInput, inputsMetadata []*driver.TransferInputMetadata) error {
	for i, actionInput := range inputs {
		if actionInput == nil || actionInput.Token == nil {
			return errors.Errorf("input at index [%d] is nil", i)
		}
		if actionInput.ID == nil {
			return errors.Errorf("input at index [%d] has nil ID", i)
		}

		inputMetadata := inputsMetadata[i]
		if inputMetadata == nil {
			return errors.Errorf("metadata for input at index [%d] is nil", i)
		}

		// Validate exactly one sender in metadata
		if len(inputMetadata.Senders) != 1 {
			return errors.Errorf(
				"input metadata at index [%d] must have exactly one sender, found [%d]",
				i,
				len(inputMetadata.Senders),
			)
		}

		if inputMetadata.Senders[0] == nil {
			return errors.Errorf("sender at index [%d] is nil", i)
		}

		// Validate sender identity matches token owner
		if !bytes.Equal(inputMetadata.Senders[0].Identity, actionInput.Token.Owner) {
			return errors.Errorf(
				"sender identity at index [%d] does not match token owner",
				i,
			)
		}

		// Validate TokenID matches the actual input token ID
		if inputMetadata.TokenID != nil && !inputMetadata.TokenID.Equal(*actionInput.ID) {
			return errors.Errorf(
				"token ID mismatch at input [%d]: metadata has [%s:%d] but action has [%s:%d]",
				i,
				inputMetadata.TokenID.TxId, inputMetadata.TokenID.Index,
				actionInput.ID.TxId, actionInput.ID.Index,
			)
		}

		// Create inspectable token for input (only need audit info for sender)
		inspectable, err := NewInspectableToken(
			actionInput.Token,
			inputMetadata.Senders[0].AuditInfo,
			tokenTypeNotNeeded,
			nil, // Value not needed for input validation
			nil, // BlindingFactor not needed for input validation
		)
		if err != nil {
			return errors.Wrapf(err, "failed to create inspectable token for input at index [%d]", i)
		}

		// Validate sender identity
		if !inspectable.Identity.Identity.IsNone() {
			if err := InspectIdentity(ctx, infoMatcher, &inspectable.Identity, i); err != nil {
				return errors.Wrapf(err, "failed inspecting input sender at index [%d]", i)
			}
		}
	}

	return nil
}

// validateTransferOutputs validates transfer action outputs against metadata.
func validateTransferOutputs(ctx context.Context, infoMatcher InfoMatcher, pedersenParams []*math.G1, curve *math.Curve, outputs []*token.Token, outputsMetadata []*driver.TransferOutputMetadata) error {
	for i, output := range outputs {
		if output == nil {
			return errors.Errorf("output at index [%d] is nil", i)
		}

		outputMetadata := outputsMetadata[i]
		if outputMetadata == nil {
			return errors.Errorf("output metadata at index [%d] is nil", i)
		}

		// Validate receivers for non-redeem outputs
		if !output.IsRedeem() {
			if err := validateOutputReceivers(ctx, infoMatcher, output.Owner, outputMetadata.Receivers, i, false); err != nil {
				return err
			}
		}

		// Deserialize token metadata
		tokenMetadata := &token.Metadata{}
		if err := tokenMetadata.Deserialize(outputMetadata.OutputMetadata); err != nil {
			return errors.Wrapf(err, "failed to deserialize token metadata at index [%d]", i)
		}

		// Create inspectable token using OutputAuditInfo (primary audit info for transfer outputs)
		inspectable, err := NewInspectableToken(
			output,
			outputMetadata.OutputAuditInfo,
			tokenMetadata.Type,
			tokenMetadata.Value,
			tokenMetadata.BlindingFactor,
		)
		if err != nil {
			return errors.Wrapf(err, "failed to create inspectable token at index [%d]", i)
		}

		// Validate output commitment and identity
		if err := InspectOutput(ctx, infoMatcher, pedersenParams, curve, inspectable, i); err != nil {
			return errors.Wrapf(err, "failed inspecting output at index [%d]", i)
		}
	}

	return nil
}

// validateOutputReceivers validates that output receivers in metadata match recipients extracted from the owner.
// The requireNonEmpty parameter controls whether empty receiver identities are allowed (issue vs transfer).
func validateOutputReceivers(
	ctx context.Context,
	infoMatcher InfoMatcher,
	owner driver.Identity,
	receivers []*driver.AuditableIdentity,
	outputIndex int,
	requireNonEmpty bool,
) error {
	// Extract recipients from the output owner
	recipients, err := infoMatcher.Recipients(owner)
	if err != nil {
		return errors.Wrapf(err, "failed to extract recipients from output owner at index [%d]", outputIndex)
	}

	// Validate that recipients slice is non-empty
	if len(recipients) == 0 {
		return errors.Errorf("output at index [%d] has no recipients", outputIndex)
	}

	// Validate that metadata provides receivers for all recipients in the action
	if len(receivers) != len(recipients) {
		return errors.Errorf(
			"output at index [%d] has [%d] recipients but metadata has [%d] receivers",
			outputIndex, len(recipients), len(receivers),
		)
	}

	// Validate all receivers and their identities
	for j, receiver := range receivers {
		if receiver == nil {
			return errors.Errorf("receiver at index [%d] for output [%d] is nil", j, outputIndex)
		}

		// Validate that the recipient from owner matches the receiver identity in metadata
		// For issue actions, identity must be non-empty and match
		// For transfer actions, empty identity is allowed (optional validation)
		if requireNonEmpty {
			if len(receiver.Identity) == 0 || !bytes.Equal(recipients[j], receiver.Identity) {
				return errors.Errorf(
					"recipient at index [%d] for output [%d] does not match receiver identity in metadata",
					j, outputIndex,
				)
			}
		} else {
			if len(receiver.Identity) > 0 && !bytes.Equal(recipients[j], receiver.Identity) {
				return errors.Errorf(
					"recipient at index [%d] for output [%d] does not match receiver identity in metadata",
					j, outputIndex,
				)
			}
		}

		// Inspect receiver identity
		receiverInspectable := InspectableIdentity{
			Identity:  receiver.Identity,
			AuditInfo: receiver.AuditInfo,
		}
		if err := InspectIdentity(ctx, infoMatcher, &receiverInspectable, outputIndex); err != nil {
			return errors.Wrapf(err, "failed inspecting receiver [%d] at output [%d]", j, outputIndex)
		}
	}

	return nil
}

// InspectOutput verifies that the commitments in an output token of a given index
// match the information provided in the clear.
func InspectOutput(ctx context.Context, infoMatcher InfoMatcher, pedersenParams []*math.G1, curve *math.Curve, output *InspectableToken, index int) error {
	if output == nil || output.Data.Data == nil {
		return errors.Errorf("invalid output at index [%d]", index)
	}
	// Recompute commitment from provided cleartext data
	tokenComm := commit([]*math.Zr{
		curve.HashToZr([]byte(output.Data.TokenType)),
		output.Data.Value,
		output.Data.BF,
	}, pedersenParams, curve)
	// Verify it matches the commitment in the token
	if !tokenComm.Equals(output.Data.Data) {
		return errors.Errorf("output at index [%d] does not match the provided opening", index)
	}
	// Verify owner identity if it's not a redeemed output
	if !output.Identity.Identity.IsNone() { // this is not a redeemed output
		if err := InspectIdentity(ctx, infoMatcher, &output.Identity, index); err != nil {
			return errors.Wrapf(err, "failed inspecting output at index [%d]", index)
		}
	}

	return nil
}

// InspectIdentity verifies that the audit info matches the token owner.
func InspectIdentity(ctx context.Context, matcher InfoMatcher, identity *InspectableIdentity, index int) error {
	if identity.Identity.IsNone() {
		return errors.Errorf("identity at index [%d] is nil, cannot inspect it", index)
	}
	if len(identity.AuditInfo) == 0 {
		return errors.Errorf("failed to inspect identity at index [%d]: audit info is nil", index)
	}
	// If identity is provided in metadata, it must match the one in the action
	if len(identity.IdentityFromMeta) != 0 {
		// enforce equality
		if !bytes.Equal(identity.IdentityFromMeta, identity.Identity) {
			return errors.Errorf("failed to inspect identity at index [%d]: identity does not match the identity from metadata", index)
		}
	}
	// Use InfoMatcher to verify that AuditInfo corresponds to the Identity
	if err := matcher.MatchIdentity(ctx, identity.Identity, identity.AuditInfo); err != nil {
		return errors.Wrapf(err, "owner at index [%d] does not match the provided opening", index)
	}

	return nil
}

// commit computes a Pedersen commitment for the given vector and generators.
func commit(vector []*math.Zr, generators []*math.G1, c *math.Curve) *math.G1 {
	com := c.NewG1()
	for i := range vector {
		com.Add(generators[i].Mul(vector[i]))
	}

	return com
}
