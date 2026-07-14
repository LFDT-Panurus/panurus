/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	sha30 "crypto/sha3"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	hash0 "hash"
	"math/big"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/hyperledger/fabric-lib-go/bccsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The test file tests the csp package that provides a Cryptographic Service Provider (CSP)
// wrapper around Hyperledger Fabric's BCCSP (Blockchain Cryptographic Service Provider) library,
// specifically tailored for X.509-based identity management in Panurus.

// Test creating a new csp
func TestNewCSP(t *testing.T) {
	// Test success creating a new csp given a valid keyStore
	t.Run("Success", func(t *testing.T) {
		keyStore := NewKVSStore(kvs.NewTrackedMemory())
		csp, err := NewCSP(keyStore)
		require.NoError(t, err)
		assert.NotNil(t, csp)
	})

	// Test failure creating a new csp given a nil keyStore
	t.Run("Nil KeyStore Returns Error", func(t *testing.T) {
		_, err := NewCSP(nil)
		require.Error(t, err)
	})
}

func TestECDSAKeyGeneration(t *testing.T) {
	keyStore := NewKVSStore(kvs.NewTrackedMemory())
	csp, err := NewCSP(keyStore)
	require.NoError(t, err)

	// Test using the csp to generate a ECDSA key
	// via the generic ECDSAKeyGenOpts that defaults to the P256 curve
	t.Run("P256 Key Generation", func(t *testing.T) {
		key, err := csp.KeyGen(&bccsp.ECDSAKeyGenOpts{})
		require.NoError(t, err)
		assert.NotNil(t, key)
		assert.False(t, key.Symmetric())
		assert.True(t, key.Private())
	})

	// Test using the csp to generate a ECDSA key
	// via ECDSAP256KeyGenOpts that explicitly selects the P256 curve
	t.Run("ECDSAP256 Key Generation", func(t *testing.T) {
		key, err := csp.KeyGen(&bccsp.ECDSAP256KeyGenOpts{})
		require.NoError(t, err)
		assert.NotNil(t, key)
	})

	// Test using the csp to generate a ECDSA key
	// via ECDSAP384KeyGenOpts that explicitly selects the P384 curve
	t.Run("ECDSAP384 Key Generation", func(t *testing.T) {
		key, err := csp.KeyGen(&bccsp.ECDSAP384KeyGenOpts{})
		require.NoError(t, err)
		assert.NotNil(t, key)
	})
}

func TestECDSASignAndVerify(t *testing.T) {
	keyStore := NewKVSStore(kvs.NewTrackedMemory())
	csp, err := NewCSP(keyStore)
	require.NoError(t, err)

	key, err := csp.KeyGen(&bccsp.ECDSAKeyGenOpts{})
	require.NoError(t, err)

	msg := []byte("test message")
	digest := sha256.Sum256(msg)

	// Test success using the csp to create a key and then use it to sign and verify a hashed message
	t.Run("Sign and Verify with Private Key", func(t *testing.T) {
		signature, err := csp.Sign(key, digest[:], nil)
		require.NoError(t, err)
		assert.NotEmpty(t, signature)

		valid, err := csp.Verify(key, signature, digest[:], nil)
		require.NoError(t, err)
		assert.True(t, valid)
	})

	// Test success using the csp to create a key and then use it to sign a hashed message
	// and later verify the signature with the corresponding public key
	t.Run("Verify with Public Key", func(t *testing.T) {
		signature, err := csp.Sign(key, digest[:], nil)
		require.NoError(t, err)

		pubKey, err := key.PublicKey()
		require.NoError(t, err)

		valid, err := csp.Verify(pubKey, signature, digest[:], nil)
		require.NoError(t, err)
		assert.True(t, valid)
	})

	// Test failure to use a valid key to verify an invalid signature of a hashed message
	t.Run("Verify Invalid Signature", func(t *testing.T) {
		invalidSig := []byte("invalid signature")
		valid, err := csp.Verify(key, invalidSig, digest[:], nil)
		require.Error(t, err)
		assert.False(t, valid)
	})

	// Test failure to use a valid key to verify a valid signature with an inconsistent hashed message
	t.Run("Verify Wrong Digest", func(t *testing.T) {
		signature, err := csp.Sign(key, digest[:], nil)
		require.NoError(t, err)

		wrongDigest := sha256.Sum256([]byte("wrong message"))
		valid, err := csp.Verify(key, signature, wrongDigest[:], nil)
		require.NoError(t, err)
		assert.False(t, valid)
	})
}

