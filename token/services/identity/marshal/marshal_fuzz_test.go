/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package marshal_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity/marshal"
	"github.com/stretchr/testify/require"
)

const maxFuzzDecodeIdentityBytes = 64 << 10

// FuzzDecodeIdentityNoPanic hunts for malformed DER that panics DecodeIdentity
// instead of returning an error. DecodeIdentity is the base ASN.1 parser
// underlying every identity type's deserialization path (x509, idemix,
// idemixnym, multisig, htlc all route through it via typed.go).
//
// For the integer variant it also asserts canonicality: an accepted encoding
// must be the *only* encoding of the identity it decoded to, so re-encoding the
// result has to reproduce the input byte-for-byte. That covers both the
// non-minimal length and the non-minimal integer-content vectors at once, since
// either one produces a second accepted spelling and therefore a second
// Identity.UniqueID() cache slot for one logical identity.
//
// The invariant is asserted for the integer variant only, because the string
// variant is deliberately many-to-one: DecodeIdentity folds UTF8String "x509",
// PrintableString "x509" and INTEGER 2 onto the same identity for legacy
// compatibility, so re-encoding a string spelling cannot reproduce its input.
func FuzzDecodeIdentityNoPanic(f *testing.F) {
	f.Add(marshal.EncodeIdentity(1, []byte("payload")))
	f.Add(marshal.Encode(marshal.Result{Str: "hello", Data: []byte("data")}))
	f.Add([]byte{})
	f.Add([]byte("not asn1"))
	f.Add([]byte{0x30, 0x81})
	f.Add([]byte{0x30, 0x02, 0x02, 0x84, 0xFF, 0xFF, 0xFF, 0xFF})
	// Non-minimal length: outer SEQUENCE length 6 written as 0x81 0x06.
	f.Add([]byte{0x30, 0x81, 0x06, 0x02, 0x01, 0x01, 0x04, 0x01, 0xAA})
	// Non-minimal integer contents: the type field 5 written as 0x00 0x05.
	f.Add([]byte{0x30, 0x07, 0x02, 0x02, 0x00, 0x05, 0x04, 0x01, 0xAA})

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzDecodeIdentityBytes {
			t.Skip()
		}
		var res marshal.Result
		var err error
		require.NotPanics(t, func() {
			res, err = marshal.DecodeIdentity(raw)
		})
		// Str stays empty only when the first element really was an INTEGER:
		// the legacy fold sets IsInt while leaving the decoded Str in place, so
		// this is what separates the one-to-one variant from the many-to-one
		// one without having to re-walk raw's tags.
		if err != nil || !res.IsInt || res.Str != "" {
			return
		}
		require.Equal(t, raw, marshal.EncodeIdentity(res.Int32, res.Data),
			"accepted bytes must be the canonical encoding of the decoded identity")
	})
}
