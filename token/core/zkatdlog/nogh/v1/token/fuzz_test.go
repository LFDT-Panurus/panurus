/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/stretchr/testify/require"
)

const maxFuzzTokenBytes = 256 << 10

// FuzzTokenDeserializeNoPanic fuzzes Token.Deserialize with arbitrary bytes.
// This is reached directly from ledger-stored, attacker-influenced output
// bytes via driver.TokenDeserializer.DeserializeToken (deserializer.go) and
// TokensService.getOutput (service.go), so any panic here is an
// unauthenticated DoS against every caller that reads token outputs.
func FuzzTokenDeserializeNoPanic(f *testing.F) {
	curve := math.Curves[math.BN254]
	rand, err := curve.Rand()
	require.NoError(f, err)

	tok := &Token{
		Owner: []byte("owner1"),
		Data:  curve.GenG1.Mul(curve.NewRandomZr(rand)),
	}
	raw, err := tok.Serialize()
	require.NoError(f, err)

	f.Add(raw)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add(raw[:len(raw)/2])

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzTokenBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_ = (&Token{}).Deserialize(raw)
		})
	})
}

// FuzzMetadataDeserializeNoPanic fuzzes Metadata.Deserialize with arbitrary
// bytes. This is reached directly from ledger-stored metadata bytes via
// driver.TokenDeserializer.DeserializeMetadata (deserializer.go) and
// TokensService.Deobfuscate/getOutput (service.go), so any panic here is an
// unauthenticated DoS against every caller that reads token metadata.
func FuzzMetadataDeserializeNoPanic(f *testing.F) {
	curve := math.BN254
	c := math.Curves[curve]
	rand, err := c.Rand()
	require.NoError(f, err)

	meta := NewMetadata(curve, "COIN", []uint64{10}, []*math.Zr{c.NewRandomZr(rand)})[0]
	meta.Issuer = []byte("issuer")
	raw, err := meta.Serialize()
	require.NoError(f, err)

	f.Add(raw)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add(raw[:len(raw)/2])

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzTokenBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_ = (&Metadata{}).Deserialize(raw)
		})
	})
}