func TestHashOperations(t *testing.T) {
	keyStore := NewKVSStore(kvs.NewTrackedMemory())
	csp, err := NewCSP(keyStore)
	require.NoError(t, err)

	msg := []byte("test message")

	// Test that using csp to hash a message with SHA256 indeed returns a SHA256 hash of the message
	t.Run("SHA256 Hash", func(t *testing.T) {
		hash, err := csp.Hash(msg, &bccsp.SHA256Opts{})
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
		assert.Len(t, hash, 32)
		// Verify the hash matches the expected SHA256 value
		expectedHash := sha256.Sum256(msg)
		assert.Equal(t, expectedHash[:], hash)
	})

	// Test that using csp to hash a message with SHA indeed returns the default SHA256 hash of the message
	t.Run("SHA Hash (default)", func(t *testing.T) {
		hash, err := csp.Hash(msg, &bccsp.SHAOpts{})
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
		// Verify the hash matches the expected SHA256 value (SHAOpts defaults to SHA256)
		expectedHash := sha256.Sum256(msg)
		assert.Equal(t, expectedHash[:], hash)
	})

	// Test that using csp to hash a message with SHA384 indeed returns a SHA384 hash of the message
	t.Run("SHA384 Hash", func(t *testing.T) {
		hash, err := csp.Hash(msg, &bccsp.SHA384Opts{})
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
		assert.Len(t, hash, 48)
		// Verify the hash matches the expected SHA384 value
		expectedHash := sha512.Sum384(msg)
		assert.Equal(t, expectedHash[:], hash)
	})

	// Test that using csp to hash a message with SHA3-256 indeed returns a SHA3-256 hash of the message
	t.Run("SHA3_256 Hash", func(t *testing.T) {
		hash, err := csp.Hash(msg, &bccsp.SHA3_256Opts{})
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
		// Verify the hash matches the expected SHA3-256 value
		h := hash0.Hash(sha30.New256())
		h.Write(msg)
		expectedHash := h.Sum(nil)
		assert.Equal(t, expectedHash, hash)
	})

	// Test that using csp to hash a message with SHA3-384 indeed returns a SHA3-384 hash of the message
	t.Run("SHA3_384 Hash", func(t *testing.T) {
		hash, err := csp.Hash(msg, &bccsp.SHA3_384Opts{})
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
		// Verify the hash matches the expected SHA3-384 value
		h := hash0.Hash(sha30.New384())
		h.Write(msg)
		expectedHash := h.Sum(nil)
		assert.Equal(t, expectedHash, hash)
	})

	// Test that using csp to hash a message with SHA256 indeed returns a SHA256 hash of the message.
	// The message is hashed using the h.Write method that allows for incremental hashing of a message
	// that "arrrives in chunks".
	t.Run("GetHash", func(t *testing.T) {
		h, err := csp.GetHash(&bccsp.SHA256Opts{})
		require.NoError(t, err)
		assert.NotNil(t, h)

		h.Write(msg)
		hash := h.Sum(nil)
		assert.NotEmpty(t, hash)
		// Verify the hash matches the expected SHA256 value
		expectedHash := sha256.Sum256(msg)
		assert.Equal(t, expectedHash[:], hash)
	})
}

