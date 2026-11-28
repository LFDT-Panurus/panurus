/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package setup

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/stretchr/testify/require"
)

const maxFuzzPublicParamsBytes = 256 << 10

// FuzzPublicParamsDeserializeNoPanic fuzzes NewPublicParamsFromBytes and Validate with arbitrary bytes.
// Public parameters are read from the ledger by every node, so a panic here is an unauthenticated DoS.
func FuzzPublicParamsDeserializeNoPanic(f *testing.F) {
	pp, err := NewWith(FabTokenDriverName, ProtocolV1, 64)
	require.NoError(f, err)
	raw, err := pp.Serialize()
	require.NoError(f, err)
	f.Add(raw)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add(raw[:len(raw)/2])

	root := crypto.NewTestCertificate(f, crypto.NewTestCATemplate("root"), nil)
	intermediate := crypto.NewTestCertificate(f, crypto.NewTestCATemplate("int"), root)
	pp.X509OwnerRoots = [][]byte{root.PEM}
	pp.X509OwnerIntermediates = [][]byte{intermediate.PEM}
	withRoots, err := pp.Serialize()
	require.NoError(f, err)
	f.Add(withRoots)
	f.Add(withRoots[:len(withRoots)-10])
	// intermediates without roots
	pp.X509OwnerRoots = nil
	noRoots, err := pp.Serialize()
	require.NoError(f, err)
	f.Add(noRoots)

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzPublicParamsBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			pp, err := NewPublicParamsFromBytes(raw, FabTokenDriverName, ProtocolV1)
			if err == nil {
				_ = pp.Validate()
			}
		})
	})
}
