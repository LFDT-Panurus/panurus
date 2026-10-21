/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"math/big"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/edwards"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/legacy"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

// decodedSpend is the canonical decoding of one SpendDescription's public
// bytes, computed exactly once per description and reused by BOTH
// public-witness construction (proof verification, see proof.go) and
// ProofResult construction (binding-signature verification).
//
// Field elements are held as *big.Int, already checked to be canonical for the
// scalar field of the curve in the public parameters, so this package never
// depends on a particular curve's element type.
type decodedSpend struct {
	Commitment     *big.Int
	ValueCommitX   *big.Int
	ValueCommitY   *big.Int
	TypeCommitment *big.Int
	// ValueCommitXBytes and ValueCommitYBytes are the wire bytes of the value
	// commitment, kept for the binding-signature path.
	ValueCommitXBytes []byte
	ValueCommitYBytes []byte
	CommitmentBytes   []byte
}

// decodedOutput is the OutputDescription equivalent.
type decodedOutput struct {
	Commitment        *big.Int
	ValueCommitX      *big.Int
	ValueCommitY      *big.Int
	TypeCommitment    *big.Int
	ValueCommitXBytes []byte
	ValueCommitYBytes []byte
	CommitmentBytes   []byte
}

// decodedMigration is the canonical decoding of a MigrationAction's public
// bytes. PedersenX/Y are coordinates of a point on the legacy driver's
// BLS12-381 G1 (a base-field element, not a scalar-field one); they are
// wrapped into emulated elements only at witness construction (proof.go).
type decodedMigration struct {
	PedersenX      *big.Int
	PedersenY      *big.Int
	CommitmentMiMC *big.Int
	ValueCommitX   *big.Int
	ValueCommitY   *big.Int
	TypeCommitment *big.Int
}

// decodeScalar decodes b as a canonical element of the scalar field of curve:
// exactly the field's byte width, big-endian, strictly below the modulus.
func decodeScalar(curve ecc.ID, b []byte) (*big.Int, error) {
	mod := curve.ScalarField()
	if want := (mod.BitLen() + 7) / 8; len(b) != want {
		return nil, errors.Wrapf(ErrInvalidEncoding, "expected %d bytes, got %d", want, len(b))
	}
	v := new(big.Int).SetBytes(b)
	if v.Cmp(mod) >= 0 {
		return nil, errors.Wrapf(ErrInvalidEncoding, "value not canonical for curve %s", curve)
	}

	return v, nil
}

// checkValueCommitPoint confirms (x, y) are canonical for curve and that the
// point lies on the twisted Edwards curve embedded in it.
func checkValueCommitPoint(curve ecc.ID, x, y []byte) error {
	suite, err := edwards.ForCurve(curve)
	if err != nil {
		return errors.Wrapf(ErrInvalidEncoding, "%s", err)
	}
	if err := suite.CheckPoint(edwards.Coords{X: x, Y: y}); err != nil {
		return errors.Wrapf(ErrInvalidEncoding, "value commitment: %s", err)
	}

	return nil
}

// decodeFields decodes the named byte fields as scalars of curve, stopping at
// the first failure.
func decodeFields(curve ecc.ID, fields ...namedField) error {
	for _, f := range fields {
		v, err := decodeScalar(curve, f.raw)
		if err != nil {
			return errors.Wrapf(err, "validator: %s not canonical", f.name)
		}
		*f.dst = v
	}

	return nil
}

type namedField struct {
	name string
	raw  []byte
	dst  **big.Int
}

// decodeSpendDescription decodes a SpendDescription's public bytes into
// canonical field elements of curve and confirms the value commitment point
// lies on the curve's embedded twisted Edwards curve. Call only after
// validateSpendDescriptionShape has confirmed every field has the correct length.
func decodeSpendDescription(i int, d snarktoken.SpendDescription, curve ecc.ID) (decodedSpend, error) {
	out := decodedSpend{
		ValueCommitXBytes: d.ValueCommitInX,
		ValueCommitYBytes: d.ValueCommitInY,
		CommitmentBytes:   d.CommitmentIn,
	}
	if err := decodeFields(curve,
		namedField{"input CommitmentIn", d.CommitmentIn, &out.Commitment},
		namedField{"input ValueCommitInX", d.ValueCommitInX, &out.ValueCommitX},
		namedField{"input ValueCommitInY", d.ValueCommitInY, &out.ValueCommitY},
		namedField{"input TypeCommitment", d.TypeCommitment, &out.TypeCommitment},
	); err != nil {
		return decodedSpend{}, errors.Wrapf(err, "input %d", i)
	}
	if err := checkValueCommitPoint(curve, d.ValueCommitInX, d.ValueCommitInY); err != nil {
		return decodedSpend{}, errors.Wrapf(err, "input %d", i)
	}

	return out, nil
}

