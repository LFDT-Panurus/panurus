/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package idemix

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewSKIProvider tests the creation of a new SKI provider
func TestNewSKIProvider(t *testing.T) {
	provider := NewSKIProvider()
	require.NotNil(t, provider)
}

// TestSKIProvider_GetSKIsFromIdentity tests SKI extraction from Idemix identities
func TestSKIProvider_GetSKIsFromIdentity(t *testing.T) {
	ctx := context.Background()
	provider := NewSKIProvider()

	t.Run("ValidIdemixIdentity", func(t *testing.T) {
		// Create a valid Idemix identity
		nymPublicKey := []byte("test-nym-public-key-12345")
		serialized := &crypto.SerializedIdemixIdentity{
			NymPublicKey: nymPublicKey,
			Proof:        []byte("test-proof"),
			Schema:       "test-schema",
		}

		identityBytes, err := proto.Marshal(serialized)
		require.NoError(t, err)

		// Extract SKI
		skis, err := provider.GetSKIsFromIdentity(ctx, identityBytes)
		require.NoError(t, err)
		require.Len(t, skis, 1)

		// Verify it matches the expected SHA-256 hash of NymPublicKey
		expectedHash := sha256.Sum256(nymPublicKey)
		expectedHex := hex.EncodeToString(expectedHash[:])
		assert.Equal(t, expectedHex, skis[0])
	})

	t.Run("EmptyIdentity", func(t *testing.T) {
		skis, err := provider.GetSKIsFromIdentity(ctx, []byte{})
		require.NoError(t, err)
		assert.Nil(t, skis)
	})

	t.Run("NilIdentity", func(t *testing.T) {
		skis, err := provider.GetSKIsFromIdentity(ctx, nil)
		require.NoError(t, err)
		assert.Nil(t, skis)
	})

	t.Run("InvalidIdentityBytes", func(t *testing.T) {
		// Use invalid bytes that cannot be unmarshalled
		invalidIdentity := []byte("invalid-protobuf-data")

		skis, err := provider.GetSKIsFromIdentity(ctx, invalidIdentity)
		require.Error(t, err)
		assert.Nil(t, skis)
		assert.Contains(t, err.Error(), "failed to extract SKI from Idemix identity")
	})

	t.Run("EmptyNymPublicKey", func(t *testing.T) {
		// Create identity with empty NymPublicKey
		serialized := &crypto.SerializedIdemixIdentity{
			NymPublicKey: []byte{},
			Proof:        []byte("test-proof"),
			Schema:       "test-schema",
		}

		identityBytes, err := proto.Marshal(serialized)
		require.NoError(t, err)

		skis, err := provider.GetSKIsFromIdentity(ctx, identityBytes)
		require.Error(t, err)
		assert.Nil(t, skis)
		assert.Contains(t, err.Error(), "invalid identity, no public key")
	})

	t.Run("NilNymPublicKey", func(t *testing.T) {
		// Create identity with nil NymPublicKey
		serialized := &crypto.SerializedIdemixIdentity{
			NymPublicKey: nil,
			Proof:        []byte("test-proof"),
			Schema:       "test-schema",
		}

		identityBytes, err := proto.Marshal(serialized)
		require.NoError(t, err)

		skis, err := provider.GetSKIsFromIdentity(ctx, identityBytes)
		require.Error(t, err)
		assert.Nil(t, skis)
		assert.Contains(t, err.Error(), "invalid identity, no public key")
	})

	t.Run("DifferentKeysProduceDifferentSKIs", func(t *testing.T) {
		// Create two identities with different NymPublicKeys
		nymKey1 := []byte("nym-public-key-1")
		nymKey2 := []byte("nym-public-key-2")

		serialized1 := &crypto.SerializedIdemixIdentity{
			NymPublicKey: nymKey1,
			Proof:        []byte("proof-1"),
			Schema:       "schema-1",
		}

		serialized2 := &crypto.SerializedIdemixIdentity{
			NymPublicKey: nymKey2,
			Proof:        []byte("proof-2"),
			Schema:       "schema-2",
		}

		identity1, err := proto.Marshal(serialized1)
		require.NoError(t, err)

		identity2, err := proto.Marshal(serialized2)
		require.NoError(t, err)

		// Get SKIs
		skis1, err := provider.GetSKIsFromIdentity(ctx, identity1)
		require.NoError(t, err)

		skis2, err := provider.GetSKIsFromIdentity(ctx, identity2)
		require.NoError(t, err)

		// Verify SKIs are different
		assert.NotEqual(t, skis1[0], skis2[0], "Different NymPublicKeys should produce different SKIs")
	})

	t.Run("SameKeyProducesSameSKI", func(t *testing.T) {
		// Create the same identity twice with same NymPublicKey
		nymPublicKey := []byte("consistent-nym-public-key")

		serialized1 := &crypto.SerializedIdemixIdentity{
			NymPublicKey: nymPublicKey,
			Proof:        []byte("proof-1"),
			Schema:       "schema-1",
		}

		serialized2 := &crypto.SerializedIdemixIdentity{
			NymPublicKey: nymPublicKey,
			Proof:        []byte("proof-2"), // Different proof
			Schema:       "schema-2",        // Different schema
		}

		identity1, err := proto.Marshal(serialized1)
		require.NoError(t, err)

		identity2, err := proto.Marshal(serialized2)
		require.NoError(t, err)

		// Get SKIs
		skis1, err := provider.GetSKIsFromIdentity(ctx, identity1)
		require.NoError(t, err)

		skis2, err := provider.GetSKIsFromIdentity(ctx, identity2)
		require.NoError(t, err)

		// Verify SKIs are the same (only NymPublicKey matters)
		assert.Equal(t, skis1[0], skis2[0], "Same NymPublicKey should produce same SKI")
	})

	t.Run("LargeNymPublicKey", func(t *testing.T) {
		// Create a large NymPublicKey (e.g., 1KB)
		largeKey := make([]byte, 1024)
		for i := range largeKey {
			largeKey[i] = byte(i % 256)
		}

		serialized := &crypto.SerializedIdemixIdentity{
			NymPublicKey: largeKey,
			Proof:        []byte("test-proof"),
			Schema:       "test-schema",
		}

		identityBytes, err := proto.Marshal(serialized)
		require.NoError(t, err)

		// Extract SKI
		skis, err := provider.GetSKIsFromIdentity(ctx, identityBytes)
		require.NoError(t, err)
		require.Len(t, skis, 1)

		// Verify the SKI is 64 characters (32 bytes in hex)
		assert.Len(t, skis[0], 64, "SKI should be 64 hex characters (32 bytes)")

		// Verify it matches the expected hash
		expectedHash := sha256.Sum256(largeKey)
		expectedHex := hex.EncodeToString(expectedHash[:])
		assert.Equal(t, expectedHex, skis[0])
	})

	t.Run("SingleByteKey", func(t *testing.T) {
		// Create identity with single-byte NymPublicKey
		singleByteKey := []byte{0x42}

		serialized := &crypto.SerializedIdemixIdentity{
			NymPublicKey: singleByteKey,
			Proof:        []byte("test-proof"),
			Schema:       "test-schema",
		}

		identityBytes, err := proto.Marshal(serialized)
		require.NoError(t, err)

		// Extract SKI
		skis, err := provider.GetSKIsFromIdentity(ctx, identityBytes)
		require.NoError(t, err)
		require.Len(t, skis, 1)

		// Verify the SKI
		expectedHash := sha256.Sum256(singleByteKey)
		expectedHex := hex.EncodeToString(expectedHash[:])
		assert.Equal(t, expectedHex, skis[0])
		assert.Len(t, skis[0], 64)
	})
}

// TestSKIProvider_ConsistencyWithSKIFromIdentity verifies that the provider
// produces the same results as the standalone SKIFromIdentity function
func TestSKIProvider_ConsistencyWithSKIFromIdentity(t *testing.T) {
	ctx := context.Background()
	provider := NewSKIProvider()

	// Create a test identity
	nymPublicKey := []byte("test-consistency-key")
	serialized := &crypto.SerializedIdemixIdentity{
		NymPublicKey: nymPublicKey,
		Proof:        []byte("test-proof"),
		Schema:       "test-schema",
	}

	identityBytes, err := proto.Marshal(serialized)
	require.NoError(t, err)

	// Get SKI using provider
	skisFromProvider, err := provider.GetSKIsFromIdentity(ctx, identityBytes)
	require.NoError(t, err)
	require.Len(t, skisFromProvider, 1)

	// Get SKI using standalone function
	skiFromFunction, err := crypto.SKIFromIdentity(identityBytes)
	require.NoError(t, err)

	// Convert function result to hex
	skiHex := hex.EncodeToString(skiFromFunction)

	// Verify they match
	assert.Equal(t, skiHex, skisFromProvider[0],
		"Provider should produce same result as SKIFromIdentity function")
}
