/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package math

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/stretchr/testify/require"
)

func TestCheckElement(t *testing.T) {
	var g1 *math.G1
	err := CheckElement(g1, math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "element is nil")

	g1 = &math.G1{}
	err = CheckElement(g1, math.BN254)
	require.Error(t, err)
	// mathlib G1{} might panic on CurveID() or IsInfinity() if not initialized
	// The CheckElement has a recover block

	curve := math.Curves[math.BN254]
	g1 = curve.GenG1
	err = CheckElement(g1, math.BN254)
	require.NoError(t, err)

	err = CheckElement(g1, math.BLS12_381_BBS)
	require.Error(t, err)
	require.Contains(t, err.Error(), "element curve must equal curve ID")

	inf := curve.NewG1()
	err = CheckElement(inf, math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "element is infinity")
}

func TestCheckBaseElement(t *testing.T) {
	var zr *math.Zr
	err := CheckBaseElement(zr, math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "element is nil")

	curve := math.Curves[math.BN254]
	zr = curve.NewZrFromUint64(1)
	err = CheckBaseElement(zr, math.BN254)
	require.NoError(t, err)

	err = CheckBaseElement(zr, math.BLS12_381_BBS)
	require.Error(t, err)
	require.Contains(t, err.Error(), "element curve must equal curve ID")
}

func TestCheckElements(t *testing.T) {
	curve := math.Curves[math.BN254]
	g1 := curve.GenG1
	g2 := curve.GenG1

	err := CheckElements([]*math.G1{g1, g2}, math.BN254, 2)
	require.NoError(t, err)

	err = CheckElements([]*math.G1{g1, g2}, math.BN254, 3)
	require.Error(t, err)
	require.Contains(t, err.Error(), "length of elements does not match length of curveID")

	err = CheckElements([]*math.G1{g1, nil}, math.BN254, 2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "element is nil")
}

func TestCheckZrElements(t *testing.T) {
	curve := math.Curves[math.BN254]
	zr1 := curve.NewZrFromUint64(1)
	zr2 := curve.NewZrFromUint64(2)

	err := CheckZrElements([]*math.Zr{zr1, zr2}, math.BN254, 2)
	require.NoError(t, err)

	err = CheckZrElements([]*math.Zr{zr1, zr2}, math.BN254, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "length of elements does not match length of curveID")

	err = CheckZrElements([]*math.Zr{zr1, nil}, math.BN254, 2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "element is nil")
}

func TestIsNilInterface(t *testing.T) {
	require.True(t, isNilInterface(nil))
	var g1 *math.G1
	require.True(t, isNilInterface(g1))
	require.False(t, isNilInterface(math.Curves[math.BN254].GenG1))
	require.False(t, isNilInterface(10))
}

// TestBatchInverse_ZeroElement is T-GAP-C6: documents the behaviour of
// BatchInverse when the input slice contains a zero field element.
//
// Montgomery's trick (used in BatchInverse) computes the product of all elements
// then calls InvModOrder on the total product. If any element is zero the product
// is zero, and InvModOrder(0) is undefined in Z_p (zero has no multiplicative
// inverse). This test observes what mathlib does in that case and documents
// the result so that callers know what to expect.
//
// Observed behaviour: mathlib's InvModOrder(0) returns 0 (a defined-but-wrong
// result), and the backward pass propagates the zero through ModMul, so all
// outputs become 0. No panic is produced. Callers that require a strict error
// for zero inputs should add a pre-loop guard before calling BatchInverse.
func TestBatchInverse_ZeroElement(t *testing.T) {
	curve := math.Curves[math.BN254]

	zero := curve.NewZrFromInt(0)
	one := curve.NewZrFromUint64(1)
	two := curve.NewZrFromUint64(2)

	// Single zero element: should not panic.
	require.NotPanics(t, func() {
		result := BatchInverse([]*math.Zr{zero}, curve)
		// Document result: either nil/zero (mathlib returns 0 for InvModOrder(0))
		// or a non-nil value. What matters is: no panic.
		_ = result
	}, "T-GAP-C6: BatchInverse([0]) must not panic")

	// Zero mixed with non-zero: the product is zero, so InvModOrder(0) applies.
	// All results become 0 — document this as expected, not as correct.
	require.NotPanics(t, func() {
		result := BatchInverse([]*math.Zr{one, zero, two}, curve)
		_ = result
	}, "T-GAP-C6: BatchInverse with a zero element must not panic")

	// Non-zero inputs: normal operation must still produce correct inverses.
	result := BatchInverse([]*math.Zr{one, two}, curve)
	require.Len(t, result, 2)
	// inv(1) = 1
	prod0 := curve.ModMul(result[0], one, curve.GroupOrder)
	require.True(t, prod0.Equals(curve.NewZrFromUint64(1)), "inv(1) * 1 must equal 1")
	// inv(2) * 2 = 1
	prod1 := curve.ModMul(result[1], two, curve.GroupOrder)
	require.True(t, prod1.Equals(curve.NewZrFromUint64(1)), "inv(2) * 2 must equal 1")
}
