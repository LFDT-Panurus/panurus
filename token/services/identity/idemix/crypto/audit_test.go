/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"context"
	"testing"

	idemix "github.com/IBM/idemix/bccsp/types"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// TestAuditInfo_Serialization verifies AuditInfo serialization and deserialization.
func TestAuditInfo_Serialization(t *testing.T) {
	auditInfo := &AuditInfo{
		Attributes: [][]byte{
			[]byte("attr1"),
			[]byte("attr2"),
			[]byte("enrollment-id"),
			[]byte("revocation-handle"),
		},
		Schema: "test-schema",
	}

	// Test Bytes() serialization
	bytes, err := auditInfo.Bytes()
	require.NoError(t, err)
	assert.NotNil(t, bytes)
	assert.NotEmpty(t, bytes)

	// Test FromBytes() round-trip
	newAuditInfo := &AuditInfo{}
	err = newAuditInfo.FromBytes(bytes)
	require.NoError(t, err)
	assert.Equal(t, auditInfo.Schema, newAuditInfo.Schema)
	assert.Len(t, auditInfo.Attributes, len(newAuditInfo.Attributes))
	for i := range auditInfo.Attributes {
		assert.Equal(t, auditInfo.Attributes[i], newAuditInfo.Attributes[i])
	}

	// Test FromBytes() with invalid JSON
	err = newAuditInfo.FromBytes([]byte("invalid json"))
	require.Error(t, err)

	// Test FromBytes() with empty JSON
	err = newAuditInfo.FromBytes([]byte("{}"))
	require.NoError(t, err)
}

// TestAuditInfo_Accessors verifies EnrollmentID and RevocationHandle accessors.
func TestAuditInfo_Accessors(t *testing.T) {
	auditInfo := &AuditInfo{
		Attributes: [][]byte{
			[]byte("attr0"),
			[]byte("attr1"),
			[]byte("test-enrollment-id"),
			[]byte("test-revocation-handle"),
		},
	}

	assert.Equal(t, "test-enrollment-id", auditInfo.EnrollmentID())
	assert.Equal(t, "test-revocation-handle", auditInfo.RevocationHandle())
}

// TestDeserializeAuditInfo verifies audit info deserialization and error handling.
func TestDeserializeAuditInfo(t *testing.T) {
	t.Run("Valid audit info", func(t *testing.T) {
		original := &AuditInfo{
			Attributes: [][]byte{[]byte("attr1"), []byte("attr2")},
			Schema:     "test-schema",
		}

		bytes, err := original.Bytes()
		require.NoError(t, err)

		result, err := DeserializeAuditInfo(bytes)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, original.Schema, result.Schema)
		assert.Len(t, original.Attributes, len(result.Attributes))
		for i := range original.Attributes {
			assert.Equal(t, original.Attributes[i], result.Attributes[i])
		}
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		_, err := DeserializeAuditInfo([]byte("invalid json"))
		require.Error(t, err)
	})

	t.Run("Empty attributes", func(t *testing.T) {
		auditInfo := &AuditInfo{Attributes: [][]byte{}, Schema: "test-schema"}
		bytes, err := auditInfo.Bytes()
		require.NoError(t, err)

		_, err = DeserializeAuditInfo(bytes)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no attributes found")
	})
}