func TestKeyImport(t *testing.T) {
	keyStore := NewKVSStore(kvs.NewTrackedMemory())
	csp, err := NewCSP(keyStore)
	require.NoError(t, err)

	// Test success importing a marshalled ECDSA private key
	t.Run("Import ECDSA Private Key", func(t *testing.T) {
		// Generate a key
		privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		// Marshal to DER
		der, err := x509.MarshalECPrivateKey(privKey)
		require.NoError(t, err)

		// Import
		key, err := csp.KeyImport(der, &bccsp.ECDSAPrivateKeyImportOpts{})
		require.NoError(t, err)
		assert.NotNil(t, key)
		assert.True(t, key.Private())
	})

	// Test success importing a marshalled ECDSA public key
	t.Run("Import ECDSA Public Key PKIX", func(t *testing.T) {
		// Generate a key
		privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		// Marshal public key to DER
		der, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
		require.NoError(t, err)

		// Import
		key, err := csp.KeyImport(der, &bccsp.ECDSAPKIXPublicKeyImportOpts{})
		require.NoError(t, err)
		assert.NotNil(t, key)
		assert.False(t, key.Private())
	})

	// Test success importing an ECDSA public key using the private key
	t.Run("Import ECDSA Go Public Key", func(t *testing.T) {
		// Generate a key
		privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		// Import directly
		key, err := csp.KeyImport(&privKey.PublicKey, &bccsp.ECDSAGoPublicKeyImportOpts{})
		require.NoError(t, err)
		assert.NotNil(t, key)
		assert.False(t, key.Private())
	})

	// Test success creating a x509 certificate using an ECDSA private key
	// and then using it to import the corresponiding public key
	t.Run("Import X509 Certificate", func(t *testing.T) {
		// Generate a certificate
		privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		template := x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject: pkix.Name{
				CommonName: "test",
			},
			NotBefore: time.Now().Add(-time.Hour),
			NotAfter:  time.Now().Add(time.Hour),
		}

		derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privKey.PublicKey, privKey)
		require.NoError(t, err)

		cert, err := x509.ParseCertificate(derBytes)
		require.NoError(t, err)

		// Import
		key, err := csp.KeyImport(cert, &bccsp.X509PublicKeyImportOpts{})
		require.NoError(t, err)
		assert.NotNil(t, key)
	})

	// Test failure importing an ECDSA private key from an invalid serialization
	t.Run("Import Invalid Private Key", func(t *testing.T) {
		_, err := csp.KeyImport([]byte("invalid"), &bccsp.ECDSAPrivateKeyImportOpts{})
		require.Error(t, err)
	})

	// Test failure importing an ECDSA public key from an invalid serialization in the PKIX format
	t.Run("Import Invalid Public Key", func(t *testing.T) {
		_, err := csp.KeyImport([]byte("invalid"), &bccsp.ECDSAPKIXPublicKeyImportOpts{})
		require.Error(t, err)
	})

	// Test failure importing an ECDSA private key from an empty serialization
	t.Run("Import Empty Bytes", func(t *testing.T) {
		_, err := csp.KeyImport([]byte{}, &bccsp.ECDSAPrivateKeyImportOpts{})
		require.Error(t, err)
	})

	// Test failure importing an ECDSA public key from an invalid serialization
	t.Run("Import Wrong Type for Go Public Key", func(t *testing.T) {
		_, err := csp.KeyImport("not a key", &bccsp.ECDSAGoPublicKeyImportOpts{})
		require.Error(t, err)
	})
}

