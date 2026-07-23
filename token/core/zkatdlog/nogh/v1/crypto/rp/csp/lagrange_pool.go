/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"sync"

	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
)

// treeArrayPool manages memory pools for tree array slabs used in
// computeNumeratorsBinaryTree. Each slab holds all three working regions
// (tree, numers, exclude) in a single contiguous allocation, keyed by total
// slab size. This means one pool lookup, one GC-tracked object, and better
// cache locality compared to three separate allocations.
type treeArrayPool[T any] struct {
	pools map[int]*sync.Pool // key: total slab size (m + 2*leafStart)
	mu    sync.RWMutex
}

// newTreeArrayPool creates a new pool for slabs of type T.
func newTreeArrayPool[T any]() *treeArrayPool[T] {
	return &treeArrayPool[T]{
		pools: make(map[int]*sync.Pool),
	}
}

// getPool returns the sync.Pool for the given total size, creating it if necessary.
func (p *treeArrayPool[T]) getPool(size int) *sync.Pool {
	p.mu.RLock()
	pool, exists := p.pools[size]
	p.mu.RUnlock()

	if exists {
		return pool
	}

	// Double-checked locking: create pool if still absent after acquiring write lock.
	p.mu.Lock()
	defer p.mu.Unlock()

	if pool, exists := p.pools[size]; exists {
		return pool
	}

	pool = &sync.Pool{
		New: func() any {
			s := make([]T, size)

			return &s
		},
	}
	p.pools[size] = pool

	return pool
}

// Global pools for BLS12-381 and BN254 curves
var (
	blsTreePool   = newTreeArrayPool[bls12381fr.Element]()
	bn254TreePool = newTreeArrayPool[bn254fr.Element]()
)

// treeArrays is a single contiguous slab used by the binary-tree numerator algorithm.
//
// Layout:
//
//	slab = [ tree+leaves (treeSize) | exclude+numers (treeSize) ]
//	         slab[0:treeSize]             slab[treeSize:2*treeSize]
//
// Within the first half: slab[0:leafStart] are internal-node products (tree),
// slab[leafStart:treeSize] are the c-j input values (leaves).
// Within the second half: slab[treeSize:treeSize+leafStart] are exclude products,
// slab[treeSize+leafStart:] are the output numerators.
//
// Callers index slab directly using leafStart and treeSize, which they already
// hold — no sub-slice fields needed.
type treeArrays[T any] struct {
	slab []T // backing allocation returned to pool on put
	pool *treeArrayPool[T]
}

// getTreeArrays retrieves a pooled slab of length 2*treeSize for type T.
// m is the number of leaves; leafStart = m-1 is derived internally.
func getTreeArrays[T any](m int) *treeArrays[T] {
	treeSize := 2*m - 1
	total := 2 * treeSize
	pool := getTypedPool[T]()
	raw := pool.getPool(total).Get()
	var slab []T
	if sp, ok := raw.(*[]T); ok && len(*sp) == total {
		slab = *sp
	} else {
		slab = make([]T, total)
	}

	return &treeArrays[T]{
		slab: slab,
		pool: pool,
	}
}

// putTreeArrays returns the slab inside ta back to its pool.
func putTreeArrays[T any](ta *treeArrays[T]) {
	if ta == nil {
		return
	}
	pool := ta.pool.getPool(len(ta.slab))
	pool.Put(&ta.slab)
}

// typedPools is a registry mapping element-type name to an untyped pool wrapper.
// We use a two-level approach: a map from type key → *treeArrayPool[T] stored
// as any, unwrapped via a typed accessor registered at init time.
var typedPoolRegistry = map[string]any{} // populated by registerTypedPool

// getTypedPool returns the *treeArrayPool[T] for type T.
// It panics if no pool has been registered for T.
func getTypedPool[T any]() *treeArrayPool[T] {
	var zero T
	key := typedPoolKey(zero)
	p, ok := typedPoolRegistry[key]
	if !ok {
		panic("csp: no treeArrayPool registered for type " + key)
	}

	return p.(*treeArrayPool[T])
}

// typedPoolKey returns a string key for a value of any type, used to look up
// the pool in typedPoolRegistry.
func typedPoolKey(v any) string {
	return typeOf(v)
}

// typeOf returns a stable string name for the dynamic type of v.
func typeOf(v any) string {
	switch v.(type) {
	case bls12381fr.Element:
		return "bls12381fr.Element"
	case bn254fr.Element:
		return "bn254fr.Element"
	default:
		panic("csp: unsupported element type")
	}
}

func init() {
	typedPoolRegistry["bls12381fr.Element"] = blsTreePool
	typedPoolRegistry["bn254fr.Element"] = bn254TreePool
}