// TestAuditInfo_Match verifies identity proof matching against audit info
// with various failing cases and with a successful match.
func TestAuditInfo_Match(t *testing.T) {
	// Helper to create identity bytes
	createIdentity := func() []byte {
		serialized := &SerializedIdemixIdentity{
			NymPublicKey: []byte("fake-nym-key"),
			Proof:        []byte("fake-proof"),
			Schema:       "test-schema",
		}
		identityBytes, _ := proto.Marshal(serialized)

		return identityBytes
	}

	// Helper to create base audit info
	createAuditInfo := func() *AuditInfo {
		return &AuditInfo{
			Attributes: [][]byte{
				[]byte("attr0"),
				[]byte("attr1"),
				[]byte("enrollment-id"),
				[]byte("revocation-handle"),
			},
			Schema: "test-schema",
		}
	}

	t.Run("Invalid protobuf identity", func(t *testing.T) {
		auditInfo := createAuditInfo()
		err := auditInfo.Match(context.Background(), []byte("invalid protobuf"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not deserialize a SerializedIdemixIdentity")
	})

	t.Run("SchemaManager errors", func(t *testing.T) {
		tests := []struct {
			name           string
			setupMock      func(*mock.SchemaManager, *mock.BCCSP)
			setupAuditInfo func(*AuditInfo)
			expectedError  string
		}{
			{
				name: "EidNymAuditOpts error",
				setupMock: func(sm *mock.SchemaManager, _ *mock.BCCSP) {
					// force sm.EidNymAuditOpts(..) called by Match to return this error pair
					sm.EidNymAuditOptsReturns(nil, errors.New("schema error"))
				},
				setupAuditInfo: func(ai *AuditInfo) {},
				expectedError:  "error while getting a RhNymAuditOpts",
			},
			{
				name: "RhNymAuditOpts error",
				setupMock: func(sm *mock.SchemaManager, csp *mock.BCCSP) {
					// force sm.EidNymAuditOpts(..) called by Match to return this pair
					sm.EidNymAuditOptsReturns(&idemix.EidNymAuditOpts{EidIndex: 2, EnrollmentID: "enrollment-id"}, nil)
					// force sm.RhNymAuditOpts(..) called by Match to return this error pair
					sm.RhNymAuditOptsReturns(nil, errors.New("rh schema error"))
					// force csp.Verify called by Match to verify true with err=nil
					csp.VerifyReturns(true, nil)
				},
				setupAuditInfo: func(ai *AuditInfo) {
					ai.EidNymAuditData = &idemix.AttrNymAuditData{}
					ai.RhNymAuditData = &idemix.AttrNymAuditData{}
				},
				expectedError: "error while getting a RhNymAuditOpts",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				mockCsp := &mock.BCCSP{}
				mockSchemaManager := &mock.SchemaManager{}
				tt.setupMock(mockSchemaManager, mockCsp)

				auditInfo := createAuditInfo()
				auditInfo.Csp = mockCsp
				auditInfo.IssuerPublicKey = &mock.Key{}
				auditInfo.SchemaManager = mockSchemaManager
				tt.setupAuditInfo(auditInfo)

				err := auditInfo.Match(context.Background(), createIdentity())
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			})
		}
	})

	t.Run("EidNymAuditData is nil - panic", func(t *testing.T) {
		mockSchemaManager := &mock.SchemaManager{}
		// force sm.EidNymAuditOpts(..) called by Match to return this valid pair
		mockSchemaManager.EidNymAuditOptsReturns(&idemix.EidNymAuditOpts{EidIndex: 2, EnrollmentID: "enrollment-id"}, nil)

		auditInfo := createAuditInfo()
		auditInfo.SchemaManager = mockSchemaManager
		auditInfo.EidNymAuditData = nil // cause Match to panic due to nil EidNymAuditData

		assert.Panics(t, func() {
			_ = auditInfo.Match(context.Background(), createIdentity())
		})
	})

	t.Run("BCCSP Verify errors and failures", func(t *testing.T) {
		tests := []struct {
			name           string
			setupMock      func(*mock.BCCSP, *mock.SchemaManager)
			setupAuditInfo func(*AuditInfo)
			expectedError  string
		}{
			{
				name: "EID verify error",
				setupMock: func(csp *mock.BCCSP, sm *mock.SchemaManager) {
					// force sm.EidNymAuditOpts(..) called by Match to return this valid pair
					sm.EidNymAuditOptsReturns(&idemix.EidNymAuditOpts{EidIndex: 2, EnrollmentID: "enrollment-id"}, nil)
					// force csp.Verify called by Match to verify false with this err
					csp.VerifyReturns(false, errors.New("verify failed"))
				},
				setupAuditInfo: func(ai *AuditInfo) {
					ai.EidNymAuditData = &idemix.AttrNymAuditData{}
				},
				expectedError: "error while verifying the nym eid",
			},
			{
				name: "EID verify returns false",
				setupMock: func(csp *mock.BCCSP, sm *mock.SchemaManager) {
					// force sm.EidNymAuditOpts(..) called by Match to return this valid pair
					sm.EidNymAuditOptsReturns(&idemix.EidNymAuditOpts{EidIndex: 2, EnrollmentID: "enrollment-id"}, nil)
					// force csp.Verify called by Match to verify false with err=nil
					csp.VerifyReturns(false, nil)
				},
				setupAuditInfo: func(ai *AuditInfo) {
					ai.EidNymAuditData = &idemix.AttrNymAuditData{}
				},
				expectedError: "invalid nym rh",
			},
			{
				name: "RH verify error",
				setupMock: func(csp *mock.BCCSP, sm *mock.SchemaManager) {
					// force sm.EidNymAuditOpts(..) called by Match to return this valid pair
					sm.EidNymAuditOptsReturns(&idemix.EidNymAuditOpts{EidIndex: 2, EnrollmentID: "enrollment-id"}, nil)
					// force sm.RhNymAuditOpts(..) called by Match to return this error pair
					sm.RhNymAuditOptsReturns(&idemix.RhNymAuditOpts{RhIndex: 3, RevocationHandle: "revocation-handle"}, nil)
					csp.VerifyReturnsOnCall(0, true, nil)                             // Audit EID verify result
					csp.VerifyReturnsOnCall(1, false, errors.New("rh verify failed")) // Audit RH verify result
				},
				setupAuditInfo: func(ai *AuditInfo) {
					ai.EidNymAuditData = &idemix.AttrNymAuditData{}
					ai.RhNymAuditData = &idemix.AttrNymAuditData{}
				},
				expectedError: "error while verifying the nym rh",
			},
			{
				name: "RH verify returns false",
				setupMock: func(csp *mock.BCCSP, sm *mock.SchemaManager) {
					sm.EidNymAuditOptsReturns(&idemix.EidNymAuditOpts{EidIndex: 2, EnrollmentID: "enrollment-id"}, nil)
					sm.RhNymAuditOptsReturns(&idemix.RhNymAuditOpts{RhIndex: 3, RevocationHandle: "revocation-handle"}, nil)
					csp.VerifyReturnsOnCall(0, true, nil)  // Audit EID verify result
					csp.VerifyReturnsOnCall(1, false, nil) // Audit RH verify result
				},
				setupAuditInfo: func(ai *AuditInfo) {
					ai.EidNymAuditData = &idemix.AttrNymAuditData{}
					ai.RhNymAuditData = &idemix.AttrNymAuditData{}
				},
				expectedError: "invalid nym eid",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				mockCsp := &mock.BCCSP{}
				mockSchemaManager := &mock.SchemaManager{}
				tt.setupMock(mockCsp, mockSchemaManager)

				auditInfo := createAuditInfo()
				auditInfo.Csp = mockCsp
				auditInfo.IssuerPublicKey = &mock.Key{}
				auditInfo.SchemaManager = mockSchemaManager
				tt.setupAuditInfo(auditInfo)

				err := auditInfo.Match(context.Background(), createIdentity())
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			})
		}
	})

	t.Run("Successful match", func(t *testing.T) {
		mockCsp := &mock.BCCSP{}
		mockSchemaManager := &mock.SchemaManager{}

		// help Match to pass: setup valid EidNymAuditOpts & RhNymAuditOpts and force Verify to accept
		mockSchemaManager.EidNymAuditOptsReturns(&idemix.EidNymAuditOpts{EidIndex: 2, EnrollmentID: "enrollment-id"}, nil)
		mockSchemaManager.RhNymAuditOptsReturns(&idemix.RhNymAuditOpts{RhIndex: 3, RevocationHandle: "revocation-handle"}, nil)
		mockCsp.VerifyReturns(true, nil)

		auditInfo := createAuditInfo()
		auditInfo.Csp = mockCsp
		auditInfo.IssuerPublicKey = &mock.Key{}
		auditInfo.SchemaManager = mockSchemaManager
		auditInfo.EidNymAuditData = &idemix.AttrNymAuditData{}
		auditInfo.RhNymAuditData = &idemix.AttrNymAuditData{}

		err := auditInfo.Match(context.Background(), createIdentity())
		require.NoError(t, err)
		assert.Equal(t, 2, mockCsp.VerifyCallCount())
	})
}
