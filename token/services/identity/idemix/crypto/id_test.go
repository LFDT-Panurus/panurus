/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"testing"

	"github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
	idemixmock "github.com/LFDT-Panurus/panurus/token/services/identity/idemix/mock"
	kvs2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test creating a new Idemix identity from its components
func TestNewIdentity(t *testing.T) {
	testNewIdentity(t, "../testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testNewIdentity(t, "../testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testNewIdentity(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()

	// Setup
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)

	config, err := NewConfig(configPath)
	require.NoError(t, err)

	keyStore, err := NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)

	cryptoProvider, err := NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	issuerPublicKey, err := cryptoProvider.KeyImport(
		config.Ipk,
		&types.IdemixIssuerPublicKeyImportOpts{
			Temporary: true,
		},
	)
	require.NoError(t, err)

	deserializer := &Deserializer{
		Name:            "test",
		Ipk:             config.Ipk,
		Csp:             cryptoProvider,
		IssuerPublicKey: issuerPublicKey,
		Epoch:           0,
		VerType:         types.ExpectEidNymRhNym,
		Schema:          "test-schema",
	}

	// Import user key and derive nym key
	userKey, err := cryptoProvider.KeyImport(
		config.Signer.Sk,
		&types.IdemixUserSecretKeyImportOpts{Temporary: true},
	)
	require.NoError(t, err)

	nymKey, err := cryptoProvider.KeyDeriv(
		userKey,
		&types.IdemixNymKeyDerivationOpts{
			Temporary: true,
			IssuerPK:  issuerPublicKey,
		},
	)
	require.NoError(t, err)

	// Test NewIdentity
	identity := NewIdentity(
		deserializer,
		nymKey,
		[]byte("fake-proof"),
		types.ExpectEidNymRhNym,
		nil, // SchemaManager not needed for construction
		"test-schema",
	)
	assert.NotNil(t, identity)
	assert.Equal(t, nymKey, identity.NymPublicKey)
	assert.Equal(t, deserializer, identity.Idemix)
	assert.Equal(t, []byte("fake-proof"), identity.AssociationProof)
	assert.Equal(t, types.ExpectEidNymRhNym, identity.VerificationType)
	assert.Equal(t, "test-schema", identity.Schema)
}

// Test serializing an Idemix identity by then desrializing and checking
func TestIdentity_Serialize(t *testing.T) {
	testIdentitySerialize(t, "../testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testIdentitySerialize(t, "../testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testIdentitySerialize(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()

	// Setup
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)

	config, err := NewConfig(configPath)
	require.NoError(t, err)

	keyStore, err := NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)

	cryptoProvider, err := NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	issuerPublicKey, err := cryptoProvider.KeyImport(
		config.Ipk,
		&types.IdemixIssuerPublicKeyImportOpts{
			Temporary: true,
		},
	)
	require.NoError(t, err)

	deserializer := &Deserializer{
		Name:            "test",
		Ipk:             config.Ipk,
		Csp:             cryptoProvider,
		IssuerPublicKey: issuerPublicKey,
		Epoch:           0,
		VerType:         types.ExpectEidNymRhNym,
		Schema:          "test-schema",
	}

	// Import user key and derive nym key
	userKey, err := cryptoProvider.KeyImport(
		config.Signer.Sk,
		&types.IdemixUserSecretKeyImportOpts{Temporary: true},
	)
	require.NoError(t, err)

	nymKey, err := cryptoProvider.KeyDeriv(
		userKey,
		&types.IdemixNymKeyDerivationOpts{
			Temporary: true,
			IssuerPK:  issuerPublicKey,
		},
	)
	require.NoError(t, err)

	identity := NewIdentity(
		deserializer,
		nymKey,
		[]byte("test-proof"),
		types.ExpectEidNymRhNym,
		nil,
		"test-schema",
	)

	// Test Serialize
	serialized, err := identity.Serialize()
	require.NoError(t, err)
	assert.NotNil(t, serialized)

	// Verify we can deserialize it back
	deserialized := &SerializedIdemixIdentity{}
	err = proto.Unmarshal(serialized, deserialized)
	require.NoError(t, err)
	assert.NotNil(t, deserialized.NymPublicKey)
	assert.Equal(t, []byte("test-proof"), deserialized.Proof)
}

