/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto/protos-go/config"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test config's ReadFile function
func TestReadFile(t *testing.T) {
	// Create a temporary file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	testContent := []byte("test content")

	err := os.WriteFile(testFile, testContent, 0600)
	require.NoError(t, err)

	t.Run("Valid file", func(t *testing.T) {
		content, err := ReadFile(testFile)
		require.NoError(t, err)
		assert.Equal(t, testContent, content)
	})

	t.Run("Non-existent file", func(t *testing.T) {
		_, err := ReadFile(filepath.Join(tmpDir, "nonexistent.txt"))
		require.Error(t, err)
	})
}

// Test creating a new config from a raw (marshaled) config
func TestNewConfigFromRaw(t *testing.T) {
	issuerPublicKey := []byte("test-issuer-public-key")
	// Test creating a new config from a raw (marshaled) valid config
	t.Run("Valid config", func(t *testing.T) {
		idemixConfig := &config.IdemixConfig{
			Version: ProtobufProtocolVersionV1,
			Ipk:     issuerPublicKey,
			Signer:  nil,
		}

		configRaw, err := proto.Marshal(idemixConfig)
		require.NoError(t, err)

		result, err := NewConfigFromRaw(issuerPublicKey, configRaw)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, ProtobufProtocolVersionV1, result.Version)
		assert.Equal(t, issuerPublicKey, result.Ipk)
	})

	// Test creating a new config from an invalid raw (marshaled) config
	t.Run("Invalid protobuf", func(t *testing.T) {
		_, err := NewConfigFromRaw(issuerPublicKey, []byte("invalid protobuf"))
		require.Error(t, err)
	})

	// Test creating a new config from a raw config with a mismatching public key
	t.Run("Mismatched public key", func(t *testing.T) {
		idemixConfig := &config.IdemixConfig{
			Version: ProtobufProtocolVersionV1,
			Ipk:     []byte("different-key"),
			Signer:  nil,
		}

		configRaw, err := proto.Marshal(idemixConfig)
		require.NoError(t, err)

		_, err = NewConfigFromRaw(issuerPublicKey, configRaw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "public key does not match")
	})

	// Test creating a new config from a raw config with an unsupported protocol version
	t.Run("Unsupported protocol version", func(t *testing.T) {
		idemixConfig := &config.IdemixConfig{
			Version: 999, // Unsupported version
			Ipk:     issuerPublicKey,
			Signer:  nil,
		}

		configRaw, err := proto.Marshal(idemixConfig)
		require.NoError(t, err)

		_, err = NewConfigFromRaw(issuerPublicKey, configRaw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported protocol version")
	})
}

// Test the assembling of a new IdemixConfig from its components
func TestAssembleConfig(t *testing.T) {
	issuerPublicKey := []byte("test-issuer-public-key")

	t.Run("Without signer", func(t *testing.T) {
		result, err := assembleConfig(issuerPublicKey, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, ProtobufProtocolVersionV1, result.Version)
		assert.Equal(t, issuerPublicKey, result.Ipk)
		assert.Nil(t, result.Signer)
	})

	t.Run("With signer", func(t *testing.T) {
		signer := &config.IdemixSignerConfig{
			Cred:                         []byte("credential"),
			Sk:                           []byte("secret-key"),
			OrganizationalUnitIdentifier: "org1",
			Role:                         1,
			EnrollmentId:                 "user1",
		}

		result, err := assembleConfig(issuerPublicKey, signer)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, ProtobufProtocolVersionV1, result.Version)
		assert.Equal(t, issuerPublicKey, result.Ipk)
		assert.Equal(t, signer, result.Signer)
	})
}

