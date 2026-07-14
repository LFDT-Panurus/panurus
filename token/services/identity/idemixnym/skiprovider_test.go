/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package idemixnym

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym/nym"
	kvs2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewSKIProvider tests the creation of a new SKI provider
func TestNewSKIProvider(t *testing.T) {
	identityStoreService := &mock.IdentityStoreService{}
	provider := NewSKIProvider(identityStoreService)
	require.NotNil(t, provider)
	assert.Equal(t, identityStoreService, provider.identityStoreService)
}

// TestSKIProvider_GetSKIsFromIdentity tests SKI extraction from IdemixNym identities
func TestSKIProvider_GetSKIsFromIdentity(t *testing.T) {
	testSKIProviderGetSKIsFromIdentity(t, "../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testSKIProviderGetSKIsFromIdentity(t, "../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testSKIProviderGetSKIsFromIdentity(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	ctx := context.Background()

	// Setup: Create a real IdemixNym identity
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	backendKM, err := idemix.NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)

	identityStoreService := &mock.IdentityStoreService{}
	keyManager := NewKeyManager(backendKM, identityStoreService)
	require.NotNil(t, keyManager)

	// Get an identity
	identityDescriptor, err := keyManager.Identity(ctx, nil)
	require.NoError(t, err)

	// Setup mock to return the signer info
	identityStoreService.GetSignerInfoReturns(identityDescriptor.SignerInfo, nil)

	// Create SKI provider
	provider := NewSKIProvider(identityStoreService)

	t.Run("ValidIdemixNymIdentity", func(t *testing.T) {
		// Extract SKI
		skis, err := provider.GetSKIsFromIdentity(ctx, identityDescriptor.Identity)
		require.NoError(t, err)
		require.Len(t, skis, 1)

		// Verify the SKI is correct by comparing with direct extraction
		auditInfo := &nym.AuditInfo{}
		err = json.Unmarshal(identityDescriptor.SignerInfo, auditInfo)
		require.NoError(t, err)

		expectedSKI, err := crypto.SKIFromIdentity(auditInfo.IdemixSignature)
		require.NoError(t, err)
		expectedHex := hex.EncodeToString(expectedSKI)

		assert.Equal(t, expectedHex, skis[0])
		assert.Len(t, skis[0], 64, "SKI should be 64 hex characters (32 bytes)")
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

	t.Run("IdentityStoreError", func(t *testing.T) {
		// Create a new provider with a mock that returns an error
		errorMock := &mock.IdentityStoreService{}
		errorMock.GetSignerInfoReturns(nil, errors.New("identity store error"))
		errorProvider := NewSKIProvider(errorMock)

		skis, err := errorProvider.GetSKIsFromIdentity(ctx, []byte("test-identity"))
		require.Error(t, err)
		assert.Nil(t, skis)
		assert.Contains(t, err.Error(), "failed to retrieve signer info")
	})

	t.Run("InvalidSignerInfo", func(t *testing.T) {
		// Create a new provider with a mock that returns invalid JSON
		invalidMock := &mock.IdentityStoreService{}
		invalidMock.GetSignerInfoReturns([]byte("invalid json"), nil)
		invalidProvider := NewSKIProvider(invalidMock)

		skis, err := invalidProvider.GetSKIsFromIdentity(ctx, []byte("test-identity"))
		require.Error(t, err)
		assert.Nil(t, skis)
		assert.Contains(t, err.Error(), "failed to deserialize audit info")
	})

	t.Run("InvalidIdemixSignature", func(t *testing.T) {
		// Create audit info with invalid IdemixSignature
		invalidAuditInfo := &nym.AuditInfo{
			IdemixSignature: []byte("invalid-idemix-signature"),
		}
		invalidSignerInfo, err := json.Marshal(invalidAuditInfo)
		require.NoError(t, err)

		invalidMock := &mock.IdentityStoreService{}
		invalidMock.GetSignerInfoReturns(invalidSignerInfo, nil)
		invalidProvider := NewSKIProvider(invalidMock)

		skis, err := invalidProvider.GetSKIsFromIdentity(ctx, []byte("test-identity"))
		require.Error(t, err)
		assert.Nil(t, skis)
		assert.Contains(t, err.Error(), "failed to extract SKI from IdemixSignature")
	})
}

// TestSKIProvider_ConsistencyWithDeserializeSigner verifies that the SKI provider
// extracts the same IdemixSignature as DeserializeSigner
func TestSKIProvider_ConsistencyWithDeserializeSigner(t *testing.T) {
	testSKIProviderConsistency(t, "../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testSKIProviderConsistency(t, "../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testSKIProviderConsistency(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	ctx := context.Background()

	// Setup
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	backendKM, err := idemix.NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)

	identityStoreService := &mock.IdentityStoreService{}
	keyManager := NewKeyManager(backendKM, identityStoreService)

	// Get an identity
	identityDescriptor, err := keyManager.Identity(ctx, nil)
	require.NoError(t, err)

	// Setup mock
	identityStoreService.GetSignerInfoReturns(identityDescriptor.SignerInfo, nil)

	// Extract SKI using provider
	provider := NewSKIProvider(identityStoreService)
	skis, err := provider.GetSKIsFromIdentity(ctx, identityDescriptor.Identity)
	require.NoError(t, err)
	require.Len(t, skis, 1)

	// Manually extract SKI following the same logic
	auditInfo := &nym.AuditInfo{}
	err = json.Unmarshal(identityDescriptor.SignerInfo, auditInfo)
	require.NoError(t, err)

	expectedSKI, err := crypto.SKIFromIdentity(auditInfo.IdemixSignature)
	require.NoError(t, err)
	expectedHex := hex.EncodeToString(expectedSKI)

	// Verify they match
	assert.Equal(t, expectedHex, skis[0],
		"SKI from provider should match manual extraction")
}

// TestSKIProvider_MultipleIdentities tests that different identities produce different SKIs
func TestSKIProvider_MultipleIdentities(t *testing.T) {
	testSKIProviderMultipleIdentities(t, "../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testSKIProviderMultipleIdentities(t, "../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testSKIProviderMultipleIdentities(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	ctx := context.Background()

	// Setup
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	backendKM, err := idemix.NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)

	identityStoreService := &mock.IdentityStoreService{}
	keyManager := NewKeyManager(backendKM, identityStoreService)

	// Get two different identities
	id1, err := keyManager.Identity(ctx, nil)
	require.NoError(t, err)

	id2, err := keyManager.Identity(ctx, nil)
	require.NoError(t, err)

	// Setup mock to return appropriate signer info for each identity
	identityStoreService.GetSignerInfoCalls(func(ctx context.Context, id []byte) ([]byte, error) {
		switch string(id) {
		case string(id1.Identity):
			return id1.SignerInfo, nil
		case string(id2.Identity):
			return id2.SignerInfo, nil
		default:
			return nil, errors.New("unknown identity")
		}
	})

	// Create provider
	provider := NewSKIProvider(identityStoreService)

	// Extract SKIs
	skis1, err := provider.GetSKIsFromIdentity(ctx, id1.Identity)
	require.NoError(t, err)
	require.Len(t, skis1, 1)

	skis2, err := provider.GetSKIsFromIdentity(ctx, id2.Identity)
	require.NoError(t, err)
	require.Len(t, skis2, 1)

	// Verify SKIs are different (different IdemixSignatures)
	assert.NotEqual(t, skis1[0], skis2[0],
		"Different identities should produce different SKIs")
}

// TestSKIProvider_DeterministicOutput tests that the same identity always produces the same SKI
func TestSKIProvider_DeterministicOutput(t *testing.T) {
	testSKIProviderDeterministic(t, "../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testSKIProviderDeterministic(t, "../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testSKIProviderDeterministic(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	ctx := context.Background()

	// Setup
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	backendKM, err := idemix.NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)

	identityStoreService := &mock.IdentityStoreService{}
	keyManager := NewKeyManager(backendKM, identityStoreService)

	// Get an identity
	identityDescriptor, err := keyManager.Identity(ctx, nil)
	require.NoError(t, err)

	// Setup mock
	identityStoreService.GetSignerInfoReturns(identityDescriptor.SignerInfo, nil)

	// Create provider
	provider := NewSKIProvider(identityStoreService)

	// Extract SKI multiple times
	skis1, err := provider.GetSKIsFromIdentity(ctx, identityDescriptor.Identity)
	require.NoError(t, err)

	skis2, err := provider.GetSKIsFromIdentity(ctx, identityDescriptor.Identity)
	require.NoError(t, err)

	skis3, err := provider.GetSKIsFromIdentity(ctx, identityDescriptor.Identity)
	require.NoError(t, err)

	// Verify all SKIs are identical
	assert.Equal(t, skis1, skis2, "Same identity should produce same SKI")
	assert.Equal(t, skis2, skis3, "Same identity should produce same SKI")
}
