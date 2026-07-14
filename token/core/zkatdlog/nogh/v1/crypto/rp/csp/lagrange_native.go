/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	mathlib "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
)

// getLagrangeMultipliersNative is the native fr.Element implementation of
// getLagrangeMultipliers. Conversions between mathlib.Zr and fr.Element occur
// only once at the boundary (once for input c, n+1 times for the output slice),
// so the O(n²) arithmetic runs entirely in native Montgomery form.
//
// The denominator inverses d_i^{-1} = (∏_{j≠i}(i-j))^{-1} depend only on n,
// not on c, so they are retrieved from the cache (computed once per n).
func getLagrangeMultipliersNative[T any, E math2.GnarkFr[T]](n uint64, c *mathlib.Zr, curve *mathlib.Curve, denomInvs []E) ([]*mathlib.Zr, error) {
	m := int(n) + 1 // #nosec G115

	// Convert c once.
	cE := math2.NativeFromZr[T, E](c)

	// cMinusJ[j] = c - j  for j = 0..n
	cMinusJ := make([]T, m)
	cMinusJE := make([]E, m)
	for j := range m {
		cMinusJE[j] = E(&cMinusJ[j])
		var jE T
		E(&jE).SetInt64(int64(j))
		cMinusJE[j].Sub(cE, E(&jE))
	}

	// Compute numerator for each Lagrange basis polynomial L_i(c).
	// Denominators come from the cache — no O(n²) recomputation.
	numers := make([]T, m)
	numersE := make([]E, m)
	for i := range numers {
		numersE[i] = E(&numers[i])
		numersE[i].SetOne()
		for j := range m {
			if j == i {
				continue
			}
			numersE[i].Mul(numersE[i], cMinusJE[j])
		}
	}

	result := make([]*mathlib.Zr, m)
	for i := range m {
		var prod T
		E(&prod).Mul(numersE[i], denomInvs[i])
		result[i] = math2.NativeToZr[T, E](E(&prod), curve)
	}

	return result, nil
}

// getLagrangeMultipliersPartialNative is the native fr.Element implementation of
// getLagrangeMultipliersPartial. Same boundary-only conversion strategy.
// Denominator inverses are retrieved from the cache.
func getLagrangeMultipliersPartialNative[T any, E math2.GnarkFr[T]](n uint64, c *mathlib.Zr, curve *mathlib.Curve, denomInvs []E) ([]*mathlib.Zr, error) {
	total := 2*int(n) + 1 // #nosec G115 // all evaluation points: 0..2n

	cE := math2.NativeFromZr[T, E](c)

	// cMinusJ[j] = c - j  for j = 0..2n
	cMinusJ := make([]T, total)
	cMinusJE := make([]E, total)
	for j := range total {
		cMinusJE[j] = E(&cMinusJ[j])
		var jE T
		E(&jE).SetInt64(int64(j))
		cMinusJE[j].Sub(cE, E(&jE))
	}

	// Relevant indices in the full point set: {0, n+1, n+2, ..., 2n}
	relevant := make([]int, int(n)+1) // #nosec G115
	relevant[0] = 0
	for k := 1; k <= int(n); k++ { // #nosec G115
		relevant[k] = int(n) + k // #nosec G115
	}

	numers := make([]T, len(relevant))
	numersE := make([]E, len(relevant))
	for k := range relevant {
		numersE[k] = E(&numers[k])
	}

	for k, i := range relevant {
		numersE[k].SetOne()
		for j := range total {
			if j == i {
				continue
			}
			numersE[k].Mul(numersE[k], cMinusJE[j])
		}
	}

	result := make([]*mathlib.Zr, len(relevant))
	for k := range relevant {
		var prod T
		E(&prod).Mul(numersE[k], denomInvs[k])
		result[k] = math2.NativeToZr[T, E](E(&prod), curve)
	}

	return result, nil
}

