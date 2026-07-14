/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package x509

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	idrivermock "github.com/LFDT-Panurus/panurus/token/services/identity/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestAuditInfo(t *testing.T) {
	ai := &AuditInfo{
		EID: "user123",
		RH:  []byte("revocation-handle"),
	}

	// test Marshalling/Unmarshalling a go structure into bytes
	t.Run("Bytes and FromBytes", func(t *testing.T) {
		data, err := ai.Bytes()
		require.NoError(t, err)
		assert.NotEmpty(t, data)

		ai2 := &AuditInfo{}
		err = ai2.FromBytes(data)
		require.NoError(t, err)
		assert.Equal(t, ai.EID, ai2.EID)
		assert.Equal(t, ai.RH, ai2.RH)
	})

	// Test extracting the EID from AuditInfo
	t.Run("EnrollmentID", func(t *testing.T) {
		eid := ai.EnrollmentID()
		assert.Equal(t, "user123", eid)
	})

	// Test extracting the RevocationHandle from AuditInfo
	t.Run("RevocationHandle", func(t *testing.T) {
		rh := ai.RevocationHandle()
		assert.Equal(t, "revocation-handle", rh)
	})

	// Test failure to unmarshal AuditInfo from an invalid byte form
	t.Run("FromBytes Invalid JSON", func(t *testing.T) {
		ai2 := &AuditInfo{}
		err := ai2.FromBytes([]byte("invalid json"))
		require.Error(t, err)
	})
}

// Test various valid/invalid paths when deserializing an identity into various targets
func TestIdentityDeserializer(t *testing.T) {
	des := &IdentityDeserializer{}
	ctx := context.Background()

	// Test success in creating an id based on a certificate
	// and then deserializing it to extract a verifier
	t.Run("Valid Identity", func(t *testing.T) {
		certPEM := crypto.GenerateTestCertWithCN(t, "test.example.com")
		verifier, err := des.DeserializeVerifier(ctx, driver.Identity(certPEM))
		require.NoError(t, err)
		assert.NotNil(t, verifier)
	})

	// Test failure in deserializing a verifier from an invalid identity
	t.Run("Invalid Identity", func(t *testing.T) {
		_, err := des.DeserializeVerifier(ctx, driver.Identity([]byte("invalid")))
		require.Error(t, err)
	})
}

func TestAuditMatcherDeserializer(t *testing.T) {
	des := &AuditMatcherDeserializer{}
	ctx := context.Background()

	t.Run("Valid AuditInfo", func(t *testing.T) {
		ai := &AuditInfo{
			EID: "user123",
			RH:  []byte("rh"),
		}
		auditInfoBytes, err := ai.Bytes()
		require.NoError(t, err)

		matcher, err := des.GetAuditInfoMatcher(ctx, nil, auditInfoBytes)
		require.NoError(t, err)
		assert.NotNil(t, matcher)

		// Test the matcher
		certPEM := crypto.GenerateTestCertWithCN(t, "user123")
		err = matcher.Match(ctx, certPEM)
		require.NoError(t, err)
	})

	t.Run("Invalid AuditInfo", func(t *testing.T) {
		_, err := des.GetAuditInfoMatcher(ctx, nil, []byte("invalid"))
		require.Error(t, err)
	})
}

// Test various valid/invalid paths when using an AuditInfoMatcher
func TestAuditInfoMatcher(t *testing.T) {
	ctx := context.Background()

	// Test success when matching an id based on a certificate with a given common name
	// against AuditInfo with the same EID
	t.Run("Matching EnrollmentID", func(t *testing.T) {
		matcher := &AuditInfoMatcher{
			EnrollmentID: "user123",
		}
		certPEM := crypto.GenerateTestCertWithCN(t, "user123")
		err := matcher.Match(ctx, certPEM)
		require.NoError(t, err)
	})

	// Test failure when matching an id based on a certificate with a given common name
	// against AuditInfo with a different EID
	t.Run("Non-Matching EnrollmentID", func(t *testing.T) {
		matcher := &AuditInfoMatcher{
			EnrollmentID: "user123",
		}
		certPEM := crypto.GenerateTestCertWithCN(t, "user456")
		err := matcher.Match(ctx, certPEM)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected [user123], got [user456]")
	})

	// Test failure when matching an invalid id against some AuditInfo
	t.Run("Invalid Certificate", func(t *testing.T) {
		matcher := &AuditInfoMatcher{
			EnrollmentID: "user123",
		}
		err := matcher.Match(ctx, []byte("invalid"))
		require.Error(t, err)
	})
}

