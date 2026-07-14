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

// TestValidateCurve verifies that the validateCurve function correctly validates
// elliptic curve instances used in CSP proofs. This is a foundational validation
// that ensures all cryptographic operations have a valid curve context.
//
// Given a curve instance (valid or nil),
// When the validateCurve function is called,
// Then it should correctly identify valid curves and reject nil instances.
func TestValidateCurve(t *testing.T) {
	curves := []mathlib.CurveID{
		mathlib.BLS12_381_BBS_GURVY,
		mathlib.BN254,
	}

	for _, curveID := range curves {
		t.Run(fmt.Sprintf("%d", curveID), func(t *testing.T) {
			curve := mathlib.Curves[curveID]

			t.Run("ValidCurve", func(t *testing.T) {
				// Verify that a valid curve instance passes validation
				err := validateCurve(curve)
				require.NoError(t, err)
			})

			t.Run("NilCurve", func(t *testing.T) {
				// Verify that nil curve is rejected to prevent nil pointer dereferences
				err := validateCurve(nil)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrNilCurve)
				assert.Contains(t, err.Error(), "curve cannot be nil")
			})
		})
	}
}

// TestValidateG1Slice verifies that G1 point slices (elliptic curve group elements)
// are properly validated before use in CSP proofs. G1 elements represent commitments,
// generators, and proof components in the cryptographic protocol.
//
// Given a slice of G1 elements (valid, nil, wrong length, or containing nil),
// When the validateG1Slice function is called,
// Then it should only accept correctly formatted slices.
func TestValidateG1Slice(t *testing.T) {
	curves := []mathlib.CurveID{
		mathlib.BLS12_381_BBS_GURVY,
		mathlib.BN254,
	}

	for _, curveID := range curves {
		t.Run(fmt.Sprintf("%d", curveID), func(t *testing.T) {
			curve := mathlib.Curves[curveID]

			t.Run("ValidSlice", func(t *testing.T) {
				// Verify that a slice of valid G1 points with correct length passes validation
				elements := []*mathlib.G1{curve.GenG1, curve.GenG1}
				err := validateG1Slice("test", elements, curve, 2)
				require.NoError(t, err)
			})

			t.Run("NilSlice", func(t *testing.T) {
				// Verify that nil slice is rejected to prevent nil pointer dereferences
				err := validateG1Slice("test", nil, curve, 2)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "test cannot be nil")
			})

			t.Run("WrongLength", func(t *testing.T) {
				// Verify that slices with incorrect length are rejected (security critical)
				elements := []*mathlib.G1{curve.GenG1}
				err := validateG1Slice("test", elements, curve, 2)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrInvalidLength)
				assert.Contains(t, err.Error(), "invalid length")
			})

			t.Run("NilElement", func(t *testing.T) {
				// Verify that slices containing nil elements are detected and rejected
				elements := []*mathlib.G1{curve.GenG1, nil}
				err := validateG1Slice("test", elements, curve, 2)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "test validation failed")
			})

			t.Run("InfinityElement", func(t *testing.T) {
				// Verify that slices containing the identity point are rejected;
				// a generator at infinity collapses the commitment scheme.
				elements := []*mathlib.G1{curve.GenG1, curve.NewG1()}
				err := validateG1Slice("test", elements, curve, 2)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "test validation failed")
			})
		})
	}
}

