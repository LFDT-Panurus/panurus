/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package audit implements the ZKAT-SNARK auditor checks: before the auditor
// endorses a token request, it verifies that the request metadata, which
// carries the cleartext of every output note, owner and issuer, really
// describes the actions of the request. It mirrors
// token/core/zkatdlog/nogh/v1/audit and plugs into the generic
// token/core/common.Auditor.
//
// The auditor does not verify the zero-knowledge proofs: that is the
// validator's job. It checks what only a party holding the cleartext can
// check: that every output note opens its on-chain commitment, that owners,
// senders, receivers and issuers match their audit info, that the spent
// tokens are the ones the auditor knows about, and that value is conserved.
package audit

import (
	"bytes"
	"context"
	"math"
	"slices"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/validator"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.opentelemetry.io/otel/trace"
)

// InfoMatcher matches identities against their audit info. It is satisfied by
// driver.Deserializer.
type InfoMatcher interface {
	// MatchIdentity returns nil if the audit info ai belongs to the identity id.
	MatchIdentity(ctx context.Context, id driver.Identity, ai []byte) error
	// Recipients returns the recipient identities behind the identity raw.
	Recipients(raw driver.Identity) ([]driver.Identity, error)
}

// InspectableIdentity contains an identity and the audit info that is claimed
// to belong to it.
type InspectableIdentity struct {
	// Identity is the identity as it appears in the action.
	Identity driver.Identity
	// IdentityFromMeta, if not empty, is the identity as reported by the
	// metadata; it must equal Identity.
	IdentityFromMeta driver.Identity
	// AuditInfo is the audit info reported by the metadata for Identity.
	AuditInfo []byte
}

// AuditContext is the context of the ZKAT-SNARK auditor checks.
type AuditContext = common.AuditContext[*pp.PublicParams, *snarktoken.IssueAction, *snarktoken.TransferAction, driver.Deserializer]

// ValidateIssueAuditFunc validates an issue action during audit.
type ValidateIssueAuditFunc = common.ValidateIssueAuditFunc[*pp.PublicParams, *snarktoken.IssueAction, *snarktoken.TransferAction, driver.Deserializer]

// ValidateTransferAuditFunc validates a transfer action during audit.
type ValidateTransferAuditFunc = common.ValidateTransferAuditFunc[*pp.PublicParams, *snarktoken.IssueAction, *snarktoken.TransferAction, driver.Deserializer]

// Auditor is the generic auditor instantiated for ZKAT-SNARK.
type Auditor = common.Auditor[*pp.PublicParams, *snarktoken.IssueAction, *snarktoken.TransferAction, driver.Deserializer]

// NewAuditor returns an Auditor that checks issue and transfer actions
// against their metadata under the public parameters p.
//
// limits are the resource limits applied when deserializing the actions; zero
// fields take the driver defaults. extraIssueValidators run, in order, before
// the generic issue checks of IssueAuditValidate (the AuditorService uses
// this for the upgrade-issuance checks).
func NewAuditor(
	logger logging.Logger,
	tracer trace.Tracer,
	infoMatcher InfoMatcher,
	p *pp.PublicParams,
	limits driver.ResourceLimits,
	extraIssueValidators ...ValidateIssueAuditFunc,
) *Auditor {
	issueValidators := append(slices.Clone(extraIssueValidators), IssueAuditValidate(infoMatcher, p))
	transferValidators := []ValidateTransferAuditFunc{TransferAuditValidate(infoMatcher, p)}

	return common.NewAuditor[*pp.PublicParams, *snarktoken.IssueAction, *snarktoken.TransferAction, driver.Deserializer](
		logger,
		tracer,
		p,
		nil, // the validators use infoMatcher instead
		&validator.ActionDeserializer{Limits: limits.WithDefaults()},
		issueValidators,
		transferValidators,
	)
}