// Test that global constants have expected values
func TestIdentity_Constants(t *testing.T) {
	// Test that constants are defined correctly
	assert.Equal(t, 2, EIDIndex)
	assert.Equal(t, 3, RHIndex)
	assert.Equal(t, "SignerConfigFull", SignerConfigFull)
}

// Test invalid signing identities
func TestSigningIdentity_Sign(t *testing.T) {
	testSigningIdentitySign(t, "../testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testSigningIdentitySign(t, "../testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testSigningIdentitySign(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()

	// Setup
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)

	config, err := NewConfig(configPath)
	require.NoError(t, err)

	keyStore, err := NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)

	cryptoProvider, err := NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	issuerPublicKey, err := cryptoProvider.KeyImport(
		config.Ipk,
		&types.IdemixIssuerPublicKeyImportOpts{
			Temporary: true,
		},
	)
	require.NoError(t, err)

	deserializer := &Deserializer{
		Name:            "test",
		Ipk:             config.Ipk,
		Csp:             cryptoProvider,
		IssuerPublicKey: issuerPublicKey,
		Epoch:           0,
		VerType:         types.ExpectEidNymRhNym,
		Schema:          "test-schema",
	}

	// Import user key and derive nym key (temporary for testing)
	userKey, err := cryptoProvider.KeyImport(
		config.Signer.Sk,
		&types.IdemixUserSecretKeyImportOpts{Temporary: true},
	)
	require.NoError(t, err)

	nymKey, err := cryptoProvider.KeyDeriv(
		userKey,
		&types.IdemixNymKeyDerivationOpts{
			Temporary: true,
			IssuerPK:  issuerPublicKey,
		},
	)
	require.NoError(t, err)

	identity := NewIdentity(
		deserializer,
		nymKey,
		[]byte("test-proof"),
		types.ExpectEidNymRhNym,
		nil,
		"test-schema",
	)

	// Test Sign with invalid nym key SKI - should fail when trying to get the key
	invalidSigning := &SigningIdentity{
		Identity:     identity,
		CSP:          cryptoProvider,
		EnrollmentId: "test-user",
		NymKeySKI:    []byte("invalid-ski-that-does-not-exist"),
		UserKeySKI:   userKey.SKI(),
	}
	_, err = invalidSigning.Sign([]byte("test message"))
	require.Error(t, err, "Sign should fail with invalid nym key SKI")

	// Test Sign with invalid user key SKI - should fail when trying to get the key
	invalidSigning2 := &SigningIdentity{
		Identity:     identity,
		CSP:          cryptoProvider,
		EnrollmentId: "test-user",
		NymKeySKI:    nymKey.SKI(),
		UserKeySKI:   []byte("invalid-ski-that-does-not-exist"),
	}
	_, err = invalidSigning2.Sign([]byte("test message"))
	require.Error(t, err, "Sign should fail with invalid user key SKI")
}

// Test deserialization of Idemix id with successful/failing verification by mock bccsp
func TestIdentity_Verify(t *testing.T) {
	// Use mock BCCSP to test Verify function
	mockBCCSP := &idemixmock.BCCSP{}
	mockIPK := &idemixmock.Key{}
	mockNymPK := &idemixmock.Key{}

	deserializer := &Deserializer{
		Name:            "test",
		Ipk:             []byte("test-ipk"),
		Csp:             mockBCCSP,
		IssuerPublicKey: mockIPK,
	}

	identity := &Identity{
		Idemix:       deserializer,
		NymPublicKey: mockNymPK,
	}

	msg := []byte("test message")
	sig := []byte("test signature")

	t.Run("Verify success", func(t *testing.T) {
		mockBCCSP.VerifyReturns(true, nil)

		err := identity.Verify(msg, sig)
		require.NoError(t, err)

		// Verify the mock was called with correct parameters
		assert.Equal(t, 1, mockBCCSP.VerifyCallCount())
		key, signature, message, opts := mockBCCSP.VerifyArgsForCall(0)
		assert.Equal(t, mockNymPK, key)
		assert.Equal(t, sig, signature)
		assert.Equal(t, msg, message)
		assert.NotNil(t, opts)
	})

	t.Run("Verify failure", func(t *testing.T) {
		mockBCCSP.VerifyReturns(false, errors.New("verification failed"))

		err := identity.Verify(msg, sig)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "verification failed")
	})
}

