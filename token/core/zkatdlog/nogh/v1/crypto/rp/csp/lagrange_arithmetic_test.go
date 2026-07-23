/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"testing"

	math "github.com/IBM/mathlib"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/stretchr/testify/require"
)

// TestFieldArithmeticOptimization verifies that the polynomial expansion
// (c-1)*(c-2) = c² - 3c + 2 holds in field arithmetic.
func TestFieldArithmeticOptimization(t *testing.T) {
	curve := math.Curves[math.BN254]
	rand, err := curve.Rand()
	require.NoError(t, err)

	// Generate a random field element c
	cZr := curve.NewRandomZr(rand)
	var c bn254fr.Element
	c.SetBigInt(cZr.BigInt())

	// Compute (c-1)
	var cMinus1 bn254fr.Element
	var one bn254fr.Element
	one.SetOne()
	cMinus1.Sub(&c, &one)

	// Compute (c-2)
	var cMinus2 bn254fr.Element
	var two bn254fr.Element
	two.SetInt64(2)
	cMinus2.Sub(&c, &two)

	// Direct multiplication: (c-1)*(c-2)
	var directProduct bn254fr.Element
	directProduct.Mul(&cMinus1, &cMinus2)

	// Optimized computation: c² - 3c + 2
	var cSquared bn254fr.Element
	cSquared.Mul(&c, &c)

	var three bn254fr.Element
	three.SetInt64(3)

	var threeC bn254fr.Element
	threeC.Mul(&three, &c)

	var optimized bn254fr.Element
	optimized.Sub(&cSquared, &threeC)
	optimized.Add(&optimized, &two)

	// Verify they are equal
	require.Equal(t, directProduct.Bytes(), optimized.Bytes(),
		"(c-1)*(c-2) should equal c² - 3c + 2 in field arithmetic")

	t.Logf("✓ Verified: (c-1)*(c-2) = c² - 3c + 2")
	t.Logf("  Direct product: %x", directProduct.Bytes())
	t.Logf("  Optimized:      %x", optimized.Bytes())
}

// TestFieldArithmeticOptimizationPattern verifies the general pattern:
// (c-i)*(c-j) = c² - c*(i+j) + i*j
func TestFieldArithmeticOptimizationPattern(t *testing.T) {
	curve := math.Curves[math.BN254]
	rand, err := curve.Rand()
	require.NoError(t, err)

	// Generate a random field element c
	cZr := curve.NewRandomZr(rand)
	var c bn254fr.Element
	c.SetBigInt(cZr.BigInt())

	// Test several (i, j) pairs
	testCases := []struct {
		i, j int64
	}{
		{0, 3}, // (c-0)*(c-3) = c² - 3c
		{1, 2}, // (c-1)*(c-2) = c² - 3c + 2
		{0, 7}, // (c-0)*(c-7) = c² - 7c
		{2, 5}, // (c-2)*(c-5) = c² - 7c + 10
	}

	for _, tc := range testCases {
		// Compute (c-i)
		var cMinusI bn254fr.Element
		var iElem bn254fr.Element
		iElem.SetInt64(tc.i)
		cMinusI.Sub(&c, &iElem)

		// Compute (c-j)
		var cMinusJ bn254fr.Element
		var jElem bn254fr.Element
		jElem.SetInt64(tc.j)
		cMinusJ.Sub(&c, &jElem)

		// Direct multiplication: (c-i)*(c-j)
		var directProduct bn254fr.Element
		directProduct.Mul(&cMinusI, &cMinusJ)

		// Optimized computation: c² - c*(i+j) + i*j
		var cSquared bn254fr.Element
		cSquared.Mul(&c, &c)

		var iPlusJ bn254fr.Element
		iPlusJ.SetInt64(tc.i + tc.j)

		var cTimesSumIJ bn254fr.Element
		cTimesSumIJ.Mul(&c, &iPlusJ)

		var iTimesJ bn254fr.Element
		iTimesJ.SetInt64(tc.i * tc.j)

		var optimized bn254fr.Element
		optimized.Sub(&cSquared, &cTimesSumIJ)
		optimized.Add(&optimized, &iTimesJ)

		// Verify they are equal
		require.Equal(t, directProduct.Bytes(), optimized.Bytes(),
			"(c-%d)*(c-%d) should equal c² - c*%d + %d in field arithmetic",
			tc.i, tc.j, tc.i+tc.j, tc.i*tc.j)

		t.Logf("✓ Verified: (c-%d)*(c-%d) = c² - c*%d + %d", tc.i, tc.j, tc.i+tc.j, tc.i*tc.j)
	}
}

// Made with Bob
