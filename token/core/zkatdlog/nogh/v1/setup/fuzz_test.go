/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package setup

import (
	"os"
	"testing"

	math3 "github.com/IBM/mathlib"
	"github.com/stretchr/testify/require"
)

const maxFuzzPublicParamsBytes = 256 << 10

// FuzzPublicParamsDeserializeNoPanic fuzzes PublicParams.Deserialize (via
// NewPublicParamsFromBytes) with arbitrary bytes. Public parameters are read
// directly from the ledger by every validator/prover/verifier on startup and
// on every params update, via driver.PublicParametersDeserializer /
// TokenDriverService, so any panic here is an unauthenticated DoS.
func FuzzPublicParamsDeserializeNoPanic(f *testing.F) {
	issuerPK, err := os.ReadFile("testdata/idemix/msp/IssuerPublicKey")
	require.NoError(f, err)
	require.NotEmpty(f, issuerPK)

	pp, err := Setup(32, issuerPK, math3.BN254)
	require.NoError(f, err)
	raw, err := pp.Serialize()
	require.NoError(f, err)

	f.Add(raw)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add(raw[:len(raw)/2])

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzPublicParamsBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_, _ = NewPublicParamsFromBytes(raw, DLogNoGHDriverName, ProtocolV1)
		})
	})
}