// IssueAuditValidate returns the generic checks for an issue action: the
// metadata matches the action, every output note opens its commitment and
// carries the action's token type and issuer, every owner and receiver
// matches its audit info, and the issuer matches its audit info and is
// authorized by the public parameters (any issuer, when the public parameters
// list none).
func IssueAuditValidate(infoMatcher InfoMatcher, p *pp.PublicParams) ValidateIssueAuditFunc {
	return func(ctx context.Context, auditCtx *AuditContext) error {
		action := auditCtx.IssueAction
		if action == nil {
			return errors.New("issue action is nil")
		}
		metadata, err := issueMetadataAt(auditCtx)
		if err != nil {
			return err
		}
		if err := metadata.Match(action); err != nil {
			return errors.Wrap(err, "issue action does not match metadata")
		}
		if err := validateIssueOutputs(ctx, infoMatcher, p, action, metadata.Outputs); err != nil {
			return err
		}
		if err := common.ValidateIssueActionTokenTypes(metadata, auditCtx.AuditTokens); err != nil {
			return errors.Wrap(err, "token type validation failed for issue action")
		}

		return validateIssuer(ctx, infoMatcher, p.Issuers(), action.Issuer, &metadata.Issuer)
	}
}

// TransferAuditValidate returns the checks for a transfer action: the
// metadata matches the action, the inputs are the ledger tokens the action
// spends and their owners match the senders and their audit info, every
// output note opens its commitment, every owner and receiver matches its audit
// info, the output notes have the inputs' type and total value, and the issuer
// approving a redeem is authorized by the public parameters.
func TransferAuditValidate(infoMatcher InfoMatcher, p *pp.PublicParams) ValidateTransferAuditFunc {
	return func(ctx context.Context, auditCtx *AuditContext) error {
		action := auditCtx.TransferAction
		if action == nil {
			return errors.New("transfer action is nil")
		}
		metadata, err := transferMetadataAt(auditCtx)
		if err != nil {
			return err
		}
		if err := metadata.Match(action); err != nil {
			return errors.Wrap(err, "transfer action does not match metadata")
		}
		if err := validateTransferInputs(ctx, infoMatcher, p, action, metadata.Inputs, auditCtx.AuditTokens); err != nil {
			return err
		}
		notes, isRedeem, err := validateTransferOutputs(ctx, infoMatcher, p, action, metadata.Outputs)
		if err != nil {
			return err
		}
		if err := common.ValidateTransferActionTokenTypes(metadata, auditCtx.AuditTokens, true, p.Precision()); err != nil {
			return errors.Wrap(err, "token type validation failed for transfer action")
		}
		if err := checkTransferConservation(notes, metadata.Inputs, auditCtx.AuditTokens, p.Precision()); err != nil {
			return err
		}

		// The issuer approving a redeem must be one of the issuers listed in the
		// public parameters. The issuer is optional: when the public parameters
		// list no issuer, redeems need no issuer and there is nothing to check.
		// Issuer identities are public in this driver, so membership is enough.
		if metadata.Issuer.Identity.IsNone() {
			return nil
		}
		if !isRedeem {
			return errors.New("transfer action without redeem outputs must not carry an issuer")
		}

		return validateRedeemIssuer(p.Issuers(), metadata.Issuer.Identity)
	}
}

// issueMetadataAt returns the issue metadata of the action being audited.
func issueMetadataAt(auditCtx *AuditContext) (*driver.IssueMetadata, error) {
	if auditCtx.TokenRequestMetadata == nil || auditCtx.ActionIndex < 0 || auditCtx.ActionIndex >= len(auditCtx.TokenRequestMetadata.Actions) {
		return nil, errors.Errorf("action index [%d] out of range", auditCtx.ActionIndex)
	}
	entry := auditCtx.TokenRequestMetadata.Actions[auditCtx.ActionIndex]
	if entry == nil || entry.IssueMetadata == nil {
		return nil, errors.Errorf("issue metadata not found at action index [%d]", auditCtx.ActionIndex)
	}

	return entry.IssueMetadata, nil
}

