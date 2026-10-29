/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"testing"

	math "github.com/IBM/mathlib"
	noghv1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
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

// Fuzzes an output and its metadata together, where the two meet in curve arithmetic.
// ToClear runs before any proof is verified, so both sides are attacker-controlled.
func FuzzToClearNoPanic(f *testing.F) {
	curve := math.BN254
	c := math.Curves[curve]
	foreign := math.Curves[math.BLS12_381]
	rand, err := c.Rand()
	require.NoError(f, err)
	pp := &noghv1.PublicParams{
		Curve: curve,
		PedersenGenerators: []*math.G1{
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
		},
	}

	meta := &Metadata{
		Type:           "COIN",
		Value:          c.NewZrFromUint64(100),
		BlindingFactor: c.NewZrFromUint64(7),
	}
	data, err := commit([]*math.Zr{
		c.HashToZr([]byte(meta.Type)),
		meta.Value,
		meta.BlindingFactor,
	}, pp.PedersenGenerators, c)
	require.NoError(f, err)

	tokRaw, err := (&Token{Owner: []byte("owner"), Data: data}).Serialize()
	require.NoError(f, err)
	metaRaw, err := meta.Serialize()
	require.NoError(f, err)

	foreignTokRaw, err := (&Token{Owner: []byte("owner"), Data: foreign.GenG1.Copy()}).Serialize()
	require.NoError(f, err)
	foreignMetaRaw, err := (&Metadata{
		Type:           "COIN",
		Value:          foreign.NewZrFromUint64(100),
		BlindingFactor: foreign.NewZrFromUint64(7),
	}).Serialize()
	require.NoError(f, err)

	f.Add(tokRaw, metaRaw)
	// The two cross-curve crashes: foreign G1 commitment, foreign Zr scalars.
	f.Add(foreignTokRaw, metaRaw)
	f.Add(tokRaw, foreignMetaRaw)
	f.Add([]byte{}, metaRaw)
	f.Add(tokRaw, []byte{})
	f.Add([]byte("malformed"), []byte("malformed"))
	f.Add(tokRaw[:len(tokRaw)/2], metaRaw[:len(metaRaw)/2])

	f.Fuzz(func(t *testing.T, tokenRaw, metadataRaw []byte) {
		if len(tokenRaw) > maxFuzzTokenBytes || len(metadataRaw) > maxFuzzTokenBytes {
			t.Skip()
		}

		require.NotPanics(t, func() {
			tok := &Token{}
			if err := tok.Deserialize(tokenRaw); err != nil {
				return
			}
			md := &Metadata{}
			if err := md.Deserialize(metadataRaw); err != nil {
				return
			}
			_, _ = tok.ToClear(md, pp)
		})
	})
}
