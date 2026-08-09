/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"testing"

	"github.com/stretchr/testify/require"

	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

func validSpendDescription() snarktoken.SpendDescription {
	return snarktoken.SpendDescription{
		CommitmentIn:   make([]byte, 32),
		ValueCommitInX: make([]byte, 32),
		ValueCommitInY: make([]byte, 32),
		TypeCommitment: make([]byte, 32),
		SpendProof:     make([]byte, 244),
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

func TestValidateSpendDescriptionShape_WrongProofLength(t *testing.T) {
	d := validSpendDescription()
	d.SpendProof = make([]byte, 191)
	require.ErrorIs(t, validateSpendDescriptionShape(0, d), ErrMalformedAction)
}

func validOutputDescription() snarktoken.OutputDescription {
	return snarktoken.OutputDescription{
		CommitmentOut:   make([]byte, 32),
		ValueCommitOutX: make([]byte, 32),
		ValueCommitOutY: make([]byte, 32),
		TypeCommitment:  make([]byte, 32),
		OutputProof:     make([]byte, 244),
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

func TestValidateMigrationActionShape_WrongProofLength(t *testing.T) {
	a := validMigrationAction()
	a.MigrationProof = make([]byte, 100)
	require.ErrorIs(t, validateMigrationActionShape(a), ErrMalformedAction)
}
