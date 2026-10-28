/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package gadgets

import (
	"github.com/consensys/gnark-crypto/ecc"
	twistededwards2 "github.com/consensys/gnark-crypto/ecc/twistededwards"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/algebra/native/twistededwards"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// curveIDToEdwards maps the outer pairing curve to the correct twisted Edwards
// companion curve. This is the same mapping gnark's own MiMC gadget uses
// internally via utils.FieldToCurve — we replicate the mapping here because
// FieldToCurve is in an internal package (github.com/consensys/gnark/internal/utils).
//
// BLS12-381 → Jubjub, BN254 → Baby Jubjub.
var curveIDToEdwards = map[ecc.ID]twistededwards2.ID{
	ecc.BLS12_381: twistededwards2.BLS12_381,
	ecc.BN254:     twistededwards2.BN254,
}

// fieldToEdwardsCurve derives the correct twisted Edwards curve ID from the
// circuit's compile-time scalar field. This fixes a correctness bug where
// BLS12_381 was previously hardcoded: compiling any circuit for BN254 would
// silently produce a value-commitment group checked against the WRONG
// embedded curve.
func fieldToEdwardsCurve(api frontend.API) (twistededwards2.ID, error) {
	field := api.Compiler().Field()
	for eccID, edID := range curveIDToEdwards {
		if eccID.ScalarField().Cmp(field) == 0 {
			return edID, nil
		}
	}

	return 0, errors.Errorf("valueCommit: unsupported scalar field — no known twisted Edwards companion curve")
}

// ValueCommitCircuit computes cv = v·V + rcv·R in-circuit over the twisted
// Edwards curve embedded in the circuit's native scalar field.
//
// The correct embedded curve (Jubjub for BLS12-381, Baby Jubjub for BN254)
// is automatically derived from the circuit's compile target via
// fieldToEdwardsCurve. This replaces the previous hardcoded BLS12_381.
//
// genV and genR are the constant generator points V and R, passed as circuit
// constants at compile time (see Decision C). v and rcv are private witnesses.
// Returns the (X, Y) circuit variables for cv.
func ValueCommitCircuit(
	api frontend.API,
	value frontend.Variable,
	rcv frontend.Variable,
	genV twistededwards.Point,
	genR twistededwards.Point,
) (twistededwards.Point, error) {
	edID, err := fieldToEdwardsCurve(api)
	if err != nil {
		return twistededwards.Point{}, err
	}

	curve, err := twistededwards.NewEdCurve(api, edID)
	if err != nil {
		return twistededwards.Point{}, err
	}

	// A single double-base scalar multiplication instead of two ScalarMul calls
	// plus an Add. This is deliberate: ScalarMul uses the FakeGLV method, whose
	// scalar-decomposition check is an emulated-field multiplication, and that
	// calls api.Commit (a BSB22 commitment). Circuits with commitments cannot be
	// proven by plonkfri, and the single call is also cheaper (~9.7k constraints
	// against ~14k for two ScalarMul calls on BN254).
	//
	// The default (complete) path is used, not WithIncompleteArithmetic, so zero
	// scalars and identity points are handled correctly. The scalars are
	// decomposed canonically by ToBinary, as ScalarMul reduces them mod the
	// subgroup order, so s and s+order still give the same point exactly as
	// before; callers that need value to be bound beyond that must range-check it
	// (OutputCircuit does).
	return curve.DoubleBaseScalarMul(genV, genR, value, rcv), nil
}
