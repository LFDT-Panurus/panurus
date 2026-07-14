/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"crypto/sha256"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSKIFromIdentity_Success tests successful SKI extraction from a valid identity
func TestSKIFromIdentity_Success(t *testing.T) {
	// Create a valid serialized identity with a nym public key
	nymPublicKey := []byte("test-nym-public-key-12345")
	serialized := &SerializedIdemixIdentity{
		NymPublicKey: nymPublicKey,
		Proof:        []byte("test-proof"),
		Schema:       "test-schema",
	}

	// Marshal the identity
	identityBytes, err := proto.Marshal(serialized)
	require.NoError(t, err)

	// Call SKIFromIdentity
	ski, err := SKIFromIdentity(view.Identity(identityBytes))
	require.NoError(t, err)
	require.NotNil(t, ski)

	// Verify the SKI is the SHA-256 hash of the NymPublicKey
	expectedHash := sha256.Sum256(nymPublicKey)
	assert.Equal(t, expectedHash[:], ski, "SKI should be SHA-256 hash of NymPublicKey")
	assert.Len(t, ski, 32, "SKI should be 32 bytes (SHA-256 output)")
}

// TestSKIFromIdentity_EmptyNymPublicKey tests error handling when NymPublicKey is empty
func TestSKIFromIdentity_EmptyNymPublicKey(t *testing.T) {
	// Create a serialized identity with empty nym public key
	serialized := &SerializedIdemixIdentity{
		NymPublicKey: []byte{}, // Empty key
		Proof:        []byte("test-proof"),
		Schema:       "test-schema",
	}

	identityBytes, err := proto.Marshal(serialized)
	require.NoError(t, err)

	// Call SKIFromIdentity - should fail
	ski, err := SKIFromIdentity(view.Identity(identityBytes))
	require.Error(t, err)
	assert.Nil(t, ski)
	assert.Contains(t, err.Error(), "invalid identity, no public key")
}

// TestSKIFromIdentity_NilNymPublicKey tests error handling when NymPublicKey is nil
func TestSKIFromIdentity_NilNymPublicKey(t *testing.T) {
	// Create a serialized identity with nil nym public key
	serialized := &SerializedIdemixIdentity{
		NymPublicKey: nil, // Nil key
		Proof:        []byte("test-proof"),
		Schema:       "test-schema",
	}

	identityBytes, err := proto.Marshal(serialized)
	require.NoError(t, err)

	// Call SKIFromIdentity - should fail
	ski, err := SKIFromIdentity(view.Identity(identityBytes))
	require.Error(t, err)
	assert.Nil(t, ski)
	assert.Contains(t, err.Error(), "invalid identity, no public key")
}

// TestSKIFromIdentity_InvalidIdentityBytes tests error handling with malformed identity bytes
func TestSKIFromIdentity_InvalidIdentityBytes(t *testing.T) {
	// Use invalid bytes that cannot be unmarshalled
	invalidIdentity := view.Identity([]byte("invalid-protobuf-data"))

	// Call SKIFromIdentity - should fail during unmarshalling
	ski, err := SKIFromIdentity(invalidIdentity)
	require.Error(t, err)
	assert.Nil(t, ski)
	assert.Contains(t, err.Error(), "failed unmarshalling identity")
}

// TestSKIFromIdentity_EmptyIdentity tests error handling with empty identity bytes
func TestSKIFromIdentity_EmptyIdentity(t *testing.T) {
	// Use empty identity bytes
	emptyIdentity := view.Identity([]byte{})

	// Call SKIFromIdentity - should succeed with empty unmarshalled identity
	// but fail on validation since NymPublicKey will be empty
	ski, err := SKIFromIdentity(emptyIdentity)
	require.Error(t, err)
	assert.Nil(t, ski)
	assert.Contains(t, err.Error(), "invalid identity, no public key")
}