// decodeOutputDescription is the OutputDescription equivalent.
func decodeOutputDescription(j int, d snarktoken.OutputDescription, curve ecc.ID) (decodedOutput, error) {
	out := decodedOutput{
		ValueCommitXBytes: d.ValueCommitOutX,
		ValueCommitYBytes: d.ValueCommitOutY,
		CommitmentBytes:   d.CommitmentOut,
	}
	if err := decodeFields(curve,
		namedField{"output CommitmentOut", d.CommitmentOut, &out.Commitment},
		namedField{"output ValueCommitOutX", d.ValueCommitOutX, &out.ValueCommitX},
		namedField{"output ValueCommitOutY", d.ValueCommitOutY, &out.ValueCommitY},
		namedField{"output TypeCommitment", d.TypeCommitment, &out.TypeCommitment},
	); err != nil {
		return decodedOutput{}, errors.Wrapf(err, "output %d", j)
	}
	if err := checkValueCommitPoint(curve, d.ValueCommitOutX, d.ValueCommitOutY); err != nil {
		return decodedOutput{}, errors.Wrapf(err, "output %d", j)
	}

	return out, nil
}

// decodeAllSpends decodes every input description in order, stopping at
// the first failure.
func decodeAllSpends(inputs []snarktoken.SpendDescription, curve ecc.ID) ([]decodedSpend, error) {
	out := make([]decodedSpend, len(inputs))
	for i, d := range inputs {
		ds, err := decodeSpendDescription(i, d, curve)
		if err != nil {
			return nil, err
		}
		out[i] = ds
	}

	return out, nil
}

// decodeAllOutputs decodes every output description in order.
func decodeAllOutputs(outputs []snarktoken.OutputDescription, curve ecc.ID) ([]decodedOutput, error) {
	out := make([]decodedOutput, len(outputs))
	for j, d := range outputs {
		do, err := decodeOutputDescription(j, d, curve)
		if err != nil {
			return nil, err
		}
		out[j] = do
	}

	return out, nil
}

// decodeMigrationAction decodes a MigrationAction's public bytes against curve,
// confirming two DIFFERENT curve-membership properties that must not be
// conflated: the Pedersen commitment must satisfy the legacy driver's
// BLS12-381 G1 equation (base-field coordinates), and the value commitment must
// satisfy the twisted Edwards equation embedded in curve's scalar field.
func decodeMigrationAction(a *snarktoken.MigrationAction, curve ecc.ID) (decodedMigration, error) {
	var out decodedMigration

	if err := legacy.CheckPedersenPoint(a.CommitmentPedersenX, a.CommitmentPedersenY); err != nil {
		return decodedMigration{}, errors.Wrapf(ErrInvalidEncoding, "%s", err)
	}
	out.PedersenX = new(big.Int).SetBytes(a.CommitmentPedersenX)
	out.PedersenY = new(big.Int).SetBytes(a.CommitmentPedersenY)

	if err := decodeFields(curve,
		namedField{"CommitmentMiMC", a.CommitmentMiMC, &out.CommitmentMiMC},
		namedField{"ValueCommitOutX", a.ValueCommitOutX, &out.ValueCommitX},
		namedField{"ValueCommitOutY", a.ValueCommitOutY, &out.ValueCommitY},
		namedField{"TypeCommitment", a.TypeCommitment, &out.TypeCommitment},
	); err != nil {
		return decodedMigration{}, err
	}

	if err := checkValueCommitPoint(curve, a.ValueCommitOutX, a.ValueCommitOutY); err != nil {
		return decodedMigration{}, err
	}

	return out, nil
}
