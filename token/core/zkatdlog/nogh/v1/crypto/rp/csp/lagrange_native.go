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

// leftChild returns the index of the left child of node i in the tree array.
func leftChild(i int) int {
	return 2*i + 1
}

// rightChild returns the index of the right child of node i in the tree array.
func rightChild(i int) int {
	return 2*i + 2
}

// computeNumeratorsBinaryTree computes the numerators for Lagrange interpolation
// using a binary tree approach. For each leaf i, it computes the product of all
// (c-j) for j != i.
//
// It first writes c-j for j in [0,m) into the leaves region of pooled.slab
// (slab[leafStart:treeSize]). pooled.tree and pooled.leaves are contiguous in
// the slab, so a single fullE pointer array of length treeSize covers the
// entire tree with no leaf/internal distinction needed at read time.
//
// The slab layout is:
//
//	slab = [ tree (leafStart) | leaves (m) | exclude (leafStart) | numers (m) ]
//	       |<---------- treeSize ---------->|<---------- treeSize ----------->|
//
// slab[treeSize:] mirrors the tree shape: internal-node slots hold exclude
// values and leaf slots hold numerator outputs. A single excludeE pointer array
// of length treeSize covering slab[treeSize:] lets the top-down pass write to
// either region with a plain index — no branch on whether a child is a leaf.
//
// Algorithm:
//  0. Initialise leaves: slab[leafStart+j] = c - j, for j in [0,m).
//  1. Build fullE[0..treeSize-1]: pointers into slab[0:treeSize] (tree+leaves).
//  2. Build excludeE[0..treeSize-1]: pointers into slab[treeSize:] (exclude+numers).
//  3. Bottom-up: for each internal node, multiply its two children's values.
//  4. Top-down: propagate exclude products; excludeE[child] receives the result
//     directly — if child < leafStart it lands in exclude, otherwise in numers.
func computeNumeratorsBinaryTree[T any, E math2.GnarkFr[T]](m int, c *mathlib.Zr, pooled *treeArrays[T]) []E {
	leafStart := m - 1
	treeSize := 2*m - 1

	// fullE: pointers into slab[0:treeSize] — the bottom-up tree (internal + leaves).
	fullE := make([]E, treeSize)
	// excludeE: pointers into slab[treeSize:2*treeSize] — mirrors the tree shape.
	excludeE := make([]E, treeSize)
	for i := range treeSize {
		fullE[i] = E(&pooled.slab[i])
		excludeE[i] = E(&pooled.slab[treeSize+i])
	}

	m2 := m / 2
	cE := math2.NativeFromZr[T, E](c)
	var jE T

	if m&1 == 1 {
		E(&jE).SetInt64(int64(m2))
		fullE[leafStart].Sub(cE, E(&jE))
	}
	for i := range m2 {
		E(&jE).SetInt64(int64(i))
		fullE[leafStart+2*i+(m&1)].Sub(cE, E(&jE))
		E(&jE).SetInt64(int64(m - 1 - i))
		fullE[leafStart+2*i+1+(m&1)].Sub(cE, E(&jE))
	}

	// start of the inner nodes whose both children are leaves
	leafPairsStart := leafStart - m2

	// Phase 1: Bottom-up — compute subtree products for internal nodes.
	var ccmT, cMinusMm1T T
	ccmE := E(&ccmT)
	E(&jE).SetInt64(int64(m - 1))
	E(&cMinusMm1T).Sub(cE, E(&jE))
	ccmE.Mul(cE, E(&cMinusMm1T))

	for i := leafStart - 1; i >= leafPairsStart; i-- {
		j := i - leafPairsStart
		E(&jE).SetInt64(int64(j * (m - 1 - j)))
		fullE[i].Add(ccmE, E(&jE))
	}

	for i := leafPairsStart - 1; i >= 0; i-- {
		left := leftChild(i)
		right := rightChild(i)

		// Both children exist: node = left × right.
		fullE[i].Mul(fullE[left], fullE[right])
	}

	// Phase 2: Top-down — compute exclude products and write leaf numerators.
	// Root's exclude is 1 (nothing excluded above it).
	excludeE[0].SetOne()

	for i := range leafStart {
		left := leftChild(i)
		right := rightChild(i)

		// Both children exist.
		// Left child's exclude = parent exclude × right subtree product.
		// Right child's exclude = parent exclude × left subtree product.
		// excludeE[child] lands in exclude if child < leafStart, numers otherwise.
		excludeE[left].Mul(excludeE[i], fullE[right])
		excludeE[right].Mul(excludeE[i], fullE[left])
	}

	numersE := make([]E, m)
	if m&1 == 1 {
		numersE[m2] = E(&pooled.slab[treeSize+leafStart])
	}
	for i := range m2 {
		numersE[i] = E(&pooled.slab[treeSize+leafStart+2*i+(m&1)])
		numersE[m-1-i] = E(&pooled.slab[treeSize+leafStart+2*i+1+(m&1)])
	}

	return numersE
}

