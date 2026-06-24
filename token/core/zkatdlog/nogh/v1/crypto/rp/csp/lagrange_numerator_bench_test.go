/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"fmt"
	"testing"

	mathlib "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
)

// benchNumerators isolates computeNumeratorsBinaryTree from the rest of transfer validation.
// The tree slab is reused across iterations so the measurement reflects field arithmetic,
// not pool/alloc churn.
func benchNumerators[T any, E math2.GnarkFr[T]](b *testing.B, m int, c *mathlib.Zr) {
	b.Helper()
	pooled := getTreeArrays[T](m)
	defer putTreeArrays(pooled)
	for b.Loop() {
		_ = computeNumeratorsBinaryTree[T, E](m, c, pooled, nil)
	}
}

// BenchmarkNumeratorsBinaryTree measures the numerator tree at the production sizes
// m=65 (full path, n=64) and m=129 (partial path, 2n+1 at n=64), on both curves.
//
// A/B: run on this branch (method0) and on the pristine base commit (method1), diff ns/op.
//
// method0 removes ~m/2 multiplications from the dominant top-down pass (the leaf level: 32 at
// m=65, 64 at m=129, each 2 products → 1 product + 1 add). Since the profile shows the tree is
// compute-bound on field multiplication (~52%), expect a real single-digit-to-~10% speedup at
// m=129 and ~1 alloc/op (scratch is pooled; the fullE/excludeE pointer slices are gone).
func BenchmarkNumeratorsBinaryTree(b *testing.B) {
	const cVal = 999983 // far from [0, m-1] so there are no zero factors

	for _, m := range []int{65, 129} {
		blsCurve := mathlib.Curves[mathlib.BLS12_381]
		cBLS := blsCurve.NewZrFromInt(cVal)
		b.Run(fmt.Sprintf("BLS12-381/m=%d", m), func(b *testing.B) {
			benchNumerators[bls12381fr.Element, *bls12381fr.Element](b, m, cBLS)
		})

		bnCurve := mathlib.Curves[mathlib.BN254]
		cBN := bnCurve.NewZrFromInt(cVal)
		b.Run(fmt.Sprintf("BN254/m=%d", m), func(b *testing.B) {
			benchNumerators[bn254fr.Element, *bn254fr.Element](b, m, cBN)
		})
	}
}

// benchNumeratorsPartial isolates the partial numerator path: total = 2n+1 points with the
// relevance mask {0, n+1, ..., 2n}, so the top-down is pruned to the kept indices.
func benchNumeratorsPartial[T any, E math2.GnarkFr[T]](b *testing.B, n int, c *mathlib.Zr) {
	b.Helper()
	total := 2*n + 1
	relevant := make([]bool, total)
	relevant[0] = true
	for k := 1; k <= n; k++ {
		relevant[n+k] = true
	}
	pooled := getTreeArrays[T](total)
	defer putTreeArrays(pooled)
	for b.Loop() {
		_ = computeNumeratorsBinaryTree[T, E](total, c, pooled, relevant)
	}
}

// BenchmarkNumeratorsPartial measures the pruned partial numerator path at n∈{32,64}
// (total = 65, 129), on both curves. The saving is the gap vs BenchmarkNumeratorsBinaryTree at
// m = total (the unpruned full tree the partial path used before this optimization).
func BenchmarkNumeratorsPartial(b *testing.B) {
	const cVal = 999983

	for _, n := range []int{32, 64} {
		blsCurve := mathlib.Curves[mathlib.BLS12_381]
		cBLS := blsCurve.NewZrFromInt(cVal)
		b.Run(fmt.Sprintf("BLS12-381/n=%d", n), func(b *testing.B) {
			benchNumeratorsPartial[bls12381fr.Element, *bls12381fr.Element](b, n, cBLS)
		})

		bnCurve := mathlib.Curves[mathlib.BN254]
		cBN := bnCurve.NewZrFromInt(cVal)
		b.Run(fmt.Sprintf("BN254/n=%d", n), func(b *testing.B) {
			benchNumeratorsPartial[bn254fr.Element, *bn254fr.Element](b, n, cBN)
		})
	}
}