// interpolateNative is the native fr.Element implementation of interpolate.
// Denominator inverses are retrieved from the cache.
func interpolateNative[T any, E math2.GnarkFr[T]](n uint64, valuesOverN []*mathlib.Zr, curve *mathlib.Curve, denomInvs []E) ([]*mathlib.Zr, error) {
	m := int(n) + 1 // #nosec G115

	// Convert all input values to native elements once.
	vals := make([]T, m)
	valsE := make([]E, m)
	for i := range m {
		valsE[i] = E(&vals[i])

		v := valuesOverN[i]
		switch {
		case v.IsZero():
			valsE[i].SetZero()
		case v.IsOne():
			valsE[i].SetOne()
		default:
			valsE[i].SetBigInt(valuesOverN[i].BigInt())
		}
	}

	// First m entries are the inputs verbatim.
	result := make([]*mathlib.Zr, 2*int(n)+1) // #nosec G115
	copy(result, valuesOverN)

	// Evaluate at each x in {n+1, ..., 2n} via Lagrange interpolation.
	for x := int(n) + 1; x <= 2*int(n); x++ { // #nosec G115
		// xMinusJ[j] = x - j, and px = ∏_j xMinusJ[j]
		xMinusJ := make([]T, m)
		xMinusJE := make([]E, m)
		var px T
		pxE := E(&px)
		pxE.SetOne()
		for j := range m {
			xMinusJE[j] = E(&xMinusJ[j])
			xMinusJE[j].SetInt64(int64(x - j)) // #nosec G115
			pxE.Mul(pxE, xMinusJE[j])
		}

		xMinusJInvs := math2.NativeBatchInverse[T, E](xMinusJE)

		var val T
		valE := E(&val)
		for i := range m {
			var li T
			liE := E(&li)
			liE.Mul(pxE, xMinusJInvs[i])
			liE.Mul(liE, denomInvs[i])
			liE.Mul(liE, valsE[i])
			valE.Add(valE, liE)
		}
		result[x] = math2.NativeToZr[T, E](valE, curve)
	}

	return result, nil
}

// nativeLagrangeMultipliers dispatches getLagrangeMultipliers to the native
// fr.Element implementation for supported curves, using cached denominator inverses.
func nativeLagrangeMultipliers(n uint64, c *mathlib.Zr, curve *mathlib.Curve) ([]*mathlib.Zr, bool, error) {
	switch curve.GroupOrder.CurveID() {
	case mathlib.BLS12_381, mathlib.BLS12_381_GURVY, mathlib.BLS12_381_BBS, mathlib.BLS12_381_BBS_GURVY:
		denomInvs := getOrComputeDenomInvsBLS(n, false)
		r, err := getLagrangeMultipliersNative[bls12381fr.Element, *bls12381fr.Element](n, c, curve, denomInvs)

		return r, true, err
	case mathlib.BN254:
		denomInvs := getOrComputeDenomInvsBN254(n, false)
		r, err := getLagrangeMultipliersNative[bn254fr.Element, *bn254fr.Element](n, c, curve, denomInvs)

		return r, true, err
	}

	return nil, false, nil
}

// nativeLagrangeMultipliersPartial dispatches getLagrangeMultipliersPartial to
// the native fr.Element implementation for supported curves, using cached denominator inverses.
func nativeLagrangeMultipliersPartial(n uint64, c *mathlib.Zr, curve *mathlib.Curve) ([]*mathlib.Zr, bool, error) {
	switch curve.GroupOrder.CurveID() {
	case mathlib.BLS12_381, mathlib.BLS12_381_GURVY, mathlib.BLS12_381_BBS, mathlib.BLS12_381_BBS_GURVY:
		denomInvs := getOrComputeDenomInvsBLS(n, true)
		r, err := getLagrangeMultipliersPartialNative[bls12381fr.Element, *bls12381fr.Element](n, c, curve, denomInvs)

		return r, true, err
	case mathlib.BN254:
		denomInvs := getOrComputeDenomInvsBN254(n, true)
		r, err := getLagrangeMultipliersPartialNative[bn254fr.Element, *bn254fr.Element](n, c, curve, denomInvs)

		return r, true, err
	}

	return nil, false, nil
}

// nativeInterpolate dispatches interpolate to the native fr.Element
// implementation for supported curves, using cached denominator inverses.
func nativeInterpolate(n uint64, vals []*mathlib.Zr, curve *mathlib.Curve) ([]*mathlib.Zr, bool, error) {
	switch curve.GroupOrder.CurveID() {
	case mathlib.BLS12_381, mathlib.BLS12_381_GURVY, mathlib.BLS12_381_BBS, mathlib.BLS12_381_BBS_GURVY:
		denomInvs := getOrComputeDenomInvsBLS(n, false)
		r, err := interpolateNative[bls12381fr.Element, *bls12381fr.Element](n, vals, curve, denomInvs)

		return r, true, err
	case mathlib.BN254:
		denomInvs := getOrComputeDenomInvsBN254(n, false)
		r, err := interpolateNative[bn254fr.Element, *bn254fr.Element](n, vals, curve, denomInvs)

		return r, true, err
	}

	return nil, false, nil
}
