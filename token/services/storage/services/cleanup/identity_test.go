/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cleanup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSKIProvider is a test implementation of TypedSKIProvider
type mockSKIProvider struct {
	skis []string
	err  error
}

func (m *mockSKIProvider) GetSKIsFromIdentity(ctx context.Context, identity []byte) ([]string, error) {
	if m.err != nil {
		return nil, m.err
	}

	return m.skis, nil
}

// TestFallbackSKIProvider_GetSKIsFromIdentity tests the fallback provider
func TestFallbackSKIProvider_GetSKIsFromIdentity(t *testing.T) {
	ctx := context.Background()
	provider := NewFallbackSKIProvider()

	t.Run("ValidIdentity", func(t *testing.T) {
		identity := []byte("test-identity-data")
		skis, err := provider.GetSKIsFromIdentity(ctx, identity)

		require.NoError(t, err)
		require.Len(t, skis, 1)

		// Verify it's the SHA256 hash
		expectedHash := sha256.Sum256(identity)
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

	t.Run("LargeIdentity", func(t *testing.T) {
		// Test with large identity data
		identity := make([]byte, 10000)
		for i := range identity {
			identity[i] = byte(i % 256)
		}

		skis, err := provider.GetSKIsFromIdentity(ctx, identity)

		require.NoError(t, err)
		require.Len(t, skis, 1)
		assert.Len(t, skis[0], 64) // SHA256 hex is 64 characters
	})

	t.Run("DeterministicOutput", func(t *testing.T) {
		identity := []byte("consistent-identity")

		skis1, err1 := provider.GetSKIsFromIdentity(ctx, identity)
		skis2, err2 := provider.GetSKIsFromIdentity(ctx, identity)

		require.NoError(t, err1)
		require.NoError(t, err2)
		assert.Equal(t, skis1, skis2, "Same identity should produce same SKI")
	})
}

// TestSKIExtractor_NewSKIExtractor tests extractor initialization
func TestSKIExtractor_NewSKIExtractor(t *testing.T) {
	extractor := NewSKIExtractor()

	require.NotNil(t, extractor)
	assert.NotNil(t, extractor.providers)
	assert.NotNil(t, extractor.defaultProvider)
	assert.Empty(t, extractor.providers, "Should start with no registered providers")
}

// TestSKIExtractor_RegisterProvider tests provider registration
func TestSKIExtractor_RegisterProvider(t *testing.T) {
	extractor := NewSKIExtractor()

	t.Run("RegisterSingleProvider", func(t *testing.T) {
		provider := &mockSKIProvider{skis: []string{"test-ski"}}
		extractor.RegisterProvider("idemix", provider)

		extractor.mu.RLock()
		registered, exists := extractor.providers["idemix"]
		extractor.mu.RUnlock()

		assert.True(t, exists)
		assert.Equal(t, provider, registered)
	})

	t.Run("RegisterMultipleProviders", func(t *testing.T) {
		extractor := NewSKIExtractor()

		idemixProvider := &mockSKIProvider{skis: []string{"idemix-ski"}}
		x509Provider := &mockSKIProvider{skis: []string{"x509-ski"}}

		extractor.RegisterProvider("idemix", idemixProvider)
		extractor.RegisterProvider("x509", x509Provider)

		extractor.mu.RLock()
		assert.Len(t, extractor.providers, 2)
		assert.Equal(t, idemixProvider, extractor.providers["idemix"])
		assert.Equal(t, x509Provider, extractor.providers["x509"])
		extractor.mu.RUnlock()
	})

	t.Run("OverwriteProvider", func(t *testing.T) {
		extractor := NewSKIExtractor()

		provider1 := &mockSKIProvider{skis: []string{"ski-1"}}
		provider2 := &mockSKIProvider{skis: []string{"ski-2"}}

		extractor.RegisterProvider("idemix", provider1)
		extractor.RegisterProvider("idemix", provider2)

		extractor.mu.RLock()
		registered := extractor.providers["idemix"]
		extractor.mu.RUnlock()

		assert.Equal(t, provider2, registered, "Second registration should overwrite first")
	})
}

// TestSKIExtractor_SetDefaultProvider tests setting default provider
func TestSKIExtractor_SetDefaultProvider(t *testing.T) {
	extractor := NewSKIExtractor()

	t.Run("SetCustomDefault", func(t *testing.T) {
		customProvider := &mockSKIProvider{skis: []string{"custom-ski"}}
		extractor.SetDefaultProvider(customProvider)

		extractor.mu.RLock()
		defaultProvider := extractor.defaultProvider
		extractor.mu.RUnlock()

		assert.Equal(t, customProvider, defaultProvider)
	})

	t.Run("DefaultProviderUsedForUnknownType", func(t *testing.T) {
		extractor := NewSKIExtractor()
		customProvider := &mockSKIProvider{skis: []string{"default-ski"}}
		extractor.SetDefaultProvider(customProvider)

		ctx := context.Background()
		identity := []byte("test-identity")

		skis, err := extractor.GetSKIsFromIdentity(ctx, identity, "unknown-type")

		require.NoError(t, err)
		assert.Equal(t, []string{"default-ski"}, skis)
	})
}

// TestSKIExtractor_GetSKIsFromIdentity tests SKI extraction with delegation
func TestSKIExtractor_GetSKIsFromIdentity(t *testing.T) {
	ctx := context.Background()

	t.Run("EmptyIdentity", func(t *testing.T) {
		extractor := NewSKIExtractor()

		skis, err := extractor.GetSKIsFromIdentity(ctx, []byte{}, "idemix")

		require.NoError(t, err)
		assert.Nil(t, skis)
	})

	t.Run("NilIdentity", func(t *testing.T) {
		extractor := NewSKIExtractor()

		skis, err := extractor.GetSKIsFromIdentity(ctx, nil, "idemix")

		require.NoError(t, err)
		assert.Nil(t, skis)
	})

	t.Run("UseRegisteredProvider", func(t *testing.T) {
		extractor := NewSKIExtractor()
		idemixProvider := &mockSKIProvider{skis: []string{"idemix-ski-1", "idemix-ski-2"}}
		extractor.RegisterProvider("idemix", idemixProvider)

		identity := []byte("idemix-identity")
		skis, err := extractor.GetSKIsFromIdentity(ctx, identity, "idemix")

		require.NoError(t, err)
		assert.Equal(t, []string{"idemix-ski-1", "idemix-ski-2"}, skis)
	})

	t.Run("UseFallbackForUnknownType", func(t *testing.T) {
		extractor := NewSKIExtractor()

		identity := []byte("unknown-identity")
		skis, err := extractor.GetSKIsFromIdentity(ctx, identity, "unknown-type")

		require.NoError(t, err)
		require.Len(t, skis, 1)

		// Should use fallback (SHA256)
		expectedHash := sha256.Sum256(identity)
		expectedHex := hex.EncodeToString(expectedHash[:])
		assert.Equal(t, expectedHex, skis[0])
	})

	t.Run("DifferentProvidersForDifferentTypes", func(t *testing.T) {
		extractor := NewSKIExtractor()

		idemixProvider := &mockSKIProvider{skis: []string{"idemix-ski"}}
		x509Provider := &mockSKIProvider{skis: []string{"x509-ski"}}

		extractor.RegisterProvider("idemix", idemixProvider)
		extractor.RegisterProvider("x509", x509Provider)

		identity := []byte("test-identity")

		idemixSKIs, err := extractor.GetSKIsFromIdentity(ctx, identity, "idemix")
		require.NoError(t, err)
		assert.Equal(t, []string{"idemix-ski"}, idemixSKIs)

		x509SKIs, err := extractor.GetSKIsFromIdentity(ctx, identity, "x509")
		require.NoError(t, err)
		assert.Equal(t, []string{"x509-ski"}, x509SKIs)
	})

	t.Run("ProviderReturnsError", func(t *testing.T) {
		extractor := NewSKIExtractor()
		expectedErr := errors.New("provider error")
		errorProvider := &mockSKIProvider{err: expectedErr}
		extractor.RegisterProvider("error-type", errorProvider)

		identity := []byte("test-identity")
		skis, err := extractor.GetSKIsFromIdentity(ctx, identity, "error-type")

		require.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, skis)
	})

	t.Run("ProviderReturnsMultipleSKIs", func(t *testing.T) {
		extractor := NewSKIExtractor()
		multiProvider := &mockSKIProvider{
			skis: []string{"ski-1", "ski-2", "ski-3"},
		}
		extractor.RegisterProvider("multi", multiProvider)

		identity := []byte("test-identity")
		skis, err := extractor.GetSKIsFromIdentity(ctx, identity, "multi")

		require.NoError(t, err)
		assert.Equal(t, []string{"ski-1", "ski-2", "ski-3"}, skis)
	})

	t.Run("ProviderReturnsEmptySKIs", func(t *testing.T) {
		extractor := NewSKIExtractor()
		emptyProvider := &mockSKIProvider{skis: []string{}}
		extractor.RegisterProvider("empty", emptyProvider)

		identity := []byte("test-identity")
		skis, err := extractor.GetSKIsFromIdentity(ctx, identity, "empty")

		require.NoError(t, err)
		assert.Empty(t, skis)
	})
}

// TestSKIExtractor_ConcurrentAccess tests thread safety
func TestSKIExtractor_ConcurrentAccess(t *testing.T) {
	extractor := NewSKIExtractor()
	ctx := context.Background()

	// Register providers
	for range 10 {
		provider := &mockSKIProvider{skis: []string{"ski"}}
		extractor.RegisterProvider("type", provider)
	}

	// Concurrent reads
	done := make(chan bool)
	for range 100 {
		go func() {
			identity := []byte("test-identity")
			_, _ = extractor.GetSKIsFromIdentity(ctx, identity, "type")
			done <- true
		}()
	}

	// Wait for all goroutines
	for range 100 {
		<-done
	}
}

// TestSKIExtractor_BackwardCompatibility tests that default behavior is preserved
func TestSKIExtractor_BackwardCompatibility(t *testing.T) {
	ctx := context.Background()
	extractor := NewSKIExtractor()

	t.Run("DefaultBehaviorPreserved", func(t *testing.T) {
		identity := []byte("test-identity-data")

		// Get SKI using new extractor
		skis, err := extractor.GetSKIsFromIdentity(ctx, identity, "any-type")

		require.NoError(t, err)
		require.Len(t, skis, 1)

		// Verify it matches the old SHA256 behavior
		expectedHash := sha256.Sum256(identity)
		expectedHex := hex.EncodeToString(expectedHash[:])
		assert.Equal(t, expectedHex, skis[0])
	})

	t.Run("EmptyIdentityBehaviorPreserved", func(t *testing.T) {
		skis, err := extractor.GetSKIsFromIdentity(ctx, []byte{}, "any-type")

		require.NoError(t, err)
		assert.Nil(t, skis)
	})
}
