/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// package htlc (internal) to access unexported recipientAsScript
package htlc

import (
	"crypto"
	"encoding/json"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/stretchr/testify/require"
)

// recipientAsScript derives the image of a freshly sampled pre-image, so it must reject a hash or
// encoding function that is not linked into the binary rather than use it: crypto.Hash.New()
// panics on one, encoding.Encoding.New() returns nil, and a script naming either could never be
// claimed by any verifier. The identifiers reach it from the caller's WithHashFunc /
// WithHashEncoding transfer options, which validate nothing.
func TestRecipientAsScriptRejectsUnavailableHashInfo(t *testing.T) {
	for _, tc := range []struct {
		name         string
		hashFunc     crypto.Hash
		hashEncoding encoding.Encoding
		errMsg       string
	}{
		{
			// crypto.MD4 is a real crypto.Hash constant whose implementation is not linked in.
			name:         "hash function not linked into the binary",
			hashFunc:     crypto.MD4,
			hashEncoding: encoding.None,
			errMsg:       "hash function not available",
		},
		{
			name:         "hash function out of range",
			hashFunc:     crypto.Hash(999),
			hashEncoding: encoding.None,
			errMsg:       "hash function not available",
		},
		{
			name:         "encoding out of range",
			hashFunc:     crypto.SHA256,
			hashEncoding: encoding.Encoding(99),
			errMsg:       "encoding function not available",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &Transaction{}

			var err error
			require.NotPanics(t, func() {
				_, _, _, err = tx.recipientAsScript(
					[]byte("sender"), []byte("recipient"), time.Hour, nil, tc.hashFunc, tc.hashEncoding,
				)
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.errMsg)
		})
	}
}

// A caller-supplied hash short-circuits the sampling branch, but the script still records the hash
// info a claimant will have to reproduce, so an unusable function must be rejected on this path too.
func TestRecipientAsScriptRejectsUnavailableHashInfoWithCallerHash(t *testing.T) {
	tx := &Transaction{}

	var err error
	require.NotPanics(t, func() {
		_, _, _, err = tx.recipientAsScript(
			[]byte("sender"), []byte("recipient"), time.Hour,
			[]byte("caller-supplied-hash"), crypto.SHA256, encoding.Encoding(99),
		)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "encoding function not available")
}

// The happy path: every available encoding yields a script whose recorded hash is exactly the image
// of the returned pre-image, so the recipient can recompute it. encoding.None must stay a no-op.
func TestRecipientAsScriptSamplesAConsistentImage(t *testing.T) {
	for _, enc := range []encoding.Encoding{encoding.None, encoding.Base64, encoding.Hex} {
		t.Run(enc.String(), func(t *testing.T) {
			tx := &Transaction{}

			raw, preImage, script, err := tx.recipientAsScript(
				[]byte("sender"), []byte("recipient"), time.Hour, nil, crypto.SHA256, enc,
			)
			require.NoError(t, err)
			require.NotEmpty(t, preImage)

			// the script's hash is the image of the pre-image handed back to the caller
			image, err := script.HashInfo.Image(preImage)
			require.NoError(t, err)
			require.Equal(t, image, script.HashInfo.Hash)
			require.NoError(t, script.HashInfo.Compare(image))

			// and the hash info survives the round trip through the owner identity
			typed, err := identity.UnmarshalTypedIdentity(raw)
			require.NoError(t, err)
			require.Equal(t, ScriptType, typed.Type)
			decoded := &Script{}
			require.NoError(t, json.Unmarshal(typed.Identity, decoded))
			require.Equal(t, script.HashInfo, decoded.HashInfo)
		})
	}
}

// A caller-supplied hash is recorded verbatim: it is the image of a pre-image only the counterparty
// knows (the second leg of a swap), so no pre-image is sampled for it.
func TestRecipientAsScriptKeepsCallerHash(t *testing.T) {
	tx := &Transaction{}
	hash := []byte("hash-from-the-first-leg")

	_, preImage, script, err := tx.recipientAsScript(
		[]byte("sender"), []byte("recipient"), time.Hour, hash, crypto.SHA256, encoding.Base64,
	)
	require.NoError(t, err)
	require.Empty(t, preImage)
	require.Equal(t, hash, script.HashInfo.Hash)
}
