/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"testing"

	"github.com/stretchr/testify/require"

	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

func validSpendDescription() snarktoken.SpendDescription {
	return snarktoken.SpendDescription{
		CommitmentIn:   make([]byte, 32),
		ValueCommitInX: make([]byte, 32),
		ValueCommitInY: make([]byte, 32),
		TypeCommitment: make([]byte, 32),
		SpendProof:     make([]byte, 292),
	}
}

func TestValidateSpendDescriptionShape_Valid(t *testing.T) {
	require.NoError(t, validateSpendDescriptionShape(0, validSpendDescription()))
}

func TestValidateSpendDescriptionShape_WrongCommitmentLength(t *testing.T) {
	d := validSpendDescription()
	d.CommitmentIn = make([]byte, 31)
	require.ErrorIs(t, validateSpendDescriptionShape(0, d), ErrMalformedAction)
}

func TestValidateSpendDescriptionShape_EmptyProof(t *testing.T) {
	d := validSpendDescription()
	d.SpendProof = nil
	require.ErrorIs(t, validateSpendDescriptionShape(0, d), ErrMalformedAction)
}

func validOutputDescription() snarktoken.OutputDescription {
	return snarktoken.OutputDescription{
		CommitmentOut:   make([]byte, 32),
		ValueCommitOutX: make([]byte, 32),
		ValueCommitOutY: make([]byte, 32),
		TypeCommitment:  make([]byte, 32),
		OutputProof:     make([]byte, 292),
	}
}

func TestValidateOutputDescriptionShape_Valid(t *testing.T) {
	require.NoError(t, validateOutputDescriptionShape(0, validOutputDescription()))
}

func TestValidateOutputDescriptionShape_WrongTypeCommitmentLength(t *testing.T) {
	d := validOutputDescription()
	d.TypeCommitment = make([]byte, 16)
	require.ErrorIs(t, validateOutputDescriptionShape(0, d), ErrMalformedAction)
}

func TestValidateOutputDescriptionShape_EmptyProof(t *testing.T) {
	d := validOutputDescription()
	d.OutputProof = nil
	require.ErrorIs(t, validateOutputDescriptionShape(0, d), ErrMalformedAction)
}

func TestValidateBindingSignatureShape(t *testing.T) {
	require.NoError(t, validateBindingSignatureShape(make([]byte, 96)))
	require.ErrorIs(t, validateBindingSignatureShape(make([]byte, 64)), ErrMalformedAction)
}

func TestValidateTransferActionShape_RejectsEmpty(t *testing.T) {
	a := &snarktoken.TransferAction{TypeCommitment: make([]byte, 32)}
	require.ErrorIs(t, validateTransferActionShape(a), ErrMalformedAction)
}

func TestValidateIssueActionShape_RequiresTotalValue(t *testing.T) {
	a := &snarktoken.IssueAction{
		TokenType:        "USD",
		TypeCommitment:   make([]byte, 32),
		Outputs:          []snarktoken.OutputDescription{validOutputDescription()},
		BindingSignature: make([]byte, 96),
		// TotalValue deliberately omitted
	}
	require.ErrorIs(t, validateIssueActionShape(a), ErrMalformedAction)
}

func validMigrationAction() *snarktoken.MigrationAction {
	return &snarktoken.MigrationAction{
		CommitmentPedersenX: make([]byte, 48),
		CommitmentPedersenY: make([]byte, 48),
		CommitmentMiMC:      make([]byte, 32),
		ValueCommitOutX:     make([]byte, 32),
		ValueCommitOutY:     make([]byte, 32),
		TypeCommitment:      make([]byte, 32),
		MigrationProof:      make([]byte, 292),
	}
}

func TestValidateMigrationActionShape_Valid(t *testing.T) {
	require.NoError(t, validateMigrationActionShape(validMigrationAction()))
}