// Test the construction of a new IdemixConfig from a given issuerPublicKey
// and a signer present or not in a given folder
func TestNewIdemixConfig(t *testing.T) {
	tmpDir := t.TempDir()
	issuerPublicKey := []byte("test-issuer-public-key")

	t.Run("No signer config", func(t *testing.T) {
		// Create directory structure without signer config
		userDir := filepath.Join(tmpDir, "no-signer", "user")
		err := os.MkdirAll(userDir, 0750)
		require.NoError(t, err)

		result, err := NewIdemixConfig(issuerPublicKey, filepath.Join(tmpDir, "no-signer"), false)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, issuerPublicKey, result.Ipk)
		assert.Nil(t, result.Signer)
	})

	t.Run("With signer config", func(t *testing.T) {
		// Create directory structure with signer config
		userDir := filepath.Join(tmpDir, "with-signer", "user")
		err := os.MkdirAll(userDir, 0750)
		require.NoError(t, err)

		// Create a valid signer config
		signerConfig := &config.IdemixSignerConfig{
			Cred:                         []byte("credential"),
			Sk:                           []byte("secret-key"),
			OrganizationalUnitIdentifier: "org1",
			Role:                         1,
			EnrollmentId:                 "user1",
		}
		signerBytes, err := proto.Marshal(signerConfig)
		require.NoError(t, err)

		signerFile := filepath.Join(userDir, "SignerConfig")
		err = os.WriteFile(signerFile, signerBytes, 0600)
		require.NoError(t, err)

		result, err := NewIdemixConfig(issuerPublicKey, filepath.Join(tmpDir, "with-signer"), false)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, issuerPublicKey, result.Ipk)
		assert.NotNil(t, result.Signer)
		assert.Equal(t, signerConfig.EnrollmentId, result.Signer.EnrollmentId)
	})

	t.Run("With SignerConfigFull", func(t *testing.T) {
		// Create directory structure with SignerConfigFull
		userDir := filepath.Join(tmpDir, "with-full-signer", "user")
		err := os.MkdirAll(userDir, 0750)
		require.NoError(t, err)

		// Create a valid signer config
		signerConfig := &config.IdemixSignerConfig{
			Cred:                         []byte("full-credential"),
			Sk:                           []byte("full-secret-key"),
			OrganizationalUnitIdentifier: "org2",
			Role:                         2,
			EnrollmentId:                 "user2",
		}
		signerBytes, err := proto.Marshal(signerConfig)
		require.NoError(t, err)

		// Write to SignerConfigFull
		signerFile := filepath.Join(userDir, SignerConfigFull)
		err = os.WriteFile(signerFile, signerBytes, 0600)
		require.NoError(t, err)

		result, err := NewIdemixConfig(issuerPublicKey, filepath.Join(tmpDir, "with-full-signer"), true)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, issuerPublicKey, result.Ipk)
		assert.NotNil(t, result.Signer)
		assert.Equal(t, "user2", result.Signer.EnrollmentId)
	})
}

// Test constructing a new Indemix Config based on a (possible) valid/invalid Fabric-CA json file for the signer
func TestNewFabricCAIdemixConfig(t *testing.T) {
	tmpDir := t.TempDir()
	issuerPublicKey := []byte("test-issuer-public-key")

	t.Run("No signer config", func(t *testing.T) {
		// Create directory structure without signer config
		userDir := filepath.Join(tmpDir, "no-ca-signer", ConfigDirUser)
		err := os.MkdirAll(userDir, 0750)
		require.NoError(t, err)

		result, err := NewFabricCAIdemixConfig(issuerPublicKey, filepath.Join(tmpDir, "no-ca-signer"))
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, issuerPublicKey, result.Ipk)
		assert.Nil(t, result.Signer)
	})

	t.Run("With JSON signer config", func(t *testing.T) {
		// Create directory structure with JSON signer config
		userDir := filepath.Join(tmpDir, "with-ca-signer", ConfigDirUser)
		err := os.MkdirAll(userDir, 0750)
		require.NoError(t, err)

		// Create a JSON signer config
		jsonConfig := `{
			"Cred": "Y3JlZGVudGlhbA==",
			"Sk": "c2VjcmV0LWtleQ==",
			"organizational_unit_identifier": "org1",
			"role": 1,
			"enrollment_id": "ca-user1",
			"revocation_handle": "rh1",
			"schema": "schema1"
		}`

		signerFile := filepath.Join(userDir, ConfigFileSigner)
		err = os.WriteFile(signerFile, []byte(jsonConfig), 0600)
		require.NoError(t, err)

		result, err := NewFabricCAIdemixConfig(issuerPublicKey, filepath.Join(tmpDir, "with-ca-signer"))
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, issuerPublicKey, result.Ipk)
		assert.NotNil(t, result.Signer)
		assert.Equal(t, "ca-user1", result.Signer.EnrollmentId)
		assert.Equal(t, "schema1", result.Signer.Schema)
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		// Create directory structure with invalid JSON
		userDir := filepath.Join(tmpDir, "invalid-json", ConfigDirUser)
		err := os.MkdirAll(userDir, 0750)
		require.NoError(t, err)

		signerFile := filepath.Join(userDir, ConfigFileSigner)
		err = os.WriteFile(signerFile, []byte("invalid json"), 0600)
		require.NoError(t, err)

		_, err = NewFabricCAIdemixConfig(issuerPublicKey, filepath.Join(tmpDir, "invalid-json"))
		require.Error(t, err)
	})
}

