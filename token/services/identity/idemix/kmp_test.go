/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package idemix

import (
	"context"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	kvs2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockConfig struct {
}

func (m mockConfig) CacheSizeForOwnerID(string) int {
	return 0
}

func (m mockConfig) TranslatePath(path string) string {
	return path
}

func (m mockConfig) IdentitiesForRole(driver.IdentityRoleType) ([]driver.ConfiguredIdentity, error) {
	return nil, nil
}

//go:norace
func TestNewKeyManagerProvider(t *testing.T) {
	testNewKeyManagerProvider(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testNewKeyManagerProvider(t, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testNewKeyManagerProvider(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)

	kmp := NewKeyManagerProvider(
		config.Ipk,
		curveID,
		keyStore,
		&mockConfig{},
		0,
		false,
		&disabled.Provider{},
	)
	assert.NotNil(t, kmp)
	idConfig := &token.IdentityConfiguration{
		ID:  "alice",
		URL: configPath,
	}
	km, err := kmp.Get(t.Context(), idConfig)
	require.NoError(t, err)
	assert.NotNil(t, km)
	assert.NotNil(t, idConfig.Raw)
	signAndVerify(t, km)
	configRaw := idConfig.Raw
	checkRawContent(t, config.Ipk, idConfig.Raw)

	idConfig.URL = ""
	km, err = kmp.Get(t.Context(), idConfig)
	require.NoError(t, err)
	assert.NotNil(t, km)
	assert.NotNil(t, idConfig.Raw)
	signAndVerify(t, km)
	checkRawContent(t, config.Ipk, idConfig.Raw)
	assert.Equal(t, configRaw, idConfig.Raw)

	km, err = kmp.Get(t.Context(), idConfig)
	require.NoError(t, err)
	assert.NotNil(t, km)
	assert.NotNil(t, idConfig.Raw)
	signAndVerify(t, km)
	checkRawContent(t, config.Ipk, idConfig.Raw)
	assert.Equal(t, configRaw, idConfig.Raw)

	// change the version in the configuration, it must fail now
	config2, err := crypto.NewConfigFromRaw(config.Ipk, idConfig.Raw)
	require.NoError(t, err)
	config2.Version = 0
	config2Raw, err := proto.Marshal(config2)
	require.NoError(t, err)
	idConfig.Raw = config2Raw
	_, err = kmp.Get(t.Context(), idConfig)
	require.Error(t, err)
	require.EqualError(t, err, "unsupported protocol version: 0")
}

func signAndVerify(t *testing.T, km membership.KeyManager) {
	t.Helper()
	identityDescriptor, err := km.Identity(t.Context(), nil)
	require.NoError(t, err)
	id := identityDescriptor.Identity
	signer, err := km.DeserializeSigner(t.Context(), id)
	require.NoError(t, err)
	msg := []byte("message")
	sigma, err := signer.Sign(msg)
	require.NoError(t, err)
	verifier, err := km.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)
	require.NoError(t, verifier.Verify(msg, sigma))
}

func checkRawContent(t *testing.T, ipk []byte, raw []byte) {
	t.Helper()
	conf, err := crypto.NewConfigFromRaw(ipk, raw)
	require.NoError(t, err)
	assert.NotNil(t, conf)
	assert.NotNil(t, conf.Signer.Ski)
	assert.Nil(t, conf.Signer.Sk)
}

// TestKeyManagerProviderErrorPaths tests various error paths in kmp
func TestKeyManagerProviderErrorPaths(t *testing.T) {
	testKeyManagerProviderErrorPaths(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testKeyManagerProviderErrorPaths(t, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testKeyManagerProviderErrorPaths(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)

	kmp := NewKeyManagerProvider(
		config.Ipk,
		curveID,
		keyStore,
		&mockConfig{},
		0,
		false,
		&disabled.Provider{},
	)

	// Test Getting a key-manager using a bad IdentityConfiguration due to an invalid raw identity config
	idConfig := &driver.IdentityConfiguration{
		ID:  "test",
		Raw: []byte{0, 1, 2, 3},
	}
	_, err = kmp.Get(context.Background(), idConfig)
	require.Error(t, err)

	// Test Getting a key-manager using a bad IdentityConfiguration due to a nonexistant URL path
	idConfig = &driver.IdentityConfiguration{
		ID:  "test",
		URL: "/nonexistent/path",
	}
	_, err = kmp.Get(context.Background(), idConfig)
	require.Error(t, err)

	// Test using a remote crypto config i.e. a wallet that has no secret key
	// Load an existing config and remove the secret key to simulate remoteness
	configForRemote, err := crypto.NewConfig(configPath)
	require.NoError(t, err)

	// Clear the secret key to make it remote
	configForRemote.Signer.Sk = nil
	configForRemote.Signer.Cred = nil
	configForRemote.Signer.Ski = nil

	configRemoteRaw, err := proto.Marshal(configForRemote)
	require.NoError(t, err)

	// Create a remote identity configuration based on the remote raw cryptographic config
	// Get a remote key-manager using the remote donfig (should succeed)
	idConfigRemote := &driver.IdentityConfiguration{
		ID:  "remote-test",
		Raw: configRemoteRaw,
	}
	kmRemote, err := kmp.Get(context.Background(), idConfigRemote)
	require.NoError(t, err)

	// Try to get an identity from the remote wallet - should fail
	_, err = kmRemote.Identity(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot invoke this function, remote must register pseudonyms")
}

// TestWrappedKeyManagerIdentity tests the WrappedKeyManager.Identity method
// that uses a pre-generated cache of Idemix pseudonyms so that calling Identity is fast
func TestWrappedKeyManagerIdentity(t *testing.T) {
	testWrappedKeyManagerIdentity(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testWrappedKeyManagerIdentity(t, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testWrappedKeyManagerIdentity(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)

	kmp := NewKeyManagerProvider(
		config.Ipk,
		curveID,
		keyStore,
		&mockConfig{},
		3, // Set cache size
		false,
		&disabled.Provider{},
	)

	idConfig := &token.IdentityConfiguration{
		ID:  "alice",
		URL: configPath,
	}
	// create the WrappedKeyManager (km) from the constructed KeyManagerProvider
	km, err := kmp.Get(context.Background(), idConfig)
	require.NoError(t, err)

	// Test that Identity method works fine through the wrapper
	id1, err := km.Identity(context.Background(), nil)
	require.NoError(t, err)
	assert.NotNil(t, id1)

	// Get another identity with same audit info
	id2, err := km.Identity(context.Background(), id1.AuditInfo)
	require.NoError(t, err)
	assert.NotNil(t, id2)
	assert.Equal(t, id1.AuditInfo, id2.AuditInfo)
}

// TestCacheSizeConfiguration tests that the cache size configuration
// is set correctly for a KeyManagerProvider
func TestCacheSizeConfiguration(t *testing.T) {
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig("./testdata/bls12_381_bbs_gurvy/idemix")
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(math.BLS12_381_BBS_GURVY, kvs2.Keystore(backend))
	require.NoError(t, err)

	// Test with custom cache size
	customCacheSize := 10
	kmp := NewKeyManagerProvider(
		config.Ipk,
		math.BLS12_381_BBS_GURVY,
		keyStore,
		&mockConfig{},
		customCacheSize,
		false,
		&disabled.Provider{},
	)

	// Verify cache size is used
	cacheSize, err := kmp.cacheSizeForID("test-id")
	require.NoError(t, err)
	assert.Equal(t, customCacheSize, cacheSize)
}

// TestKeyManagerProviderWithIgnoreVerifyOnlyWallet tests the ignoreVerifyOnlyWallet flag.
// The flag is set to true in the test, which should trigger a search in the testdata dir
// for a signer-configuration that allows signing and not just verification.
// fall back to the verify only wallet.
func TestKeyManagerProviderWithIgnoreVerifyOnlyWallet(t *testing.T) {
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig("./testdata/bls12_381_bbs_gurvy/idemix")
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(math.BLS12_381_BBS_GURVY, kvs2.Keystore(backend))
	require.NoError(t, err)

	// Test with ignoreVerifyOnlyWallet = true
	kmp := NewKeyManagerProvider(
		config.Ipk,
		math.BLS12_381_BBS_GURVY,
		keyStore,
		&mockConfig{},
		0,
		true, // ignoreVerifyOnlyWallet
		&disabled.Provider{},
	)

	idConfig := &token.IdentityConfiguration{
		ID:  "alice",
		URL: "./testdata/bls12_381_bbs_gurvy/idemix",
	}
	km, err := kmp.Get(context.Background(), idConfig)
	require.NoError(t, err)
	assert.NotNil(t, km)
}

// TestKeyManagerProviderGetWithRawConfig tests using the kmp with an idConfig
// that's created based on just a Raw config (no URL).
func TestKeyManagerProviderGetWithRawConfig(t *testing.T) {
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig("./testdata/bls12_381_bbs_gurvy/idemix")
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(math.BLS12_381_BBS_GURVY, kvs2.Keystore(backend))
	require.NoError(t, err)

	kmp := NewKeyManagerProvider(
		config.Ipk,
		math.BLS12_381_BBS_GURVY,
		keyStore,
		&mockConfig{},
		0,
		false,
		&disabled.Provider{},
	)

	// First get an idConfig from a URL to populate a valid Raw idConfig
	idConfig := &token.IdentityConfiguration{
		ID:  "alice",
		URL: "./testdata/bls12_381_bbs_gurvy/idemix",
	}
	_, err = kmp.Get(context.Background(), idConfig)
	require.NoError(t, err)
	require.NotNil(t, idConfig.Raw)

	// Now get another idConfig using just the Raw config (no URL)
	idConfig2 := &token.IdentityConfiguration{
		ID:  "alice2",
		Raw: idConfig.Raw,
	}
	// get a KeyManager using this idConfig based on just the Raw config
	km2, err := kmp.Get(context.Background(), idConfig2)
	require.NoError(t, err)
	assert.NotNil(t, km2)
}
