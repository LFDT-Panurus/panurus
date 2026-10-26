/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token_test

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/stretchr/testify/require"

	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

const maxFuzzBytes = 256 << 10

// FuzzNoteDeserializeNoPanic fuzzes Note deserialization (which supports both
// protobuf and legacy binary formats) with arbitrary bytes.
func FuzzNoteDeserializeNoPanic(f *testing.F) {
	note, err := snarktoken.NewRandomNote(100, "USD", ecc.BN254)
	require.NoError(f, err)
	note.Issuer = []byte("issuer")

	rawProto, err := note.Serialize()
	require.NoError(f, err)

	f.Add(rawProto)
	f.Add([]byte{})
	f.Add([]byte("malformed-bytes"))
	f.Add(rawProto[:len(rawProto)/2])

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_, _ = snarktoken.Deserialize(raw)
		})
	})
}

// FuzzOutputDescriptionDeserializeNoPanic fuzzes DeserializeOutputDescription with arbitrary bytes.
func FuzzOutputDescriptionDeserializeNoPanic(f *testing.F) {
	out := &snarktoken.OutputDescription{
		CommitmentOut:   make([]byte, 32),
		ValueCommitOutX: make([]byte, 32),
		ValueCommitOutY: make([]byte, 32),
		TypeCommitment:  make([]byte, 32),
		OutputProof:     make([]byte, 292),
		Recipient:       []byte("bob"),
	}
	raw, err := out.Serialize()
	require.NoError(f, err)

	f.Add(raw)
	f.Add([]byte{})
	f.Add([]byte("malformed-bytes"))
	f.Add(raw[:len(raw)/2])

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_, _ = snarktoken.DeserializeOutputDescription(raw)
		})
	})
}

// FuzzMigrationActionDeserializeNoPanic fuzzes MigrationAction.Deserialize with arbitrary bytes.
func FuzzMigrationActionDeserializeNoPanic(f *testing.F) {
	action := &snarktoken.MigrationAction{
		CommitmentPedersenX: make([]byte, 48),
		CommitmentPedersenY: make([]byte, 48),
		CommitmentMiMC:      make([]byte, 32),
		ValueCommitOutX:     make([]byte, 32),
		ValueCommitOutY:     make([]byte, 32),
		TypeCommitment:      make([]byte, 32),
		MigrationProof:      make([]byte, 100),
		Recipient:           []byte("alice"),
	}
	raw, err := action.Serialize()
	require.NoError(f, err)

	f.Add(raw)
	f.Add([]byte{})
	f.Add([]byte("malformed-bytes"))
	f.Add(raw[:len(raw)/2])

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			ma := &snarktoken.MigrationAction{}
			_ = ma.Deserialize(raw)
		})
	})
}