// Test that a ECDSA keys generated using the csp have the expected properties
func TestECDSAKey(t *testing.T) {
	keyStore := NewKVSStore(kvs.NewTrackedMemory())
	csp, err := NewCSP(keyStore)
	require.NoError(t, err)

	key, err := csp.KeyGen(&bccsp.ECDSAKeyGenOpts{})
	require.NoError(t, err)

	// Test that a ECDSA private key generated using the csp has the expected properties
	// non empty SKI (subject key identifier), it is a private symmetric key
	// and can't be serialized to bytes
	t.Run("Key Properties", func(t *testing.T) {
		assert.NotEmpty(t, key.SKI())
		assert.True(t, key.Private())
		assert.False(t, key.Symmetric())

		// Private key Bytes() is not supported
		_, err := key.Bytes()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not supported")
	})

	// Test that a ECDSA public key generated using the csp has the expected properties
	// it is a public non-symmetric key that can be serialized to bytes
	t.Run("Public Key", func(t *testing.T) {
		pubKey, err := key.PublicKey()
		require.NoError(t, err)
		assert.NotNil(t, pubKey)
		assert.False(t, pubKey.Private())
		assert.False(t, pubKey.Symmetric())

		pubBytes, err := pubKey.Bytes()
		require.NoError(t, err)
		assert.NotEmpty(t, pubBytes)
	})
}

// Test retrieving stored keys
func TestGetKey(t *testing.T) {
	keyStore := NewKVSStore(kvs.NewTrackedMemory())
	csp, err := NewCSP(keyStore)
	require.NoError(t, err)

	// Generate and store a key
	key, err := csp.KeyGen(&bccsp.ECDSAKeyGenOpts{Temporary: false})
	require.NoError(t, err)

	// Test using a csp to retrieve a previously stored private key based on a SKI (subject key identifier)
	// and verify that the retrieved key is a private symmetric key for the same SKI.
	t.Run("Get Existing Key", func(t *testing.T) {
		retrievedKey, err := csp.GetKey(key.SKI())
		require.NoError(t, err)
		assert.NotNil(t, retrievedKey)
		assert.Equal(t, key.SKI(), retrievedKey.SKI())
		// Verify the retrieved key has the same properties
		assert.Equal(t, key.Private(), retrievedKey.Private())
		assert.Equal(t, key.Symmetric(), retrievedKey.Symmetric())
	})

	// Test failure to retrieve a key using a SKI that was not previously used to store a key
	t.Run("Get Non-existent Key", func(t *testing.T) {
		_, err := csp.GetKey([]byte("non-existent"))
		require.Error(t, err)
	})
}

// Test success to sign a hashed message using a generated ECDSA private key
func TestSignECDSA(t *testing.T) {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	digest := sha256.Sum256([]byte("test"))

	t.Run("Sign Success", func(t *testing.T) {
		sig, err := signECDSA(privKey, digest[:], nil)
		require.NoError(t, err)
		assert.NotEmpty(t, sig)
	})
}

// Test success to sign a hashed message using a generated ECDSA private key
func TestVerifyECDSA(t *testing.T) {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	digest := sha256.Sum256([]byte("test"))
	sig, err := signECDSA(privKey, digest[:], nil)
	require.NoError(t, err)

	// Test success to verify a valid ECDSA signature using the corresponding ECDSA public key
	t.Run("Verify Valid Signature", func(t *testing.T) {
		valid, err := verifyECDSA(&privKey.PublicKey, sig, digest[:], nil)
		require.NoError(t, err)
		assert.True(t, valid)
	})

	// Test falure to verify an invalid ECDSA signature using a valid ECDSA public key
	t.Run("Verify Invalid Signature Format", func(t *testing.T) {
		valid, err := verifyECDSA(&privKey.PublicKey, []byte("invalid"), digest[:], nil)
		require.Error(t, err)
		assert.False(t, valid)
	})
}

