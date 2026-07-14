/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto/csp"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/hyperledger/fabric-lib-go/bccsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This test file tests various identity loading and serialization/deserialization paths

func TestDeserializeVerifier(t *testing.T) {
	certPEM, _ := GenerateTestCert(t)

	// Test success to deserialize a verifier from an identity based on a valid certificate
	t.Run("Success", func(t *testing.T) {
		verifier, err := DeserializeVerifier(driver.Identity(certPEM))
		require.NoError(t, err)
		assert.NotNil(t, verifier)
	})

	// Test failure to deserialize a verifier from an invalid identity
	t.Run("Invalid PEM", func(t *testing.T) {
		_, err := DeserializeVerifier(driver.Identity([]byte("invalid")))
		require.Error(t, err)
	})

	// Test failure to deserialize a verifier from an identity based on an invalid public key
	t.Run("Not a valid public Key", func(t *testing.T) {
		block := &pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: []byte("not a valid key"),
		}
		pemBytes := pem.EncodeToMemory(block)
		_, err := DeserializeVerifier(driver.Identity(pemBytes))
		require.Error(t, err)
	})
}

func TestInfo(t *testing.T) {
	certPEM, _ := GenerateTestCert(t)

	// Test that the info string returned by the Info method for a certicicate returns the
	// expected string information
	t.Run("Success", func(t *testing.T) {
		info, err := Info(certPEM)
		require.NoError(t, err)
		assert.Contains(t, info, "X509:")
		assert.Contains(t, info, "test.example.com")
	})

	// Test failure getting string information when applying Info on an invalid raw certicifate
	t.Run("Invalid Certificate", func(t *testing.T) {
		_, err := Info([]byte("invalid"))
		require.Error(t, err)
	})
}

func TestGetEnrollmentID(t *testing.T) {
	// Test encoding a raw pem certificate with a common name and then decoding it to extract the expected EID
	t.Run("Simple CommonName", func(t *testing.T) {
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		template := x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject: pkix.Name{
				CommonName: "user123",
			},
			NotBefore: time.Now().Add(-time.Hour),
			NotAfter:  time.Now().Add(time.Hour),
		}

		derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
		require.NoError(t, err)

		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

		eid, err := GetEnrollmentID(certPEM)
		require.NoError(t, err)
		assert.Equal(t, "user123", eid)
	})

	// Test encoding a raw pem certificate with a URL common name
	// and then decoding it to extract the expected EID
	t.Run("CommonName With @", func(t *testing.T) {
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		template := x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject: pkix.Name{
				CommonName: "user123@example.com",
			},
			NotBefore: time.Now().Add(-time.Hour),
			NotAfter:  time.Now().Add(time.Hour),
		}

		derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
		require.NoError(t, err)

		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

		eid, err := GetEnrollmentID(certPEM)
		require.NoError(t, err)
		assert.Equal(t, "user123", eid)
	})

	// Test failure ot extract an EID from an invalid raw certificate
	t.Run("Invalid Certificate", func(t *testing.T) {
		_, err := GetEnrollmentID([]byte("invalid"))
		require.Error(t, err)
	})
}

func TestGetRevocationHandle(t *testing.T) {
	// Test that extracting a RH (revocation handler) twice from the same pem certificate
	// returns the same RH
	t.Run("Valid Certificate", func(t *testing.T) {
		certPEM, _ := GenerateTestCert(t)
		rh, err := GetRevocationHandle(certPEM)
		require.NoError(t, err)
		assert.NotEmpty(t, rh)

		// Verify the revocation handle is consistent
		rh2, err := GetRevocationHandle(certPEM)
		require.NoError(t, err)
		assert.Equal(t, rh, rh2, "Revocation handle should be deterministic")
	})

	// Test failure to extract a RH from an invalid certificate
	t.Run("Invalid Certificate", func(t *testing.T) {
		_, err := GetRevocationHandle([]byte("invalid"))
		require.Error(t, err)
	})
}