// Test creating a new Idemix config from a folder with/out the issuer's PK
func TestNewConfig(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("Valid config with IPK file", func(t *testing.T) {
		// Create MSP directory structure
		mspDir := filepath.Join(tmpDir, "valid-msp", "msp")
		userDir := filepath.Join(tmpDir, "valid-msp", "user")
		err := os.MkdirAll(mspDir, 0750)
		require.NoError(t, err)
		err = os.MkdirAll(userDir, 0750)
		require.NoError(t, err)

		// Write issuer public key file
		ipkFile := filepath.Join(mspDir, "IssuerPublicKey")
		ipkContent := []byte("test-issuer-public-key")
		err = os.WriteFile(ipkFile, ipkContent, 0600)
		require.NoError(t, err)

		result, err := NewConfig(filepath.Join(tmpDir, "valid-msp"))
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, ipkContent, result.Ipk)
	})

	t.Run("Missing IPK file", func(t *testing.T) {
		// Create directory without IPK file
		err := os.MkdirAll(filepath.Join(tmpDir, "no-ipk", "msp"), 0750)
		require.NoError(t, err)

		_, err = NewConfig(filepath.Join(tmpDir, "no-ipk"))
		require.Error(t, err)
	})
}

// Test creating a new Idemix config from a folder with a signer in Fabric CA format
func TestNewConfigWithIPK_FallbackPaths(t *testing.T) {
	tmpDir := t.TempDir()
	issuerPublicKey := []byte("test-issuer-public-key")

	t.Run("Fallback to Fabric CA format", func(t *testing.T) {
		// Create Fabric CA style directory
		userDir := filepath.Join(tmpDir, "ca-format", ConfigDirUser)
		err := os.MkdirAll(userDir, 0750)
		require.NoError(t, err)

		// Create JSON signer config (Fabric CA format)
		jsonConfig := `{
			"Cred": "Y3JlZGVudGlhbA==",
			"Sk": "c2VjcmV0LWtleQ==",
			"organizational_unit_identifier": "org1",
			"role": 1,
			"enrollment_id": "user1"
		}`
		signerFile := filepath.Join(userDir, ConfigFileSigner)
		err = os.WriteFile(signerFile, []byte(jsonConfig), 0600)
		require.NoError(t, err)

		result, err := NewConfigWithIPK(issuerPublicKey, filepath.Join(tmpDir, "ca-format"), false)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "user1", result.Signer.EnrollmentId)
	})
}

