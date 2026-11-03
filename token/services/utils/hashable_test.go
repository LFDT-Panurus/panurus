/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests hashable.go which provides SHA256 hashing utilities.
// Tests cover Hashable type (byte slice with hash methods) and Hasher (builder for incremental hashing).
package utils

import (
	"encoding/base64"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHashable_Raw verifies that Raw() computes SHA256 hash correctly
func TestHashable_Raw(t *testing.T) {
	t.Parallel()

	t.Run("non-empty data", func(t *testing.T) {
		data := Hashable([]byte("test data"))
		hash := data.Raw()

		assert.NotNil(t, hash)
		assert.Len(t, hash, 32, "SHA256 hash should be 32 bytes")
	})

	t.Run("empty data", func(t *testing.T) {
		data := Hashable([]byte{})
		hash := data.Raw()

		assert.Nil(t, hash, "empty data should return nil")
	})

	t.Run("nil data", func(t *testing.T) {
		var data Hashable
		hash := data.Raw()

		assert.Nil(t, hash, "nil data should return nil")
	})

	t.Run("deterministic", func(t *testing.T) {
		data := Hashable([]byte("test data"))
		hash1 := data.Raw()
		hash2 := data.Raw()

		assert.Equal(t, hash1, hash2, "hash should be deterministic")
	})
}

// TestHashable_String verifies that String() returns base64 encoded hash
func TestHashable_String(t *testing.T) {
	t.Parallel()

	data := Hashable([]byte("test data"))
	str := data.String()

	// Verify it's valid base64
	decoded, err := base64.StdEncoding.DecodeString(str)
	require.NoError(t, err)
	assert.Len(t, decoded, 32, "decoded hash should be 32 bytes")

	// Verify it matches Raw()
	assert.Equal(t, data.Raw(), decoded)
}

// TestHashable_RawString verifies that RawString() returns raw bytes as string
func TestHashable_RawString(t *testing.T) {
	t.Parallel()

	data := Hashable([]byte("test data"))
	rawStr := data.RawString()

	assert.Equal(t, string(data.Raw()), rawStr)
	assert.Len(t, rawStr, 32, "raw string should be 32 bytes")
}

// TestHasher_AddInt32 verifies adding int32 values to hash
func TestHasher_AddInt32(t *testing.T) {
	t.Parallel()

	h := NewSHA256Hasher()

	err := h.AddInt32(42)
	require.NoError(t, err)

	digest := h.Digest()
	assert.Len(t, digest, 32)
}

// TestHasher_AddInt verifies adding int values to hash
func TestHasher_AddInt(t *testing.T) {
	t.Parallel()

	h := NewSHA256Hasher()

	err := h.AddInt(12345)
	require.NoError(t, err)

	digest := h.Digest()
	assert.Len(t, digest, 32)
}

// TestHasher_AddUInt64 verifies adding uint64 values to hash
func TestHasher_AddUInt64(t *testing.T) {
	t.Parallel()

	h := NewSHA256Hasher()

	err := h.AddUInt64(9876543210)
	require.NoError(t, err)

	digest := h.Digest()
	assert.Len(t, digest, 32)
}

// TestHasher_AddBytes verifies adding byte slices to hash
func TestHasher_AddBytes(t *testing.T) {
	t.Parallel()

	h := NewSHA256Hasher()

	err := h.AddBytes([]byte("test data"))
	require.NoError(t, err)

	digest := h.Digest()
	assert.Len(t, digest, 32)
}

// TestHasher_AddString verifies adding strings to hash
func TestHasher_AddString(t *testing.T) {
	t.Parallel()

	h := NewSHA256Hasher()

	err := h.AddString("test string")
	require.NoError(t, err)

	digest := h.Digest()
	assert.Len(t, digest, 32)
}

// TestHasher_AddBool verifies adding boolean values to hash
func TestHasher_AddBool(t *testing.T) {
	t.Parallel()

	t.Run("true", func(t *testing.T) {
		h := NewSHA256Hasher()

		n, err := h.AddBool(true)
		require.NoError(t, err)
		assert.Equal(t, 1, n, "should write 1 byte")

		digest := h.Digest()
		assert.Len(t, digest, 32)
	})

	t.Run("false", func(t *testing.T) {
		h := NewSHA256Hasher()

		n, err := h.AddBool(false)
		require.NoError(t, err)
		assert.Equal(t, 1, n, "should write 1 byte")

		digest := h.Digest()
		assert.Len(t, digest, 32)
	})

	t.Run("different hashes", func(t *testing.T) {
		h1 := NewSHA256Hasher()
		_, _ = h1.AddBool(true)
		digest1 := h1.Digest()

		h2 := NewSHA256Hasher()
		_, _ = h2.AddBool(false)
		digest2 := h2.Digest()

		assert.NotEqual(t, digest1, digest2, "true and false should produce different hashes")
	})
}

// TestHasher_AddFloat64 verifies adding float64 values to hash
func TestHasher_AddFloat64(t *testing.T) {
	t.Parallel()

	h := NewSHA256Hasher()

	err := h.AddFloat64(3.14159)
	require.NoError(t, err)

	digest := h.Digest()
	assert.Len(t, digest, 32)
}

// TestHasher_Digest verifies Digest returns correct hash
func TestHasher_Digest(t *testing.T) {
	t.Parallel()

	h := NewSHA256Hasher()
	_ = h.AddString("test")

	digest := h.Digest()
	assert.Len(t, digest, 32, "SHA256 digest should be 32 bytes")
}

// TestHasher_HexDigest verifies HexDigest returns hex-encoded hash
func TestHasher_HexDigest(t *testing.T) {
	t.Parallel()

	h := NewSHA256Hasher()
	_ = h.AddString("test")

	hexDigest := h.HexDigest()
	assert.Len(t, hexDigest, 64, "hex-encoded SHA256 should be 64 characters")

	// Verify it's valid hex
	for _, c := range hexDigest {
		assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
			"should only contain hex characters")
	}
}