// Test valid/invalid deserialization of AuditInfo
func TestAuditInfoDeserializer(t *testing.T) {
	des := &AuditInfoDeserializer{}
	ctx := context.Background()

	// Test success deserializing raw AuditInfo
	t.Run("Valid AuditInfo", func(t *testing.T) {
		ai := &AuditInfo{
			EID: "user123",
			RH:  []byte("rh"),
		}
		auditInfoBytes, err := ai.Bytes()
		require.NoError(t, err)

		deserializedAI, err := des.DeserializeAuditInfo(ctx, nil, auditInfoBytes)
		require.NoError(t, err)
		assert.NotNil(t, deserializedAI)
		assert.Equal(t, "user123", deserializedAI.EnrollmentID())
		assert.Equal(t, "rh", deserializedAI.RevocationHandle())
	})

	// Test failure deserializing invalid raw AuditInfo
	t.Run("Invalid AuditInfo", func(t *testing.T) {
		_, err := des.DeserializeAuditInfo(ctx, nil, []byte("invalid"))
		require.Error(t, err)
	})
}

// Test local Key managers (with private signing keys)
func TestKeyManager_Local(t *testing.T) {
	keyStore := NewKeyStore(kvs.NewTrackedMemory())

	// Test that a key-manager created from a folder with private keys is identified as local/not-remote
	t.Run("Local KeyManager", func(t *testing.T) {
		// ./testdata/msp includes private signing keys
		km, _, err := NewKeyManager("./testdata/msp", nil, keyStore)
		require.NoError(t, err)
		assert.False(t, km.IsRemote())
	})

	// Test that a key-manager created from a folder with a full folder name and with private keys
	// is identified as local/not-remote
	t.Run("Local KeyManager with keystoreFull", func(t *testing.T) {
		// ./testdata/msp2 also includes private signing keys
		km, _, err := NewKeyManagerFromConf(nil, "./testdata/msp2", KeystoreFullFolder, nil, keyStore)
		require.NoError(t, err)
		assert.False(t, km.IsRemote())
	})
}

// Test that the fields of an identity created from a key-manager that uses a valid folder
// are not empty
func TestKeyManager_Identity(t *testing.T) {
	keyStore := NewKeyStore(kvs.NewTrackedMemory())
	km, _, err := NewKeyManager("./testdata/msp", nil, keyStore)
	require.NoError(t, err)

	ctx := context.Background()
	idDesc, err := km.Identity(ctx, nil)
	require.NoError(t, err)
	assert.NotNil(t, idDesc)
	assert.NotEmpty(t, idDesc.Identity)
	assert.NotEmpty(t, idDesc.AuditInfo)

	// Verify the audit info can be deserialized and matches the enrollment ID
	ai := &AuditInfo{}
	err = ai.FromBytes(idDesc.AuditInfo)
	require.NoError(t, err)
	assert.Equal(t, "auditor.org1.example.com", ai.EnrollmentID())
}

// Test that its possible to extract the correct EID using EnrollmentID() of a key-manager
// that uses a valid folder
func TestKeyManager_EnrollmentID(t *testing.T) {
	keyStore := NewKeyStore(kvs.NewTrackedMemory())
	km, _, err := NewKeyManager("./testdata/msp", nil, keyStore)
	require.NoError(t, err)

	eid := km.EnrollmentID()
	assert.Equal(t, "auditor.org1.example.com", eid)
}

