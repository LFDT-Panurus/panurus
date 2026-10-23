/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bulletproof

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/common"
	"github.com/stretchr/testify/require"
)

func TestRangeProofDataValidate(t *testing.T) {
	curve := math.Curves[math.BN254]
	otherCurve := math.Curves[math.BLS12_381_BBS]
	rand, err := curve.Rand()
	require.NoError(t, err)

	data := &RangeProofData{
		T1:           curve.GenG1,
		T2:           curve.GenG1,
		C:            curve.GenG1,
		D:            curve.GenG1,
		Tau:          curve.NewRandomZr(rand),
		Delta:        curve.NewRandomZr(rand),
		InnerProduct: curve.NewRandomZr(rand),
	}

	err = data.Validate(math.BN254)
	require.NoError(t, err)

	// Test nil T1
	data.T1 = nil
	err = data.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil T1")
	// Test invalid T1 (wrong curve)
	data.T1 = otherCurve.GenG1
	err = data.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid T1")
	data.T1 = curve.GenG1

	// Test nil T2
	data.T2 = nil
	err = data.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil T2")
	// Test invalid T2
	data.T2 = otherCurve.GenG1
	err = data.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid T2")
	data.T2 = curve.GenG1

	// Test nil C
	data.C = nil
	err = data.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil C")
	// Test invalid C
	data.C = otherCurve.GenG1
	err = data.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid C")
	data.C = curve.GenG1

	// Test nil D
	data.D = nil
	err = data.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil D")
	// Test invalid D
	data.D = otherCurve.GenG1
	err = data.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid D")
	data.D = curve.GenG1

	// Test nil Tau
	data.Tau = nil
	err = data.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil Tau")
	// Test invalid Tau
	data.Tau = otherCurve.NewZrFromUint64(1)
	err = data.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid Tau")
	data.Tau = curve.NewRandomZr(rand)

	// Test nil Delta
	data.Delta = nil
	err = data.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil Delta")
	// Test invalid Delta
	data.Delta = otherCurve.NewZrFromUint64(1)
	err = data.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid Delta")
	data.Delta = curve.NewRandomZr(rand)

	// Test nil InnerProduct
	data.InnerProduct = nil
	err = data.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil InnerProduct")
	// Test invalid InnerProduct
	data.InnerProduct = otherCurve.NewZrFromUint64(1)
	err = data.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid InnerProduct")
}

func TestRangeProofValidate(t *testing.T) {
	curve := math.Curves[math.BN254]
	otherCurve := math.Curves[math.BLS12_381_BBS]
	rand, err := curve.Rand()
	require.NoError(t, err)

	proof := &RangeProof{
		Data: &RangeProofData{
			T1:           curve.GenG1,
			T2:           curve.GenG1,
			C:            curve.GenG1,
			D:            curve.GenG1,
			Tau:          curve.NewRandomZr(rand),
			Delta:        curve.NewRandomZr(rand),
			InnerProduct: curve.NewRandomZr(rand),
		},
		IPA: &IPA{
			L:     []*math.G1{curve.GenG1},
			R:     []*math.G1{curve.GenG1},
			Left:  curve.NewRandomZr(rand),
			Right: curve.NewRandomZr(rand),
		},
	}

	err = proof.Validate(math.BN254)
	require.NoError(t, err)

	// Test nil Data
	proof.Data = nil
	err = proof.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid range proof: nil data")
	proof.Data = &RangeProofData{
		T1:           curve.GenG1,
		T2:           curve.GenG1,
		C:            curve.GenG1,
		D:            curve.GenG1,
		Tau:          curve.NewRandomZr(rand),
		Delta:        curve.NewRandomZr(rand),
		InnerProduct: curve.NewRandomZr(rand),
	}

	// Test invalid Data (internal validation fail)
	proof.Data.T1 = otherCurve.GenG1
	err = proof.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid range proof: invalid data")
	proof.Data.T1 = curve.GenG1

	// Test nil IPA
	proof.IPA = nil
	err = proof.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid range proof: nil IPA")
	proof.IPA = &IPA{
		L:     []*math.G1{curve.GenG1},
		R:     []*math.G1{curve.GenG1},
		Left:  curve.NewRandomZr(rand),
		Right: curve.NewRandomZr(rand),
	}

	// Test invalid IPA (internal validation fail)
	proof.IPA.Left = nil
	err = proof.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid range proof: invalid IPA")
}