// Test creating a new Idemix config from a folder with/out an extra msp subdirectory
func TestNewConfigWithIPK(t *testing.T) {
	tmpDir := t.TempDir()
	issuerPublicKey := []byte("test-issuer-public-key")

	t.Run("Valid config without extra path", func(t *testing.T) {
		// Create directory structure
		userDir := filepath.Join(tmpDir, "valid-config", "user")
		err := os.MkdirAll(userDir, 0750)
		require.NoError(t, err)

		result, err := NewConfigWithIPK(issuerPublicKey, filepath.Join(tmpDir, "valid-config"), false)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("Valid config with extra path element", func(t *testing.T) {
		// Create directory structure with msp subdirectory
		userDir := filepath.Join(tmpDir, "with-extra", ExtraPathElement, "user")
		err := os.MkdirAll(userDir, 0750)
		require.NoError(t, err)

		result, err := NewConfigWithIPK(issuerPublicKey, filepath.Join(tmpDir, "with-extra"), false)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})
}

// Test creating a new Idemix config, where NewIdemixConfig fails but then NewFabricCAIdemixConfig succeeds
func TestNewConfigWithIPK_FallbackBehavior(t *testing.T) {
	tmpDir := t.TempDir()
	issuerPublicKey := []byte("test-issuer-public-key")

	t.Run("NewIdemixConfig fails, NewFabricCAIdemixConfig succeeds", func(t *testing.T) {
		// Create only Fabric CA format (no protobuf SignerConfig)
		// This will make NewIdemixConfig fail but NewFabricCAIdemixConfig succeed
		userDir := filepath.Join(tmpDir, "ca-only", ConfigDirUser)
		err := os.MkdirAll(userDir, 0750)
		require.NoError(t, err)

		// Create JSON signer config (Fabric CA format)
		jsonConfig := `{
			"Cred": "Y3JlZGVudGlhbA==",
			"Sk": "c2VjcmV0LWtleQ==",
			"organizational_unit_identifier": "org1",
			"role": 1,
			"enrollment_id": "fallback-user"
		}`
		signerFile := filepath.Join(userDir, ConfigFileSigner)
		err = os.WriteFile(signerFile, []byte(jsonConfig), 0600)
		require.NoError(t, err)

		// This should use the fallback path
		result, err := newConfigWithIPK(issuerPublicKey, filepath.Join(tmpDir, "ca-only"), false)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "fallback-user", result.Signer.EnrollmentId)
	})
}

// Testing creating a new FabricCAIdemixConfig with a folder with a file with no read permissions
func TestNewFabricCAIdemixConfig_ErrorPaths(t *testing.T) {
	tmpDir := t.TempDir()
	issuerPublicKey := []byte("test-issuer-public-key")

	t.Run("ReadFile error that is not IsNotExist", func(t *testing.T) {
		// Create directory structure where we'll put a problematic SignerConfig file
		userDir := filepath.Join(tmpDir, "bad-read", ConfigDirUser)
		err := os.MkdirAll(userDir, 0750)
		require.NoError(t, err)

		// Instead of creating a file, create a DIRECTORY where the file should be
		// so we trigger the !IsNotExist branch in the following call to NewFabricCAIdemixConfig
		signerPath := filepath.Join(userDir, ConfigFileSigner)
		err = os.Mkdir(signerPath, 0750)
		require.NoError(t, err)

		_, err = NewFabricCAIdemixConfig(issuerPublicKey, filepath.Join(tmpDir, "bad-read"))
		require.Error(t, err)

		assert.Contains(t, err.Error(), "failed to read the content of signer config")
	})
}

// Test creating a new Idemix config where both NewIdemixConfig and NewFabricCAIdemixConfig paths fail
// repeated also in an msp subdirectory
func TestNewConfigWithIPK_ErrorPaths(t *testing.T) {
	tmpDir := t.TempDir()
	issuerPublicKey := []byte("test-issuer-public-key")

	t.Run("Both direct and extra path attempts fail", func(t *testing.T) {
		// Create a directory structure where both NewIdemixConfig and NewFabricCAIdemixConfig will fail
		// NewIdemixConfig will fail due to invalid protobuf
		// NewFabricCAIdemixConfig will fail due to invalid JSON
		testDir := filepath.Join(tmpDir, "both-fail")
		userDir := filepath.Join(testDir, "user")
		err := os.MkdirAll(userDir, 0750)
		require.NoError(t, err)

		// Create invalid protobuf for NewIdemixConfig (will try this first)
		signerFile := filepath.Join(userDir, "SignerConfig")
		err = os.WriteFile(signerFile, []byte("invalid protobuf content"), 0600)
		require.NoError(t, err)

		// Create invalid JSON for NewFabricCAIdemixConfig (fallback)
		jsonSignerFile := filepath.Join(userDir, ConfigFileSigner)
		err = os.WriteFile(jsonSignerFile, []byte("invalid json {{{"), 0600)
		require.NoError(t, err)

		// Also create the same structure in the msp subdirectory
		mspUserDir := filepath.Join(testDir, ExtraPathElement, "user")
		err = os.MkdirAll(mspUserDir, 0750)
		require.NoError(t, err)

		mspSignerFile := filepath.Join(mspUserDir, "SignerConfig")
		err = os.WriteFile(mspSignerFile, []byte("invalid protobuf"), 0600)
		require.NoError(t, err)

		mspJsonSignerFile := filepath.Join(mspUserDir, ConfigFileSigner)
		err = os.WriteFile(mspJsonSignerFile, []byte("invalid json {{{"), 0600)
		require.NoError(t, err)

		// This should fail both attempts
		result, err := NewConfigWithIPK(issuerPublicKey, testDir, false)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed reading idemix configuration")
	})
}

// Test creating a new Idemix config where the direct path fails due to unreadable file
// but with an extra valid msp subdirectory that should enable the creation
func TestNewConfigWithIPK_DirectPathFails(t *testing.T) {
	tmpDir := t.TempDir()
	issuerPublicKey := []byte("test-issuer-public-key")

	t.Run("Direct path fails, extra path succeeds", func(t *testing.T) {
		// Create structure where direct path has unreadable file
		// but msp subdirectory has valid config
		testDir := filepath.Join(tmpDir, "direct-fail-extra-ok")

		// Direct path - create unreadable file
		userDir := filepath.Join(testDir, ConfigDirUser)
		err := os.MkdirAll(userDir, 0750)
		require.NoError(t, err)

		signerFile := filepath.Join(userDir, ConfigFileSigner)
		err = os.WriteFile(signerFile, []byte("content"), 0000)
		require.NoError(t, err)

		// Extra path - create valid config
		mspUserDir := filepath.Join(testDir, ExtraPathElement, ConfigDirUser)
		err = os.MkdirAll(mspUserDir, 0750)
		require.NoError(t, err)

		// This should succeed using the extra path
		result, err := NewConfigWithIPK(issuerPublicKey, testDir, false)
		require.NoError(t, err)
		assert.NotNil(t, result)

		// Clean up
		err = os.Chmod(signerFile, 0600)
		require.NoError(t, err)
	})
}