// Test that one can deserialize a verifier from a raw certificate from data taken from a valid folder
func TestKeyManager_DeserializeVerifier(t *testing.T) {
	keyStore := NewKeyStore(kvs.NewTrackedMemory())
	km, _, err := NewKeyManager("./testdata/msp", nil, keyStore)
	require.NoError(t, err)

	ctx := context.Background()
	certPEM := crypto.GenerateTestCertWithCN(t, "test")

	verifier, err := km.DeserializeVerifier(ctx, certPEM)
	require.NoError(t, err)
	assert.NotNil(t, verifier)
}

// Test that one can deserialize a signer from a raw certificate from data taken from a valid folder
func TestKeyManager_DeserializeSigner(t *testing.T) {
	keyStore := NewKeyStore(kvs.NewTrackedMemory())
	km, _, err := NewKeyManager("./testdata/msp", nil, keyStore)
	require.NoError(t, err)

	ctx := context.Background()
	idDesc, err := km.Identity(ctx, nil)
	require.NoError(t, err)

	signer, err := km.DeserializeSigner(ctx, idDesc.Identity)
	require.NoError(t, err)
	assert.NotNil(t, signer)

	// Verify the signer can sign and the signature can be verified
	message := []byte("test message")
	signature, err := signer.Sign(message)
	require.NoError(t, err)
	assert.NotEmpty(t, signature)

	// Verify with the verifier
	verifier, err := km.DeserializeVerifier(ctx, idDesc.Identity)
	require.NoError(t, err)
	err = verifier.Verify(message, signature)
	require.NoError(t, err)
}

// Test that one can extract the expected Audit-Info from a raw certificate from data taken from a valid folder
func TestKeyManager_Info(t *testing.T) {
	keyStore := NewKeyStore(kvs.NewTrackedMemory())
	km, _, err := NewKeyManager("./testdata/msp", nil, keyStore)
	require.NoError(t, err)

	ctx := context.Background()
	certPEM := crypto.GenerateTestCertWithCN(t, "test.example.com")

	info, err := km.Info(ctx, certPEM, nil)
	require.NoError(t, err)
	assert.Contains(t, info, "X509:")
	assert.Contains(t, info, "test.example.com")
}

// Test that a key-manager created from a folder with identity information
// is identified as non anonymous
func TestKeyManager_Anonymous(t *testing.T) {
	keyStore := NewKeyStore(kvs.NewTrackedMemory())
	km, _, err := NewKeyManager("./testdata/msp", nil, keyStore)
	require.NoError(t, err)

	assert.False(t, km.Anonymous())
}

// Test that a key-manager created from a given folder returns the expected
// string description
func TestKeyManager_String(t *testing.T) {
	keyStore := NewKeyStore(kvs.NewTrackedMemory())
	km, _, err := NewKeyManager("./testdata/msp", nil, keyStore)
	require.NoError(t, err)

	str := km.String()
	assert.Contains(t, str, "X509 KeyManager")
	assert.Contains(t, str, "auditor.org1.example.com")
}

// Test that a key-manager created from a given folder returns the expected
// identity type (x509)
func TestKeyManager_IdentityType(t *testing.T) {
	keyStore := NewKeyStore(kvs.NewTrackedMemory())
	km, _, err := NewKeyManager("./testdata/msp", nil, keyStore)
	require.NoError(t, err)

	idType := km.IdentityType()
	assert.Equal(t, IdentityType, idType)
}

// Test a key-manager created from a folder with private keys
func TestKeyManager_SigningIdentity(t *testing.T) {
	keyStore := NewKeyStore(kvs.NewTrackedMemory())

	// Test that a key-manager created from a folder with private keys
	// can produce a signing identity
	t.Run("With Signing Capability", func(t *testing.T) {
		km, _, err := NewKeyManager("./testdata/msp", nil, keyStore)
		require.NoError(t, err)

		sID := km.SigningIdentity()
		assert.NotNil(t, sID)
	})

	// Test that a key-manager created from a folder with a full folder name and with private keys
	// can produce a signing identity
	t.Run("Without Signing Capability", func(t *testing.T) {
		// msp2 actually has signing capability via keystoreFull
		km, _, err := NewKeyManagerFromConf(nil, "./testdata/msp2", KeystoreFullFolder, nil, keyStore)
		require.NoError(t, err)

		sID := km.SigningIdentity()
		assert.NotNil(t, sID)
	})
}

