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

	// Test various sizes including edge cases
	testSizes := []int{23, 30, 31, 32, 33}

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

// Made with Bob
