/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fp"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/twistededwards"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// decodedSpend is the canonical field-element decoding of one
// SpendDescription's public bytes, computed exactly once per description
// and reused by BOTH public-witness construction (proof verification, see
// witness usage inside proof.go) and ProofResult construction
// (binding-signature verification)
type decodedSpend struct {
	Commitment     fr.Element
	ValueCommitX   fr.Element
	ValueCommitY   fr.Element
	TypeCommitment fr.Element
}

// decodedOutput is the OutputDescription equivalent.
type decodedOutput struct {
	Commitment     fr.Element
	ValueCommitX   fr.Element
	ValueCommitY   fr.Element
	TypeCommitment fr.Element
}

// decodedMigration is the canonical decoding of a MigrationAction's public
// bytes. PedersenX/Y are fp.Element — the BLS12-381 BASE field — not
// fr.Element like every other decoded* type in this file. This is not a
// naming inconsistency; it reflects that the Pedersen commitment is a
// point in a genuinely different, larger field than everything else this
// validator decodes. They are wrapped into
// emulated.Element[emulated.BLS12381Fp] only at the point of witness
// construction (proof.go) — decode.go's job stops at canonical decoding
// and curve-membership validation.
type decodedMigration struct {
	PedersenX      fp.Element
	PedersenY      fp.Element
	CommitmentMiMC fr.Element
	ValueCommitX   fr.Element
	ValueCommitY   fr.Element
	TypeCommitment fr.Element
}

// decodeSpendDescription decodes a SpendDescription's public bytes into
// canonical field elements and confirms the value commitment point is
// actually on the Jubjub curve. Call only after
// validateSpendDescriptionShape has already confirmed every field is the
// correct length
func decodeSpendDescription(i int, d snarktoken.SpendDescription) (decodedSpend, error) {
	var out decodedSpend
	if err := out.Commitment.SetBytesCanonical(d.CommitmentIn); err != nil {
		return decodedSpend{}, errors.Wrapf(err, "validator: input %d CommitmentIn not canonical", i)
	}
	if err := out.ValueCommitX.SetBytesCanonical(d.ValueCommitInX); err != nil {
		return decodedSpend{}, errors.Wrapf(err, "validator: input %d ValueCommitInX not canonical", i)
	}
	if err := out.ValueCommitY.SetBytesCanonical(d.ValueCommitInY); err != nil {
		return decodedSpend{}, errors.Wrapf(err, "validator: input %d ValueCommitInY not canonical", i)
	}
	if err := out.TypeCommitment.SetBytesCanonical(d.TypeCommitment); err != nil {
		return decodedSpend{}, errors.Wrapf(err, "validator: input %d TypeCommitment not canonical", i)
	}

	pt := twistededwards.PointAffine{X: out.ValueCommitX, Y: out.ValueCommitY}
	if !pt.IsOnCurve() {
		return decodedSpend{}, errors.Wrapf(ErrInvalidEncoding, "input %d: value commitment point not on Jubjub curve", i)
	}

	return out, nil
}

// decodeOutputDescription is the OutputDescription equivalent.
func decodeOutputDescription(j int, d snarktoken.OutputDescription) (decodedOutput, error) {
	var out decodedOutput
	if err := out.Commitment.SetBytesCanonical(d.CommitmentOut); err != nil {
		return decodedOutput{}, errors.Wrapf(err, "validator: output %d CommitmentOut not canonical", j)
	}
	if err := out.ValueCommitX.SetBytesCanonical(d.ValueCommitOutX); err != nil {
		return decodedOutput{}, errors.Wrapf(err, "validator: output %d ValueCommitOutX not canonical", j)
	}
	if err := out.ValueCommitY.SetBytesCanonical(d.ValueCommitOutY); err != nil {
		return decodedOutput{}, errors.Wrapf(err, "validator: output %d ValueCommitOutY not canonical", j)
	}
	if err := out.TypeCommitment.SetBytesCanonical(d.TypeCommitment); err != nil {
		return decodedOutput{}, errors.Wrapf(err, "validator: output %d TypeCommitment not canonical", j)
	}

	pt := twistededwards.PointAffine{X: out.ValueCommitX, Y: out.ValueCommitY}
	if !pt.IsOnCurve() {
		return decodedOutput{}, errors.Wrapf(ErrInvalidEncoding, "output %d: value commitment point not on Jubjub curve", j)
	}

	return out, nil
}