// Test creating a KeyStore
func TestNewKeyStore(t *testing.T) {
	kvs := kvs.NewTrackedMemory()
	ks := NewKeyStore(kvs)
	assert.NotNil(t, ks)
}

// Test various paths for getting a KeyManager from a KeyManagerProvider
func TestKeyManagerProvider_Get(t *testing.T) {
	keyStore := NewKeyStore(kvs.NewTrackedMemory())

	// Mock config
	mockConfig := &idrivermock.Config{}
	mockConfig.TranslatePathCalls(func(path string) string {
		return path
	})
	mockConfig.CacheSizeForOwnerIDReturns(-1)

	provider := NewKeyManagerProvider(mockConfig, keyStore, false)

	// Test getting a KeyManager from a KeyManagerProvider
	// using an IdentityConfiguration based on a valid folder with identity data
	t.Run("Valid Identity Config", func(t *testing.T) {
		idConfig := &driver.IdentityConfiguration{
			ID:  "test-id",
			URL: "./testdata/msp",
		}

		km, err := provider.Get(context.Background(), idConfig)
		require.NoError(t, err)
		assert.NotNil(t, km)
		assert.Equal(t, 1, mockConfig.TranslatePathCallCount())
	})

	// Test getting a KeyManager from a KeyManagerProvider
	// using an IdentityConfiguration based on a valid folder with identity data
	// and given valid YAML related options
	t.Run("With YAML Config", func(t *testing.T) {
		mockConfig.TranslatePathCalls(func(path string) string {
			return path
		})

		opts := map[string]any{
			"BCCSP": map[string]any{
				"Default": "SW",
			},
		}
		optsBytes, err := yaml.Marshal(opts)
		require.NoError(t, err)

		idConfig := &driver.IdentityConfiguration{
			ID:     "test-id",
			URL:    "./testdata/msp",
			Config: optsBytes,
		}

		km, err := provider.Get(context.Background(), idConfig)
		require.NoError(t, err)
		assert.NotNil(t, km)
		assert.Equal(t, 2, mockConfig.TranslatePathCallCount())
	})

	// Test failure getting a KeyManager from a KeyManagerProvider
	// using an IdentityConfiguration based on a valid folder with identity data
	// and given invalid YAML related options
	t.Run("Invalid YAML Config", func(t *testing.T) {
		idConfig := &driver.IdentityConfiguration{
			ID:     "test-id",
			URL:    "./testdata/msp",
			Config: []byte("invalid: yaml: ["),
		}

		_, err := provider.Get(context.Background(), idConfig)
		require.Error(t, err)
		assert.Equal(t, 2, mockConfig.TranslatePathCallCount())
	})

	// Test failure getting a KeyManager from a KeyManagerProvider
	// using an IdentityConfiguration based on a non-existent folder
	t.Run("Non-existent Path", func(t *testing.T) {
		idConfig := &driver.IdentityConfiguration{
			ID:  "test-id",
			URL: "./non/existent/path",
		}

		_, err := provider.Get(context.Background(), idConfig)
		require.Error(t, err)
		// Verify expected number of calls (3 total: 2 from previous tests + 1 from this test)
		assert.Equal(t, 3, mockConfig.TranslatePathCallCount())
	})
}

// Test getting the KeyStore path from a KeyManagerProvider
func TestKeyManagerProvider_keyStorePath(t *testing.T) {
	keyStore := NewKeyStore(kvs.NewTrackedMemory())
	mockConfig := &idrivermock.Config{}
	mockConfig.TranslatePathCalls(func(path string) string {
		return path
	})
	mockConfig.CacheSizeForOwnerIDReturns(-1)

	// Test getting the KeyStore path from a KeyManagerProvider set with ignoreVerifyOnlyWallet=false
	// (the default) which returns an empty path, leading to the standard "keyStore" path
	t.Run("IgnoreVerifyOnlyWallet False", func(t *testing.T) {
		provider := NewKeyManagerProvider(mockConfig, keyStore, false)
		path := provider.keyStorePath()
		assert.Empty(t, path)
		// Verify no calls to mock methods (keyStorePath doesn't use the config)
		assert.Equal(t, 0, mockConfig.TranslatePathCallCount())
		assert.Equal(t, 0, mockConfig.CacheSizeForOwnerIDCallCount())
	})

	// Test getting the KeyStore path from a KeyManagerProvider set with ignoreVerifyOnlyWallet=true
	// which returns the "keystoreFull" path (presumably with additional private signing keys)
	t.Run("IgnoreVerifyOnlyWallet True", func(t *testing.T) {
		provider := NewKeyManagerProvider(mockConfig, keyStore, true)
		path := provider.keyStorePath()
		assert.Equal(t, KeystoreFullFolder, path)
		// Verify no additional calls to mock methods
		assert.Equal(t, 0, mockConfig.TranslatePathCallCount())
		assert.Equal(t, 0, mockConfig.CacheSizeForOwnerIDCallCount())
	})
}

