/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prepared"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

var logger = logging.MustGetLogger()

// ValidateTransferFunc is a function that validates a transfer action.
type ValidateTransferFunc = common.ValidateTransferFunc[*pp.PublicParams, *snarktoken.Input, *snarktoken.TransferAction, *snarktoken.IssueAction, driver.Deserializer]

// ValidateIssueFunc is a function that validates an issue action.
type ValidateIssueFunc = common.ValidateIssueFunc[*pp.PublicParams, *snarktoken.Input, *snarktoken.TransferAction, *snarktoken.IssueAction, driver.Deserializer]

// ValidateAuditingFunc is a function that validates auditing information.
type ValidateAuditingFunc = common.ValidateAuditingFunc[*pp.PublicParams, *snarktoken.Input, *snarktoken.TransferAction, *snarktoken.IssueAction, driver.Deserializer]

// Context is the validation context used by validator callbacks.
type Context = common.Context[*pp.PublicParams, *snarktoken.Input, *snarktoken.TransferAction, *snarktoken.IssueAction, driver.Deserializer]

// CommonValidator is the common.Validator type parameterized for zkatsnark.
type CommonValidator = common.Validator[*pp.PublicParams, *snarktoken.Input, *snarktoken.TransferAction, *snarktoken.IssueAction, driver.Deserializer]

// Validator wraps the common.Validator infrastructure with zkatsnark-specific
// cryptographic checks.
type Validator struct {
	*CommonValidator

	pp       *pp.PublicParams
	prepared *prepared.Verifier
	// legacy are the ZKAT-DLOG public parameters whose outputs can be
	// upgraded, nil when ZKAT-DLOG upgrades are not supported.
	legacy *pp.LegacyDLog
}

// NewValidator creates a new Validator over already-prepared public
// parameters for a verifying role.
func NewValidator(p *prepared.Verifier, deserializer driver.Deserializer, limits driver.ResourceLimits) (*Validator, error) {
	v := &Validator{pp: p.PP(), prepared: p}
	legacyDLog, err := p.PP().LegacyDLog()
	switch {
	case err == nil:
		v.legacy = legacyDLog
	case !errors.Is(err, pp.ErrNoLegacyDLog):
		return nil, errors.WithMessagef(err, "validator: invalid ZKAT-DLOG public parameters")
	}

	transferValidators := []ValidateTransferFunc{
		TransferSignatureValidate,
		v.TransferZKValidate,
		TransferHTLCValidate,
		common.TransferApplicationDataValidate[*pp.PublicParams, *snarktoken.Input, *snarktoken.TransferAction, *snarktoken.IssueAction, driver.Deserializer],
	}

	issueValidators := []ValidateIssueFunc{
		v.IssueZKValidate,
		IssueSignatureValidate,
		v.IssueUpgradeInputsValidate,
		common.IssueApplicationDataValidate[*pp.PublicParams, *snarktoken.Input, *snarktoken.TransferAction, *snarktoken.IssueAction, driver.Deserializer],
	}

	auditingValidators := []ValidateAuditingFunc{
		common.AuditingSignaturesValidate[*pp.PublicParams, *snarktoken.Input, *snarktoken.TransferAction, *snarktoken.IssueAction, driver.Deserializer],
	}

	v.CommonValidator = common.NewValidator(
		logger,
		v.pp,
		deserializer,
		limits,
		&ActionDeserializer{Limits: limits},
		transferValidators,
		issueValidators,
		auditingValidators,
	)

	return v, nil
}

// ValidateTransfer checks the cryptographic validity of a TransferAction.
// Order: structural shape → decode → type commitment homogeneity → parallel
// proof verification → binding signature.
func (v *Validator) ValidateTransfer(a *snarktoken.TransferAction) error {
	if err := validateTransferActionShape(a); err != nil {
		return err
	}

	decodedInputs, err := decodeAllSpends(a.Inputs, v.pp.Curve)
	if err != nil {
		return err
	}

	decodedOutputs, err := decodeAllOutputs(a.Outputs, v.pp.Curve)
	if err != nil {
		return err
	}

	actionTC, err := decodeScalar(v.pp.Curve, a.TypeCommitment)
	if err != nil {
		return errors.Wrapf(err, "validator: TypeCommitment")
	}

	if err := checkTypeCommitmentHomogeneity(actionTC, decodedInputs, decodedOutputs); err != nil {
		return err
	}

	if err := verifyAllProofs(v.prepared, a.Inputs, decodedInputs, a.Outputs, decodedOutputs); err != nil {
		return err
	}

	return verifyBindingSignature(
		v.pp.Curve,
		snarktoken.ActionTypeTransfer, a.TypeCommitment,
		decodedInputs, decodedOutputs,
		a.BindingSignature, 0,
	)
}

// ValidateIssue checks the cryptographic validity of an IssueAction and, for
// an upgrade issuance, its binding to the redeemed inputs (type, type
// commitment opening and, for fabtoken inputs, total value; see
// IssueUpgradeInputsValidate). It checks no signature.
func (v *Validator) ValidateIssue(a *snarktoken.IssueAction) error {
	if err := v.verifyIssueCrypto(a); err != nil {
		return err
	}

	// an upgrade issuance must also be bound to the tokens it redeems, as in
	// the ledger pipeline (IssueUpgradeInputsValidate)
	return v.validateUpgradeInputs(a)
}
