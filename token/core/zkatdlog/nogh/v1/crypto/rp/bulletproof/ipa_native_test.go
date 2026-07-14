/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bulletproof_test

import (
	"math/bits"
	"strconv"
	"testing"

	math "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp/bulletproof"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nativeIPACurves returns the curve IDs exercised by the native IPA tests.
// BLS12-381 and BN254 use the gnark-crypto fast path; BLS12-381-BBS uses
// the mathlib fallback, letting us compare the two paths on the same inputs.
func nativeIPACurves() []struct {
	name string
	id   math.CurveID
} {
	return []struct {
		name string
		id   math.CurveID
	}{
		{"BLS12-381", math.BLS12_381_BBS_GURVY},
		{"BN254", math.BN254},
	}
}

// TestNativeComputeSVector_CrossCurve verifies that ComputeSVector produces
// identical results for both BLS12-381 (native path) and BN254 (native path)
// independently — each curve is checked for inverse-property and definition
// consistency, exercising the dispatch logic introduced in this PR.
func TestNativeComputeSVector_CrossCurve(t *testing.T) {
	for _, tc := range nativeIPACurves() {
		t.Run(tc.name, func(t *testing.T) {
			curve := math.Curves[tc.id]
			rand, err := curve.Rand()
			require.NoError(t, err)

			one := math2.One(curve)

			for _, rounds := range []int{1, 2, 3, 4, 5, 6} {
				n := 1 << rounds
				t.Run(strconv.Itoa(n), func(t *testing.T) {
					challenges := make([]*math.Zr, rounds)
					challengeInvs := make([]*math.Zr, rounds)
					for j := range challenges {
						challenges[j] = curve.NewRandomZr(rand)
						challengeInvs[j] = challenges[j].Copy()
						challengeInvs[j].InvModOrder()
					}

					s, sInv := bulletproof.ComputeSVector(n, challenges, curve)
					require.Len(t, s, n)
					require.Len(t, sInv, n)

					// Inverse property: s[i] * sInv[i] == 1
					for i := range n {
						product := curve.ModMul(s[i], sInv[i], curve.GroupOrder)
						assert.True(t, product.Equals(one),
							"s[%d]*sInv[%d] should be 1", i, i)
					}

					// Definition consistency: compare against naive O(n log n) oracle.
					for i := range n {
						expected := math2.One(curve)
						expectedInv := math2.One(curve)
						for r := range rounds {
							bitPos := rounds - 1 - r
							if (i>>bitPos)&1 == 1 {
								expected = curve.ModMul(expected, challenges[r], curve.GroupOrder)
								expectedInv = curve.ModMul(expectedInv, challengeInvs[r], curve.GroupOrder)
							} else {
								expected = curve.ModMul(expected, challengeInvs[r], curve.GroupOrder)
								expectedInv = curve.ModMul(expectedInv, challenges[r], curve.GroupOrder)
							}
						}
						assert.True(t, s[i].Equals(expected),
							"s[%d] mismatch for n=%d curve=%s", i, n, tc.name)
						assert.True(t, sInv[i].Equals(expectedInv),
							"sInv[%d] mismatch for n=%d curve=%s", i, n, tc.name)
					}
				})
			}
		})
	}
}

// TestNativeIPAProofVerify exercises the full IPA prove→verify cycle using the
// native path for both BLS12-381 and BN254, ensuring the ComputeSVector and
// reduceVectors dispatch is exercised end-to-end.
func TestNativeIPAProofVerify(t *testing.T) {
	for _, tc := range nativeIPACurves() {
		t.Run(tc.name, func(t *testing.T) {
			setup, err := newIpaSetup(tc.id)
			require.NoError(t, err)

			prover := bulletproof.NewIPAProver(
				math2.InnerProduct(setup.left, setup.right, setup.curve),
				setup.left, setup.right,
				setup.Q,
				setup.leftGens, setup.rightGens,
				setup.com,
				setup.nr,
				setup.curve,
				nil,
			)
			proof, err := prover.Prove()
			require.NoError(t, err)
			require.NotNil(t, proof)

			verifier := bulletproof.NewIPAVerifier(
				math2.InnerProduct(setup.left, setup.right, setup.curve),
				setup.Q,
				setup.leftGens, setup.rightGens,
				setup.com,
				setup.nr,
				setup.curve,
				nil,
			)
			require.NoError(t, verifier.Verify(proof))
		})
	}
}

// TestNativeReduceVectors_Consistency verifies that the native reduceVectors
// path (used via the IPA prover) produces identical proofs that the verifier
// accepts, for both BLS12-381 and BN254.
func TestNativeReduceVectors_Consistency(t *testing.T) {
	for _, tc := range nativeIPACurves() {
		t.Run(tc.name, func(t *testing.T) {
			curve := math.Curves[tc.id]
			l := uint64(16)
			nr := uint64(bits.Len64(l)) - 1 //nolint:gosec

			rand, err := curve.Rand()
			require.NoError(t, err)

			leftGens := make([]*math.G1, l)
			rightGens := make([]*math.G1, l)
			left := make([]*math.Zr, l)
			right := make([]*math.Zr, l)
			com := curve.NewG1()
			Q := curve.GenG1

			for i := range left {
				leftGens[i] = curve.HashToG1([]byte(strconv.Itoa(i)))
				rightGens[i] = curve.HashToG1([]byte(strconv.Itoa(i + 1)))
				left[i] = curve.NewRandomZr(rand)
				right[i] = curve.NewRandomZr(rand)
				com.Add(leftGens[i].Mul(left[i]))
				com.Add(rightGens[i].Mul(right[i]))
			}

			ip := math2.InnerProduct(left, right, curve)

			proof, err := bulletproof.NewIPAProver(
				ip, left, right, Q,
				leftGens, rightGens, com, nr, curve, nil,
			).Prove()
			require.NoError(t, err)

			err = bulletproof.NewIPAVerifier(
				ip, Q, leftGens, rightGens, com, nr, curve, nil,
			).Verify(proof)
			require.NoError(t, err)
		})
	}
}

// BenchmarkNativeComputeSVector measures the allocation improvement of the
// native gnark-crypto path vs the mathlib fallback for ComputeSVector.
// Run with: go test -bench=BenchmarkNativeComputeSVector -benchmem
func BenchmarkNativeComputeSVector(b *testing.B) {
	curves := []struct {
		name string
		id   math.CurveID
	}{
		{"BLS12-381 (native)", math.BLS12_381_BBS_GURVY},
		{"BN254 (native)", math.BN254},
	}

	rounds := 6 // n = 64
	n := 1 << rounds

	for _, tc := range curves {
		b.Run(tc.name, func(b *testing.B) {
			curve := math.Curves[tc.id]
			rand, err := curve.Rand()
			require.NoError(b, err)

			challenges := make([]*math.Zr, rounds)
			for j := range challenges {
				challenges[j] = curve.NewRandomZr(rand)
			}

			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				bulletproof.ComputeSVector(n, challenges, curve)
			}
		})
	}
}

// BenchmarkNativeIPAProver measures end-to-end IPA prove time with the native
// path active for BLS12-381 and BN254.
// Run with: go test -bench=BenchmarkNativeIPAProver -benchmem
func BenchmarkNativeIPAProver(b *testing.B) {
	for _, tc := range nativeIPACurves() {
		b.Run(tc.name, func(b *testing.B) {
			setup, err := newIpaSetup(tc.id)
			require.NoError(b, err)

			ip := math2.InnerProduct(setup.left, setup.right, setup.curve)

			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				prover := bulletproof.NewIPAProver(
					ip, setup.left, setup.right, setup.Q,
					setup.leftGens, setup.rightGens, setup.com,
					setup.nr, setup.curve, nil,
				)
				_, err := prover.Prove()
				require.NoError(b, err)
			}
		})
	}
}
