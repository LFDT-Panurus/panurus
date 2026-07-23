/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"testing"

	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/stretchr/testify/require"
)

func TestTreeArrayPoolBLS(t *testing.T) {
	// Test basic get/put operations
	m := 65
	treeSize := 2*m - 1

	arrays := getTreeArrays[bls12381fr.Element](m)
	require.NotNil(t, arrays)
	require.Len(t, arrays.slab, 2*treeSize)

	// Return to pool
	putTreeArrays(arrays)

	// Get again - should reuse from pool
	arrays2 := getTreeArrays[bls12381fr.Element](m)
	require.NotNil(t, arrays2)
	require.Len(t, arrays2.slab, 2*treeSize)

	putTreeArrays(arrays2)
}

func TestTreeArrayPoolBN254(t *testing.T) {
	// Test basic get/put operations
	m := 129
	treeSize := 2*m - 1

	arrays := getTreeArrays[bn254fr.Element](m)
	require.NotNil(t, arrays)
	require.Len(t, arrays.slab, 2*treeSize)

	// Return to pool
	putTreeArrays(arrays)

	// Get again - should reuse from pool
	arrays2 := getTreeArrays[bn254fr.Element](m)
	require.NotNil(t, arrays2)
	require.Len(t, arrays2.slab, 2*treeSize)

	putTreeArrays(arrays2)
}

func TestTreeArrayPoolConcurrent(t *testing.T) {
	// Test concurrent access to pool
	const goroutines = 10
	const iterations = 100
	const m = 65
	const leafStart = m - 1
	const treeSize = 2*m - 1

	done := make(chan bool, goroutines)

	for range goroutines {
		go func() {
			for range iterations {
				arrays := getTreeArrays[bls12381fr.Element](m)
				// Simulate some work using direct slab offsets.
				// slab[0]          → tree[0]
				// slab[treeSize]   → exclude[0]
				// slab[treeSize+leafStart] → numers[0]
				arrays.slab[0].SetOne()
				arrays.slab[treeSize].SetZero()
				arrays.slab[treeSize+leafStart].SetOne()
				putTreeArrays(arrays)
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for range goroutines {
		<-done
	}
}

func TestTreeArrayPoolDifferentSizes(t *testing.T) {
	// Test that different sizes use different pools
	ms := []int{33, 65, 129, 257}

	for _, m := range ms {
		treeSize := 2*m - 1
		arrays := getTreeArrays[bls12381fr.Element](m)
		require.Len(t, arrays.slab, 2*treeSize)
		putTreeArrays(arrays)
	}
}

// BenchmarkTreeArrayPoolBLS benchmarks the pool performance for BLS12-381
func BenchmarkTreeArrayPoolBLS(b *testing.B) {
	const m = 65
	const leafStart = m - 1

	b.Run("with_pool", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			arrays := getTreeArrays[bls12381fr.Element](m)
			// Simulate some work: touch tree[0] at slab[0].
			arrays.slab[0].SetOne()
			putTreeArrays(arrays)
		}
	})

	b.Run("without_pool", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			tree := make([]bls12381fr.Element, leafStart)
			numers := make([]bls12381fr.Element, m)
			exclude := make([]bls12381fr.Element, leafStart)
			// Simulate some work
			tree[0].SetOne()
			_ = numers
			_ = exclude
		}
	})
}

// BenchmarkTreeArrayPoolBN254 benchmarks the pool performance for BN254
func BenchmarkTreeArrayPoolBN254(b *testing.B) {
	const m = 129
	const leafStart = m - 1

	b.Run("with_pool", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			arrays := getTreeArrays[bn254fr.Element](m)
			// Simulate some work: touch tree[0] at slab[0].
			arrays.slab[0].SetOne()
			putTreeArrays(arrays)
		}
	})

	b.Run("without_pool", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			tree := make([]bn254fr.Element, leafStart)
			numers := make([]bn254fr.Element, m)
			exclude := make([]bn254fr.Element, leafStart)
			// Simulate some work
			tree[0].SetOne()
			_ = numers
			_ = exclude
		}
	})
}

// Made with Bob
