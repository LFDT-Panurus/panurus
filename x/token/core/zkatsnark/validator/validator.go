/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
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

// zkKeys captures the loaded groth16 verification keys. It is stored
// inside the Validator and referenced by the validation callback closures.
type zkKeys struct {
	vkSpend     groth16.VerifyingKey
	vkOutput    groth16.VerifyingKey
	vkMigration groth16.VerifyingKey
}

// Validator wraps the common.Validator infrastructure with zkatsnark-specific
// cryptographic checks and a standalone ValidateMigration method.
type Validator struct {
	*CommonValidator

	pp   *pp.PublicParams
	keys *zkKeys
}

// NewValidator constructs a Validator from PublicParams, loading the Spend
// and Output verification keys eagerly. The Migration verification key is
// loaded best-effort.
func NewValidator(p *pp.PublicParams) (*Validator, error) {
	vkSpend, err := setup.LoadVerifyingKey(p, setup.CircuitSpend)
	if err != nil {
		return nil, errors.Wrapf(err, "validator: loading spend verifying key")
	}

	vkOutput, err := setup.LoadVerifyingKey(p, setup.CircuitOutput)
	if err != nil {
		return nil, errors.Wrapf(err, "validator: loading output verifying key")
	}

	keys := &zkKeys{vkSpend: vkSpend, vkOutput: vkOutput}
	if vkm, err := setup.LoadVerifyingKey(p, setup.CircuitMigration); err == nil {
		keys.vkMigration = vkm
	}

	v := &Validator{pp: p, keys: keys}

	transferValidators := []ValidateTransferFunc{
		v.TransferZKValidate,
	}

	issueValidators := []ValidateIssueFunc{
		v.IssueZKValidate,
	}

	auditingValidators := []ValidateAuditingFunc{
		common.AuditingSignaturesValidate[*pp.PublicParams, *snarktoken.Input, *snarktoken.TransferAction, *snarktoken.IssueAction, driver.Deserializer],
	}

	v.CommonValidator = common.NewValidator(
		logger,
		p,
		nil, // DS (Deserializer), not required for zkatsnark ZK-only validation
		&ActionDeserializer{},
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

	decodedInputs, err := decodeAllSpends(a.Inputs)
	if err != nil {
		return err
	}

	decodedOutputs, err := decodeAllOutputs(a.Outputs)
	if err != nil {
		return err
	}

	var actionTC fr.Element
	if err := actionTC.SetBytesCanonical(a.TypeCommitment); err != nil {
		return err
	}

	if err := checkTypeCommitmentHomogeneity(actionTC, decodedInputs, decodedOutputs); err != nil {
		return err
	}

	if err := verifyAllProofs(v.keys.vkSpend, v.keys.vkOutput, v.pp.Curve, a.Inputs, decodedInputs, a.Outputs, decodedOutputs); err != nil {
		return err
	}

	return verifyBindingSignature(
		snarktoken.ActionTypeTransfer, actionTC,
		decodedInputs, decodedOutputs,
		a.BindingSignature, 0,
	)
}

// ValidateIssue checks the cryptographic validity of an IssueAction.
func (v *Validator) ValidateIssue(a *snarktoken.IssueAction) error {
	if err := validateIssueActionShape(a); err != nil {
		return err
	}

	decodedOutputs, err := decodeAllOutputs(a.Outputs)
	if err != nil {
		return err
	}

	var actionTC fr.Element
	if err := actionTC.SetBytesCanonical(a.TypeCommitment); err != nil {
		return err
	}

	if err := checkTypeCommitmentHomogeneity(actionTC, nil, decodedOutputs); err != nil {
		return err
	}

	if err := verifyAllProofs(v.keys.vkSpend, v.keys.vkOutput, v.pp.Curve, nil, nil, a.Outputs, decodedOutputs); err != nil {
		return err
	}

	totalValue, err := parseTotalValue(a.TotalValue)
	if err != nil {
		return err
	}

	return verifyBindingSignature(
		snarktoken.ActionTypeIssue, actionTC,
		nil, decodedOutputs,
		a.BindingSignature, totalValue,
	)
}

// ValidateMigration checks the cryptographic validity of a MigrationAction.
// Migration is zkatsnark-specific and has no slot in the common.Validator
// framework, so it is a standalone method on the wrapper struct.
func (v *Validator) ValidateMigration(a *snarktoken.MigrationAction) error {
	if v.keys.vkMigration == nil {
		return ErrMigrationNotConfigured
	}

	if err := validateMigrationActionShape(a); err != nil {
		return err
	}

	decoded, err := decodeMigrationAction(a)
	if err != nil {
		return err
	}

	return verifyMigrationProof(v.keys.vkMigration, v.pp.Curve, a.MigrationProof, decoded)
}
