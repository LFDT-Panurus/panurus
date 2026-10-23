/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bulletproof_test

import (
	"strconv"
	"testing"

	math "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp/bulletproof"
	"github.com/stretchr/testify/require"
)

const maxFuzzBulletproofBytes = 256 << 10

// FuzzRangeProofDeserializeNoPanic fuzzes RangeProof.Deserialize/Validate/Verify with
// arbitrary bytes against a fixed, valid public statement. This is the shape of call a
// transfer/issue verifier makes on unauthenticated wire bytes (see
// transfer.BulletProofVerifier.Verify), so any panic anywhere in the
// Deserialize/Validate/Verify chain — not just Deserialize itself — is an unauthenticated
// validator DoS. Validate alone is not sufficient coverage here: e.g. a foreign-curve
// scalar surviving Validate's own curve checks can still panic deep inside Verify via a
// cross-curve backend type assertion (see the comment on RangeProofData.Validate).
func FuzzRangeProofDeserializeNoPanic(f *testing.F) {
	curve := math.Curves[math.BN254]
	const bitLength = 32
	const rounds = 5

	leftGens := make([]*math.G1, bitLength)
	rightGens := make([]*math.G1, bitLength)
	for i := range bitLength {
		leftGens[i] = curve.HashToG1([]byte("fuzz.L" + strconv.Itoa(i)))
		rightGens[i] = curve.HashToG1([]byte("fuzz.R" + strconv.Itoa(i)))
	}
	G := curve.HashToG1([]byte("fuzz.G"))
	H := curve.HashToG1([]byte("fuzz.H"))
	P := curve.HashToG1([]byte("fuzz.P"))
	Q := curve.HashToG1([]byte("fuzz.Q"))

	rand, err := curve.Rand()
	require.NoError(f, err)
	bf := curve.NewRandomZr(rand)
	const value = 42
	com := G.Mul(curve.NewZrFromInt(value))
	com.Add(H.Mul(bf))

	prover := bulletproof.NewRangeProver(
		com, value, []*math.G1{G, H}, bf, leftGens, rightGens, P, Q, rounds, bitLength, curve, nil,
	)
	proof, err := prover.Prove()
	require.NoError(f, err)
	validBytes, err := proof.Serialize()
	require.NoError(f, err)

	f.Add(validBytes)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add(validBytes[:len(validBytes)/2])

	verifier := bulletproof.NewRangeVerifier(com, []*math.G1{G, H}, leftGens, rightGens, P, Q, rounds, bitLength, curve, nil)

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzBulletproofBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			p := &bulletproof.RangeProof{}
			if err := p.Deserialize(raw); err != nil {
				return
			}
			if err := p.Validate(math.BN254); err != nil {
				return
			}
			_ = verifier.Verify(p)
		})
	})
}

// FuzzIPADeserializeNoPanic mirrors FuzzRangeProofDeserializeNoPanic for the standalone
// IPA sub-proof: IPA.Deserialize/Validate/Verify with arbitrary bytes against a fixed,
// valid statement.
func FuzzIPADeserializeNoPanic(f *testing.F) {
	curve := math.Curves[math.BN254]
	const n = 32
	const rounds = 5

	leftGens := make([]*math.G1, n)
	rightGens := make([]*math.G1, n)
	left := make([]*math.Zr, n)
	right := make([]*math.Zr, n)
	rand, err := curve.Rand()
	require.NoError(f, err)
	com := curve.NewG1()
	Q := curve.HashToG1([]byte("fuzz.ipa.Q"))
	for i := range n {
		leftGens[i] = curve.HashToG1([]byte("fuzz.ipa.L" + strconv.Itoa(i)))
		rightGens[i] = curve.HashToG1([]byte("fuzz.ipa.R" + strconv.Itoa(i)))
		left[i] = curve.NewRandomZr(rand)
		right[i] = curve.NewRandomZr(rand)
		com.Add(leftGens[i].Mul(left[i]))
		com.Add(rightGens[i].Mul(right[i]))
	}
	ip := math2.InnerProduct(left, right, curve)

	// newStandaloneIPATranscript is defined in ipa_test.go (same bulletproof_test package).
	proof, err := bulletproof.NewIPAProver(
		ip, left, right, Q, leftGens, rightGens, com, rounds, curve,
		newStandaloneIPATranscript(curve), nil,
	).Prove()
	require.NoError(f, err)
	validBytes, err := proof.Serialize()
	require.NoError(f, err)

	f.Add(validBytes)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add(validBytes[:len(validBytes)/2])

	verifier := bulletproof.NewIPAVerifier(ip, Q, leftGens, rightGens, com, rounds, curve, newStandaloneIPATranscript(curve), nil)

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzBulletproofBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			p := &bulletproof.IPA{}
			if err := p.Deserialize(raw); err != nil {
				return
			}
			if err := p.Validate(math.BN254); err != nil {
				return
			}
			_ = verifier.Verify(p)
		})
	})
}