// Test serialization of Idemix id that fails due to bad Nym PK
func TestIdentity_Serialize_ErrorPaths(t *testing.T) {
	// Test error path when NymPublicKey.Bytes() fails
	mockBCCSP := &idemixmock.BCCSP{}
	mockIPK := &idemixmock.Key{}
	mockNymPK := &idemixmock.Key{}

	deserializer := &Deserializer{
		Name:            "test",
		Ipk:             []byte("test-ipk"),
		Csp:             mockBCCSP,
		IssuerPublicKey: mockIPK,
	}

	identity := &Identity{
		Idemix:           deserializer,
		NymPublicKey:     mockNymPK,
		AssociationProof: []byte("proof"),
		Schema:           "test-schema",
	}

	t.Run("NymPublicKey.Bytes() fails", func(t *testing.T) {
		mockNymPK.BytesReturns(nil, errors.New("bytes error"))

		_, err := identity.Serialize()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not serialize nym")
	})
}

// Test various successful and failing identity proofs
func TestIdentity_verifyProof(t *testing.T) {
	mockBCCSP := &idemixmock.BCCSP{}
	mockIPK := &idemixmock.Key{}
	mockNymPK := &idemixmock.Key{}

	// Test successful proof of id with desrializer NymEID (using mock bccsp)
	t.Run("verifyProof with NymEID", func(t *testing.T) {
		mockRevPK := &idemixmock.Key{}
		deserializer := &Deserializer{
			Name:            "test",
			Ipk:             []byte("test-ipk"),
			Csp:             mockBCCSP,
			IssuerPublicKey: mockIPK,
			NymEID:          []byte("nym-eid"),
			RhNym:           []byte("rh-nym"),
			RevocationPK:    mockRevPK,
			Epoch:           1,
		}

		identity := &Identity{
			Idemix:           deserializer,
			NymPublicKey:     mockNymPK,
			AssociationProof: []byte("proof"),
			VerificationType: types.ExpectEidNymRhNym,
		}

		mockBCCSP.VerifyReturns(true, nil)

		err := identity.verifyProof()
		require.NoError(t, err)

		// Verify the mock was called
		assert.Equal(t, 1, mockBCCSP.VerifyCallCount())
	})

	// Test successful proof of id without desrializer NymEID (using mock bccsp)
	t.Run("verifyProof without NymEID", func(t *testing.T) {
		mockRevPK := &idemixmock.Key{}
		deserializer := &Deserializer{
			Name:            "test",
			Ipk:             []byte("test-ipk"),
			Csp:             mockBCCSP,
			IssuerPublicKey: mockIPK,
			NymEID:          nil, // No NymEID
			RevocationPK:    mockRevPK,
			Epoch:           1,
		}

		identity := &Identity{
			Idemix:           deserializer,
			NymPublicKey:     mockNymPK,
			AssociationProof: []byte("proof"),
			VerificationType: types.ExpectStandard,
		}

		mockBCCSP.VerifyReturns(true, nil)

		err := identity.verifyProof()
		require.NoError(t, err)
	})

	// Test proof of id that fails to verify (by mock bccsp)
	t.Run("verifyProof returns error", func(t *testing.T) {
		deserializer := &Deserializer{
			Name:            "test",
			Ipk:             []byte("test-ipk"),
			Csp:             mockBCCSP,
			IssuerPublicKey: mockIPK,
		}

		identity := &Identity{
			Idemix:           deserializer,
			NymPublicKey:     mockNymPK,
			AssociationProof: []byte("proof"),
		}

		mockBCCSP.VerifyReturns(false, errors.New("verify error"))

		err := identity.verifyProof()
		require.Error(t, err)
	})

	// Test proof of id that fails to verify (by mock bccsp that returns "false")
	t.Run("verifyProof returns false without error", func(t *testing.T) {
		deserializer := &Deserializer{
			Name:            "test",
			Ipk:             []byte("test-ipk"),
			Csp:             mockBCCSP,
			IssuerPublicKey: mockIPK,
		}

		identity := &Identity{
			Idemix:           deserializer,
			NymPublicKey:     mockNymPK,
			AssociationProof: []byte("proof"),
		}

		// This should trigger an error path
		mockBCCSP.VerifyReturns(false, nil)

		err := identity.verifyProof()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected condition")
	})
}

