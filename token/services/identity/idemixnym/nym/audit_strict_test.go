/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nym

import (
	"encoding/json"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFromBytes_RejectsUnknownFields covers the JSON strictness asymmetry reported in issue #2073:
// FromBytes used stdlib encoding/json while the embedded crypto.AuditInfo is parsed with the
// project's DisallowUnknownFields wrapper on every other entry point, so the same logical type was
// laxer when it arrived through the nym wrapper.
func TestFromBytes_RejectsUnknownFields(t *testing.T) {
	_, err := DeserializeAuditInfo([]byte(`{"Attributes":[],"NotAField":1}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")

	err = (&AuditInfo{}).FromBytes([]byte(`{"IdemixSignature":"AAAA","NotAField":1}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")
}

// TestFromBytes_RejectsNilEmbeddedAuditInfo covers the missing nil-embedded-pointer guard reported
// in issue #2073: a payload that carries none of the promoted crypto.AuditInfo fields left the
// embedded pointer nil, and the exported FromBytes returned success, so any caller dereferencing it
// through a promoted method or field panicked.
func TestFromBytes_RejectsNilEmbeddedAuditInfo(t *testing.T) {
	// Nothing but the nym-level field: encoding/json never allocates the embedded pointer.
	raw, err := json.Marshal(map[string]any{"IdemixSignature": []byte("sig")})
	require.NoError(t, err)

	ai := &AuditInfo{}
	err = ai.FromBytes(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no audit info found")

	// The same payload through DeserializeAuditInfo, which is where the guard already existed.
	result, err := DeserializeAuditInfo(raw)
	require.Error(t, err)
	assert.Nil(t, result)
}

// TestFromBytes_PopulatesEmbeddedAuditInfo pins the accepted shape: as soon as the payload carries a
// promoted crypto.AuditInfo field, the embedded pointer is allocated and FromBytes succeeds.
func TestFromBytes_PopulatesEmbeddedAuditInfo(t *testing.T) {
	original := &AuditInfo{
		AuditInfo: &crypto.AuditInfo{
			Attributes: [][]byte{[]byte("a0"), []byte("a1"), []byte("eid"), []byte("rh")},
			Schema:     "",
		},
		IdemixSignature: []byte("sig"),
	}
	raw, err := json.Marshal(original)
	require.NoError(t, err)

	ai := &AuditInfo{}
	require.NoError(t, ai.FromBytes(raw))
	require.NotNil(t, ai.AuditInfo)
	assert.Equal(t, "eid", ai.EnrollmentID())
	assert.Equal(t, []byte("sig"), ai.IdemixSignature)
}
