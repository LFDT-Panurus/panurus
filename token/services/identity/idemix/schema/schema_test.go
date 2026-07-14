/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package schema

import (
	"testing"

	bccsp "github.com/IBM/idemix/bccsp/types"
	"github.com/IBM/idemix/msp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests construction of a default schema manager
func TestNewDefaultManager(t *testing.T) {
	manager := NewDefaultManager()
	require.NotNil(t, manager)
	assert.IsType(t, &DefaultManager{}, manager)
}

// Test that NymSignerOpts indeed returns the options for signing with pseudonyms
// with various valid/invalid schemes
func TestDefaultManager_NymSignerOpts(t *testing.T) {
	manager := NewDefaultManager()

	tests := []struct {
		name          string
		schema        string
		expectedOpts  *bccsp.IdemixNymSignerOpts
		expectedError string
	}{
		{
			name:   "default schema (empty string)",
			schema: "",
			expectedOpts: &bccsp.IdemixNymSignerOpts{
				SKIndex: 0,
			},
			expectedError: "",
		},
		{
			name:   "default schema constant",
			schema: DefaultSchema,
			expectedOpts: &bccsp.IdemixNymSignerOpts{
				SKIndex: 0,
			},
			expectedError: "",
		},
		{
			name:   "w3c-v0.0.1 schema",
			schema: "w3c-v0.0.1",
			expectedOpts: &bccsp.IdemixNymSignerOpts{
				SKIndex: 24,
			},
			expectedError: "",
		},
		{
			name:          "unsupported schema",
			schema:        "unsupported-schema",
			expectedOpts:  nil,
			expectedError: "unsupported schema 'unsupported-schema' for NymSignerOpts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := manager.NymSignerOpts(tt.schema)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Equal(t, tt.expectedError, err.Error())
				assert.Nil(t, opts)
			} else {
				require.NoError(t, err)
				require.NotNil(t, opts)
				assert.Equal(t, tt.expectedOpts.SKIndex, opts.SKIndex)
			}
		})
	}
}

// Test that PublicKeyImportOpts indeed returns the options for
// importing issuer public keys (with the correct attribute names)
// with various valid/invalid schemes
func TestDefaultManager_PublicKeyImportOpts(t *testing.T) {
	manager := NewDefaultManager()

	tests := []struct {
		name              string
		schema            string
		expectedAttrCount int
		expectedAttrNames []string
		expectedTemporary bool
		expectedError     string
	}{
		{
			name:              "default schema",
			schema:            "",
			expectedAttrCount: 4,
			expectedAttrNames: []string{
				msp.AttributeNameOU,
				msp.AttributeNameRole,
				msp.AttributeNameEnrollmentId,
				msp.AttributeNameRevocationHandle,
			},
			expectedTemporary: true,
			expectedError:     "",
		},
		{
			name:              "w3c-v0.0.1 schema",
			schema:            "w3c-v0.0.1",
			expectedAttrCount: len(attributeNames) + 1, // +1 for the empty string prepended
			expectedTemporary: true,
			expectedError:     "",
		},
		{
			name:          "unsupported schema",
			schema:        "invalid-schema",
			expectedError: "unsupported schema 'invalid-schema' for PublicKeyImportOpts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := manager.PublicKeyImportOpts(tt.schema)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Equal(t, tt.expectedError, err.Error())
				assert.Nil(t, opts)
			} else {
				require.NoError(t, err)
				require.NotNil(t, opts)
				assert.Equal(t, tt.expectedTemporary, opts.Temporary)
				assert.Len(t, opts.AttributeNames, tt.expectedAttrCount)

				if tt.expectedAttrNames != nil {
					assert.Equal(t, tt.expectedAttrNames, opts.AttributeNames)
				}

				// For w3c schema, verify the first attribute is empty string
				if tt.schema == "w3c-v0.0.1" {
					assert.Empty(t, opts.AttributeNames[0])
				}
			}
		})
	}
}

// Test that SignerOpts indeed returns the options for creating signatures/proofs
// with the correct attribute positions and hidden attributes
// with various valid/invalid schemes
func TestDefaultManager_SignerOpts(t *testing.T) {
	manager := NewDefaultManager()

	tests := []struct {
		name                 string
		schema               string
		expectedAttrCount    int
		expectedRhIndex      int
		expectedEidIndex     int
		expectedSKIndex      int
		expectedVerification bccsp.VerificationType
		expectedError        string
	}{
		{
			name:                 "default schema",
			schema:               "",
			expectedAttrCount:    4,
			expectedRhIndex:      rhIdx,
			expectedEidIndex:     eidIdx,
			expectedSKIndex:      0,
			expectedVerification: bccsp.VerificationType(0), // default value
			expectedError:        "",
		},
		{
			name:                 "w3c-v0.0.1 schema",
			schema:               "w3c-v0.0.1",
			expectedAttrCount:    len(attributeNames),
			expectedRhIndex:      27,
			expectedEidIndex:     26,
			expectedSKIndex:      24,
			expectedVerification: bccsp.ExpectEidNymRhNym,
			expectedError:        "",
		},
		{
			name:          "unsupported schema",
			schema:        "unknown",
			expectedError: "unsupported schema 'unknown' for NymSignerOpts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := manager.SignerOpts(tt.schema)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Equal(t, tt.expectedError, err.Error())
				assert.Nil(t, opts)
			} else {
				require.NoError(t, err)
				require.NotNil(t, opts)
				assert.Len(t, opts.Attributes, tt.expectedAttrCount)
				assert.Equal(t, tt.expectedRhIndex, opts.RhIndex)
				assert.Equal(t, tt.expectedEidIndex, opts.EidIndex)
				assert.Equal(t, tt.expectedSKIndex, opts.SKIndex)
				assert.Equal(t, tt.expectedVerification, opts.VerificationType)

				// Verify all attributes are hidden
				for i, attr := range opts.Attributes {
					assert.Equal(t, bccsp.IdemixHiddenAttribute, attr.Type,
						"attribute at index %d should be hidden", i)
				}
			}
		})
	}
}

