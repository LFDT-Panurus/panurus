/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"context"
	"testing"

	"github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/mock"
	kvs2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test deserialization of IdemixIdentity under various success and failure conditions
func TestDeserializer_Deserialize(t *testing.T) {
	testDeserializerDeserialize(t, "../testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testDeserializerDeserialize(t, "../testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testDeserializerDeserialize(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()

	// Setup: Create real BCCSP and load configuration
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)

	config, err := NewConfig(configPath)
	require.NoError(t, err)

	keyStore, err := NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)

	cryptoProvider, err := NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	// Import issuer public key
	issuerPublicKey, err := cryptoProvider.KeyImport(
		config.Ipk,
		&types.IdemixIssuerPublicKeyImportOpts{
			Temporary: true,
		},
	)
	require.NoError(t, err)

	// Create deserializer (RevocationPK can be nil for these tests)
	deserializer := &Deserializer{
		Name:            "test",
		Ipk:             config.Ipk,
		Csp:             cryptoProvider,
		IssuerPublicKey: issuerPublicKey,
		RevocationPK:    nil, // Not needed for error path tests
		Epoch:           0,
		VerType:         types.ExpectEidNymRhNym,
		Schema:          "test-schema",
	}

	// Test deserialization of IdemixIdentity with/out an EID
	t.Run("Valid deserialization with mocked BCCSP", func(t *testing.T) {
		// Create Counterfeiter mocks
		mockBCCSP := &mock.BCCSP{}
		mockKey := &mock.Key{}

		// Setup mock behaviors
		mockKey.BytesReturns([]byte("mock-key-bytes"), nil)
		mockKey.SKIReturns([]byte("mock-ski"))
		mockBCCSP.KeyImportReturns(mockKey, nil)
		mockBCCSP.VerifyReturns(true, nil)

		// Create deserializer with mock BCCSP
		mockDeserializer := &Deserializer{
			Name:            "test",
			Ipk:             []byte("ipk"),
			Csp:             mockBCCSP,
			IssuerPublicKey: mockKey,
			RevocationPK:    mockKey,
			Epoch:           0,
			VerType:         types.ExpectEidNymRhNym,
			Schema:          "test-schema",
			SchemaManager:   nil,
		}

		// Create a valid serialized identity
		serialized := &SerializedIdemixIdentity{
			NymPublicKey: []byte("valid-nym-key"),
			Proof:        []byte("valid-proof"),
			Schema:       "test-schema",
		}
		raw, err := proto.Marshal(serialized)
		require.NoError(t, err)

		// Test without nymEID
		result, err := mockDeserializer.DeserializeAgainstNymEID(raw, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotNil(t, result.Identity)
		assert.NotNil(t, result.NymPublicKey)
		assert.Equal(t, Schema("test-schema"), result.Identity.Schema)

		// Test with nymEID
		result2, err := mockDeserializer.DeserializeAgainstNymEID(raw, []byte("test-nym-eid"))
		require.NoError(t, err)
		assert.NotNil(t, result2)
		assert.NotNil(t, result2.Identity)
		assert.Equal(t, []byte("test-nym-eid"), result2.Identity.Idemix.NymEID)
	})

	// Test deserialization of IdemixIdentity with failure due to failure to import key (by mock)
	t.Run("KeyImport fails", func(t *testing.T) {
		// Create Counterfeiter mock that fails on KeyImport
		mockBCCSP := &mock.BCCSP{}
		mockBCCSP.KeyImportReturns(nil, errors.New("key import failed"))

		mockDeserializer := &Deserializer{
			Name:   "test",
			Csp:    mockBCCSP,
			Schema: "test-schema",
		}

		serialized := &SerializedIdemixIdentity{
			NymPublicKey: []byte("nym-key"),
			Proof:        []byte("proof"),
			Schema:       "test-schema",
		}
		raw, err := proto.Marshal(serialized)
		require.NoError(t, err)

		_, err = mockDeserializer.DeserializeAgainstNymEID(raw, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to import nym public key")
	})

	// Test deserialization of IdemixIdentity with failure due to failing identity verification (by mock bccsp)
	t.Run("Validate fails", func(t *testing.T) {
		// Create Counterfeiter mocks - KeyImport succeeds but Verify fails
		mockBCCSP := &mock.BCCSP{}
		mockKey := &mock.Key{}

		mockKey.BytesReturns([]byte("mock-key-bytes"), nil)
		mockKey.SKIReturns([]byte("mock-ski"))
		mockBCCSP.KeyImportReturns(mockKey, nil)
		mockBCCSP.VerifyReturns(false, errors.New("verification failed"))

		mockDeserializer := &Deserializer{
			Name:            "test",
			Ipk:             []byte("ipk"),
			Csp:             mockBCCSP,
			IssuerPublicKey: mockKey,
			RevocationPK:    mockKey,
			Epoch:           0,
			VerType:         types.ExpectEidNymRhNym,
			Schema:          "test-schema",
		}

		serialized := &SerializedIdemixIdentity{
			NymPublicKey: []byte("nym-key"),
			Proof:        []byte("proof"),
			Schema:       "test-schema",
		}
		raw, err := proto.Marshal(serialized)
		require.NoError(t, err)

		_, err = mockDeserializer.DeserializeAgainstNymEID(raw, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot deserialize, invalid identity")
	})

	// Test deserialization of IdemixIdentity with failure due to empty raw id
	t.Run("Empty identity", func(t *testing.T) {
		_, err := deserializer.Deserialize(context.Background(), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty identity")
	})

	// Test deserialization of IdemixIdentity with failure due to invalid protobuf id
	t.Run("Invalid protobuf", func(t *testing.T) {
		_, err := deserializer.Deserialize(context.Background(), []byte{0, 1, 2, 3})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not deserialize")
	})

	// Test deserialization of IdemixIdentity with failure due empty pseudonym PK
	t.Run("Empty nym public key", func(t *testing.T) {
		serialized := &SerializedIdemixIdentity{
			NymPublicKey: nil,
			Proof:        []byte("proof"),
			Schema:       "test-schema",
		}
		raw, err := proto.Marshal(serialized)
		require.NoError(t, err)

		_, err = deserializer.Deserialize(context.Background(), raw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pseudonym's public key is empty")
	})

	// Test deserialization of IdemixIdentity with failure due mismatch between
	// deserializer and serialized id's schema
	t.Run("Schema mismatch", func(t *testing.T) {
		serialized := &SerializedIdemixIdentity{
			NymPublicKey: []byte("fake-nym-key"),
			Proof:        []byte("proof"),
			Schema:       "wrong-schema",
		}
		raw, err := proto.Marshal(serialized)
		require.NoError(t, err)

		_, err = deserializer.Deserialize(context.Background(), raw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "schema does not match")
	})
}

// Test deserialization of AuditInfo under various success and failure conditions
func TestDeserializer_DeserializeAuditInfo(t *testing.T) {
	testDeserializerDeserializeAuditInfo(t, "../testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testDeserializerDeserializeAuditInfo(t, "../testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testDeserializerDeserializeAuditInfo(t *testing.T, configPath string, curveID math.CurveID) {
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
		Schema:          "test-schema",
	}

	// Test successful deserialization of AuditInfo
	t.Run("Valid audit info", func(t *testing.T) {
		auditInfo := &AuditInfo{
			Attributes: [][]byte{
				[]byte("attr0"),
				[]byte("attr1"),
				[]byte("enrollment-id"),
				[]byte("revocation-handle"),
			},
			Schema: "test-schema",
		}
		raw, err := auditInfo.Bytes()
		require.NoError(t, err)

		result, err := deserializer.DeserializeAuditInfo(context.Background(), raw)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "enrollment-id", result.EnrollmentID())
		assert.Equal(t, "revocation-handle", result.RevocationHandle())
		assert.Equal(t, "test-schema", result.Schema)
		assert.Equal(t, cryptoProvider, result.Csp)
		assert.Equal(t, issuerPublicKey, result.IssuerPublicKey)
	})

	// Test deserialization of AuditInfo with failure due invalid serializeed json
	t.Run("Invalid JSON", func(t *testing.T) {
		_, err := deserializer.DeserializeAuditInfo(context.Background(), []byte("invalid json"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed deserializing audit info")
	})

	// Test deserialization of AuditInfo with failure due empty attributes in serialized data
	t.Run("Empty attributes", func(t *testing.T) {
		auditInfo := &AuditInfo{
			Attributes: [][]byte{},
			Schema:     "test-schema",
		}
		raw, err := auditInfo.Bytes()
		require.NoError(t, err)

		_, err = deserializer.DeserializeAuditInfo(context.Background(), raw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no attributes found")
	})
}