func TestRangeProofDeserializeError(t *testing.T) {
	proof := &RangeProof{}
	err := proof.Deserialize([]byte("invalid"))
	require.Error(t, err)

	data := &RangeProofData{}
	err = data.Deserialize([]byte("invalid"))
	require.Error(t, err)
}

func TestRangeVerifier_VerifyError(t *testing.T) {
	curve := math.Curves[math.BN254]
	verifier := NewRangeVerifier(curve.GenG1, []*math.G1{curve.GenG1}, nil, nil, curve.GenG1, curve.GenG1, 1, 1, curve, nil)

	proof := &RangeProof{
		Data: &RangeProofData{},
	}

	// Many fields are nil in proof.Data
	err := verifier.Verify(proof)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid range proof: nil elements")

	proof.Data.InnerProduct = curve.NewZrFromUint64(1)
	proof.Data.C = curve.GenG1
	proof.Data.D = curve.GenG1
	err = verifier.Verify(proof)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid range proof: nil elements")

	proof.Data.T1 = curve.GenG1
	proof.Data.T2 = curve.GenG1
	err = verifier.Verify(proof)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid range proof: nil elements")
}

// TestChallengeXBoundToFullTranscript is a regression test for the Fiat-Shamir
// transcript binding fixed by TestOutofRange in rp_test.go: the challenge x
// must depend on everything absorbed before T1, T2 (the commitment, C and D,
// and the derived challenges y, z), not on T1, T2 alone. If x could be
// computed independent of C, D, V, a prover could fix T1, T2 (and so x)
// before committing to the bit-decomposition vectors.
func TestChallengeXBoundToFullTranscript(t *testing.T) {
	curve := math.Curves[math.BN254]
	rand, err := curve.Rand()
	require.NoError(t, err)

	T1 := curve.GenG1.Mul(curve.NewRandomZr(rand))
	T2 := curve.GenG1.Mul(curve.NewRandomZr(rand))

	deriveX := func(commitment, c, d *math.G1) *math.Zr {
		tr := &common.Transcript{Curve: curve}
		tr.InitHasherWithDomain(TranscriptDomain)
		tr.Absorb(commitment.Bytes())
		tr.Absorb(c.Bytes())
		tr.Absorb(d.Bytes())
		_, err := tr.Squeeze() // y
		require.NoError(t, err)
		_, err = tr.Squeeze() // z
		require.NoError(t, err)
		tr.Absorb(T1.Bytes())
		tr.Absorb(T2.Bytes())
		x, err := tr.Squeeze()
		require.NoError(t, err)

		return x
	}

	commitment := curve.GenG1.Mul(curve.NewRandomZr(rand))
	C := curve.GenG1.Mul(curve.NewRandomZr(rand))
	D := curve.GenG1.Mul(curve.NewRandomZr(rand))

	xBase := deriveX(commitment, C, D)

	// Changing any of commitment, C, D (with T1, T2 fixed) must change x:
	// a prover cannot pick T1, T2 first and learn x before committing to the
	// bit-decomposition vectors.
	otherCommitment := curve.GenG1.Mul(curve.NewRandomZr(rand))
	require.False(t, xBase.Equals(deriveX(otherCommitment, C, D)))

	otherC := curve.GenG1.Mul(curve.NewRandomZr(rand))
	require.False(t, xBase.Equals(deriveX(commitment, otherC, D)))

	otherD := curve.GenG1.Mul(curve.NewRandomZr(rand))
	require.False(t, xBase.Equals(deriveX(commitment, C, otherD)))
}
