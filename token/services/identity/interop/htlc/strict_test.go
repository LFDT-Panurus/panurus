/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc_test

import (
	"crypto"
	"encoding/json"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/identity"
	ihtlc "github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc/mock"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScriptInfo_UnmarshalRejectsUnknownFields covers the strictness asymmetry reported in issue
// #2073: info.go parsed ScriptInfo with stdlib encoding/json while deserializer.go parses the same
// wire type with the project's DisallowUnknownFields wrapper.
func TestScriptInfo_UnmarshalRejectsUnknownFields(t *testing.T) {
	si := &ihtlc.ScriptInfo{}
	err := si.Unmarshal([]byte(`{"Sender":"cw==","Recipient":"cg==","Extra":1}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")

	// The well-formed payload still round-trips.
	raw, err := (&ihtlc.ScriptInfo{Sender: []byte("s"), Recipient: []byte("r")}).Marshal()
	require.NoError(t, err)
	require.NoError(t, si.Unmarshal(raw))
	assert.Equal(t, []byte("s"), si.Sender)
	assert.Equal(t, []byte("r"), si.Recipient)
}

// TestVerifyOwner_RejectsUnknownScriptFields covers the same asymmetry on the validator side: the
// htlc.Script carried by a TypedIdentity must be parsed as strictly here as in deserializer.go, so
// the validator cannot accept an identity payload the deserializer would reject.
func TestVerifyOwner_RejectsUnknownScriptFields(t *testing.T) {
	deadline := time.Now().Add(time.Hour)
	script := &htlc.Script{Sender: []byte("s"), Recipient: []byte("r"), Deadline: deadline}
	raw, err := json.Marshal(script)
	require.NoError(t, err)

	// Splice an extra field into the serialized script.
	var asMap map[string]any
	require.NoError(t, json.Unmarshal(raw, &asMap))
	asMap["NotAField"] = 1
	tampered, err := json.Marshal(asMap)
	require.NoError(t, err)

	typed, err := identity.WrapWithType(ihtlc.ScriptType, tampered)
	require.NoError(t, err)

	_, _, err = ihtlc.VerifyOwner(typed, []byte("r"), time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")

	// The untampered script is still accepted.
	typed, err = identity.WrapWithType(ihtlc.ScriptType, raw)
	require.NoError(t, err)
	parsed, op, err := ihtlc.VerifyOwner(typed, []byte("r"), time.Now())
	require.NoError(t, err)
	assert.Equal(t, ihtlc.Claim, op)
	assert.Equal(t, script.Recipient, parsed.Recipient)
}

// TestMetadataClaimKeyCheck_RejectsUnknownClaimSignatureFields covers the ClaimSignature decode in
// the validator, which shares the strictness change.
func TestMetadataClaimKeyCheck_RejectsUnknownClaimSignatureFields(t *testing.T) {
	script := &htlc.Script{Sender: []byte("s"), Recipient: []byte("r"), HashInfo: htlc.HashInfo{Hash: []byte("h")}}
	act := &mock.Action{}
	act.GetMetadataReturns(map[string][]byte{})

	_, err := ihtlc.MetadataClaimKeyCheck(act, script, ihtlc.Claim,
		[]byte(`{"RecipientSignature":"c2ln","Preimage":"cHJl","NotAField":1}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")
}

// TestMetadataClaimKeyCheck_PreimageComparison pins the behaviour of the constant-time preimage
// comparison: an exact match is accepted, and any difference - including a same-length one - is
// rejected.
func TestMetadataClaimKeyCheck_PreimageComparison(t *testing.T) {
	script := &htlc.Script{
		Sender:    []byte("s"),
		Recipient: []byte("r"),
		HashInfo: htlc.HashInfo{
			Hash:         []byte("h"),
			HashFunc:     crypto.SHA256,
			HashEncoding: encoding.Base64,
		},
	}
	pre := []byte("preimage")
	image, err := script.HashInfo.Image(pre)
	require.NoError(t, err)
	key := htlc.ClaimKey(image)

	sig, err := json.Marshal(&htlc.ClaimSignature{RecipientSignature: []byte("sig"), Preimage: pre})
	require.NoError(t, err)

	for _, tc := range []struct {
		name  string
		value []byte
		ok    bool
	}{
		{"exact match", []byte("preimage"), true},
		{"same length, last byte differs", []byte("preimagf"), false},
		{"same length, first byte differs", []byte("Preimage"), false},
		{"prefix only", []byte("pre"), false},
		{"longer", []byte("preimage!"), false},
		{"empty", []byte{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			act := &mock.Action{}
			act.GetMetadataReturns(map[string][]byte{key: tc.value})
			k, err := ihtlc.MetadataClaimKeyCheck(act, script, ihtlc.Claim, sig)
			if tc.ok {
				require.NoError(t, err)
				assert.Equal(t, key, k)

				return
			}
			require.Error(t, err)
		})
	}
}
