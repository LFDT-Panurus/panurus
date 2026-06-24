/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"fmt"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/stretchr/testify/require"
)

// TestLagrangeNumeratorsVariousSizes verifies that the binary tree method
// produces correct numerators for various numbers of leaves, including
// edge cases like odd numbers and powers of 2.
func TestLagrangeNumeratorsVariousSizes(t *testing.T) {
	curve := math.Curves[math.BN254]
	rand, err := curve.Rand()
	require.NoError(t, err)

	// Test various sizes, even and odd (method0 supports both): imperfect trees, powers of 2
	// and ±1, and the production sizes {17,33,65,129}.
	testSizes := []int{16, 17, 23, 30, 31, 32, 33, 64, 65, 128, 129}

	for _, m := range testSizes {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			// Generate a random challenge c
			cZr := curve.NewRandomZr(rand)

			// Compute numerators using binary tree method
			binaryTreeResult, ok, err := nativeLagrangeMultipliers(uint64(m-1), cZr, curve) // #nosec G115
			require.NoError(t, err)
			require.True(t, ok)
			require.Len(t, binaryTreeResult, m)

			// Compute numerators using fallback method
			fallbackResult, err := getLagrangeMultipliers(uint64(m-1), cZr, curve) // #nosec G115
			require.NoError(t, err)
			require.Len(t, fallbackResult, m)

			// Verify they match
			for i := range m {
				require.True(t, binaryTreeResult[i].Equals(fallbackResult[i]),
					"Mismatch at index %d for m=%d", i, m)
			}

			t.Logf("✓ Verified m=%d: binary tree matches fallback", m)
		})
	}
}

// TestGetLagrangeMultipliersReconstruction validates the full-multiplier path for BOTH
// parities (method0 supports even m). It is independent of the tree internals: for a random
// degree-(m-1) polynomial p over the points {0,...,m-1}, the Lagrange multipliers μ_i at c
// must satisfy p(c) == Σ_i μ_i · p(i).
func TestGetLagrangeMultipliersReconstruction(t *testing.T) {
	for _, curveID := range []math.CurveID{math.BN254, math.BLS12_381} {
		curve := math.Curves[curveID]
		rand, err := curve.Rand()
		require.NoError(t, err)
		order := curve.GroupOrder

		for _, m := range []int{2, 3, 4, 5, 8, 9, 16, 17} { // even and odd
			c := curve.NewRandomZr(rand)

			// Random polynomial p(x) = Σ_k coeffs[k] x^k; eval via Horner.
			coeffs := make([]*math.Zr, m)
			for k := range coeffs {
				coeffs[k] = curve.NewRandomZr(rand)
			}
			eval := func(x *math.Zr) *math.Zr {
				acc := curve.NewZrFromInt(0)
				for k := m - 1; k >= 0; k-- {
					acc = curve.ModAdd(curve.ModMul(acc, x, order), coeffs[k], order)
				}

				return acc
			}

			mu, err := getLagrangeMultipliers(uint64(m-1), c, curve) // #nosec G115
			require.NoError(t, err, "curve=%v m=%d must succeed (both parities)", curveID, m)
			require.Len(t, mu, m)

			got := curve.NewZrFromInt(0)
			for i := range m {
				yi := eval(curve.NewZrFromInt(int64(i))) // #nosec G115
				got = curve.ModAdd(got, curve.ModMul(mu[i], yi, order), order)
			}
			require.True(t, got.Equals(eval(c)),
				"curve=%v m=%d: Σ μ_i·p(i) != p(c)", curveID, m)
		}
	}
}

// Made with Bob