// TestValidateZrSlice verifies that Zr scalar slices (field elements) are properly
// validated before use in CSP proofs. Zr elements represent witnesses, challenges,
// responses, and other scalar values in the cryptographic protocol.
//
// Given a slice of Zr elements (valid, nil, wrong length, or containing nil),
// When the validateZrSlice function is called,
// Then it should only accept correctly formatted slices consistent with the curve field.
func TestValidateZrSlice(t *testing.T) {
	curves := []mathlib.CurveID{
		mathlib.BLS12_381_BBS_GURVY,
		mathlib.BN254,
	}

	for _, curveID := range curves {
		t.Run(fmt.Sprintf("%d", curveID), func(t *testing.T) {
			curve := mathlib.Curves[curveID]

			t.Run("ValidSlice", func(t *testing.T) {
				// Verify that a slice of valid Zr scalars with correct length passes validation
				elements := []*mathlib.Zr{curve.NewZrFromInt(1), curve.NewZrFromInt(2)}
				err := validateZrSlice("test", elements, curve, 2)
				require.NoError(t, err)
			})

			t.Run("NilSlice", func(t *testing.T) {
				// Verify that nil slice is rejected to prevent nil pointer dereferences
				err := validateZrSlice("test", nil, curve, 2)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "test cannot be nil")
			})

			t.Run("WrongLength", func(t *testing.T) {
				// Verify that slices with incorrect length are rejected (security critical)
				elements := []*mathlib.Zr{curve.NewZrFromInt(1)}
				err := validateZrSlice("test", elements, curve, 2)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrInvalidLength)
				assert.Contains(t, err.Error(), "invalid length")
			})

			t.Run("NilElement", func(t *testing.T) {
				// Verify that slices containing nil elements are detected and rejected
				elements := []*mathlib.Zr{curve.NewZrFromInt(1), nil}
				err := validateZrSlice("test", elements, curve, 2)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "validation failed")
			})
		})
	}
}

// TestValidateCSPProverInputs verifies that all inputs to the CSP prover are properly
// validated before proof generation. The CSP (Compressed Sigma Protocol) prover requires
// specific structural constraints on its inputs to ensure correct and secure proof generation.
//
// Given a CSP prover instance with various input configurations,
// When the validateCSPProverInputs function is called,
// Then it should correctly validate the curve, commitment, value, generators, and witness.
func TestValidateCSPProverInputs(t *testing.T) {
	curves := []mathlib.CurveID{
		mathlib.BLS12_381_BBS_GURVY,
		mathlib.BN254,
	}

	for _, curveID := range curves {
		t.Run(fmt.Sprintf("%d", curveID), func(t *testing.T) {
			curve := mathlib.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			// Create valid prover with 2^2 = 4 elements
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
				Commitment:     curve.GenG1,
				Generators:     gens,
				LinearForm:     lf,
				Value:          curve.NewRandomZr(rand),
				NumberOfRounds: rounds,
				Curve:          curve,
				witness:        wit,
			}

			t.Run("ValidInputs", func(t *testing.T) {
				// Verify that a properly constructed prover passes all validation checks
				err := validateCSPProverInputs(curve, p)
				require.NoError(t, err)
			})

			t.Run("NilCurve", func(t *testing.T) {
				// Verify that nil curve is rejected (required for all crypto operations)
				pCopy := *p
				pCopy.Curve = nil
				err := validateCSPProverInputs(nil, &pCopy)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrNilCurve)
				assert.Contains(t, err.Error(), "invalid curve")
			})

			t.Run("NilCommitment", func(t *testing.T) {
				// Verify that nil commitment is rejected (the public statement being proven)
				pCopy := *p
				pCopy.Commitment = nil
				err := validateCSPProverInputs(curve, &pCopy)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrNilCommitment)
				assert.Contains(t, err.Error(), "commitment cannot be nil")
			})

			t.Run("WrongGeneratorsLength", func(t *testing.T) {
				// Verify that generators with incorrect length are rejected (must be 2^rounds)
				pCopy := *p
				pCopy.Generators = gens[:size-1]
				err := validateCSPProverInputs(curve, &pCopy)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrInvalidLength)
				assert.Contains(t, err.Error(), "invalid length")
			})
		})
	}
}

