/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"testing"

	mathlib "github.com/IBM/mathlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateCurveEnhanced covers edge cases for validateCurve.
func TestValidateCurveEnhanced(t *testing.T) {
	t.Run("NilGroupOrder", func(t *testing.T) {
		curve := &mathlib.Curve{
			GroupOrder: nil,
			GenG1:      nil,
		}
		err := validateCurve(curve)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidCurve)
		assert.Contains(t, err.Error(), "group order cannot be nil")
	})

	t.Run("NilGenG1", func(t *testing.T) {
		curve := &mathlib.Curve{
			GroupOrder: mathlib.Curves[mathlib.BN254].GroupOrder,
			GenG1:      nil,
		}
		err := validateCurve(curve)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidCurve)
		assert.Contains(t, err.Error(), "generator G1 cannot be nil")
	})
}

// TestValidateG1SliceEnhanced covers more edge cases for validateG1Slice.
func TestValidateG1SliceEnhanced(t *testing.T) {
	curveBN := mathlib.Curves[mathlib.BN254]
	curveBLS := mathlib.Curves[mathlib.BLS12_381_BBS_GURVY]

	t.Run("WrongCurveID", func(t *testing.T) {
		elements := []*mathlib.G1{curveBLS.GenG1}
		err := validateG1Slice("test", elements, curveBN, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "test validation failed")
	})

	t.Run("InfinityElement", func(t *testing.T) {
		// Verify that the identity point is rejected from generator slices.
		elements := []*mathlib.G1{curveBN.NewG1()}
		err := validateG1Slice("test", elements, curveBN, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "test validation failed")
	})
}

// TestValidateCSPProverInputsEnhanced covers more edge cases for validateCSPProverInputs.
func TestValidateCSPProverInputsEnhanced(t *testing.T) {
	curve := mathlib.Curves[mathlib.BN254]
	rand, err := curve.Rand()
	require.NoError(t, err)

	rounds := uint64(1)
	gens := []*mathlib.G1{curve.GenG1, curve.GenG1}
	lf := []*mathlib.Zr{curve.NewRandomZr(rand), curve.NewRandomZr(rand)}
	wit := []*mathlib.Zr{curve.NewRandomZr(rand), curve.NewRandomZr(rand)}

	p := &prover{
		Commitment:     curve.GenG1,
		Generators:     gens,
		LinearForm:     lf,
		Value:          curve.NewRandomZr(rand),
		NumberOfRounds: rounds,
		Curve:          curve,
		witness:        wit,
	}

	t.Run("NilValue", func(t *testing.T) {
		pCopy := *p
		pCopy.Value = nil
		err := validateCSPProverInputs(curve, &pCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilValue)
	})

	t.Run("InvalidLinearForm", func(t *testing.T) {
		pCopy := *p
		pCopy.LinearForm = nil
		err := validateCSPProverInputs(curve, &pCopy)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "linear form cannot be nil")
	})

	t.Run("InvalidWitness", func(t *testing.T) {
		pCopy := *p
		pCopy.witness = nil
		err := validateCSPProverInputs(curve, &pCopy)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "witness cannot be nil")
	})
}

// TestValidateCSPVerifierInputs verifies that all inputs to the CSP verifier are properly validated.
func TestValidateCSPVerifierInputs(t *testing.T) {
	curve := mathlib.Curves[mathlib.BN254]
	rand, err := curve.Rand()
	require.NoError(t, err)

	rounds := uint64(1)
	gens := []*mathlib.G1{curve.GenG1, curve.GenG1}
	lf := []*mathlib.Zr{curve.NewRandomZr(rand), curve.NewRandomZr(rand)}

	v := &verifier{
		Commitment:     curve.GenG1,
		Generators:     gens,
		LinearForm:     lf,
		Value:          curve.NewRandomZr(rand),
		NumberOfRounds: rounds,
		Curve:          curve,
	}

	t.Run("ValidInputs", func(t *testing.T) {
		err := validateCSPVerifierInputs(curve, v)
		require.NoError(t, err)
	})

	t.Run("NilCurve", func(t *testing.T) {
		err := validateCSPVerifierInputs(nil, v)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilCurve)
		assert.Contains(t, err.Error(), "invalid curve")
	})

	t.Run("NilCommitment", func(t *testing.T) {
		vCopy := *v
		vCopy.Commitment = nil
		err := validateCSPVerifierInputs(curve, &vCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilCommitment)
	})

	t.Run("NilValue", func(t *testing.T) {
		vCopy := *v
		vCopy.Value = nil
		err := validateCSPVerifierInputs(curve, &vCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilValue)
	})

	t.Run("InvalidGenerators", func(t *testing.T) {
		vCopy := *v
		vCopy.Generators = gens[:1]
		err := validateCSPVerifierInputs(curve, &vCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidLength)
	})

	t.Run("InvalidLinearForm", func(t *testing.T) {
		vCopy := *v
		vCopy.LinearForm = nil
		err := validateCSPVerifierInputs(curve, &vCopy)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "linear form cannot be nil")
	})
}

