/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"fmt"
	"testing"

	mathlib "github.com/IBM/mathlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTypedErrors verifies that validation functions return the correct typed errors
// and that callers can use errors.Is() for error checking.
// Given various invalid input scenarios,
// When the validation functions are called,
// Then they should return specific typed errors (e.g., ErrNilCurve, ErrInvalidLength).
func TestTypedErrors(t *testing.T) {
	curves := []mathlib.CurveID{
		mathlib.BLS12_381_BBS_GURVY,
		mathlib.BN254,
	}

	for _, curveID := range curves {
		t.Run(fmt.Sprintf("%d", curveID), func(t *testing.T) {
			curve := mathlib.Curves[curveID]

			t.Run("NilCurveError", func(t *testing.T) {
				// Test validateCurve with nil curve
				err := validateCurve(nil)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrNilCurve)
			})

			t.Run("InvalidLengthError", func(t *testing.T) {
				// Test validateG1Slice with wrong length
				elements := []*mathlib.G1{curve.GenG1}
				err := validateG1Slice("test", elements, curve, 2)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrInvalidLength)
			})

			t.Run("NilElementError", func(t *testing.T) {
				// Test validateG1Slice with nil element
				elements := []*mathlib.G1{curve.GenG1, nil}
				err := validateG1Slice("test", elements, curve, 2)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "test validation failed")
			})

			t.Run("WrongCurveIDError", func(t *testing.T) {
				// Test validateG1Slice with element from different curve
				otherCurveID := mathlib.BN254
				if curveID == mathlib.BN254 {
					otherCurveID = mathlib.BLS12_381_BBS_GURVY
				}
				otherCurve := mathlib.Curves[otherCurveID]
				elements := []*mathlib.G1{curve.GenG1, otherCurve.GenG1}
				err := validateG1Slice("test", elements, curve, 2)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "test validation failed")
			})

			t.Run("NilCommitmentError", func(t *testing.T) {
				// Test CSP prover with nil commitment
				rand, err := curve.Rand()
				require.NoError(t, err)

				rounds := uint64(2)
				size := 1 << rounds
				gens := make([]*mathlib.G1, size)
				lf := make([]*mathlib.Zr, size)
				wit := make([]*mathlib.Zr, size)
				for i := range size {
					gens[i] = curve.GenG1
					lf[i] = curve.NewRandomZr(rand)
					wit[i] = curve.NewRandomZr(rand)
				}

				p := &prover{
					Commitment:     nil, // nil commitment
					Generators:     gens,
					LinearForm:     lf,
					Value:          curve.NewRandomZr(rand),
					NumberOfRounds: rounds,
					Curve:          curve,
					witness:        wit,
				}

				err = validateCSPProverInputs(curve, p)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrNilCommitment)
			})

			t.Run("NilValueError", func(t *testing.T) {
				// Test range prover with nil value
				n := uint64(8)
				vGens := []*mathlib.G1{curve.GenG1, curve.GenG1}
				aGens := make([]*mathlib.G1, n+1)
				bGens := make([]*mathlib.G1, n+1)
				for i := range aGens {
					aGens[i] = curve.GenG1
					bGens[i] = curve.GenG1
				}

				p := &rangeProver{
					VCommitment:  curve.GenG1,
					v:            nil, // nil value
					r:            curve.NewZrFromInt(1),
					VGenerators:  vGens,
					AGenerators:  aGens,
					BGenerators:  bGens,
					NumberOfBits: n,
					Curve:        curve,
				}

				err := validateRangeProverInputs(curve, p)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrNilValue)
			})

			t.Run("InvalidBitCountError", func(t *testing.T) {
				// Test range prover with invalid bit count
				vGens := []*mathlib.G1{curve.GenG1, curve.GenG1}

				p := &rangeProver{
					VCommitment:  curve.GenG1,
					v:            curve.NewZrFromInt(1),
					r:            curve.NewZrFromInt(1),
					VGenerators:  vGens,
					AGenerators:  []*mathlib.G1{},
					BGenerators:  []*mathlib.G1{},
					NumberOfBits: 0, // invalid bit count
					Curve:        curve,
				}

				err := validateRangeProverInputs(curve, p)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrInvalidBitCount)
			})

			t.Run("NilProofError", func(t *testing.T) {
				// Test validateRangeProof with nil proof
				err := validateRangeProof(curve, nil)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrNilProof)
			})
		})
	}
}