// TestValidateRangeProverInputs verifies that all inputs to the range prover are properly
// validated before proof generation. The range prover proves that a committed value lies
// within a specific range [0, 2^n) without revealing the value itself.
//
// Given a range prover instance with various input configurations,
// When the validateRangeProverInputs function is called,
// Then it should correctly validate the curve, commitment, value, randomness, and bits.
func TestValidateRangeProverInputs(t *testing.T) {
	curves := []mathlib.CurveID{
		mathlib.BLS12_381_BBS_GURVY,
		mathlib.BN254,
	}

	for _, curveID := range curves {
		t.Run(fmt.Sprintf("%d", curveID), func(t *testing.T) {
			curve := mathlib.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			// Create valid range prover for 8-bit range [0, 256)
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
				v:            curve.NewRandomZr(rand),
				r:            curve.NewRandomZr(rand),
				VGenerators:  vGens,
				AGenerators:  aGens,
				BGenerators:  bGens,
				NumberOfBits: n,
				Curve:        curve,
			}

			t.Run("ValidInputs", func(t *testing.T) {
				// Verify that a properly constructed range prover passes all validation checks
				err := validateRangeProverInputs(curve, p)
				require.NoError(t, err)
			})

			t.Run("NilValue", func(t *testing.T) {
				// Verify that nil value is rejected (the secret being range-proven)
				pCopy := *p
				pCopy.v = nil
				err := validateRangeProverInputs(curve, &pCopy)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrNilValue)
				assert.Contains(t, err.Error(), "value cannot be nil")
			})

			t.Run("ZeroBits", func(t *testing.T) {
				// Verify that zero bits is rejected (must prove at least 1 bit)
				pCopy := *p
				pCopy.NumberOfBits = 0
				err := validateRangeProverInputs(curve, &pCopy)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrInvalidBitCount)
				assert.Contains(t, err.Error(), "invalid number of bits")
				assert.Contains(t, err.Error(), "must be greater than 0")
			})

			t.Run("TooManyBits", func(t *testing.T) {
				// Verify that more than 64 bits is rejected (implementation limit)
				pCopy := *p
				pCopy.NumberOfBits = 65
				err := validateRangeProverInputs(curve, &pCopy)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrInvalidBitCount)
				assert.Contains(t, err.Error(), "invalid number of bits")
				assert.Contains(t, err.Error(), "cannot exceed 64")
			})
		})
	}
}

// TestValidateRangeProof verifies that range proofs are properly validated before
// verification. A range proof demonstrates that a committed value lies within [0, 2^n)
// without revealing the value. The proof structure combines a CSP proof with additional
// components for the range constraint.
//
// Given a range proof structure (valid or malformed),
// When the validateRangeProof function is called,
// Then it should correctly identify structural invalidities.
func TestValidateRangeProof(t *testing.T) {
	curves := []mathlib.CurveID{
		mathlib.BLS12_381_BBS_GURVY,
		mathlib.BN254,
	}

	for _, curveID := range curves {
		t.Run(fmt.Sprintf("%d", curveID), func(t *testing.T) {
			curve := mathlib.Curves[curveID]

			proof := &RangeProof{
				pComm: curve.GenG1,
				pokV: pokCommitment{
					A: curve.GenG1,
					Z: []*mathlib.Zr{curve.NewZrFromInt(1), curve.NewZrFromInt(2)},
				},
				u:     curve.NewZrFromInt(1),
				sComm: curve.GenG1,
				sEval: curve.NewZrFromInt(1),
				cspProof: Proof{
					Curve:  curve,
					Left:   []*mathlib.G1{curve.GenG1},
					Right:  []*mathlib.G1{curve.GenG1},
					VLeft:  []*mathlib.Zr{curve.NewZrFromInt(1)},
					VRight: []*mathlib.Zr{curve.NewZrFromInt(1)},
				},
			}

			t.Run("ValidProof", func(t *testing.T) {
				// Verify that a properly constructed range proof passes all validation checks
				err := validateRangeProof(curve, proof)
				require.NoError(t, err)
			})

			t.Run("NilProof", func(t *testing.T) {
				// Verify that nil proof is rejected to prevent nil pointer dereferences
				err := validateRangeProof(curve, nil)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrNilProof)
				assert.Contains(t, err.Error(), "proof cannot be nil")
			})

			t.Run("NilPComm", func(t *testing.T) {
				// Verify that nil polynomial commitment is rejected (critical proof component)
				proofCopy := *proof
				proofCopy.pComm = nil
				err := validateRangeProof(curve, &proofCopy)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrNilCommitment)
				assert.Contains(t, err.Error(), "pComm cannot be nil")
			})
		})
	}
}