// Test that RhNymAuditOpts indeed returns the options for auditing revocation handle pseudonyms
// with the correct attribute positions with various valid/invalid schemes
func TestDefaultManager_RhNymAuditOpts(t *testing.T) {
	manager := NewDefaultManager()

	tests := []struct {
		name            string
		schema          string
		attrs           [][]byte
		expectedRhIndex int
		expectedSKIndex int
		expectedRhValue string
		expectedError   string
	}{
		{
			name:   "default schema",
			schema: "",
			attrs: [][]byte{
				[]byte("sk"),
				[]byte("ou"),
				[]byte("eid"),
				[]byte("rh-value"),
			},
			expectedRhIndex: rhIdx,
			expectedSKIndex: skIdx,
			expectedRhValue: "rh-value",
			expectedError:   "",
		},
		{
			name:            "w3c-v0.0.1 schema",
			schema:          "w3c-v0.0.1",
			attrs:           make([][]byte, 28), // Need at least 28 elements for index 27
			expectedRhIndex: 27,
			expectedSKIndex: 24,
			expectedRhValue: "",
			expectedError:   "",
		},
		{
			name:          "unsupported schema",
			schema:        "invalid",
			attrs:         [][]byte{},
			expectedError: "unsupported schema 'invalid' for NymSignerOpts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// For w3c schema, set the RH value at the correct index
			if tt.schema == "w3c-v0.0.1" {
				tt.attrs[27] = []byte("w3c-rh-value")
				tt.expectedRhValue = "w3c-rh-value"
			}

			opts, err := manager.RhNymAuditOpts(tt.schema, tt.attrs)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Equal(t, tt.expectedError, err.Error())
				assert.Nil(t, opts)
			} else {
				require.NoError(t, err)
				require.NotNil(t, opts)
				assert.Equal(t, tt.expectedRhIndex, opts.RhIndex)
				assert.Equal(t, tt.expectedSKIndex, opts.SKIndex)
				assert.Equal(t, tt.expectedRhValue, opts.RevocationHandle)
			}
		})
	}
}

// Test that EidNymAuditOpts indeed returns the options for auditing enrollment ID pseudonyms
// with the correct attribute positions with various valid/invalid schemes
func TestDefaultManager_EidNymAuditOpts(t *testing.T) {
	manager := NewDefaultManager()

	tests := []struct {
		name             string
		schema           string
		attrs            [][]byte
		expectedEidIndex int
		expectedSKIndex  int
		expectedEidValue string
		expectedError    string
	}{
		{
			name:   "default schema",
			schema: "",
			attrs: [][]byte{
				[]byte("sk"),
				[]byte("ou"),
				[]byte("enrollment-id"),
				[]byte("rh"),
			},
			expectedEidIndex: eidIdx,
			expectedSKIndex:  skIdx,
			expectedEidValue: "enrollment-id",
			expectedError:    "",
		},
		{
			name:             "w3c-v0.0.1 schema",
			schema:           "w3c-v0.0.1",
			attrs:            make([][]byte, 27), // Need at least 27 elements for index 26
			expectedEidIndex: 26,
			expectedSKIndex:  24,
			expectedEidValue: "",
			expectedError:    "",
		},
		{
			name:          "unsupported schema",
			schema:        "unknown-schema",
			attrs:         [][]byte{},
			expectedError: "unsupported schema 'unknown-schema' for NymSignerOpts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// For w3c schema, set the EID value at the correct index
			if tt.schema == "w3c-v0.0.1" {
				tt.attrs[26] = []byte("w3c-enrollment-id")
				tt.expectedEidValue = "w3c-enrollment-id"
			}

			opts, err := manager.EidNymAuditOpts(tt.schema, tt.attrs)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Equal(t, tt.expectedError, err.Error())
				assert.Nil(t, opts)
			} else {
				require.NoError(t, err)
				require.NotNil(t, opts)
				assert.Equal(t, tt.expectedEidIndex, opts.EidIndex)
				assert.Equal(t, tt.expectedSKIndex, opts.SKIndex)
				assert.Equal(t, tt.expectedEidValue, opts.EnrollmentID)
			}
		})
	}
}

// Test that the global attribute position constants are as expected
func TestConstants(t *testing.T) {
	// Verify the constants are set correctly
	assert.Equal(t, 2, eidIdx, "eidIdx should be 2")
	assert.Equal(t, 3, rhIdx, "rhIdx should be 3")
	assert.Equal(t, 0, skIdx, "skIdx should be 0")
	assert.Empty(t, DefaultSchema, "DefaultSchema should be empty string")
}

// Test that the number of attributes for the `w3c` schema is as expected
// and also verify that selected names are as expected
func TestAttributeNames(t *testing.T) {
	// Verify attributeNames slice has the expected length
	assert.Len(t, attributeNames, 27, "attributeNames should have 27 elements")

	// Verify some key attribute names
	assert.Contains(t, attributeNames[0], "http://www.w3.")
	assert.Contains(t, attributeNames[1], "https://w3id.o")
	assert.Contains(t, attributeNames[23], "cbdccard:2_ou")
	assert.Contains(t, attributeNames[24], "cbdccard:3_rol")
	assert.Contains(t, attributeNames[25], "cbdccard:4_eid")
	assert.Contains(t, attributeNames[26], "cbdccard:5_rh")
}