// transferMetadataAt returns the transfer metadata of the action being audited.
func transferMetadataAt(auditCtx *AuditContext) (*driver.TransferMetadata, error) {
	if auditCtx.TokenRequestMetadata == nil || auditCtx.ActionIndex < 0 || auditCtx.ActionIndex >= len(auditCtx.TokenRequestMetadata.Actions) {
		return nil, errors.Errorf("action index [%d] out of range", auditCtx.ActionIndex)
	}
	entry := auditCtx.TokenRequestMetadata.Actions[auditCtx.ActionIndex]
	if entry == nil || entry.TransferMetadata == nil {
		return nil, errors.Errorf("transfer metadata not found at action index [%d]", auditCtx.ActionIndex)
	}

	return entry.TransferMetadata, nil
}

// openNote deserializes the note carried by rawNote and checks that it opens
// the commitment cm under the curve of p.
func openNote(p *pp.PublicParams, rawNote []byte, cm []byte, index int) (*snarktoken.Note, error) {
	if len(rawNote) == 0 {
		return nil, errors.Errorf("output [%d]: missing note", index)
	}
	note, err := snarktoken.Deserialize(rawNote)
	if err != nil {
		return nil, errors.Wrapf(err, "output [%d]: failed to deserialize note", index)
	}
	commitment, err := note.Commitment(p.Curve)
	if err != nil {
		return nil, errors.Wrapf(err, "output [%d]: failed to compute commitment", index)
	}
	if !bytes.Equal(commitment, cm) {
		return nil, errors.Errorf("output [%d]: note does not open the output commitment", index)
	}

	return note, nil
}

// validateIssueOutputs checks every issue output against its metadata.
//
// The note type is checked against the cleartext IssueAction.TokenType, which
// the validator uses for issuer authorization: on chain, only the homogeneity
// of the type commitments is checked, so the auditor is the only party that
// can check the minted notes carry that type. The note issuer is not bound by
// the commitment, but it is what the token service reports as the token's
// issuer, so it must be the action's issuer.
func validateIssueOutputs(ctx context.Context, infoMatcher InfoMatcher, p *pp.PublicParams, action *snarktoken.IssueAction, outputsMetadata []*driver.IssueOutputMetadata) error {
	for i := range action.Outputs {
		out := &action.Outputs[i]
		md := outputsMetadata[i]
		if md == nil {
			return errors.Errorf("output [%d]: missing metadata", i)
		}
		if out.IsRedeem() {
			return errors.Errorf("output [%d]: issue action cannot redeem tokens", i)
		}
		note, err := openNote(p, md.OutputMetadata, out.CommitmentOut, i)
		if err != nil {
			return err
		}
		if note.TokenType != action.TokenType {
			return errors.Errorf("output [%d]: note type [%s] differs from the action type [%s]", i, note.TokenType, action.TokenType)
		}
		if !bytes.Equal(note.Issuer, action.Issuer) {
			return errors.Errorf("output [%d]: note issuer differs from the action issuer", i)
		}
		owner := InspectableIdentity{Identity: out.Recipient, AuditInfo: md.OutputAuditInfo}
		if err := InspectIdentity(ctx, infoMatcher, &owner, i); err != nil {
			return errors.Wrapf(err, "failed inspecting owner of output [%d]", i)
		}
		if err := validateOutputReceivers(ctx, infoMatcher, out.Recipient, md.Receivers, i, true); err != nil {
			return err
		}
	}

	return nil
}