// Test successful and failing signing operations
func TestSigningIdentity_Sign_ErrorPaths(t *testing.T) {
	// Test signing that fails because the nym key can't be found (by mock bccsp)
	t.Run("Sign fails at GetKey for nym key", func(t *testing.T) {
		// Create fresh mocks for this test
		mockCSP := &idemixmock.BCCSP{}
		mockIPK := &idemixmock.Key{}
		mockNymPK := &idemixmock.Key{}

		deserializer := &Deserializer{
			Name:            "test",
			Ipk:             []byte("test-ipk"),
			Csp:             &idemixmock.BCCSP{},
			IssuerPublicKey: mockIPK,
		}

		identity := &Identity{
			Idemix:       deserializer,
			NymPublicKey: mockNymPK,
		}

		signingIdentity := &SigningIdentity{
			Identity:     identity,
			CSP:          mockCSP,
			EnrollmentId: "test-user",
			NymKeySKI:    []byte("nym-ski"),
			UserKeySKI:   []byte("user-ski"),
		}

		// Mock GetKey to fail for nym key
		mockCSP.GetKeyReturns(nil, errors.New("nym key not found"))

		_, err := signingIdentity.Sign([]byte("test message"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot find nym secret key")
	})

	// Test signing that fails because the user key can't be found (by mock bccsp)
	t.Run("Sign fails at GetKey for user key", func(t *testing.T) {
		// Create fresh mocks for this test
		mockCSP := &idemixmock.BCCSP{}
		mockIPK := &idemixmock.Key{}
		mockNymPK := &idemixmock.Key{}
		mockNymSK := &idemixmock.Key{}

		deserializer := &Deserializer{
			Name:            "test",
			Ipk:             []byte("test-ipk"),
			Csp:             &idemixmock.BCCSP{},
			IssuerPublicKey: mockIPK,
		}

		identity := &Identity{
			Idemix:       deserializer,
			NymPublicKey: mockNymPK,
		}

		signingIdentity := &SigningIdentity{
			Identity:     identity,
			CSP:          mockCSP,
			EnrollmentId: "test-user",
			NymKeySKI:    []byte("nym-ski"),
			UserKeySKI:   []byte("user-ski"),
		}

		// Mock GetKey to succeed for nym key, fail for user key
		mockCSP.GetKeyReturnsOnCall(0, mockNymSK, nil)
		mockCSP.GetKeyReturnsOnCall(1, nil, errors.New("user key not found"))

		_, err := signingIdentity.Sign([]byte("test message"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to retrieve user key")
	})

	// Test signing that fails because the sign operation (by the mock bccsp) returns error
	t.Run("Sign fails at CSP.Sign", func(t *testing.T) {
		// Create fresh mocks for this test
		mockBCCSP := &idemixmock.BCCSP{}
		mockIPK := &idemixmock.Key{}
		mockNymPK := &idemixmock.Key{}
		mockNymSK := &idemixmock.Key{}
		mockUserSK := &idemixmock.Key{}

		deserializer := &Deserializer{
			Name:            "test",
			Ipk:             []byte("test-ipk"),
			Csp:             mockBCCSP,
			IssuerPublicKey: mockIPK,
		}

		identity := &Identity{
			Idemix:       deserializer,
			NymPublicKey: mockNymPK,
		}

		signingIdentity := &SigningIdentity{
			Identity:     identity,
			CSP:          mockBCCSP,
			EnrollmentId: "test-user",
			NymKeySKI:    []byte("nym-ski"),
			UserKeySKI:   []byte("user-ski"),
		}

		// Mock GetKey to return keys
		mockBCCSP.GetKeyReturnsOnCall(0, mockNymSK, nil)  // First call for nym key
		mockBCCSP.GetKeyReturnsOnCall(1, mockUserSK, nil) // Second call for user key

		// Mock Sign to fail
		mockBCCSP.SignReturns(nil, errors.New("sign error"))

		_, err := signingIdentity.Sign([]byte("test message"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sign error")
	})

	// Test a successful sign operation by a valid signing identity
	t.Run("Sign succeeds", func(t *testing.T) {
		// Create fresh mocks for this test
		mockCSP := &idemixmock.BCCSP{}       // For GetKey calls
		mockIdemixCSP := &idemixmock.BCCSP{} // For Sign call
		mockIPK := &idemixmock.Key{}
		mockNymPK := &idemixmock.Key{}
		mockNymSK := &idemixmock.Key{}
		mockUserSK := &idemixmock.Key{}

		deserializer := &Deserializer{
			Name:            "test",
			Ipk:             []byte("test-ipk"),
			Csp:             mockIdemixCSP, // This is used for Sign
			IssuerPublicKey: mockIPK,
		}

		identity := &Identity{
			Idemix:       deserializer,
			NymPublicKey: mockNymPK,
		}

		signingIdentity := &SigningIdentity{
			Identity:     identity,
			CSP:          mockCSP, // This is used for GetKey
			EnrollmentId: "test-user",
			NymKeySKI:    []byte("nym-ski"),
			UserKeySKI:   []byte("user-ski"),
		}

		// Mock GetKey to return keys (on mockCSP)
		mockCSP.GetKeyReturnsOnCall(0, mockNymSK, nil)
		mockCSP.GetKeyReturnsOnCall(1, mockUserSK, nil)

		// Mock Sign to succeed (on mockIdemixCSP)
		expectedSig := []byte("signature")
		mockIdemixCSP.SignReturns(expectedSig, nil)

		sig, err := signingIdentity.Sign([]byte("test message"))
		require.NoError(t, err)
		assert.Equal(t, expectedSig, sig)
	})
}

// Test NymSignature verification of an invalid signature
func TestNymSignatureVerifier_Verify(t *testing.T) {
	testNymSignatureVerifierVerify(t, "../testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testNymSignatureVerifierVerify(t, "../testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testNymSignatureVerifierVerify(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()

	// Setup
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)

	config, err := NewConfig(configPath)
	require.NoError(t, err)

	keyStore, err := NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)

	cryptoProvider, err := NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	issuerPublicKey, err := cryptoProvider.KeyImport(
		config.Ipk,
		&types.IdemixIssuerPublicKeyImportOpts{
			Temporary: true,
		},
	)
	require.NoError(t, err)

	// Import user key and derive nym key
	userKey, err := cryptoProvider.KeyImport(
		config.Signer.Sk,
		&types.IdemixUserSecretKeyImportOpts{Temporary: false},
	)
	require.NoError(t, err)

	nymKey, err := cryptoProvider.KeyDeriv(
		userKey,
		&types.IdemixNymKeyDerivationOpts{
			Temporary: false,
			IssuerPK:  issuerPublicKey,
		},
	)
	require.NoError(t, err)

	// Get the public part
	nymPublicKey, err := nymKey.PublicKey()
	require.NoError(t, err)

	verifier := &NymSignatureVerifier{
		CSP:    cryptoProvider,
		IPK:    issuerPublicKey,
		NymPK:  nymPublicKey,
		Schema: "test-schema",
	}

	// Test with invalid signature
	err = verifier.Verify([]byte("message"), []byte("invalid-signature"))
	require.Error(t, err)
}
