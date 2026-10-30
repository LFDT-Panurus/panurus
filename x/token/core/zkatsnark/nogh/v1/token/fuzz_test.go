/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token_test

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
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

// FuzzIssueActionWithInputsDeserializeNoPanic fuzzes IssueAction.Deserialize
// followed by Validate, ExtraSigners and SummarizeUpgradeInputs: an upgrade
// issuance carries legacy ledger tokens chosen by the issuer, which the
// validator, the auditor and every endorser parse.
func FuzzIssueActionWithInputsDeserializeNoPanic(f *testing.F) {
	owner := []byte("owner")
	fab, err := (&actions.Output{Owner: owner, Type: "USD", Quantity: "0x10"}).Serialize()
	if err != nil {
		f.Fatal(err)
	}
	valid := &snarktoken.IssueAction{
		Issuer:         []byte("issuer"),
		TokenType:      "USD",
		Outputs:        []snarktoken.OutputDescription{{Recipient: owner}},
		Inputs:         []*snarktoken.IssueActionInput{{ID: token2.ID{TxId: "tx", Index: 1}, Token: fab}},
		TypeRandomness: make([]byte, 32),
	}
	raw, err := valid.Serialize()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(raw)
	f.Add([]byte{})
	f.Add(raw[:len(raw)/2])
	f.Add([]byte{0x42, 0x02, 0x0a, 0x00}) // input with an empty id message

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzBytes {
			t.Skip()
		}
		a := &snarktoken.IssueAction{}
		if err := a.Deserialize(raw); err != nil {
			return
		}
		_ = a.Validate()
		_ = a.ExtraSigners()
		_ = a.GetInputs()
		_, _ = a.GetSerializedInputs()
		_, _ = snarktoken.SummarizeUpgradeInputs(a.Inputs, 64)
	})
}

// FuzzParseFabtokenOutputNoPanic fuzzes the fabtoken parser shared by both
// upgrade paths with arbitrary ledger token bytes.
func FuzzParseFabtokenOutputNoPanic(f *testing.F) {
	fab, err := (&actions.Output{Owner: []byte("owner"), Type: "USD", Quantity: "0x10"}).Serialize()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(fab)
	f.Add([]byte{})
	f.Add(fab[:len(fab)/2])
	f.Add([]byte("not asn1"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzBytes {
			t.Skip()
		}
		_ = snarktoken.IsFabtokenOutput(raw)
		_, _ = snarktoken.ParseFabtokenOutput(raw, 64)
		_, _ = snarktoken.ParseUpgradeInput(raw, 16)
	})
}