// TestValidateCSPProof verifies that CSP proofs are properly validated.
func TestValidateCSPProof(t *testing.T) {
	curve := mathlib.Curves[mathlib.BN254]
	rounds := uint64(1)

	proof := &Proof{
		Left:   []*mathlib.G1{curve.GenG1},
		Right:  []*mathlib.G1{curve.GenG1},
		VLeft:  []*mathlib.Zr{curve.NewZrFromInt(1)},
		VRight: []*mathlib.Zr{curve.NewZrFromInt(1)},
		Curve:  curve,
	}

	t.Run("ValidProof", func(t *testing.T) {
		err := validateCSPProof(curve, proof, rounds)
		require.NoError(t, err)
	})

	t.Run("NilProof", func(t *testing.T) {
		err := validateCSPProof(curve, nil, rounds)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilProof)
	})

	t.Run("InvalidCurve", func(t *testing.T) {
		err := validateCSPProof(nil, proof, rounds)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilCurve)
		assert.Contains(t, err.Error(), "invalid proof curve")
	})

	t.Run("NilLeft", func(t *testing.T) {
		pCopy := *proof
		pCopy.Left = nil
		err := validateCSPProof(curve, &pCopy, rounds)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "proof.Left cannot be nil")
	})

	t.Run("WrongLeftLength", func(t *testing.T) {
		pCopy := *proof
		pCopy.Left = []*mathlib.G1{}
		err := validateCSPProof(curve, &pCopy, rounds)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidLength)
	})

	t.Run("NilRight", func(t *testing.T) {
		pCopy := *proof
		pCopy.Right = nil
		err := validateCSPProof(curve, &pCopy, rounds)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "proof.Right cannot be nil")
	})

	t.Run("WrongRightLength", func(t *testing.T) {
		pCopy := *proof
		pCopy.Right = []*mathlib.G1{}
		err := validateCSPProof(curve, &pCopy, rounds)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidLength)
	})

	t.Run("NilInLeft", func(t *testing.T) {
		pCopy := *proof
		pCopy.Left = []*mathlib.G1{nil}
		err := validateCSPProof(curve, &pCopy, rounds)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "proof.Left validation failed")
	})

	t.Run("WrongCurveInLeft", func(t *testing.T) {
		curveBLS := mathlib.Curves[mathlib.BLS12_381_BBS_GURVY]
		pCopy := *proof
		pCopy.Left = []*mathlib.G1{curveBLS.GenG1}
		err := validateCSPProof(curve, &pCopy, rounds)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "proof.Left validation failed")
	})

	t.Run("InfinityInLeft", func(t *testing.T) {
		// Regression test for HIGH finding: identity element in Left must be rejected
		// to prevent round-constraint neutralisation and range-proof soundness weakening.
		pCopy := *proof
		pCopy.Left = []*mathlib.G1{curve.NewG1()} // point at infinity
		err := validateCSPProof(curve, &pCopy, rounds)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "proof.Left validation failed")
	})

	t.Run("NilInRight", func(t *testing.T) {
		pCopy := *proof
		pCopy.Right = []*mathlib.G1{nil}
		err := validateCSPProof(curve, &pCopy, rounds)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "proof.Right validation failed")
	})

	t.Run("WrongCurveInRight", func(t *testing.T) {
		curveBLS := mathlib.Curves[mathlib.BLS12_381_BBS_GURVY]
		pCopy := *proof
		pCopy.Right = []*mathlib.G1{curveBLS.GenG1}
		err := validateCSPProof(curve, &pCopy, rounds)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "proof.Right validation failed")
	})

	t.Run("InfinityInRight", func(t *testing.T) {
		// Regression test for HIGH finding: identity element in Right must be rejected
		// to prevent round-constraint neutralisation and range-proof soundness weakening.
		pCopy := *proof
		pCopy.Right = []*mathlib.G1{curve.NewG1()} // point at infinity
		err := validateCSPProof(curve, &pCopy, rounds)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "proof.Right validation failed")
	})

	t.Run("WrongVLeftLength", func(t *testing.T) {
		pCopy := *proof
		pCopy.VLeft = []*mathlib.Zr{}
		err := validateCSPProof(curve, &pCopy, rounds)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidLength)
	})

	t.Run("WrongVRightLength", func(t *testing.T) {
		pCopy := *proof
		pCopy.VRight = []*mathlib.Zr{}
		err := validateCSPProof(curve, &pCopy, rounds)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidLength)
	})

	t.Run("InvalidVLeft", func(t *testing.T) {
		pCopy := *proof
		pCopy.VLeft = []*mathlib.Zr{nil}
		err := validateCSPProof(curve, &pCopy, rounds)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "proof.VLeft validation failed")
	})

	t.Run("InvalidVRight", func(t *testing.T) {
		pCopy := *proof
		pCopy.VRight = []*mathlib.Zr{nil}
		err := validateCSPProof(curve, &pCopy, rounds)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "proof.VRight validation failed")
	})
}

