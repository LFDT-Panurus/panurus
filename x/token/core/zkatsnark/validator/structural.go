/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// Byte lengths, named so a failing check identifies which constant it
// compared against, not a bare magic number.
const (
	fieldElementLen   = 32  // one BLS12-381 Fr element, canonical encoding
	groth16ProofLen   = 244 // gnark's WriteTo size for a standard BLS12-381 Groth16 proof (spend/output circuits)
	migrationProofLen = 292 // migration circuit proof; gnark adds one G1 commitment (48 bytes) for emulated-field limb range checks
	signatureLen      = 96  // R.X (32) || R.Y (32) || S (32)
	fpElementLen      = 48  // the canonical encoding length of one BLS12-381 base-field (Fp) element, 48 bytes
)

// validateSpendDescriptionShape checks byte lengths only — no field-element
// decoding happens here. This must run BEFORE decode.go's canonical
// decoding: SetBytesCanonical's behavior on a wrong-length slice is not
// something to rely on for a clear error message, so length is confirmed
// first, deliberately, as its own pass.
func validateSpendDescriptionShape(i int, d snarktoken.SpendDescription) error {
	if len(d.CommitmentIn) != fieldElementLen {
		return errors.Wrapf(ErrMalformedAction, "input %d: CommitmentIn must be %d bytes, got %d", i, fieldElementLen, len(d.CommitmentIn))
	}
	if len(d.ValueCommitInX) != fieldElementLen {
		return errors.Wrapf(ErrMalformedAction, "input %d: ValueCommitInX must be %d bytes, got %d", i, fieldElementLen, len(d.ValueCommitInX))
	}
	if len(d.ValueCommitInY) != fieldElementLen {
		return errors.Wrapf(ErrMalformedAction, "input %d: ValueCommitInY must be %d bytes, got %d", i, fieldElementLen, len(d.ValueCommitInY))
	}
	if len(d.TokenType) != fieldElementLen {
		return errors.Wrapf(ErrMalformedAction, "input %d: TokenType must be %d bytes, got %d", i, fieldElementLen, len(d.TokenType))
	}
	if len(d.SpendProof) != groth16ProofLen {
		return errors.Wrapf(ErrMalformedAction, "input %d: SpendProof must be %d bytes, got %d", i, groth16ProofLen, len(d.SpendProof))
	}

	return nil
}

// validateOutputDescriptionShape is the OutputDescription equivalent.
func validateOutputDescriptionShape(j int, d snarktoken.OutputDescription) error {
	if len(d.CommitmentOut) != fieldElementLen {
		return errors.Wrapf(ErrMalformedAction, "output %d: CommitmentOut must be %d bytes, got %d", j, fieldElementLen, len(d.CommitmentOut))
	}
	if len(d.ValueCommitOutX) != fieldElementLen {
		return errors.Wrapf(ErrMalformedAction, "output %d: ValueCommitOutX must be %d bytes, got %d", j, fieldElementLen, len(d.ValueCommitOutX))
	}
	if len(d.ValueCommitOutY) != fieldElementLen {
		return errors.Wrapf(ErrMalformedAction, "output %d: ValueCommitOutY must be %d bytes, got %d", j, fieldElementLen, len(d.ValueCommitOutY))
	}
	if len(d.TokenType) != fieldElementLen {
		return errors.Wrapf(ErrMalformedAction, "output %d: TokenType must be %d bytes, got %d", j, fieldElementLen, len(d.TokenType))
	}
	if len(d.OutputProof) != groth16ProofLen {
		return errors.Wrapf(ErrMalformedAction, "output %d: OutputProof must be %d bytes, got %d", j, groth16ProofLen, len(d.OutputProof))
	}

	return nil
}

func validateBindingSignatureShape(sig []byte) error {
	if len(sig) != signatureLen {
		return errors.Wrapf(ErrMalformedAction, "BindingSignature must be %d bytes, got %d", signatureLen, len(sig))
	}

	return nil
}

// validateTransferActionShape runs every structural check for a
// TransferAction, cheapest first.
func validateTransferActionShape(a *snarktoken.TransferAction) error {
	if len(a.Inputs) == 0 && len(a.Outputs) == 0 {
		return errors.Wrapf(ErrMalformedAction, "transfer action has no inputs and no outputs")
	}
	for i, d := range a.Inputs {
		if err := validateSpendDescriptionShape(i, d); err != nil {
			return err
		}
	}
	for j, d := range a.Outputs {
		if err := validateOutputDescriptionShape(j, d); err != nil {
			return err
		}
	}

	return validateBindingSignatureShape(a.BindingSignature)
}

// validateIssueActionShape is the IssueAction equivalent. TotalValue is
// required — see token/issue.go's doc comment on that field for why.
func validateIssueActionShape(a *snarktoken.IssueAction) error {
	if len(a.Outputs) == 0 {
		return errors.Wrapf(ErrMalformedAction, "issue action has no outputs")
	}
	if len(a.TotalValue) != fieldElementLen {
		return errors.Wrapf(ErrMalformedAction, "TotalValue must be %d bytes, got %d", fieldElementLen, len(a.TotalValue))
	}
	for j, d := range a.Outputs {
		if err := validateOutputDescriptionShape(j, d); err != nil {
			return err
		}
	}

	return validateBindingSignatureShape(a.BindingSignature)
}

// validateMigrationActionShape checks byte lengths for a MigrationAction.
func validateMigrationActionShape(a *snarktoken.MigrationAction) error {
	if len(a.CommitmentPedersenX) != fpElementLen {
		return errors.Wrapf(ErrMalformedAction, "CommitmentPedersenX must be %d bytes, got %d", fpElementLen, len(a.CommitmentPedersenX))
	}
	if len(a.CommitmentPedersenY) != fpElementLen {
		return errors.Wrapf(ErrMalformedAction, "CommitmentPedersenY must be %d bytes, got %d", fpElementLen, len(a.CommitmentPedersenY))
	}
	if len(a.CommitmentMiMC) != fieldElementLen {
		return errors.Wrapf(ErrMalformedAction, "CommitmentMiMC must be %d bytes, got %d", fieldElementLen, len(a.CommitmentMiMC))
	}
	if len(a.ValueCommitOutX) != fieldElementLen {
		return errors.Wrapf(ErrMalformedAction, "ValueCommitOutX must be %d bytes, got %d", fieldElementLen, len(a.ValueCommitOutX))
	}
	if len(a.ValueCommitOutY) != fieldElementLen {
		return errors.Wrapf(ErrMalformedAction, "ValueCommitOutY must be %d bytes, got %d", fieldElementLen, len(a.ValueCommitOutY))
	}
	if len(a.TokenType) != fieldElementLen {
		return errors.Wrapf(ErrMalformedAction, "TokenType must be %d bytes, got %d", fieldElementLen, len(a.TokenType))
	}
	if len(a.MigrationProof) != migrationProofLen {
		return errors.Wrapf(ErrMalformedAction, "MigrationProof must be %d bytes, got %d", migrationProofLen, len(a.MigrationProof))
	}

	return nil
}