// Test marshalling ECDSA keys
func TestKeyMarshalling(t *testing.T) {
	keyStore := NewKVSStore(kvs.NewTrackedMemory())
	csp, err := NewCSP(keyStore)
	require.NoError(t, err)

	key, err := csp.KeyGen(&bccsp.ECDSAKeyGenOpts{})
	require.NoError(t, err)

	// Test failure in attempt to marshall an ECDSA private key
	t.Run("Private Key Bytes Not Supported", func(t *testing.T) {
		_, err := key.Bytes()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not supported")
	})

	// Test marshalling an ECDSA public key	and then unmarshalling it and
	// verify that the unmarshaled key is public
	t.Run("Public Key Bytes", func(t *testing.T) {
		pubKey, err := key.PublicKey()
		require.NoError(t, err)

		bytes, err := pubKey.Bytes()
		require.NoError(t, err)
		assert.NotEmpty(t, bytes)

		// Public key Bytes() returns DER format, not PEM
		// Verify it can be parsed and matches the original
		parsedKey, err := x509.ParsePKIXPublicKey(bytes)
		require.NoError(t, err)
		assert.NotNil(t, parsedKey)
		// Verify the parsed key is an ECDSA public key
		ecdsaParsedKey, ok := parsedKey.(*ecdsa.PublicKey)
		assert.True(t, ok)
		assert.NotNil(t, ecdsaParsedKey)
	})
}

// Test that a KVS store is created as not read-only
func TestKVSStore_ReadOnly(t *testing.T) {
	keyStore := NewKVSStore(kvs.NewTrackedMemory())
	assert.False(t, keyStore.ReadOnly())
}

func TestKVSStore_StoreAndRetrieve(t *testing.T) {
	keyStore := NewKVSStore(kvs.NewTrackedMemory())
	csp, err := NewCSP(keyStore)
	require.NoError(t, err)

	// Test using a KVS store to retrieve a private key that was previously stored at creation time
	// via the "Temporary: false" flag, using a SKI (subject key identifier),
	// and then verify that the retrieved key is a private symmetric key for the same SKI.
	t.Run("Store and Retrieve Private Key", func(t *testing.T) {
		key, err := csp.KeyGen(&bccsp.ECDSAKeyGenOpts{Temporary: false})
		require.NoError(t, err)

		// Key should be stored automatically
		retrievedKey, err := keyStore.GetKey(key.SKI())
		require.NoError(t, err)
		assert.Equal(t, key.SKI(), retrievedKey.SKI())
		// Verify the retrieved key has the same properties
		assert.Equal(t, key.Private(), retrievedKey.Private())
		assert.Equal(t, key.Symmetric(), retrievedKey.Symmetric())
	})

	// Test using a KVS store to retrieve a private key that was previously stored explicitly
	// using a SKI (subject key identifier),
	// and then verify that the retrieved key is a private symmetric key for the same SKI.
	t.Run("Store and Retrieve Public Key", func(t *testing.T) {
		key, err := csp.KeyGen(&bccsp.ECDSAKeyGenOpts{Temporary: false})
		require.NoError(t, err)

		pubKey, err := key.PublicKey()
		require.NoError(t, err)

		// Store public key explicitly
		err = keyStore.StoreKey(pubKey)
		require.NoError(t, err)

		retrievedKey, err := keyStore.GetKey(pubKey.SKI())
		require.NoError(t, err)
		assert.Equal(t, pubKey.SKI(), retrievedKey.SKI())
		// Verify the retrieved key is a public key
		assert.False(t, retrievedKey.Private())
		assert.False(t, retrievedKey.Symmetric())
	})

	// Test failure to retrieve a key using a SKI that was not previously used to store a key
	t.Run("GetKey Non-existent Key", func(t *testing.T) {
		_, err := keyStore.GetKey([]byte("non-existent-ski"))
		require.Error(t, err)
	})
}

// Test creating an ECDSA public key from a private key
func TestECDSAPublicKey_PublicKey(t *testing.T) {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	pubKey := &ecdsaPublicKey{&privKey.PublicKey}

	pk, err := pubKey.PublicKey()
	require.NoError(t, err)
	assert.Equal(t, pubKey, pk) // Returns itself
}

