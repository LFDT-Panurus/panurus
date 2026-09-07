/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package upgrade

import (
	"os"
	"path/filepath"
	"testing"

	mathlib "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	token2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

const maxFuzzProofBytes = 256 << 10

// FuzzProofDeserializeNoPanic fuzzes Proof.Deserialize with arbitrary bytes.
// Proof is read directly from an untrusted upgrade request via
// Service.checkUpgradeProof (service.go), so any panic here is an
// unauthenticated DoS against the token-upgrade flow.
func FuzzProofDeserializeNoPanic(f *testing.F) {
	p := &Proof{
		Challenge: []byte("test-challenge"),
		Tokens: []token.LedgerToken{{
			ID:            token.ID{TxId: "tx1", Index: 1},
			Token:         []byte("token1"),
			TokenMetadata: []byte("meta1"),
			Format:        token.Format("token format1"),
		}},
		Signatures: []Signature{
			[]byte("sig1"),
		},
	}
	raw, err := p.Serialize()
	require.NoError(f, err)

	// a proof for a commitment token also declares the public parameters that produced it
	withPPHashes := *p
	withPPHashes.PublicParamsHashes = []driver.PPHash{[]byte("a public params hash")}
	rawWithPPHashes, err := withPPHashes.Serialize()
	require.NoError(f, err)

	f.Add(raw)
	f.Add(rawWithPPHashes)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add([]byte("{"))
	f.Add(raw[:len(raw)/2])
	f.Add(rawWithPPHashes[:len(rawWithPPHashes)/2])

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzProofBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_ = (&Proof{}).Deserialize(raw)
		})
	})
}

// FuzzProcessCommTokenNoPanic fuzzes the commitment and the opening of a token carried by an
// upgrade request. Both are attacker-controlled bytes that Service.processCommToken deserializes
// and then feeds to commitment arithmetic, which type-asserts its arguments, so a panic here is
// an unauthenticated DoS against the token-upgrade flow: the request is processed before any
// signature is verified.
func FuzzProcessCommTokenNoPanic(f *testing.F) {
	idemixPK, err := os.ReadFile(filepath.Join("..", "..", "setup", "testdata", "idemix", "msp", "IssuerPublicKey"))
	require.NoError(f, err)
	pp, err := setup.Setup(32, idemixPK, mathlib.BN254)
	require.NoError(f, err)
	curve := mathlib.Curves[pp.Curve]

	// a well-formed commitment together with the opening that produces it
	commitments, metadata, err := token2.GetTokensWithWitness([]uint64{42}, token.Type("USD"), pp.PedersenGenerators, curve)
	require.NoError(f, err)
	output := &token2.Token{Owner: []byte("owner"), Data: commitments[0]}
	tokenRaw, err := output.Serialize()
	require.NoError(f, err)
	openingRaw, err := metadata[0].Serialize()
	require.NoError(f, err)

	// the same opening, but with scalars from another curve: the historical shape that panicked
	// inside the commitment arithmetic before the opening was validated against pp.Curve
	foreign := mathlib.Curves[mathlib.BLS12_381]
	foreignOpening := &token2.Metadata{
		Type:           token.Type("USD"),
		Value:          foreign.NewZrFromInt(42),
		BlindingFactor: foreign.NewZrFromInt(1),
	}
	foreignOpeningRaw, err := foreignOpening.Serialize()
	require.NoError(f, err)

	f.Add(tokenRaw, openingRaw)
	f.Add(tokenRaw, foreignOpeningRaw)
	f.Add(tokenRaw, []byte{})
	f.Add([]byte{}, openingRaw)
	f.Add([]byte{}, []byte{})
	f.Add([]byte("malformed"), []byte("malformed"))
	f.Add(tokenRaw[:len(tokenRaw)/2], openingRaw[:len(openingRaw)/2])

	s, err := NewService(nil, pp.QuantityPrecision, nil, nil, nil, nil)
	require.NoError(f, err)

	f.Fuzz(func(t *testing.T, tokenRaw, openingRaw []byte) {
		if len(tokenRaw) > maxFuzzProofBytes || len(openingRaw) > maxFuzzProofBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_, _ = s.processCommToken(token.LedgerToken{
				ID:            token.ID{TxId: "tx", Index: 0},
				Token:         tokenRaw,
				TokenMetadata: openingRaw,
			}, pp)
		})
	})
}
