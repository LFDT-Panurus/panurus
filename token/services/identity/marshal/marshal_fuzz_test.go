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
func FuzzDecodeIdentityNoPanic(f *testing.F) {
	f.Add(marshal.EncodeIdentity(1, []byte("payload")))
	f.Add(marshal.Encode(marshal.Result{Str: "hello", Data: []byte("data")}))
	f.Add([]byte{})
	f.Add([]byte("not asn1"))
	f.Add([]byte{0x30, 0x81})
	f.Add([]byte{0x30, 0x02, 0x02, 0x84, 0xFF, 0xFF, 0xFF, 0xFF})

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzDecodeIdentityBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_, _ = marshal.DecodeIdentity(raw)
		})
	})
}