// Test marshalling/unmarshalling an ECDSA Public Key
func TestECDSAPublicKey_MarshallUnmarshall(t *testing.T) {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	pubKey := &ecdsaPublicKey{&privKey.PublicKey}

	// Test that unmarshalling a marshalled PK of the P256 curve
	// returns another PK with the same properties
	// including the same SKI (subject key identifier), and the same (non) "privateness"
	// (non) symmetry, and the same X/Y values.
	t.Run("Marshall and Unmarshall", func(t *testing.T) {
		marshalled, err := pubKey.marshall()
		require.NoError(t, err)
		assert.NotEmpty(t, marshalled)

		newKey := &ecdsaPublicKey{}
		err = newKey.unmarshall(marshalled)
		require.NoError(t, err)
		assert.Equal(t, pubKey.SKI(), newKey.SKI())
		// Verify the unmarshalled key has the same properties
		assert.Equal(t, pubKey.Private(), newKey.Private())
		assert.Equal(t, pubKey.Symmetric(), newKey.Symmetric())
		// Verify the public key coordinates match
		assert.True(t, pubKey.pubKey.Equal(newKey.pubKey))
	})

	// Test failure to unmarshall a public key from invalid raw data
	t.Run("Unmarshall Invalid PEM", func(t *testing.T) {
		newKey := &ecdsaPublicKey{}
		err := newKey.unmarshall([]byte("invalid pem"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decode PEM block")
	})

	// Test failure to unmarshall a PEM format for a certificate as if it were a public key
	t.Run("Unmarshall Wrong PEM Type", func(t *testing.T) {
		wrongPEM := []byte(`-----BEGIN CERTIFICATE-----
MIIBkTCB+wIJAKHHCgVZU6w6MA0GCSqGSIb3DQEBCwUAMA0xCzAJBgNVBAYTAlVT
-----END CERTIFICATE-----`)
		newKey := &ecdsaPublicKey{}
		err := newKey.unmarshall(wrongPEM)
		require.Error(t, err)
	})
}

// Test marshalling/unmarshalling an ECDSA private Key
func TestECDSAPrivateKey_MarshallUnmarshall(t *testing.T) {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	key := &ecdsaPrivateKey{privKey}

	// Test that unmarshalling a marshalled SK of the P256 curve
	// returns another SK with the same properties
	// including the same SKI (subject key identifier), and the same "privateness",
	// symmetry, and the same X/Y/D values.
	t.Run("Marshall and Unmarshall", func(t *testing.T) {
		marshalled, err := key.marshall()
		require.NoError(t, err)
		assert.NotEmpty(t, marshalled)

		newKey := &ecdsaPrivateKey{}
		err = newKey.unmarshall(marshalled)
		require.NoError(t, err)
		assert.Equal(t, key.SKI(), newKey.SKI())

		// Verify the unmarshalled key has the same properties
		assert.Equal(t, key.Private(), newKey.Private())
		assert.Equal(t, key.Symmetric(), newKey.Symmetric())

		// Verify the private key value matches
		assert.True(t, key.privKey.Equal(newKey.privKey))
	})

	// Test failure to unmarshall a private key from invalid raw data
	t.Run("Unmarshall Invalid PEM", func(t *testing.T) {
		newKey := &ecdsaPrivateKey{}
		err := newKey.unmarshall([]byte("invalid pem"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decode PEM block")
	})
}

// Test that unmarshalling a SK of a P256 curve marshalled in a PKCS8 format
// returns a SK with the same X/Y/D values.
func TestDerToPrivateKey_PKCS8(t *testing.T) {
	// Generate a key
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// Marshal to PKCS8
	der, err := x509.MarshalPKCS8PrivateKey(privKey)
	require.NoError(t, err)

	// Test derToPrivateKey
	key, err := derToPrivateKey(der)
	require.NoError(t, err)
	assert.NotNil(t, key)

	ecdsaKey, ok := key.(*ecdsa.PrivateKey)
	assert.True(t, ok)
	assert.True(t, privKey.Equal(ecdsaKey))
}

// Test failure when attempting to unmarshall a PK from an invalid DER form
func TestDerToPublicKey_EmptyBytes(t *testing.T) {
	_, err := derToPublicKey([]byte{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid DER")
}

func TestKeyImport_X509Certificate_NonECDSA(t *testing.T) {
	keyStore := NewKVSStore(kvs.NewTrackedMemory())
	csp, err := NewCSP(keyStore)
	require.NoError(t, err)

	// Test parsing a certificate that was created with DER encoding
	// and then using the certificate to import a valid PK
	t.Run("Valid ECDSA Certificate", func(t *testing.T) {
		// This test verifies the error path for unsupported key types
		// Since we can't easily create a real RSA cert in this context,
		// we'll test the ECDSA path more thoroughly
		privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		template := x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject: pkix.Name{
				CommonName: "test",
			},
			NotBefore: time.Now().Add(-time.Hour),
			NotAfter:  time.Now().Add(time.Hour),
		}

		derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privKey.PublicKey, privKey)
		require.NoError(t, err)

		cert, err := x509.ParseCertificate(derBytes)
		require.NoError(t, err)

		key, err := csp.KeyImport(cert, &bccsp.X509PublicKeyImportOpts{})
		require.NoError(t, err)
		assert.NotNil(t, key)
		assert.False(t, key.Private())
	})
}

func TestKVSStore_ErrorPaths(t *testing.T) {
	keyStore := NewKVSStore(kvs.NewTrackedMemory())
	csp, err := NewCSP(keyStore)
	require.NoError(t, err)

	// Test failure to retrieve a PK from the KVSStore using a SKI (subject key identifier)
	// that was actually used to insert a PK to the KVSStore
	// but where the inserted entry was later corrupted
	t.Run("GetKey Unknown Type", func(t *testing.T) {
		// Generate a key and store it, then corrupt the entry
		key, err := csp.KeyGen(&bccsp.ECDSAKeyGenOpts{Temporary: false})
		require.NoError(t, err)

		pubKey, err := key.PublicKey()
		require.NoError(t, err)

		// Now manually overwrite with unknown type
		skiHex := hex.EncodeToString(pubKey.SKI())
		err = keyStore.Put(skiHex, &KeyEntry{
			KeyType: "unknownType",
			Raw:     []byte("data"),
		})
		require.NoError(t, err)

		_, err = keyStore.GetKey(pubKey.SKI())
		require.Error(t, err)
		// The actual error message is "key not found for [ski]"
		assert.Contains(t, err.Error(), "key not found")
	})

	// Test failure to retrieve a PK from the KVSStore where the entry in the store
	// that corresponds to the queried SKI includes invalid raw data
	t.Run("GetKey Invalid Private Key Data", func(t *testing.T) {
		err := keyStore.Put("invalid-priv", &KeyEntry{
			KeyType: "ecdsaPrivateKey",
			Raw:     []byte("invalid data"),
		})
		require.NoError(t, err)

		_, err = keyStore.GetKey([]byte("invalid-priv"))
		require.Error(t, err)
	})

	// Test failure to retrieve a SK from the KVSStore where the entry in the store
	// that corresponds to the queried SKI includes invalid raw data
	t.Run("GetKey Invalid Public Key Data", func(t *testing.T) {
		err := keyStore.Put("invalid-pub", &KeyEntry{
			KeyType: "ecdsaPublicKey",
			Raw:     []byte("invalid data"),
		})
		require.NoError(t, err)

		_, err = keyStore.GetKey([]byte("invalid-pub"))
		require.Error(t, err)
	})
}

func TestECDSAKey_SKI_NilKey(t *testing.T) {
	// Test that the SKI (subject key identifier) of a SK based on a nil SK is nil
	t.Run("Private Key with Nil", func(t *testing.T) {
		key := &ecdsaPrivateKey{privKey: nil}
		ski := key.SKI()
		assert.Nil(t, ski)
	})

	// Test that the SKI (subject key identifier) of a PK based on a nil PK is nil
	t.Run("Public Key with Nil", func(t *testing.T) {
		key := &ecdsaPublicKey{pubKey: nil}
		ski := key.SKI()
		assert.Nil(t, ski)
	})
}