// getLagrangeMultipliersNative is the native fr.Element implementation of
// getLagrangeMultipliers. Conversions between mathlib.Zr and fr.Element occur
// only once at the boundary (once for input c, n+1 times for the output slice),
// so the O(n²) arithmetic runs entirely in native Montgomery form.
//
// The denominator inverses d_i^{-1} = (∏_{j≠i}(i-j))^{-1} depend only on n,
// not on c, so they are retrieved from the cache (computed once per n).
func getLagrangeMultipliersNative[T any, E math2.GnarkFr[T]](n uint64, c *mathlib.Zr, curve *mathlib.Curve, denomInvs []E) ([]*mathlib.Zr, error) {
	m := int(n) + 1 // #nosec G115

	// Compute numerator for each Lagrange basis polynomial L_i(c).
	// Denominators come from the cache — no O(n²) recomputation.
	pooled := getTreeArrays[T](m)
	numersE := computeNumeratorsBinaryTree[T, E](m, c, pooled)

	result := make([]*mathlib.Zr, m)
	for i := range m {
		var prod T
		E(&prod).Mul(numersE[i], denomInvs[i])
		result[i] = math2.NativeToZr[T, E](E(&prod), curve)
	}
	putTreeArrays(pooled)

	return result, nil
}

// getLagrangeMultipliersPartialNative is the native fr.Element implementation of
// getLagrangeMultipliersPartial. Same boundary-only conversion strategy.
// Denominator inverses are retrieved from the cache.
func getLagrangeMultipliersPartialNative[T any, E math2.GnarkFr[T]](n uint64, c *mathlib.Zr, curve *mathlib.Curve, denomInvs []E) ([]*mathlib.Zr, error) {
	total := 2*int(n) + 1 // #nosec G115 // all evaluation points: 0..2n

	// Compute numerators for all points, then extract relevant ones.
	// Relevant indices in the full point set: {0, n+1, n+2, ..., 2n}
	// relevant[0]=0, relevant[k]=n+k for k>=1 — computed inline, no allocation needed.
	pooled := getTreeArrays[T](total)
	allNumersE := computeNumeratorsBinaryTree[T, E](total, c, pooled)

	result := make([]*mathlib.Zr, int(n)+1) // #nosec G115

	// k=0: relevant index is 0
	var prod0 T
	E(&prod0).Mul(allNumersE[0], denomInvs[0])
	result[0] = math2.NativeToZr[T, E](E(&prod0), curve)

	// k=1..n: relevant index is n+k
	for k := 1; k <= int(n); k++ { // #nosec G115
		var prod T
		E(&prod).Mul(allNumersE[int(n)+k], denomInvs[k]) // #nosec G115
		result[k] = math2.NativeToZr[T, E](E(&prod), curve)
	}
	putTreeArrays(pooled)

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

	// Scratch buffers reused across every x in the loop below. Calling
	// Mul/Add through the generic dictionary-dispatched E defeats escape
	// analysis, so anything declared inside the loop would heap-allocate on
	// every one of the O(n) outer iterations (each doing an O(m) batch
	// inversion); declaring them once and reusing them (each iteration fully
	// overwrites every entry before reading it) amortizes that to a single
	// allocation per buffer for the whole call.
	var li, px, val T
	liE, pxE, valE := E(&li), E(&px), E(&val)

	xMinusJ := make([]T, m)
	xMinusJE := make([]E, m)
	for j := range m {
		xMinusJE[j] = E(&xMinusJ[j])
	}

	invPrefix := make([]T, m)
	xMinusJInvs := make([]T, m)
	xMinusJInvsE := make([]E, m)
	for j := range m {
		xMinusJInvsE[j] = E(&xMinusJInvs[j])
	}

	// Evaluate at each x in {n+1, ..., 2n} via Lagrange interpolation.
	for x := int(n) + 1; x <= 2*int(n); x++ { // #nosec G115
		// xMinusJ[j] = x - j, and px = ∏_j xMinusJ[j]
		pxE.SetOne()
		for j := range m {
			xMinusJE[j].SetInt64(int64(x - j)) // #nosec G115
			pxE.Mul(pxE, xMinusJE[j])
		}

		math2.NativeBatchInverseInto[T, E](xMinusJE, invPrefix, xMinusJInvsE)

		valE.SetZero()
		for i := range m {
			liE.Mul(pxE, xMinusJInvsE[i])
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