// TestValidateRangeProverInputsEnhanced covers more edge cases for validateRangeProverInputs.
func TestValidateRangeProverInputsEnhanced(t *testing.T) {
	curve := mathlib.Curves[mathlib.BN254]
	rand, err := curve.Rand()
	require.NoError(t, err)

	n := uint64(1)
	vGens := []*mathlib.G1{curve.GenG1, curve.GenG1}
	aGens := []*mathlib.G1{curve.GenG1, curve.GenG1}
	bGens := []*mathlib.G1{curve.GenG1, curve.GenG1}

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

	t.Run("NilVCommitment", func(t *testing.T) {
		pCopy := *p
		pCopy.VCommitment = nil
		err := validateRangeProverInputs(curve, &pCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilCommitment)
	})

	t.Run("NilR", func(t *testing.T) {
		pCopy := *p
		pCopy.r = nil
		err := validateRangeProverInputs(curve, &pCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilRandomness)
	})

	t.Run("InvalidVGenerators", func(t *testing.T) {
		pCopy := *p
		pCopy.VGenerators = vGens[:1]
		err := validateRangeProverInputs(curve, &pCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidLength)
	})

	t.Run("InvalidAGenerators", func(t *testing.T) {
		pCopy := *p
		pCopy.AGenerators = aGens[:1]
		err := validateRangeProverInputs(curve, &pCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidLength)
	})

	t.Run("InvalidBGenerators", func(t *testing.T) {
		pCopy := *p
		pCopy.BGenerators = bGens[:1]
		err := validateRangeProverInputs(curve, &pCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidLength)
	})

	t.Run("InvalidCurve", func(t *testing.T) {
		err := validateRangeProverInputs(nil, p)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilCurve)
		assert.Contains(t, err.Error(), "invalid curve")
	})
}

