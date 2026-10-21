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
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/backend"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/setup"
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

// zkKeys captures the loaded verification key bytes. They are stored
// inside the Validator and referenced by the validation callback closures.
// Keys are kept as raw bytes — deserialization happens inside the
// ProofSystemBackend on each Verify call.
type zkKeys struct {
	vkSpendBytes     []byte
	vkOutputBytes    []byte
	vkMigrationBytes []byte
}

// Validator wraps the common.Validator infrastructure with zkatsnark-specific
// cryptographic checks and a standalone ValidateMigration method.
type Validator struct {
	*CommonValidator

	pp      *pp.PublicParams
	keys    *zkKeys
	backend backend.ProofSystemBackend
}

// NewValidator creates a new Validator using the provided public parameters and verifier deserializer.
func NewValidator(p *pp.PublicParams, deserializer driver.Deserializer, limits driver.ResourceLimits) (*Validator, error) {
	b, err := backend.NewProofSystemBackend(p.ProofSystem, p.Curve)
	if err != nil {
		return nil, errors.Wrapf(err, "validator: creating proof system backend")
	}

	vkSpendBytes, err := setup.LoadVerifyingKeyBytes(p, setup.CircuitSpend)
	if err != nil {
		return nil, errors.Wrapf(err, "validator: loading spend verifying key")
	}

	vkOutputBytes, err := setup.LoadVerifyingKeyBytes(p, setup.CircuitOutput)
	if err != nil {
		return nil, errors.Wrapf(err, "validator: loading output verifying key")
	}

	keys := &zkKeys{vkSpendBytes: vkSpendBytes, vkOutputBytes: vkOutputBytes}
	if vkmBytes, err := setup.LoadVerifyingKeyBytes(p, setup.CircuitMigration); err == nil {
		keys.vkMigrationBytes = vkmBytes
	}

	v := &Validator{pp: p, keys: keys, backend: b}

	transferValidators := []ValidateTransferFunc{
		TransferSignatureValidate,
		v.TransferZKValidate,
		TransferHTLCValidate,
		common.TransferApplicationDataValidate[*pp.PublicParams, *snarktoken.Input, *snarktoken.TransferAction, *snarktoken.IssueAction, driver.Deserializer],
	}

	issueValidators := []ValidateIssueFunc{
		v.IssueZKValidate,
		IssueSignatureValidate,
		common.IssueApplicationDataValidate[*pp.PublicParams, *snarktoken.Input, *snarktoken.TransferAction, *snarktoken.IssueAction, driver.Deserializer],
	}

	auditingValidators := []ValidateAuditingFunc{
		common.AuditingSignaturesValidate[*pp.PublicParams, *snarktoken.Input, *snarktoken.TransferAction, *snarktoken.IssueAction, driver.Deserializer],
	}

	v.CommonValidator = common.NewValidator(
		logger,
		p,
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

	if err := verifyAllProofs(v.backend, v.keys.vkSpendBytes, v.keys.vkOutputBytes, v.pp.Curve, a.Inputs, decodedInputs, a.Outputs, decodedOutputs); err != nil {
		return err
	}

	return verifyBindingSignature(
		v.pp.Curve,
		snarktoken.ActionTypeTransfer, a.TypeCommitment,
		decodedInputs, decodedOutputs,
		a.BindingSignature, 0,
	)
}

// ValidateIssue checks the cryptographic validity of an IssueAction.
func (v *Validator) ValidateIssue(a *snarktoken.IssueAction) error {
	if err := validateIssueActionShape(a); err != nil {
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

	if err := checkTypeCommitmentHomogeneity(actionTC, nil, decodedOutputs); err != nil {
		return err
	}

	if err := verifyAllProofs(v.backend, v.keys.vkSpendBytes, v.keys.vkOutputBytes, v.pp.Curve, nil, nil, a.Outputs, decodedOutputs); err != nil {
		return err
	}

	totalValue, err := parseTotalValue(v.pp.Curve, a.TotalValue)
	if err != nil {
		return err
	}

	return verifyBindingSignature(
		v.pp.Curve,
		snarktoken.ActionTypeIssue, a.TypeCommitment,
		nil, decodedOutputs,
		a.BindingSignature, totalValue,
	)
}

// ValidateMigration checks the cryptographic validity of a MigrationAction.
// Migration is zkatsnark-specific and has no slot in the common.Validator
// framework, so it is a standalone method on the wrapper struct.
func (v *Validator) ValidateMigration(a *snarktoken.MigrationAction) error {
	if len(v.keys.vkMigrationBytes) == 0 {
		return ErrMigrationNotConfigured
	}

	if err := validateMigrationActionShape(a); err != nil {
		return err
	}

	decoded, err := decodeMigrationAction(a, v.pp.Curve)
	if err != nil {
		return err
	}

	return verifyMigrationProof(v.backend, v.keys.vkMigrationBytes, v.pp.Curve, a.MigrationProof, decoded)
}
