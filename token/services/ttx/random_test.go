/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests random.go which provides cryptographic random number generation.
// Tests verify proper buffer filling, length validation, and randomness properties.
package ttx_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetRandomBytes_Success verifies that GetRandomBytes returns the correct length
// of random bytes and that the buffer is fully filled.
func TestGetRandomBytes_Success(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{"zero length", 0},
		{"single byte", 1},
		{"nonce size", ttx.NonceSize},
		{"small buffer", 16},
		{"medium buffer", 64},
		{"large buffer", 256},
		{"very large buffer", 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ttx.GetRandomBytes(tt.length)

			require.NoError(t, err)
			assert.Len(t, result, tt.length, "returned buffer should have requested length")

			// For non-zero lengths, verify buffer is not all zeros (extremely unlikely with crypto/rand)
			if tt.length > 0 {
				allZeros := true
				for _, b := range result {
					if b != 0 {
						allZeros = false

						break
					}
				}
				assert.False(t, allZeros, "random bytes should not be all zeros")
			}
		})
	}
}

// TestGetRandomBytes_Uniqueness verifies that multiple calls to GetRandomBytes
// produce different results (with overwhelming probability).
func TestGetRandomBytes_Uniqueness(t *testing.T) {
	length := 32
	iterations := 100

	seen := make(map[string]bool)
	for range iterations {
		result, err := ttx.GetRandomBytes(length)
		require.NoError(t, err)

		key := string(result)
		assert.False(t, seen[key], "should not generate duplicate random bytes")
		seen[key] = true
	}

	assert.Len(t, seen, iterations, "all generated values should be unique")
}

// TestGetRandomBytes_Distribution verifies basic statistical properties of the
// random bytes to ensure they're not obviously biased.
func TestGetRandomBytes_Distribution(t *testing.T) {
	// Generate a large sample
	length := 1000
	result, err := ttx.GetRandomBytes(length)
	require.NoError(t, err)

	// Count byte values
	counts := make([]int, 256)
	for _, b := range result {
		counts[b]++
	}

	// Verify we have reasonable distribution (not all same value)
	uniqueValues := 0
	for _, count := range counts {
		if count > 0 {
			uniqueValues++
		}
	}

	// With 1000 random bytes, we should see a good variety of values
	// (statistically, we'd expect to see most byte values at least once)
	assert.Greater(t, uniqueValues, 200, "should have good distribution of byte values")
}

// TestGetRandomNonce_Success verifies that GetRandomNonce returns a nonce
// of the correct size.
func TestGetRandomNonce_Success(t *testing.T) {
	result, err := ttx.GetRandomNonce()

	require.NoError(t, err)
	assert.Len(t, result, ttx.NonceSize, "nonce should be NonceSize bytes")

	// Verify not all zeros
	allZeros := true
	for _, b := range result {
		if b != 0 {
			allZeros = false

			break
		}
	}
	assert.False(t, allZeros, "nonce should not be all zeros")
}

// TestGetRandomNonce_Uniqueness verifies that multiple calls to GetRandomNonce
// produce different nonces.
func TestGetRandomNonce_Uniqueness(t *testing.T) {
	iterations := 100
	seen := make(map[string]bool)

	for range iterations {
		result, err := ttx.GetRandomNonce()
		require.NoError(t, err)

		key := string(result)
		assert.False(t, seen[key], "should not generate duplicate nonces")
		seen[key] = true
	}

	assert.Len(t, seen, iterations, "all generated nonces should be unique")
}

// TestGetRandomNonce_UsesCorrectSize verifies that the NonceSize constant
// is used correctly by GetRandomNonce.
func TestGetRandomNonce_UsesCorrectSize(t *testing.T) {
	// Call GetRandomNonce multiple times and verify consistent size
	for range 10 {
		result, err := ttx.GetRandomNonce()
		require.NoError(t, err)
		assert.Len(t, result, ttx.NonceSize, "nonce size should match NonceSize constant")
	}
}

// TestNonceSize_Value verifies the NonceSize constant has the expected value.
func TestNonceSize_Value(t *testing.T) {
	assert.Equal(t, 24, ttx.NonceSize, "NonceSize should be 24 bytes")
}