func TestLoadConfig(t *testing.T) {
	// Test success loading a config from a valid msp folder
	t.Run("Valid MSP Directory", func(t *testing.T) {
		config, err := LoadConfig("../testdata/msp", "")
		require.NoError(t, err)
		assert.NotNil(t, config)
		assert.Equal(t, ProtobufProtocolVersionV1, config.Version)
		assert.NotNil(t, config.SigningIdentity)
		assert.NotEmpty(t, config.SigningIdentity.PublicSigner)
	})

	// Test success loading a config from a valid msp folder with a path suffix for the KeyStore
	t.Run("Custom KeyStore Directory", func(t *testing.T) {
		config, err := LoadConfig("../testdata/msp2", "keystoreFull")
		require.NoError(t, err)
		assert.NotNil(t, config)
		assert.NotNil(t, config.SigningIdentity.PrivateSigner)
	})

	// Test failure loading a config from a non existent folder
	t.Run("Non-existent Directory", func(t *testing.T) {
		_, err := LoadConfig("/non/existent/path", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not load a valid signer certificate")
	})

	// Test failure loading a config from a folder that doesn't include an expected signer certificate
	t.Run("Directory Without Certificates", func(t *testing.T) {
		dir := t.TempDir()
		signcertDir := filepath.Join(dir, SignCertsDirName)
		err := os.MkdirAll(signcertDir, 0o750)
		require.NoError(t, err)

		_, err = LoadConfig(dir, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no signer certificate found")
	})
}

func TestLoadConfigWithIdentityInfo(t *testing.T) {
	certPEM, _ := GenerateTestCert(t)

	// Test that a config based on a signing-id has the expected fields
	t.Run("Valid Identity Info", func(t *testing.T) {
		info := &SigningIdentityInfo{
			PublicSigner: certPEM,
			PrivateSigner: &KeyInfo{
				KeyIdentifier: "test-key",
				KeyMaterial:   []byte("key-material"),
			},
		}

		config, err := LoadConfigWithIdentityInfo(info)
		require.NoError(t, err)
		assert.NotNil(t, config)
		assert.Equal(t, ProtobufProtocolVersionV1, config.Version)
		assert.Equal(t, bccsp.SHA2, config.CryptoConfig.SignatureHashFamily)
	})
}

// Test removing private signer information from an identity config
func TestRemovePrivateSigner(t *testing.T) {
	certPEM, _ := GenerateTestCert(t)
	config := &Config{
		Version: ProtobufProtocolVersionV1,
		SigningIdentity: &SigningIdentityInfo{
			PublicSigner: certPEM,
			PrivateSigner: &KeyInfo{
				KeyIdentifier: "test",
				KeyMaterial:   []byte("material"),
			},
		},
	}

	result, err := RemovePrivateSigner(config)
	require.NoError(t, err)
	assert.Nil(t, result.SigningIdentity.PrivateSigner)
}

func TestSerializeIdentity(t *testing.T) {
	// Test loading an id config from a valid folder, serializing it and decoding it back to
	// a valid ceritifcate and the expected EID
	t.Run("Valid Config", func(t *testing.T) {
		config, err := LoadConfig("../testdata/msp", "")
		require.NoError(t, err)

		serialized, err := SerializeIdentity(config)
		require.NoError(t, err)
		assert.NotEmpty(t, serialized)

		// Verify the serialized identity is a valid PEM certificate
		cert, err := PemDecodeCert(serialized)
		require.NoError(t, err)
		assert.NotNil(t, cert)

		// Verify the enrollment ID matches
		eid, err := GetEnrollmentID(serialized)
		require.NoError(t, err)
		assert.Equal(t, "auditor.org1.example.com", eid)
	})

	// Test failure to serialize an id config that includes an invalid signing id
	t.Run("Invalid Config", func(t *testing.T) {
		config := &Config{
			SigningIdentity: &SigningIdentityInfo{
				PublicSigner: []byte("invalid"),
			},
			CryptoConfig: &CryptoConfig{
				SignatureHashFamily: bccsp.SHA2,
			},
		}

		_, err := SerializeIdentity(config)
		require.Error(t, err)
	})
}

func TestGetSigningIdentity(t *testing.T) {
	keyStore := csp.NewKVSStore(kvs.NewTrackedMemory())

	// Test getting a signing id from a valid folder
	// and then using that id to sign and verify a message
	t.Run("Valid Config", func(t *testing.T) {
		config, err := LoadConfig("../testdata/msp", "")
		require.NoError(t, err)

		identity, err := GetSigningIdentity(config, nil, keyStore)
		require.NoError(t, err)
		assert.NotNil(t, identity)

		// Test signing
		msg := []byte("test message")
		sig, err := identity.Sign(msg)
		require.NoError(t, err)
		assert.NotEmpty(t, sig)

		// Test verification
		err = identity.Verify(msg, sig)
		require.NoError(t, err)
	})

	// Test failure to get a signing id from an id config with an invalid signing id
	t.Run("Invalid Config", func(t *testing.T) {
		config := &Config{
			SigningIdentity: &SigningIdentityInfo{
				PublicSigner: []byte("invalid"),
			},
			CryptoConfig: &CryptoConfig{
				SignatureHashFamily: bccsp.SHA2,
			},
		}

		_, err := GetSigningIdentity(config, nil, keyStore)
		require.Error(t, err)
	})
}

func TestDeserializeIdentity(t *testing.T) {
	keyStore := csp.NewKVSStore(kvs.NewTrackedMemory())

	// Test getting a signing id from a valid folder
	// the serializing and deserializing it into an equal signing id
	// and then using that id to sign and verify a message
	t.Run("Valid Identity", func(t *testing.T) {
		// First, get a valid signing identity
		config, err := LoadConfig("../testdata/msp", "")
		require.NoError(t, err)

		origIdentity, err := GetSigningIdentity(config, nil, keyStore)
		require.NoError(t, err)

		// Serialize it
		serialized, err := origIdentity.Serialize()
		require.NoError(t, err)

		// Deserialize it
		identity, err := DeserializeIdentity(serialized, nil, keyStore)
		require.NoError(t, err)
		assert.NotNil(t, identity)

		// Test that it can sign
		msg := []byte("test message")
		sig, err := identity.Sign(msg)
		require.NoError(t, err)
		assert.NotEmpty(t, sig)

		// Verify the signature with the original identity
		err = origIdentity.Verify(msg, sig)
		require.NoError(t, err)

		// Verify the deserialized identity can also verify
		err = identity.Verify(msg, sig)
		require.NoError(t, err)
	})

	// Test failure to deserialize an invalid raw identity
	t.Run("Invalid Identity", func(t *testing.T) {
		_, err := DeserializeIdentity([]byte("invalid"), nil, keyStore)
		require.Error(t, err)
	})
}

func TestIdentityFactory(t *testing.T) {
	keyStore := csp.NewKVSStore(kvs.NewTrackedMemory())
	cspInstance, err := GetDefaultBCCSP(keyStore)
	require.NoError(t, err)

	factory := NewIdentityFactory(cspInstance, bccsp.SHA2)
	assert.NotNil(t, factory)

	// Test getting a full (sign+verify) id from a valid configuration folder
	// and using the id to sign and verify
	t.Run("GetFullIdentity", func(t *testing.T) {
		config, err := LoadConfig("../testdata/msp", "")
		require.NoError(t, err)

		identity, err := factory.GetFullIdentity(config.SigningIdentity)
		require.NoError(t, err)
		assert.NotNil(t, identity)

		// Test signing and verification
		msg := []byte("test")
		sig, err := identity.Sign(msg)
		require.NoError(t, err)
		err = identity.Verify(msg, sig)
		require.NoError(t, err)
	})

	// Test getting a verify-only id from a valid configuration folder
	// that doesn't include a private signing key
	t.Run("GetIdentity", func(t *testing.T) {
		config, err := LoadConfig("../testdata/msp1", "")
		require.NoError(t, err)

		identity, err := factory.GetIdentity(config.SigningIdentity)
		require.NoError(t, err)
		assert.NotNil(t, identity)
	})

	// Test getting a full (sign+verify) id from a valid configuration folder,
	// the serializing and deserialing it into an equal id
	// and finally using the deserialized if to sign and verify
	t.Run("DeserializeFullIdentity", func(t *testing.T) {
		config, err := LoadConfig("../testdata/msp", "")
		require.NoError(t, err)

		origIdentity, err := factory.GetFullIdentity(config.SigningIdentity)
		require.NoError(t, err)

		serialized, err := origIdentity.Serialize()
		require.NoError(t, err)

		identity, err := factory.DeserializeFullIdentity(serialized)
		require.NoError(t, err)
		assert.NotNil(t, identity)

		// Verify the deserialized identity can sign and verify
		msg := []byte("test message")
		sig, err := identity.Sign(msg)
		require.NoError(t, err)
		assert.NotEmpty(t, sig)

		err = identity.Verify(msg, sig)
		require.NoError(t, err)

		// Verify the signature is compatible with the original identity
		err = origIdentity.Verify(msg, sig)
		require.NoError(t, err)
	})
}

// Test using a verifying id to verify a signature
func TestVerifyingIdentity(t *testing.T) {
	keyStore := csp.NewKVSStore(kvs.NewTrackedMemory())
	cspInstance, err := GetDefaultBCCSP(keyStore)
	require.NoError(t, err)

	factory := NewIdentityFactory(cspInstance, bccsp.SHA2)

	// msp1 includes just a public verification key
	config, err := LoadConfig("../testdata/msp1", "")
	require.NoError(t, err)

	// msp1 includes just a public verification key
	// so PrivateSigner.KeyMaterial is nil and can't be used for signing
	identity, err := factory.GetIdentity(config.SigningIdentity)
	require.NoError(t, err)

	// Test serializing the verify-only id created via an id factory
	t.Run("Serialize", func(t *testing.T) {
		serialized, err := identity.Serialize()
		require.NoError(t, err)
		assert.NotEmpty(t, serialized)
	})

	// Load a full (sign+verify) id config from a valid folder via an id factory
	// and then using that id to sign and verify
	t.Run("Verify Valid Signature", func(t *testing.T) {
		// Get a full identity to create a signature
		fullConfig, err := LoadConfig("../testdata/msp", "")
		require.NoError(t, err)

		fullIdentity, err := factory.GetFullIdentity(fullConfig.SigningIdentity)
		require.NoError(t, err)

		msg := []byte("test message for verification")
		sig, err := fullIdentity.Sign(msg)
		require.NoError(t, err)

		// Now verify with the verifying identity
		err = identity.Verify(msg, sig)
		require.NoError(t, err)
	})

	// Test failure verifying an invalid signature
	t.Run("Verify Invalid Signature", func(t *testing.T) {
		msg := []byte("test message")
		invalidSig := []byte("invalid signature")

		err := identity.Verify(msg, invalidSig)
		require.Error(t, err)
	})
}

func TestFullIdentity_Verify(t *testing.T) {
	keyStore := csp.NewKVSStore(kvs.NewTrackedMemory())
	cspInstance, err := GetDefaultBCCSP(keyStore)
	require.NoError(t, err)

	factory := NewIdentityFactory(cspInstance, bccsp.SHA2)
	config, err := LoadConfig("../testdata/msp", "")
	require.NoError(t, err)

	identity, err := factory.GetFullIdentity(config.SigningIdentity)
	require.NoError(t, err)

	// Use a full id (sign+verify) to both sign a message and verify the resulting signature
	t.Run("Verify Own Signature", func(t *testing.T) {
		msg := []byte("test message")
		sig, err := identity.Sign(msg)
		require.NoError(t, err)

		err = identity.Verify(msg, sig)
		require.NoError(t, err)
	})

	// Test failure of using a verifying id to verify an invalid signature
	t.Run("Verify Invalid Signature", func(t *testing.T) {
		msg := []byte("test message")
		err := identity.Verify(msg, []byte("invalid"))
		require.Error(t, err)
	})

	// Test failure of using a verifying id to verify a valid signature but not
	// with the original signed message
	t.Run("Verify Wrong Message", func(t *testing.T) {
		msg := []byte("test message")
		sig, err := identity.Sign(msg)
		require.NoError(t, err)

		err = identity.Verify([]byte("wrong message"), sig)
		require.Error(t, err)
	})
}

// Encode a valid certificate with a URL common name
// and then decode the EID from it and compare with the expected local-part of the URL
func TestGetEnrollmentID_WithAt(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "user@domain.com",
		},
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(time.Hour),
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	eid, err := GetEnrollmentID(certPEM)
	require.NoError(t, err)
	assert.Equal(t, "user", eid)
}

func TestVerifyingIdentity_Serialize(t *testing.T) {
	// Test serializing a verifying-id based on a valid certificate
	// and then decoding back the certificate from the id and comparing it with the original
	t.Run("Serialize With Valid Cert", func(t *testing.T) {
		// Even with empty Raw bytes, pem.EncodeToMemory doesn't fail
		// It just encodes an empty certificate
		certPEM, _ := GenerateTestCert(t)
		cert, err := PemDecodeCert(certPEM)
		require.NoError(t, err)

		vi := &verifyingIdentity{
			cert: cert,
		}

		// This should succeed
		serialized, err := vi.Serialize()
		require.NoError(t, err)
		assert.NotEmpty(t, serialized)

		// Verify the serialized data can be decoded back to a certificate
		decodedCert, err := PemDecodeCert(serialized)
		require.NoError(t, err)
		assert.NotNil(t, decodedCert)

		// Verify the certificate matches the original
		assert.Equal(t, cert.Raw, decodedCert.Raw)
	})
}

func TestFullIdentity_SignError(t *testing.T) {
	// Test failure to sign with a full (signing) id configured with an invalid hash family
	t.Run("Sign With Invalid Hash Family", func(t *testing.T) {
		csp, err := GetDefaultBCCSP(nil)
		require.NoError(t, err)

		// Generate a key
		key, err := csp.KeyGen(&bccsp.ECDSAP256KeyGenOpts{Temporary: true})
		require.NoError(t, err)

		certPEM, priv := GenerateTestCert(t)
		cert, err := PemDecodeCert(certPEM)
		require.NoError(t, err)

		signer, err := NewSKIBasedSigner(csp, key.SKI(), &priv.PublicKey)
		require.NoError(t, err)

		fi := &fullIdentity{
			verifyingIdentity: &verifyingIdentity{
				bccsp:               csp,
				SignatureHashFamily: "INVALID",
				cert:                cert,
				pk:                  key,
			},
			signer: signer,
		}

		_, err = fi.Sign([]byte("test message"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "hash")
	})
}

func TestFullIdentity_VerifyError(t *testing.T) {
	// Test failure to veirfy with a full (sign+verify) id configured with an invalid hash family
	t.Run("Verify With Invalid Hash Family", func(t *testing.T) {
		csp, err := GetDefaultBCCSP(nil)
		require.NoError(t, err)

		key, err := csp.KeyGen(&bccsp.ECDSAP256KeyGenOpts{Temporary: true})
		require.NoError(t, err)

		certPEM, _ := GenerateTestCert(t)
		cert, err := PemDecodeCert(certPEM)
		require.NoError(t, err)

		fi := &fullIdentity{
			verifyingIdentity: &verifyingIdentity{
				bccsp:               csp,
				SignatureHashFamily: "INVALID",
				cert:                cert,
				pk:                  key,
			},
		}

		err = fi.Verify([]byte("message"), []byte("signature"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "hash")
	})
}

func TestIdentityFactory_GetFullIdentity_Errors(t *testing.T) {
	csp, err := GetDefaultBCCSP(nil)
	require.NoError(t, err)

	factory := NewIdentityFactory(csp, bccsp.SHA2)

	// Test failure to create a full (signing) id without providing a SigningIdentityInfo
	t.Run("Nil SigningIdentityInfo", func(t *testing.T) {
		_, err := factory.GetFullIdentity(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil")
	})

	// Test failure to create a full (signing) id based on SigningIdentityInfo with an invalid certificate
	t.Run("Invalid Certificate", func(t *testing.T) {
		sidInfo := &SigningIdentityInfo{
			PublicSigner: []byte("invalid cert"),
		}
		_, err := factory.GetFullIdentity(sidInfo)
		require.Error(t, err)
	})

	// Test failure to create a full (signing) id based on SigningIdentityInfo with no key material
	t.Run("Missing Private Key Material", func(t *testing.T) {
		certPEM, _ := GenerateTestCert(t)

		sidInfo := &SigningIdentityInfo{
			PublicSigner: certPEM,
			PrivateSigner: &KeyInfo{
				KeyIdentifier: "test",
				KeyMaterial:   nil, // No key material
			},
		}

		_, err := factory.GetFullIdentity(sidInfo)
		require.Error(t, err)
	})

	// Test failure to create a full (signing) id based on SigningIdentityInfo with invalid pem key material
	t.Run("Invalid PEM Private Key", func(t *testing.T) {
		certPEM, _ := GenerateTestCert(t)

		sidInfo := &SigningIdentityInfo{
			PublicSigner: certPEM,
			PrivateSigner: &KeyInfo{
				KeyIdentifier: "test",
				KeyMaterial:   []byte("not a pem key"),
			},
		}

		_, err := factory.GetFullIdentity(sidInfo)
		require.Error(t, err)
	})
}

func TestIdentityFactory_DeserializeFullIdentity_Errors(t *testing.T) {
	csp, err := GetDefaultBCCSP(nil)
	require.NoError(t, err)

	factory := NewIdentityFactory(csp, bccsp.SHA2)

	// Test failure to deserialize empty raw full id data
	t.Run("Empty Identity", func(t *testing.T) {
		_, err := factory.DeserializeFullIdentity([]byte{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil")
	})

	// Test failure to deserialize a raw full id from a certificate
	// using a factory that was setup with an empty keystore
	// and which was never updated with the queried SKI
	t.Run("Key Not Found in KeyStore", func(t *testing.T) {
		certPEM, _ := GenerateTestCert(t)

		_, err := factory.DeserializeFullIdentity(certPEM)
		require.Error(t, err)
	})
}

func TestIdentityFactory_GetIdentity_Errors(t *testing.T) {
	csp, err := GetDefaultBCCSP(nil)
	require.NoError(t, err)

	factory := NewIdentityFactory(csp, bccsp.SHA2)

	// Test failure to get an identity when no SigningIdentityInfo is provided
	t.Run("Nil SigningIdentityInfo", func(t *testing.T) {
		_, err := factory.GetIdentity(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil")
	})

	// Test failure to get an identity from a SigningIdentityInfo with invalid signer info
	t.Run("Invalid Certificate", func(t *testing.T) {
		sidInfo := &SigningIdentityInfo{
			PublicSigner: []byte("invalid"),
		}
		_, err := factory.GetIdentity(sidInfo)
		require.Error(t, err)
	})
}

func TestGetIdentityFromConf_Errors(t *testing.T) {
	csp, err := GetDefaultBCCSP(nil)
	require.NoError(t, err)

	factory := NewIdentityFactory(csp, bccsp.SHA2)

	// Test failure gettign an id from an invalid raw pem configuration
	t.Run("Invalid PEM", func(t *testing.T) {
		_, _, _, err := factory.getIdentityFromConf([]byte("not a pem"))
		require.Error(t, err)
	})
}

func TestGetCertFromPem_Errors(t *testing.T) {
	csp, err := GetDefaultBCCSP(nil)
	require.NoError(t, err)

	factory := NewIdentityFactory(csp, bccsp.SHA2)

	// Test failure to get a certificate when the raw pem id is nil
	t.Run("Nil Bytes", func(t *testing.T) {
		_, err := factory.getCertFromPem(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil")
	})

	// Test failure to get a certificate when the raw pem id is invalid
	t.Run("Invalid PEM", func(t *testing.T) {
		_, err := factory.getCertFromPem([]byte("not pem"))
		require.Error(t, err)
	})

	// Test failure to get a certificate when the raw pem certificate is invalid
	t.Run("Invalid Certificate", func(t *testing.T) {
		invalidPEM := pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: []byte("invalid cert bytes"),
		})
		_, err := factory.getCertFromPem(invalidPEM)
		require.Error(t, err)
	})
}