// validateIssuer checks the issuer identity against its metadata and audit
// info, and that it is one of the issuers listed in the public parameters.
// When the public parameters list no issuer, issuance is open-policy (as in
// the validator's IssueValidate) and membership is not enforced.
func validateIssuer(ctx context.Context, infoMatcher InfoMatcher, issuers []driver.Identity, issuer driver.Identity, issuerMetadata *driver.AuditableIdentity) error {
	id := InspectableIdentity{
		Identity:         issuer,
		IdentityFromMeta: issuerMetadata.Identity,
		AuditInfo:        issuerMetadata.AuditInfo,
	}
	if err := InspectIdentity(ctx, infoMatcher, &id, 0); err != nil {
		return errors.Wrap(err, "failed checking issuer identity")
	}
	if len(issuers) != 0 && !slices.ContainsFunc(issuers, issuer.Equal) {
		return errors.Errorf("issuer [%s] is not a recognized issuer", issuer)
	}

	return nil
}

// validateRedeemIssuer checks that the issuer approving a redeem is one of the
// issuers listed in the public parameters.
func validateRedeemIssuer(issuers []driver.Identity, issuer driver.Identity) error {
	if slices.ContainsFunc(issuers, issuer.Equal) {
		return nil
	}

	return errors.Errorf("redeem issuer [%s] is not a recognized issuer", issuer)
}

// inputOwner returns the owner of a spent ledger token, serialized as either a
// zkatsnark output description or a legacy fabtoken output (upgraded in place).
func inputOwner(raw []byte, precision uint64) (driver.Identity, error) {
	if snarktoken.IsFabtokenOutput(raw) {
		fab, err := snarktoken.ParseFabtokenOutput(raw, precision)
		if err != nil {
			return nil, err
		}

		return fab.Owner, nil
	}
	od, err := snarktoken.DeserializeOutputDescription(raw)
	if err != nil {
		return nil, err
	}

	return od.Recipient, nil
}

// validateTransferInputs checks every transfer input against its metadata and
// the auditor's own copy of the spent token.
//
// The binding that matters is TokenID == InputIDs[i] together with
// sender == auditTokens[TokenID].Owner: InputIDs are what the ledger spends,
// and the validator reads the owners from the ledger through them (see
// validator.ledgerInputOwner), while auditTokens is the auditor's own view of
// those ledger tokens. InputTokens is attacker-controlled and never checked by
// the validator, so comparing the sender with the owner parsed from it is
// defense in depth only: it must not replace the audit-token comparison.
func validateTransferInputs(
	ctx context.Context,
	infoMatcher InfoMatcher,
	p *pp.PublicParams,
	action *snarktoken.TransferAction,
	inputsMetadata []*driver.TransferInputMetadata,
	auditTokens map[string]*token.Token,
) error {
	if len(action.InputIDs) != len(action.Inputs) {
		return errors.Errorf("expected [%d] input IDs, got [%d]", len(action.Inputs), len(action.InputIDs))
	}
	if len(action.InputTokens) != len(action.Inputs) {
		return errors.Errorf("expected [%d] input tokens, got [%d]", len(action.Inputs), len(action.InputTokens))
	}
	for i := range action.Inputs {
		md := inputsMetadata[i]
		if md == nil {
			return errors.Errorf("input [%d]: missing metadata", i)
		}
		if len(md.Senders) != 1 {
			return errors.Errorf("input [%d]: expected exactly one sender, found [%d]", i, len(md.Senders))
		}
		sender := md.Senders[0]
		if sender == nil {
			return errors.Errorf("input [%d]: sender is nil", i)
		}
		if md.TokenID == nil || action.InputIDs[i] == nil || !md.TokenID.Equal(*action.InputIDs[i]) {
			return errors.Errorf("input [%d]: metadata token ID does not match the spent token ID", i)
		}
		audited := auditTokens[md.TokenID.String()]
		if audited == nil {
			return errors.Errorf("input [%d]: token [%s] not found among the audit tokens", i, md.TokenID)
		}
		if !sender.Identity.Equal(audited.Owner) {
			return errors.Errorf("input [%d]: sender differs from the owner of the audited token [%s]", i, md.TokenID)
		}
		owner, err := inputOwner(action.InputTokens[i], p.Precision())
		if err != nil {
			return errors.Wrapf(err, "input [%d]: invalid input token", i)
		}
		if !sender.Identity.Equal(owner) {
			return errors.Errorf("input [%d]: sender differs from the owner of the input token", i)
		}
		id := InspectableIdentity{Identity: sender.Identity, AuditInfo: sender.AuditInfo}
		if err := InspectIdentity(ctx, infoMatcher, &id, i); err != nil {
			return errors.Wrapf(err, "failed inspecting sender of input [%d]", i)
		}
	}

	return nil
}