// Test error paths when creating a KeyManager
func TestNewKeyManagerFromConf_Errors(t *testing.T) {
	// Test failure when attempting to create a KeyManager without specifying a keyStore
	t.Run("Nil KeyStore", func(t *testing.T) {
		_, _, err := NewKeyManagerFromConf(nil, "./testdata/msp", "", nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no keyStore provided")
	})

	// Test failure when attempting to create a KeyManager using a crypto configuration
	// of an unsupported protocol version
	t.Run("Unsupported Protocol Version", func(t *testing.T) {
		keyStore := NewKeyStore(kvs.NewTrackedMemory())
		// Create a config with invalid version
		conf := &crypto.Config{
			Version: 999,
		}

		_, _, err := NewKeyManagerFromConf(conf, "./testdata/msp", "", nil, keyStore)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported protocol version")
	})
}

func TestNewVerifyingKeyManager(t *testing.T) {
	// Test creating a KeytManager with a configuration based on a folder that doesn't have the privSate key
	t.Run("Load Verifying Only Identity", func(t *testing.T) {
		// Load config from msp1 which doesn't have private key
		conf, err := crypto.LoadConfig("./testdata/msp1", "")
		require.NoError(t, err)

		km, updatedConf, err := newVerifyingKeyManager(conf, nil)
		require.NoError(t, err)
		assert.NotNil(t, km)
		assert.NotNil(t, updatedConf)
		assert.True(t, km.IsRemote())
		assert.Nil(t, km.SigningIdentity())
		// Verify the returned config matches the input config
		assert.Equal(t, conf, updatedConf)
	})

	// Test failure creating a verifying KeyManager based on a config with an invalid signing PK
	t.Run("Invalid Config", func(t *testing.T) {
		conf := &crypto.Config{
			SigningIdentity: &crypto.SigningIdentityInfo{
				PublicSigner: []byte("invalid"),
			},
			CryptoConfig: &crypto.CryptoConfig{
				SignatureHashFamily: "SHA2",
			},
		}

		_, _, err := newVerifyingKeyManager(conf, nil)
		require.Error(t, err)
	})
}

// Test KeyManagerProvider registration
func TestKeyManagerProvider_RegisterProvider(t *testing.T) {
	keyStore := NewKeyStore(kvs.NewTrackedMemory())
	mockConfig := &idrivermock.Config{}
	mockConfig.TranslatePathCalls(func(path string) string {
		return path
	})
	mockConfig.CacheSizeForOwnerIDReturns(-1)

	provider := NewKeyManagerProvider(mockConfig, keyStore, false)

	// Test registering a KeyManagerProvider based on a valid id and marshalled configuration
	t.Run("With Raw Config", func(t *testing.T) {
		// Load a config and marshal it
		conf, err := crypto.LoadConfig("./testdata/msp", "")
		require.NoError(t, err)

		confRaw, err := crypto.MarshalConfig(conf)
		require.NoError(t, err)

		configedId := &idriver.ConfiguredIdentity{
			ID:   "test-with-raw",
			Path: "./testdata/msp",
		}

		idConfig := &driver.IdentityConfiguration{
			ID:  "test-with-raw",
			URL: "./testdata/msp",
			Raw: confRaw,
		}

		km, err := provider.registerProvider(context.Background(), nil, configedId, idConfig)
		require.NoError(t, err)
		assert.NotNil(t, km)
		assert.Equal(t, 1, mockConfig.TranslatePathCallCount())
	})

	// Test registering a KeyManagerProvider based on a valid id and configuration
	// for a directory with an extra path element that includes a valid certificate
	t.Run("With ExtraPathElement", func(t *testing.T) {
		// Create a temp directory structure with msp subdirectory
		dir := t.TempDir()
		mspDir := filepath.Join(dir, "msp")
		err := os.MkdirAll(filepath.Join(mspDir, "signcerts"), 0750)
		require.NoError(t, err)

		// Copy a certificate
		certData, err := os.ReadFile("./testdata/msp/signcerts/auditor.org1.example.com-cert.pem")
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(mspDir, "signcerts", "cert.pem"), certData, 0644) //nolint:gosec // G703: path is constructed from t.TempDir()
		require.NoError(t, err)

		configedId := &idriver.ConfiguredIdentity{
			ID:   "test-extra-path",
			Path: dir,
		}

		idConfig := &driver.IdentityConfiguration{
			ID:  "test-extra-path",
			URL: dir,
		}

		km, err := provider.registerProvider(context.Background(), nil, configedId, idConfig)
		require.NoError(t, err)
		assert.NotNil(t, km)
		// Verify expected number of calls (2 total: 1 from previous test + 1 from this test)
		assert.Equal(t, 2, mockConfig.TranslatePathCallCount())
	})
}

