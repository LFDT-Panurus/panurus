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
	leafStart := 64
	m := 65

	arrays := getTreeArrays[bls12381fr.Element](leafStart, m)
	require.NotNil(t, arrays)
	require.Equal(t, leafStart, len(arrays.tree))
	require.Equal(t, m, len(arrays.numers))
	require.Equal(t, leafStart, len(arrays.exclude))

	// Return to pool
	putTreeArrays(arrays)

	// Get again - should reuse from pool
	arrays2 := getTreeArrays[bls12381fr.Element](leafStart, m)
	require.NotNil(t, arrays2)
	require.Equal(t, leafStart, len(arrays2.tree))
	require.Equal(t, m, len(arrays2.numers))
	require.Equal(t, leafStart, len(arrays2.exclude))

	putTreeArrays(arrays2)
}

func TestTreeArrayPoolBN254(t *testing.T) {
	// Test basic get/put operations
	leafStart := 128
	m := 129

	arrays := getTreeArrays[bn254fr.Element](leafStart, m)
	require.NotNil(t, arrays)
	require.Equal(t, leafStart, len(arrays.tree))
	require.Equal(t, m, len(arrays.numers))
	require.Equal(t, leafStart, len(arrays.exclude))

	// Return to pool
	putTreeArrays(arrays)

	// Get again - should reuse from pool
	arrays2 := getTreeArrays[bn254fr.Element](leafStart, m)
	require.NotNil(t, arrays2)
	require.Equal(t, leafStart, len(arrays2.tree))
	require.Equal(t, m, len(arrays2.numers))
	require.Equal(t, leafStart, len(arrays2.exclude))

	putTreeArrays(arrays2)
}

func TestTreeArrayPoolConcurrent(t *testing.T) {
	// Test concurrent access to pool
	const goroutines = 10
	const iterations = 100

	done := make(chan bool, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			for j := 0; j < iterations; j++ {
				arrays := getTreeArrays[bls12381fr.Element](64, 65)
				// Simulate some work
				arrays.tree[0].SetOne()
				arrays.numers[0].SetZero()
				arrays.exclude[0].SetOne()
				putTreeArrays(arrays)
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < goroutines; i++ {
		<-done
	}
}

func TestTreeArrayPoolDifferentSizes(t *testing.T) {
	// Test that different sizes use different pools
	sizes := []struct {
		leafStart int
		m         int
	}{
		{32, 33},
		{64, 65},
		{128, 129},
		{256, 257},
	}

	for _, size := range sizes {
		arrays := getTreeArrays[bls12381fr.Element](size.leafStart, size.m)
		require.Equal(t, size.leafStart, len(arrays.tree))
		require.Equal(t, size.m, len(arrays.numers))
		require.Equal(t, size.leafStart, len(arrays.exclude))
		putTreeArrays(arrays)
	}
}

// BenchmarkTreeArrayPoolBLS benchmarks the pool performance for BLS12-381
func BenchmarkTreeArrayPoolBLS(b *testing.B) {
	leafStart := 64
	m := 65

	b.Run("with_pool", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			arrays := getTreeArrays[bls12381fr.Element](leafStart, m)
			// Simulate some work
			arrays.tree[0].SetOne()
			putTreeArrays(arrays)
		}
	})

	b.Run("without_pool", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
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
	leafStart := 128
	m := 129

	b.Run("with_pool", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			arrays := getTreeArrays[bn254fr.Element](leafStart, m)
			// Simulate some work
			arrays.tree[0].SetOne()
			putTreeArrays(arrays)
		}
	})

	b.Run("without_pool", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
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
