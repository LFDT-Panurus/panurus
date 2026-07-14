/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cleanup

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSKIProvider(t *testing.T) {
	provider := NewNoopSKIProvider()
	require.NotNil(t, provider, "NewSKIProvider should return a non-nil provider")
}

func TestSKIProvider_GetSKIsFromIdentity_ReturnsEmpty(t *testing.T) {
	provider := NewNoopSKIProvider()
	ctx := context.Background()

	// Test with a sample identity (content doesn't matter for X.509)
	identity := []byte("sample x509 certificate")

	skis, err := provider.GetSKIsFromIdentity(ctx, identity)

	require.NoError(t, err, "GetSKIsFromIdentity should not return an error")
	assert.Nil(t, skis, "GetSKIsFromIdentity should return nil for X.509 identities")
}

func TestSKIProvider_GetSKIsFromIdentity_WithNilIdentity(t *testing.T) {
	provider := NewNoopSKIProvider()
	ctx := context.Background()

	skis, err := provider.GetSKIsFromIdentity(ctx, nil)

	require.NoError(t, err, "GetSKIsFromIdentity should not return an error even with nil identity")
	assert.Nil(t, skis, "GetSKIsFromIdentity should return nil")
}

func TestSKIProvider_GetSKIsFromIdentity_WithEmptyIdentity(t *testing.T) {
	provider := NewNoopSKIProvider()
	ctx := context.Background()

	identity := []byte{}

	skis, err := provider.GetSKIsFromIdentity(ctx, identity)

	require.NoError(t, err, "GetSKIsFromIdentity should not return an error with empty identity")
	assert.Nil(t, skis, "GetSKIsFromIdentity should return nil")
}

func TestSKIProvider_GetSKIsFromIdentity_WithLargeIdentity(t *testing.T) {
	provider := NewNoopSKIProvider()
	ctx := context.Background()

	// Create a large identity (simulating a certificate)
	largeIdentity := make([]byte, 10000)
	for i := range largeIdentity {
		largeIdentity[i] = byte(i % 256)
	}

	skis, err := provider.GetSKIsFromIdentity(ctx, largeIdentity)

	require.NoError(t, err, "GetSKIsFromIdentity should not return an error with large identity")
	assert.Nil(t, skis, "GetSKIsFromIdentity should return nil regardless of identity size")
}

func TestSKIProvider_GetSKIsFromIdentity_MultipleCallsConsistent(t *testing.T) {
	provider := NewNoopSKIProvider()
	ctx := context.Background()
	identity := []byte("test certificate")

	// Call multiple times
	skis1, err1 := provider.GetSKIsFromIdentity(ctx, identity)
	skis2, err2 := provider.GetSKIsFromIdentity(ctx, identity)
	skis3, err3 := provider.GetSKIsFromIdentity(ctx, identity)

	require.NoError(t, err1, "First call should not error")
	require.NoError(t, err2, "Second call should not error")
	require.NoError(t, err3, "Third call should not error")

	assert.Nil(t, skis1, "First call should return nil")
	assert.Nil(t, skis2, "Second call should return nil")
	assert.Nil(t, skis3, "Third call should return nil")
}

func TestSKIProvider_GetSKIsFromIdentity_WithCancelledContext(t *testing.T) {
	provider := NewNoopSKIProvider()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	identity := []byte("test certificate")

	// Even with cancelled context, should return nil without error
	// since the implementation doesn't use the context
	skis, err := provider.GetSKIsFromIdentity(ctx, identity)

	require.NoError(t, err, "GetSKIsFromIdentity should not return an error even with cancelled context")
	assert.Nil(t, skis, "GetSKIsFromIdentity should return nil")
}

func TestSKIProvider_GetSKIsFromIdentity_DifferentIdentities(t *testing.T) {
	provider := NewNoopSKIProvider()
	ctx := context.Background()

	testCases := []struct {
		name     string
		identity []byte
	}{
		{
			name:     "Short identity",
			identity: []byte("short"),
		},
		{
			name:     "Medium identity",
			identity: []byte("this is a medium length identity string"),
		},
		{
			name:     "Binary identity",
			identity: []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD},
		},
		{
			name:     "UTF-8 identity",
			identity: []byte("Hello 世界 🌍"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			skis, err := provider.GetSKIsFromIdentity(ctx, tc.identity)

			require.NoError(t, err, "GetSKIsFromIdentity should not return an error for %s", tc.name)
			assert.Nil(t, skis, "GetSKIsFromIdentity should return nil for %s", tc.name)
		})
	}
}

// TestSKIProvider_Rationale documents why X.509 returns empty SKI
func TestSKIProvider_Rationale(t *testing.T) {
	// This test documents the design decision:
	// X.509 identities are certificate-based and don't require SKI extraction
	// for cleanup purposes. The certificate itself serves as the identity,
	// and cleanup can be performed based on the certificate's properties
	// rather than extracting a Subject Key Identifier.

	provider := NewNoopSKIProvider()
	ctx := context.Background()
	identity := []byte("x509 certificate")

	skis, err := provider.GetSKIsFromIdentity(ctx, identity)

	require.NoError(t, err)
	assert.Nil(t, skis, "X.509 provider intentionally returns nil - no SKI extraction needed")
}