// validateTransferOutputs checks every transfer output against its metadata
// and returns the opened notes, in output order, and whether any output is a
// redeem.
//
// Redeem outputs carry a note as well (the transfer service always attaches
// one), so that the burned value is accounted for in the conservation check.
// They have no owner, so they must not carry any owner or receiver audit info.
func validateTransferOutputs(
	ctx context.Context,
	infoMatcher InfoMatcher,
	p *pp.PublicParams,
	action *snarktoken.TransferAction,
	outputsMetadata []*driver.TransferOutputMetadata,
) ([]*snarktoken.Note, bool, error) {
	notes := make([]*snarktoken.Note, len(action.Outputs))
	isRedeem := false
	for i := range action.Outputs {
		out := &action.Outputs[i]
		md := outputsMetadata[i]
		if md == nil {
			return nil, false, errors.Errorf("output [%d]: missing metadata", i)
		}
		if out.IsRedeem() {
			isRedeem = true
			if err := checkRedeemOutputMetadata(md, i); err != nil {
				return nil, false, err
			}
		} else {
			owner := InspectableIdentity{Identity: out.Recipient, AuditInfo: md.OutputAuditInfo}
			if err := InspectIdentity(ctx, infoMatcher, &owner, i); err != nil {
				return nil, false, errors.Wrapf(err, "failed inspecting owner of output [%d]", i)
			}
			if err := validateOutputReceivers(ctx, infoMatcher, out.Recipient, md.Receivers, i, false); err != nil {
				return nil, false, err
			}
		}
		note, err := openNote(p, md.OutputMetadata, out.CommitmentOut, i)
		if err != nil {
			return nil, false, err
		}
		if len(note.Issuer) != 0 {
			return nil, false, errors.Errorf("output [%d]: transferred note must not carry an issuer", i)
		}
		notes[i] = note
	}

	return notes, isRedeem, nil
}

// checkRedeemOutputMetadata checks that the metadata of a redeem output does
// not report an owner: no output audit info, and only empty receivers.
func checkRedeemOutputMetadata(md *driver.TransferOutputMetadata, index int) error {
	if len(md.OutputAuditInfo) != 0 {
		return errors.Errorf("output [%d]: redeem output must not carry owner audit info", index)
	}
	for j, r := range md.Receivers {
		if r != nil && (len(r.Identity) != 0 || len(r.AuditInfo) != 0) {
			return errors.Errorf("output [%d]: redeem output must not carry receiver [%d]", index, j)
		}
	}

	return nil
}