// TestValidateRangeVerifierInputs verifies that all inputs to the range verifier are properly validated.
func TestValidateRangeVerifierInputs(t *testing.T) {
	curve := mathlib.Curves[mathlib.BN254]
	n := uint64(1)
	vGens := []*mathlib.G1{curve.GenG1, curve.GenG1}
	aGens := []*mathlib.G1{curve.GenG1, curve.GenG1}
	bGens := []*mathlib.G1{curve.GenG1, curve.GenG1}

	v := &rangeVerifier{
		VCommitment:  curve.GenG1,
		VGenerators:  vGens,
		AGenerators:  aGens,
		BGenerators:  bGens,
		NumberOfBits: n,
		Curve:        curve,
	}

	t.Run("ValidInputs", func(t *testing.T) {
		err := validateRangeVerifierInputs(curve, v)
		require.NoError(t, err)
	})

	t.Run("NilCurve", func(t *testing.T) {
		err := validateRangeVerifierInputs(nil, v)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilCurve)
		assert.Contains(t, err.Error(), "invalid curve")
	})

	t.Run("NilVCommitment", func(t *testing.T) {
		vCopy := *v
		vCopy.VCommitment = nil
		err := validateRangeVerifierInputs(curve, &vCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilCommitment)
	})

	t.Run("ZeroBits", func(t *testing.T) {
		vCopy := *v
		vCopy.NumberOfBits = 0
		err := validateRangeVerifierInputs(curve, &vCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidBitCount)
	})

	t.Run("TooManyBits", func(t *testing.T) {
		vCopy := *v
		vCopy.NumberOfBits = 65
		err := validateRangeVerifierInputs(curve, &vCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidBitCount)
	})

	t.Run("InvalidVGenerators", func(t *testing.T) {
		vCopy := *v
		vCopy.VGenerators = vGens[:1]
		err := validateRangeVerifierInputs(curve, &vCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidLength)
	})

	t.Run("InvalidAGenerators", func(t *testing.T) {
		vCopy := *v
		vCopy.AGenerators = aGens[:1]
		err := validateRangeVerifierInputs(curve, &vCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidLength)
	})

	t.Run("InvalidBGenerators", func(t *testing.T) {
		vCopy := *v
		vCopy.BGenerators = bGens[:1]
		err := validateRangeVerifierInputs(curve, &vCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidLength)
	})
}

// TestValidateRangeProofEnhanced covers more edge cases for validateRangeProof.
func TestValidateRangeProofEnhanced(t *testing.T) {
	curve := mathlib.Curves[mathlib.BN254]
	curveBLS := mathlib.Curves[mathlib.BLS12_381_BBS_GURVY]

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

	t.Run("WrongCurvePComm", func(t *testing.T) {
		pCopy := *proof
		pCopy.pComm = curveBLS.GenG1
		err := validateRangeProof(curve, &pCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrWrongCurveID)
	})

	t.Run("NilPokVA", func(t *testing.T) {
		pCopy := *proof
		pCopy.pokV.A = nil
		err := validateRangeProof(curve, &pCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilCommitment)
		assert.Contains(t, err.Error(), "proof.pokV.A cannot be nil")
	})

	t.Run("WrongCurvePokVA", func(t *testing.T) {
		pCopy := *proof
		pCopy.pokV.A = curveBLS.GenG1
		err := validateRangeProof(curve, &pCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrWrongCurveID)
	})

	t.Run("InvalidPokVZ", func(t *testing.T) {
		pCopy := *proof
		pCopy.pokV.Z = nil
		err := validateRangeProof(curve, &pCopy)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "proof.pokV.Z cannot be nil")
	})

	t.Run("NilU", func(t *testing.T) {
		pCopy := *proof
		pCopy.u = nil
		err := validateRangeProof(curve, &pCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilValue)
		assert.Contains(t, err.Error(), "proof.u cannot be nil")
	})

	t.Run("NilSComm", func(t *testing.T) {
		pCopy := *proof
		pCopy.sComm = nil
		err := validateRangeProof(curve, &pCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilCommitment)
		assert.Contains(t, err.Error(), "proof.sComm cannot be nil")
	})

	t.Run("WrongCurveSComm", func(t *testing.T) {
		pCopy := *proof
		pCopy.sComm = curveBLS.GenG1
		err := validateRangeProof(curve, &pCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrWrongCurveID)
	})

	t.Run("NilSEval", func(t *testing.T) {
		pCopy := *proof
		pCopy.sEval = nil
		err := validateRangeProof(curve, &pCopy)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilValue)
		assert.Contains(t, err.Error(), "proof.sEval cannot be nil")
	})

	t.Run("InvalidCurve", func(t *testing.T) {
		err := validateRangeProof(nil, proof)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNilCurve)
		assert.Contains(t, err.Error(), "invalid curve")
	})
}
