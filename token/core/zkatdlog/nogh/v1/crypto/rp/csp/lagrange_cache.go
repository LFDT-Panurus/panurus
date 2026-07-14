/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"sync"

	mathlib "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
)

// lagrangeDenomCache caches the precomputed denominator inverses for Lagrange
// interpolation. The denominators d_i = ∏_{j≠i}(i-j) depend only on n (the
// number of evaluation points), not on the challenge c. Caching them eliminates
// O(n²) field multiplications on every proof/verify call that shares the same n.
//
// The cache is keyed by (curveID, n) and stores the batch-inverted denominators
// as native gnark-crypto field elements, avoiding big.Int round-trips on lookup.
//
// Thread safety: a sync.Map is used so concurrent proof generations with the
// same parameters share a single precomputed entry without locking.
var lagrangeDenomCache sync.Map // key: lagrangeCacheKey → *lagrangeCacheEntry

type lagrangeCacheKey struct {
	curveID mathlib.CurveID
	n       uint64
	partial bool // true → partial (2n+1 points), false → full (n+1 points)
}

// lagrangeCacheEntry holds the precomputed batch-inverted denominators for a
// specific (curveID, n, partial) triple. The slice is immutable after creation.
type lagrangeCacheEntry struct {
	// denomInvsBLS stores inverted denominators as BLS12-381 fr.Element values.
	// Populated only when curveID is a BLS12-381 variant.
	denomInvsBLS []*bls12381fr.Element
	// denomInvsBN254 stores inverted denominators as BN254 fr.Element values.
	// Populated only when curveID is BN254.
	denomInvsBN254 []*bn254fr.Element
}

// getOrComputeDenomInvsBLS returns the cached (or freshly computed) batch-inverted
// Lagrange denominators for BLS12-381 curves.
//
// For the full variant (partial=false) the evaluation points are {0,1,...,n} and
// d_i = ∏_{j≠i}(i-j).
//
// For the partial variant (partial=true) the evaluation points are {0,1,...,2n}
// but only the n+1 relevant indices {0, n+1, ..., 2n} are returned.
func getOrComputeDenomInvsBLS(n uint64, partial bool) []*bls12381fr.Element {
	key := lagrangeCacheKey{curveID: mathlib.BLS12_381_BBS_GURVY, n: n, partial: partial}
	if v, ok := lagrangeDenomCache.Load(key); ok {
		return v.(*lagrangeCacheEntry).denomInvsBLS
	}

	invs := computeDenomInvs[bls12381fr.Element, *bls12381fr.Element](n, partial)
	entry := &lagrangeCacheEntry{denomInvsBLS: invs}
	// Store only if not already present (another goroutine may have raced us).
	actual, _ := lagrangeDenomCache.LoadOrStore(key, entry)

	return actual.(*lagrangeCacheEntry).denomInvsBLS
}

// getOrComputeDenomInvsBN254 is the BN254 counterpart of getOrComputeDenomInvsBLS.
func getOrComputeDenomInvsBN254(n uint64, partial bool) []*bn254fr.Element {
	key := lagrangeCacheKey{curveID: mathlib.BN254, n: n, partial: partial}
	if v, ok := lagrangeDenomCache.Load(key); ok {
		return v.(*lagrangeCacheEntry).denomInvsBN254
	}

	invs := computeDenomInvs[bn254fr.Element, *bn254fr.Element](n, partial)
	entry := &lagrangeCacheEntry{denomInvsBN254: invs}
	actual, _ := lagrangeDenomCache.LoadOrStore(key, entry)

	return actual.(*lagrangeCacheEntry).denomInvsBN254
}

// computeDenomInvs computes and batch-inverts the Lagrange denominators for the
// given parameters using native gnark-crypto arithmetic (no big.Int).
//
// Full variant (partial=false):
//
//	m = n+1 evaluation points {0,...,n}
//	d_i = ∏_{j=0, j≠i}^{n} (i-j)   for i = 0..n
//
// Partial variant (partial=true):
//
//	total = 2n+1 evaluation points {0,...,2n}
//	relevant indices = {0, n+1, ..., 2n}  (n+1 entries)
//	d_k = ∏_{j=0, j≠relevant[k]}^{2n} (relevant[k]-j)
func computeDenomInvs[T any, E math2.GnarkFr[T]](n uint64, partial bool) []E {
	if !partial {
		m := int(n) + 1 // #nosec G115
		denoms := make([]T, m)
		denomsE := make([]E, m)
		for i := range denoms {
			denomsE[i] = E(&denoms[i])
			denomsE[i].SetOne()
			var diff T
			diffE := E(&diff)
			for j := range m {
				if j == i {
					continue
				}
				diffE.SetInt64(int64(i - j))
				denomsE[i].Mul(denomsE[i], diffE)
			}
		}

		return math2.NativeBatchInverse[T, E](denomsE)
	}

	// Partial: relevant indices are {0, n+1, ..., 2n}.
	total := 2*int(n) + 1             // #nosec G115
	relevant := make([]int, int(n)+1) // #nosec G115
	relevant[0] = 0
	for k := 1; k <= int(n); k++ { // #nosec G115
		relevant[k] = int(n) + k // #nosec G115
	}

	denoms := make([]T, len(relevant))
	denomsE := make([]E, len(relevant))
	for k := range relevant {
		denomsE[k] = E(&denoms[k])
		denomsE[k].SetOne()
		var diff T
		diffE := E(&diff)
		for j := range total {
			if j == relevant[k] {
				continue
			}
			diffE.SetInt64(int64(relevant[k] - j))
			denomsE[k].Mul(denomsE[k], diffE)
		}
	}

	return math2.NativeBatchInverse[T, E](denomsE)
}