// TestValidateMigrationActionShape_WrongPedersenXLength is the specific
// mistake fpElementLen/fieldElementLen exist to catch: an Fr-sized (32
// byte) field mistakenly supplied where an Fp-sized (48 byte) one belongs.
func TestValidateMigrationActionShape_WrongPedersenXLength(t *testing.T) {
	a := validMigrationAction()
	a.CommitmentPedersenX = make([]byte, 32)
	require.ErrorIs(t, validateMigrationActionShape(a), ErrMalformedAction)
}

func TestValidateMigrationActionShape_EmptyProof(t *testing.T) {
	a := validMigrationAction()
	a.MigrationProof = nil
	require.ErrorIs(t, validateMigrationActionShape(a), ErrMalformedAction)
}

// ── Additional spend description shape tests ────────────────────────────────

func TestValidateSpendDescriptionShape_WrongValueCommitInXLength(t *testing.T) {
	d := validSpendDescription()
	d.ValueCommitInX = make([]byte, 16)
	require.ErrorIs(t, validateSpendDescriptionShape(0, d), ErrMalformedAction)
}

func TestValidateSpendDescriptionShape_WrongValueCommitInYLength(t *testing.T) {
	d := validSpendDescription()
	d.ValueCommitInY = make([]byte, 64)
	require.ErrorIs(t, validateSpendDescriptionShape(0, d), ErrMalformedAction)
}

func TestValidateSpendDescriptionShape_WrongTypeCommitmentLength(t *testing.T) {
	d := validSpendDescription()
	d.TypeCommitment = make([]byte, 48)
	require.ErrorIs(t, validateSpendDescriptionShape(0, d), ErrMalformedAction)
}

// ── Additional output description shape tests ───────────────────────────────

func TestValidateOutputDescriptionShape_WrongCommitmentOutLength(t *testing.T) {
	d := validOutputDescription()
	d.CommitmentOut = make([]byte, 31)
	require.ErrorIs(t, validateOutputDescriptionShape(0, d), ErrMalformedAction)
}

func TestValidateOutputDescriptionShape_WrongValueCommitOutXLength(t *testing.T) {
	d := validOutputDescription()
	d.ValueCommitOutX = make([]byte, 33)
	require.ErrorIs(t, validateOutputDescriptionShape(0, d), ErrMalformedAction)
}

func TestValidateOutputDescriptionShape_WrongValueCommitOutYLength(t *testing.T) {
	d := validOutputDescription()
	d.ValueCommitOutY = make([]byte, 0)
	require.ErrorIs(t, validateOutputDescriptionShape(0, d), ErrMalformedAction)
}

// ── Additional issue action shape tests ─────────────────────────────────────

func TestValidateIssueActionShape_NoOutputs(t *testing.T) {
	a := &snarktoken.IssueAction{
		TokenType:      "USD",
		TypeCommitment: make([]byte, 32),
		TotalValue:     make([]byte, 32),
	}
	require.ErrorIs(t, validateIssueActionShape(a), ErrMalformedAction)
}

func TestValidateIssueActionShape_WrongTypeCommitmentLength(t *testing.T) {
	a := &snarktoken.IssueAction{
		TokenType:        "USD",
		TypeCommitment:   make([]byte, 16),
		TotalValue:       make([]byte, 32),
		Outputs:          []snarktoken.OutputDescription{validOutputDescription()},
		BindingSignature: make([]byte, 96),
	}
	require.ErrorIs(t, validateIssueActionShape(a), ErrMalformedAction)
}

func TestValidateIssueActionShape_Valid(t *testing.T) {
	a := &snarktoken.IssueAction{
		TokenType:        "USD",
		TypeCommitment:   make([]byte, 32),
		TotalValue:       make([]byte, 32),
		Outputs:          []snarktoken.OutputDescription{validOutputDescription()},
		BindingSignature: make([]byte, 96),
	}
	require.NoError(t, validateIssueActionShape(a))
}

func TestValidateIssueActionShape_BadOutputDescriptionPropagates(t *testing.T) {
	badOut := validOutputDescription()
	badOut.CommitmentOut = make([]byte, 1)

	a := &snarktoken.IssueAction{
		TokenType:        "USD",
		TypeCommitment:   make([]byte, 32),
		TotalValue:       make([]byte, 32),
		Outputs:          []snarktoken.OutputDescription{badOut},
		BindingSignature: make([]byte, 96),
	}
	require.ErrorIs(t, validateIssueActionShape(a), ErrMalformedAction)
}