// TestSKIFromIdentity_DifferentKeys tests that different keys produce different SKIs
func TestSKIFromIdentity_DifferentKeys(t *testing.T) {
	// Create two identities with different nym public keys
	nymKey1 := []byte("nym-public-key-1")
	nymKey2 := []byte("nym-public-key-2")

	serialized1 := &SerializedIdemixIdentity{
		NymPublicKey: nymKey1,
		Proof:        []byte("proof-1"),
		Schema:       "schema-1",
	}

	serialized2 := &SerializedIdemixIdentity{
		NymPublicKey: nymKey2,
		Proof:        []byte("proof-2"),
		Schema:       "schema-2",
	}

	identity1, err := proto.Marshal(serialized1)
	require.NoError(t, err)

	identity2, err := proto.Marshal(serialized2)
	require.NoError(t, err)

	// Get SKIs for both identities
	ski1, err := SKIFromIdentity(view.Identity(identity1))
	require.NoError(t, err)

	ski2, err := SKIFromIdentity(view.Identity(identity2))
	require.NoError(t, err)

	// Verify SKIs are different
	assert.NotEqual(t, ski1, ski2, "Different NymPublicKeys should produce different SKIs")
}

// TestSKIFromIdentity_SameKeyProducesSameSKI tests deterministic behavior
func TestSKIFromIdentity_SameKeyProducesSameSKI(t *testing.T) {
	// Create the same identity twice
	nymPublicKey := []byte("consistent-nym-public-key")

	serialized1 := &SerializedIdemixIdentity{
		NymPublicKey: nymPublicKey,
		Proof:        []byte("proof-1"),
		Schema:       "schema-1",
	}

	serialized2 := &SerializedIdemixIdentity{
		NymPublicKey: nymPublicKey,
		Proof:        []byte("proof-2"), // Different proof
		Schema:       "schema-2",        // Different schema
	}

	identity1, err := proto.Marshal(serialized1)
	require.NoError(t, err)

	identity2, err := proto.Marshal(serialized2)
	require.NoError(t, err)

	// Get SKIs for both identities
	ski1, err := SKIFromIdentity(view.Identity(identity1))
	require.NoError(t, err)

	ski2, err := SKIFromIdentity(view.Identity(identity2))
	require.NoError(t, err)

	// Verify SKIs are the same (only NymPublicKey matters)
	assert.Equal(t, ski1, ski2, "Same NymPublicKey should produce same SKI regardless of other fields")
}

// TestSKIFromIdentity_LargeNymPublicKey tests handling of large public keys
func TestSKIFromIdentity_LargeNymPublicKey(t *testing.T) {
	// Create a large nym public key (e.g., 1KB)
	largeKey := make([]byte, 1024)
	for i := range largeKey {
		largeKey[i] = byte(i % 256)
	}

	serialized := &SerializedIdemixIdentity{
		NymPublicKey: largeKey,
		Proof:        []byte("test-proof"),
		Schema:       "test-schema",
	}

	identityBytes, err := proto.Marshal(serialized)
	require.NoError(t, err)

	// Call SKIFromIdentity
	ski, err := SKIFromIdentity(view.Identity(identityBytes))
	require.NoError(t, err)
	require.NotNil(t, ski)

	// Verify the SKI is still 32 bytes (SHA-256 output size)
	assert.Len(t, ski, 32, "SKI should always be 32 bytes regardless of input size")

	// Verify it matches the expected hash
	expectedHash := sha256.Sum256(largeKey)
	assert.Equal(t, expectedHash[:], ski)
}

// TestSKIFromIdentity_SingleByteKey tests handling of minimal valid key
func TestSKIFromIdentity_SingleByteKey(t *testing.T) {
	// Create identity with single-byte nym public key
	singleByteKey := []byte{0x42}

	serialized := &SerializedIdemixIdentity{
		NymPublicKey: singleByteKey,
		Proof:        []byte("test-proof"),
		Schema:       "test-schema",
	}

	identityBytes, err := proto.Marshal(serialized)
	require.NoError(t, err)

	// Call SKIFromIdentity
	ski, err := SKIFromIdentity(view.Identity(identityBytes))
	require.NoError(t, err)
	require.NotNil(t, ski)

	// Verify the SKI
	expectedHash := sha256.Sum256(singleByteKey)
	assert.Equal(t, expectedHash[:], ski)
	assert.Len(t, ski, 32)
}