// Test a KeyManager based on a configuration with a private signing key
func TestNewSigningKeyManager(t *testing.T) {
	keyStore := NewKeyStore(kvs.NewTrackedMemory())

	// Test creating a KeyManager based on a configuration with a private signing key
	t.Run("Valid Config", func(t *testing.T) {
		conf, err := crypto.LoadConfig("./testdata/msp", "")
		require.NoError(t, err)

		km, err := newSigningKeyManager(conf, nil, keyStore)
		require.NoError(t, err)
		assert.NotNil(t, km)
		assert.False(t, km.IsRemote())
		assert.NotNil(t, km.SigningIdentity())
		// Verify the enrollment ID matches the expected value
		assert.Equal(t, "auditor.org1.example.com", km.EnrollmentID())
	})

	// Test failure creating a signing KeyManager based on a config with an invalid signing PK
	t.Run("Invalid Config", func(t *testing.T) {
		conf := &crypto.Config{
			SigningIdentity: &crypto.SigningIdentityInfo{
				PublicSigner: []byte("invalid"),
			},
			CryptoConfig: &crypto.CryptoConfig{
				SignatureHashFamily: "SHA2",
			},
		}

		_, err := newSigningKeyManager(conf, nil, keyStore)
		require.Error(t, err)
	})
}

// Test success in creating a KeyManager based on a config with a valid folder
func TestNewKeyManager_WithConfig(t *testing.T) {
	keyStore := NewKeyStore(kvs.NewTrackedMemory())

	t.Run("With Conf Parameter", func(t *testing.T) {
		conf, err := crypto.LoadConfig("./testdata/msp", "")
		require.NoError(t, err)

		km, returnedConf, err := NewKeyManagerFromConf(conf, "./testdata/msp", "", nil, keyStore)
		require.NoError(t, err)
		assert.NotNil(t, km)
		assert.NotNil(t, returnedConf)
		assert.Equal(t, conf, returnedConf)
	})
}

// Test failure paths when creating a KetManager
func TestNewKeyManager_Errors(t *testing.T) {
	// Test failure creating a KeyManage based on a nonexistant path
	t.Run("Invalid Path", func(t *testing.T) {
		keyStore := NewKeyStore(kvs.NewTrackedMemory())
		_, _, err := NewKeyManager("/non/existent/path", nil, keyStore)
		require.Error(t, err)
	})

	// Test failure creating a KeyManage based on an existent path but no KeyStore
	t.Run("Nil KeyStore", func(t *testing.T) {
		_, _, err := NewKeyManager("./testdata/msp", nil, nil)
		require.Error(t, err)
	})
}