func TestValidateIssueActionShape_BadBindingSignature(t *testing.T) {
	a := &snarktoken.IssueAction{
		TokenType:        "USD",
		TypeCommitment:   make([]byte, 32),
		TotalValue:       make([]byte, 32),
		Outputs:          []snarktoken.OutputDescription{validOutputDescription()},
		BindingSignature: make([]byte, 64),
	}
	require.ErrorIs(t, validateIssueActionShape(a), ErrMalformedAction)
}

// ── Additional migration action shape tests ─────────────────────────────────

func TestValidateMigrationActionShape_WrongPedersenYLength(t *testing.T) {
	a := validMigrationAction()
	a.CommitmentPedersenY = make([]byte, 32)
	require.ErrorIs(t, validateMigrationActionShape(a), ErrMalformedAction)
}

func TestValidateMigrationActionShape_WrongCommitmentMiMCLength(t *testing.T) {
	a := validMigrationAction()
	a.CommitmentMiMC = make([]byte, 48)
	require.ErrorIs(t, validateMigrationActionShape(a), ErrMalformedAction)
}

func TestValidateMigrationActionShape_WrongValueCommitOutXLength(t *testing.T) {
	a := validMigrationAction()
	a.ValueCommitOutX = make([]byte, 64)
	require.ErrorIs(t, validateMigrationActionShape(a), ErrMalformedAction)
}

func TestValidateMigrationActionShape_WrongValueCommitOutYLength(t *testing.T) {
	a := validMigrationAction()
	a.ValueCommitOutY = make([]byte, 0)
	require.ErrorIs(t, validateMigrationActionShape(a), ErrMalformedAction)
}

func TestValidateMigrationActionShape_WrongTypeCommitmentLength(t *testing.T) {
	a := validMigrationAction()
	a.TypeCommitment = make([]byte, 48)
	require.ErrorIs(t, validateMigrationActionShape(a), ErrMalformedAction)
}

// ── Additional transfer action shape tests ──────────────────────────────────

func TestValidateTransferActionShape_WrongTypeCommitmentLength(t *testing.T) {
	a := &snarktoken.TransferAction{
		TypeCommitment: make([]byte, 16),
		Inputs:         []snarktoken.SpendDescription{validSpendDescription()},
	}
	require.ErrorIs(t, validateTransferActionShape(a), ErrMalformedAction)
}

func TestValidateTransferActionShape_BadInputPropagates(t *testing.T) {
	badIn := validSpendDescription()
	badIn.CommitmentIn = make([]byte, 1)

	a := &snarktoken.TransferAction{
		TypeCommitment:   make([]byte, 32),
		Inputs:           []snarktoken.SpendDescription{badIn},
		BindingSignature: make([]byte, 96),
	}
	require.ErrorIs(t, validateTransferActionShape(a), ErrMalformedAction)
}

func TestValidateTransferActionShape_BadOutputPropagates(t *testing.T) {
	badOut := validOutputDescription()
	badOut.OutputProof = nil

	a := &snarktoken.TransferAction{
		TypeCommitment:   make([]byte, 32),
		Inputs:           []snarktoken.SpendDescription{validSpendDescription()},
		Outputs:          []snarktoken.OutputDescription{badOut},
		BindingSignature: make([]byte, 96),
	}
	require.ErrorIs(t, validateTransferActionShape(a), ErrMalformedAction)
}

func TestValidateTransferActionShape_Valid(t *testing.T) {
	a := &snarktoken.TransferAction{
		TypeCommitment:   make([]byte, 32),
		Inputs:           []snarktoken.SpendDescription{validSpendDescription()},
		Outputs:          []snarktoken.OutputDescription{validOutputDescription()},
		BindingSignature: make([]byte, 96),
	}
	require.NoError(t, validateTransferActionShape(a))
}