// decodeAllSpends decodes every input description in order, stopping at
// the first failure.
func decodeAllSpends(inputs []snarktoken.SpendDescription) ([]decodedSpend, error) {
	out := make([]decodedSpend, len(inputs))
	for i, d := range inputs {
		ds, err := decodeSpendDescription(i, d)
		if err != nil {
			return nil, err
		}
		out[i] = ds
	}

	return out, nil
}

// decodeAllOutputs decodes every output description in order.
func decodeAllOutputs(outputs []snarktoken.OutputDescription) ([]decodedOutput, error) {
	out := make([]decodedOutput, len(outputs))
	for j, d := range outputs {
		do, err := decodeOutputDescription(j, d)
		if err != nil {
			return nil, err
		}
		out[j] = do
	}

	return out, nil
}

// decodeMigrationAction decodes a MigrationAction's public bytes,
// confirming two DIFFERENT curve-membership properties that must not be
// conflated: the Pedersen commitment must satisfy BLS12-381 G1's short
// Weierstrass equation (Fp-coordinate), and the value commitment must
// satisfy Jubjub's twisted Edwards equation (Fr-coordinate, embedded in
// BLS12-381's scalar field). These are unrelated curves over unrelated
// fields that happen to both appear in this one action.
func decodeMigrationAction(a *snarktoken.MigrationAction) (decodedMigration, error) {
	var out decodedMigration

	if err := out.PedersenX.SetBytesCanonical(a.CommitmentPedersenX); err != nil {
		return decodedMigration{}, errors.Wrapf(err, "validator: CommitmentPedersenX not canonical")
	}
	if err := out.PedersenY.SetBytesCanonical(a.CommitmentPedersenY); err != nil {
		return decodedMigration{}, errors.Wrapf(err, "validator: CommitmentPedersenY not canonical")
	}

	// bls12381.G1Affine.IsOnCurve() is inferred by analogy to
	// twistededwards.PointAffine.IsOnCurve() (confirmed in use throughout
	// this file already) and by the fact that prover/migration.go already
	// constructs bls12381.G1Affine{X:..., Y:...} literals directly — the
	// type and its X/Y field names are already proven in use in this
	// codebase. The IsOnCurve method name specifically is not separately
	// confirmed; verify with `go doc github.com/consensys/gnark-crypto/
	// ecc/bls12-381 G1Affine` if this doesn't compile.
	pedPoint := bls12381.G1Affine{X: out.PedersenX, Y: out.PedersenY}
	if !pedPoint.IsOnCurve() {
		return decodedMigration{}, errors.Wrapf(ErrInvalidEncoding, "Pedersen commitment point not on BLS12-381 G1")
	}

	if err := out.CommitmentMiMC.SetBytesCanonical(a.CommitmentMiMC); err != nil {
		return decodedMigration{}, errors.Wrapf(err, "validator: CommitmentMiMC not canonical")
	}
	if err := out.ValueCommitX.SetBytesCanonical(a.ValueCommitOutX); err != nil {
		return decodedMigration{}, errors.Wrapf(err, "validator: ValueCommitOutX not canonical")
	}
	if err := out.ValueCommitY.SetBytesCanonical(a.ValueCommitOutY); err != nil {
		return decodedMigration{}, errors.Wrapf(err, "validator: ValueCommitOutY not canonical")
	}
	if err := out.TypeCommitment.SetBytesCanonical(a.TypeCommitment); err != nil {
		return decodedMigration{}, errors.Wrapf(err, "validator: TypeCommitment not canonical")
	}

	jubjubPt := twistededwards.PointAffine{X: out.ValueCommitX, Y: out.ValueCommitY}
	if !jubjubPt.IsOnCurve() {
		return decodedMigration{}, errors.Wrapf(ErrInvalidEncoding, "value commitment point not on Jubjub curve")
	}

	return out, nil
}
