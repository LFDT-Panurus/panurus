/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bulletproof

import (
	mathlib "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
)

// nativeComputeSVector computes the s vector and its entry-wise inverse using
// native gnark-crypto field arithmetic to avoid big.Int allocations.
//
// The dual-butterfly recurrence is identical to ComputeSVector, but all
// intermediate multiplications use in-place gnark field operations (T) rather
// than mathlib.Zr wrappers around big.Int.  This eliminates the O(n) heap
// allocations per butterfly round that the mathlib path incurs.
//
// The results are converted back to []*mathlib.Zr at the end so the caller
// interface is unchanged.
func nativeComputeSVector[T any, E math2.GnarkFr[T]](n int, challenges []*mathlib.Zr, curve *mathlib.Curve) ([]*mathlib.Zr, []*mathlib.Zr) {
	log2n := len(challenges)

	// Convert all challenges and their inverses to native field elements once.
	// This costs 2·log(n) big.Int conversions total — far fewer than the O(n)
	// allocations that the mathlib butterfly loop would generate.
	cNative := make([]T, log2n)
	cInvNative := make([]T, log2n)
	for r := range log2n {
		math2.SetNativeFromZr[T, E](challenges[r], E(&cNative[r]))
		// Inverse of the native element directly — no big.Int round-trip needed.
		E(&cInvNative[r]).Inverse(E(&cNative[r]))
	}

	// Allocate native storage for s and sInv vectors.
	sNative := make([]T, n)
	sInvNative := make([]T, n)
	E(&sNative[0]).SetOne()
	E(&sInvNative[0]).SetOne()
	for i := 1; i < n; i++ {
		E(&sNative[i]).SetZero()
		E(&sInvNative[i]).SetZero()
	}

	// Dual butterfly: O(n) in-place multiplications with no allocation.
	for r := range log2n {
		halfLen := 1 << r
		c := E(&cNative[log2n-1-r])
		cInv := E(&cInvNative[log2n-1-r])
		for i := range halfLen {
			// s[i+halfLen] = s[i] * c   (bit set → challenge)
			E(&sNative[i+halfLen]).Mul(E(&sNative[i]), c)
			// s[i] = s[i] * cInv        (bit unset → inverse)
			E(&sNative[i]).Mul(E(&sNative[i]), cInv)

			// sInv[i+halfLen] = sInv[i] * cInv  (swapped)
			E(&sInvNative[i+halfLen]).Mul(E(&sInvNative[i]), cInv)
			// sInv[i] = sInv[i] * c             (swapped)
			E(&sInvNative[i]).Mul(E(&sInvNative[i]), c)
		}
	}

	// Convert back to mathlib.Zr for the caller.
	s := make([]*mathlib.Zr, n)
	sInv := make([]*mathlib.Zr, n)
	for i := range n {
		s[i] = math2.NativeToZr[T, E](E(&sNative[i]), curve)
		sInv[i] = math2.NativeToZr[T, E](E(&sInvNative[i]), curve)
	}

	return s, sInv
}

// nativeReduceVectors reduces the left and right vectors by half using native
// gnark-crypto field arithmetic, eliminating the intermediate mathlib.Zr
// allocations that the mathlib path incurs per element.
//
// The recurrence is identical to reduceVectors:
//
//	leftPrime[i]  = left[i]*x  + left[i+l]*xInv
//	rightPrime[i] = right[i]*xInv + right[i+l]*x
func nativeReduceVectors[T any, E math2.GnarkFr[T]](
	left, right []*mathlib.Zr,
	x, xInv *mathlib.Zr,
	curve *mathlib.Curve,
) ([]*mathlib.Zr, []*mathlib.Zr) {
	l := len(left) / 2

	// Convert x and xInv once.
	var xE, xInvE T
	math2.SetNativeFromZr[T, E](x, E(&xE))
	math2.SetNativeFromZr[T, E](xInv, E(&xInvE))

	leftPrime := make([]*mathlib.Zr, l)
	rightPrime := make([]*mathlib.Zr, l)

	for i := range l {
		var liE, liHalfE, riE, riHalfE T
		math2.SetNativeFromZr[T, E](left[i], E(&liE))
		math2.SetNativeFromZr[T, E](left[i+l], E(&liHalfE))
		math2.SetNativeFromZr[T, E](right[i], E(&riE))
		math2.SetNativeFromZr[T, E](right[i+l], E(&riHalfE))

		// leftPrime[i] = left[i]*x + left[i+l]*xInv
		var tmp T
		E(&liE).Mul(E(&liE), E(&xE))
		E(&tmp).Mul(E(&liHalfE), E(&xInvE))
		E(&liE).Add(E(&liE), E(&tmp))
		leftPrime[i] = math2.NativeToZr[T, E](E(&liE), curve)

		// rightPrime[i] = right[i]*xInv + right[i+l]*x
		E(&riE).Mul(E(&riE), E(&xInvE))
		E(&tmp).Mul(E(&riHalfE), E(&xE))
		E(&riE).Add(E(&riE), E(&tmp))
		rightPrime[i] = math2.NativeToZr[T, E](E(&riE), curve)
	}

	return leftPrime, rightPrime
}