// checkTransferConservation checks that the opened output notes, redeem
// outputs included, have the type of the audited inputs and carry, in total,
// exactly the audited input value. The common token-type check does not
// compare sums (see common.ValidateTransferActionTokenTypes), so this is where
// the auditor makes sure the metadata does not misreport the moved value.
func checkTransferConservation(notes []*snarktoken.Note, inputsMetadata []*driver.TransferInputMetadata, auditTokens map[string]*token.Token, precision uint64) error {
	if len(inputsMetadata) == 0 {
		return errors.New("transfer action has no inputs")
	}
	var inputType token.Type
	var inputSum uint64
	for i, md := range inputsMetadata {
		if md == nil || md.TokenID == nil {
			return errors.Errorf("input [%d]: missing token ID", i)
		}
		audited := auditTokens[md.TokenID.String()]
		if audited == nil {
			return errors.Errorf("input [%d]: token [%s] not found among the audit tokens", i, md.TokenID)
		}
		if i == 0 {
			inputType = audited.Type
		} else if audited.Type != inputType {
			return errors.Errorf("input [%d]: type [%s] differs from type [%s]", i, audited.Type, inputType)
		}
		q, err := token.NewUBigQuantity(audited.Quantity, precision)
		if err != nil {
			return errors.Wrapf(err, "input [%d]: invalid audited quantity [%s]", i, audited.Quantity)
		}
		if !q.IsUint64() {
			return errors.Errorf("input [%d]: audited quantity [%s] out of range", i, audited.Quantity)
		}
		if q.Uint64() > math.MaxUint64-inputSum {
			return errors.Errorf("input [%d]: total input value overflows", i)
		}
		inputSum += q.Uint64()
	}

	var outputSum uint64
	for i, note := range notes {
		if token.Type(note.TokenType) != inputType {
			return errors.Errorf("output [%d]: note type [%s] differs from the input type [%s]", i, note.TokenType, inputType)
		}
		if note.Value > math.MaxUint64-outputSum {
			return errors.Errorf("output [%d]: total output value overflows", i)
		}
		outputSum += note.Value
	}
	if outputSum != inputSum {
		return errors.Errorf("output value [%d] differs from the input value [%d]", outputSum, inputSum)
	}

	return nil
}

// validateOutputReceivers checks that the receivers reported by the metadata
// are the recipients behind the output owner, and that each matches its audit
// info. For issues, every receiver identity must be present; for transfers,
// an empty receiver identity is allowed, but a present one must match.
func validateOutputReceivers(
	ctx context.Context,
	infoMatcher InfoMatcher,
	owner driver.Identity,
	receivers []*driver.AuditableIdentity,
	outputIndex int,
	requireNonEmpty bool,
) error {
	recipients, err := infoMatcher.Recipients(owner)
	if err != nil {
		return errors.Wrapf(err, "failed to extract recipients from the owner of output [%d]", outputIndex)
	}
	if len(recipients) == 0 {
		return errors.Errorf("output [%d] has no recipients", outputIndex)
	}
	if len(receivers) != len(recipients) {
		return errors.Errorf("output [%d] has [%d] recipients but metadata has [%d] receivers", outputIndex, len(recipients), len(receivers))
	}
	for j, receiver := range receivers {
		if receiver == nil {
			return errors.Errorf("receiver [%d] of output [%d] is nil", j, outputIndex)
		}
		if (requireNonEmpty || len(receiver.Identity) != 0) && !recipients[j].Equal(receiver.Identity) {
			return errors.Errorf("recipient [%d] of output [%d] does not match the receiver identity in metadata", j, outputIndex)
		}
		id := InspectableIdentity{Identity: receiver.Identity, AuditInfo: receiver.AuditInfo}
		if err := InspectIdentity(ctx, infoMatcher, &id, outputIndex); err != nil {
			return errors.Wrapf(err, "failed inspecting receiver [%d] of output [%d]", j, outputIndex)
		}
	}

	return nil
}

// InspectIdentity checks that identity is not empty, that its audit info is
// not empty, that it equals the identity reported by the metadata (if any),
// and that the audit info belongs to it.
func InspectIdentity(ctx context.Context, matcher InfoMatcher, identity *InspectableIdentity, index int) error {
	if identity == nil || identity.Identity.IsNone() {
		return errors.Errorf("identity at index [%d] is nil, cannot inspect it", index)
	}
	if len(identity.AuditInfo) == 0 {
		return errors.Errorf("failed to inspect identity at index [%d]: audit info is nil", index)
	}
	if len(identity.IdentityFromMeta) != 0 && !identity.IdentityFromMeta.Equal(identity.Identity) {
		return errors.Errorf("failed to inspect identity at index [%d]: identity does not match the identity from metadata", index)
	}
	if err := matcher.MatchIdentity(ctx, identity.Identity, identity.AuditInfo); err != nil {
		return errors.Wrapf(err, "identity at index [%d] does not match the provided audit info", index)
	}

	return nil
}
