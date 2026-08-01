/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"math/big"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// Validator verifies the cryptographic validity of zkatsnark actions. It
// holds no ledger connection and no mutable state beyond its loaded
// verification keys, every ValidateX call is a pure function of its
// argument and PublicParams.
type Validator struct {
	pp          *pp.PublicParams
	vkSpend     groth16.VerifyingKey
	vkOutput    groth16.VerifyingKey
	vkMigration groth16.VerifyingKey // nil until migration setup has run for this deployment
}

// NewValidator constructs a Validator from PublicParams, loading the Spend
// and Output verification keys eagerly. The Migration verification key is
// loaded best-effort: MigrationCircuit setup (setup.SetupMigration) may not
// have run yet for a given deployment, and that must not prevent
// constructing a Validator at all, Transfer and Issue validation have to
// keep working regardless of Migration's state.
func NewValidator(p *pp.PublicParams) (*Validator, error) {
	vkSpend, err := setup.LoadVerifyingKey(p, setup.CircuitSpend)
	if err != nil {
		return nil, errors.Wrapf(err, "validator: loading spend verifying key")
	}
	vkOutput, err := setup.LoadVerifyingKey(p, setup.CircuitOutput)
	if err != nil {
		return nil, errors.Wrapf(err, "validator: loading output verifying key")
	}

	v := &Validator{pp: p, vkSpend: vkSpend, vkOutput: vkOutput}

	if vkm, err := setup.LoadVerifyingKey(p, setup.CircuitMigration); err == nil {
		v.vkMigration = vkm
	}

	return v, nil
}

// ValidateTransfer checks the cryptographic validity of a TransferAction.
// Order: structural shape, decode, type homogeneity, parallel proof
// verification, binding signature, cheapest checks first, so a
// structurally broken or type-inconsistent action never reaches the
// millisecond-scale pairing computations in proof verification.
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

	if err := checkTypeHomogeneitySpend(a.TokenType, decodedInputs); err != nil {
		return err
	}
	if err := checkTypeHomogeneityOutput(a.TokenType, decodedOutputs); err != nil {
		return err
	}

	if err := verifyAllProofs(v.vkSpend, v.vkOutput, v.pp.Curve, a.Inputs, decodedInputs, a.Outputs, decodedOutputs); err != nil {
		return err
	}

	// Transfers reconcile Σvalue_in against Σvalue_out entirely through
	// hidden inputs and outputs, no publicly-visible value enters or
	// leaves the hidden set, so publicValueDelta is always 0
	// here.
	return verifyBindingSignature(
		snarktoken.ActionTypeTransfer, a.TokenType,
		decodedInputs, decodedOutputs,
		a.BindingSignature, 0,
	)
}

// ValidateIssue checks the cryptographic validity of an IssueAction.
// This depends on IssueAction.TotalValue is the public, canonically-encoded
// Σvalue_out across every output, because there is no other public source
// for ComputeBVK's publicValueDelta term when there are zero inputs.
// Without it, reconstructing bvk for an issuance is mathematically
// impossible, not merely inconvenient.
func (v *Validator) ValidateIssue(a *snarktoken.IssueAction) error {
	if err := validateIssueActionShape(a); err != nil {
		return err
	}

	decodedOutputs, err := decodeAllOutputs(a.Outputs)
	if err != nil {
		return err
	}

	if err := checkTypeHomogeneityOutput(a.TokenType, decodedOutputs); err != nil {
		return err
	}

	if err := verifyAllProofs(v.vkSpend, v.vkOutput, v.pp.Curve, nil, nil, a.Outputs, decodedOutputs); err != nil {
		return err
	}

	var totalValueField fr.Element
	if err := totalValueField.SetBytesCanonical(a.TotalValue); err != nil {
		return errors.Wrapf(err, "validator: TotalValue not canonical")
	}

	totalValue := totalValueField.BigInt(new(big.Int)).Uint64()

	return verifyBindingSignature(
		snarktoken.ActionTypeIssue, a.TokenType,
		nil, decodedOutputs,
		a.BindingSignature, totalValue,
	)
}

// ValidateMigration checks the cryptographic validity of a MigrationAction.
// No binding-signature check happens here, MigrationCircuit
// proves value conservation entirely through its own shared-witness
// binding across the Pedersen-opening and MiMC/value-commitment constraint
// groups, a structural R1CS property, not an additional Schnorr proof.
func (v *Validator) ValidateMigration(a *snarktoken.MigrationAction) error {
	if v.vkMigration == nil {
		return ErrMigrationNotConfigured
	}

	if err := validateMigrationActionShape(a); err != nil {
		return err
	}

	decoded, err := decodeMigrationAction(a)
	if err != nil {
		return err
	}

	return verifyMigrationProof(v.vkMigration, v.pp.Curve, a.MigrationProof, decoded)
}