// TestHasher_AddG1s verifies adding G1 elements to hash
func TestHasher_AddG1s(t *testing.T) {
	t.Parallel()

	curve := math.Curves[1] // BN254
	g1 := curve.GenG1

	t.Run("single element", func(t *testing.T) {
		h := NewSHA256Hasher()

		err := h.AddG1s([]*math.G1{g1})
		require.NoError(t, err)

		digest := h.Digest()
		assert.Len(t, digest, 32)
	})

	t.Run("multiple elements", func(t *testing.T) {
		h := NewSHA256Hasher()

		g1_2 := g1.Copy()
		g1_2.Add(g1)

		err := h.AddG1s([]*math.G1{g1, g1_2})
		require.NoError(t, err)

		digest := h.Digest()
		assert.Len(t, digest, 32)
	})

	t.Run("empty slice", func(t *testing.T) {
		h := NewSHA256Hasher()

		err := h.AddG1s([]*math.G1{})
		require.NoError(t, err)

		digest := h.Digest()
		assert.Len(t, digest, 32)
	})
}

// TestHasher_Deterministic verifies that hashing is deterministic
func TestHasher_Deterministic(t *testing.T) {
	t.Parallel()

	// Create two hashers with the same data
	h1 := NewSHA256Hasher()
	_ = h1.AddString("test")
	_ = h1.AddInt(42)
	_ = h1.AddBytes([]byte("data"))

	h2 := NewSHA256Hasher()
	_ = h2.AddString("test")
	_ = h2.AddInt(42)
	_ = h2.AddBytes([]byte("data"))

	assert.Equal(t, h1.Digest(), h2.Digest(), "same data should produce same hash")
	assert.Equal(t, h1.HexDigest(), h2.HexDigest(), "same data should produce same hex hash")
}

// TestHasher_OrderMatters verifies that order of additions matters
func TestHasher_OrderMatters(t *testing.T) {
	t.Parallel()

	h1 := NewSHA256Hasher()
	_ = h1.AddString("first")
	_ = h1.AddString("second")

	h2 := NewSHA256Hasher()
	_ = h2.AddString("second")
	_ = h2.AddString("first")

	assert.NotEqual(t, h1.Digest(), h2.Digest(), "different order should produce different hash")
}
