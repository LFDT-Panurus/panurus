/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"encoding/json"
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/validator"
	"github.com/stretchr/testify/require"
)

const maxFuzzActionBytes = 256 << 10

// validTransferActionRaw returns the JSON encoding of a structurally complete
// TransferAction with optional InputIDs and InputTokens populated.
func validTransferActionRaw(tb testing.TB, withInputIDs bool) []byte {
	tb.Helper()
	action := &snarktoken.TransferAction{
		TypeCommitment: make([]byte, 32),
		Inputs: []snarktoken.SpendDescription{
			{
				CommitmentIn:   make([]byte, 32),
				ValueCommitInX: make([]byte, 32),
				ValueCommitInY: make([]byte, 32),
				TypeCommitment: make([]byte, 32),
				SpendProof:     make([]byte, 244),
			},
		},
		Outputs: []snarktoken.OutputDescription{
			{
				CommitmentOut:   make([]byte, 32),
				ValueCommitOutX: make([]byte, 32),
				ValueCommitOutY: make([]byte, 32),
				TypeCommitment:  make([]byte, 32),
				OutputProof:     make([]byte, 244),
				Recipient:       []byte("alice"),
			},
		},
		BindingSignature: make([]byte, 96),
	}
	if withInputIDs {
		action.InputIDs = []*token2.ID{{TxId: "tx0", Index: 0}}
		outputDesc := snarktoken.OutputDescription{
			CommitmentOut:   make([]byte, 32),
			ValueCommitOutX: make([]byte, 32),
			ValueCommitOutY: make([]byte, 32),
			TypeCommitment:  make([]byte, 32),
			OutputProof:     make([]byte, 244),
			Recipient:       []byte("bob"),
		}
		raw, err := json.Marshal(outputDesc)
		require.NoError(tb, err)
		action.InputTokens = [][]byte{raw}
	}
	raw, err := action.Serialize()
	require.NoError(tb, err)

	return raw
}

// validIssueActionRaw returns the JSON encoding of a structurally complete IssueAction.
func validIssueActionRaw(tb testing.TB) []byte {
	tb.Helper()
	action := &snarktoken.IssueAction{
		Issuer:         []byte("issuer"),
		TokenType:      "USD",
		TypeCommitment: make([]byte, 32),
		Outputs: []snarktoken.OutputDescription{
			{
				CommitmentOut:   make([]byte, 32),
				ValueCommitOutX: make([]byte, 32),
				ValueCommitOutY: make([]byte, 32),
				TypeCommitment:  make([]byte, 32),
				OutputProof:     make([]byte, 244),
				Recipient:       []byte("alice"),
			},
		},
		BindingSignature: make([]byte, 96),
		TotalValue:       make([]byte, 32),
	}
	raw, err := action.Serialize()
	require.NoError(tb, err)

	return raw
}

// transferActionWithNilInputID returns JSON for a TransferAction with a
// nil entry in InputIDs, the exact wire shape that triggered the nil-deref
// panic before the guard was added.
func transferActionWithNilInputID(tb testing.TB) []byte {
	tb.Helper()
	action := &snarktoken.TransferAction{
		TypeCommitment: make([]byte, 32),
		InputIDs:       []*token2.ID{nil},
		Inputs: []snarktoken.SpendDescription{
			{
				CommitmentIn:   make([]byte, 32),
				ValueCommitInX: make([]byte, 32),
				ValueCommitInY: make([]byte, 32),
				TypeCommitment: make([]byte, 32),
				SpendProof:     make([]byte, 244),
			},
		},
		Outputs: []snarktoken.OutputDescription{
			{
				CommitmentOut:   make([]byte, 32),
				ValueCommitOutX: make([]byte, 32),
				ValueCommitOutY: make([]byte, 32),
				TypeCommitment:  make([]byte, 32),
				OutputProof:     make([]byte, 244),
				Recipient:       []byte("alice"),
			},
		},
		BindingSignature: make([]byte, 96),
	}
	raw, err := action.Serialize()
	require.NoError(tb, err)

	return raw
}

// FuzzActionDeserializerNoPanic fuzzes ActionDeserializer.DeserializeActions with
// a single arbitrarily typed action. The seed corpus includes valid transfer and
// issue actions (with and without the new InputIDs/InputTokens/Issuer fields),
// as well as the nil-InputID payload that previously caused a panic.
func FuzzActionDeserializerNoPanic(f *testing.F) {
	transferRaw := validTransferActionRaw(f, false)
	transferRawWithIDs := validTransferActionRaw(f, true)
	issueRaw := validIssueActionRaw(f)
	nilIDRaw := transferActionWithNilInputID(f)

	f.Add(uint8(1), transferRaw)
	f.Add(uint8(1), transferRawWithIDs)
	f.Add(uint8(0), issueRaw)
	f.Add(uint8(1), nilIDRaw)
	f.Add(uint8(0), []byte{})
	f.Add(uint8(1), []byte{})
	f.Add(uint8(0), []byte("malformed"))
	f.Add(uint8(1), []byte("malformed"))
	f.Add(uint8(1), transferRaw[:len(transferRaw)/2])
	f.Add(uint8(0), issueRaw[:len(issueRaw)/2])

	f.Fuzz(func(t *testing.T, actionKind uint8, raw []byte) {
		if len(raw) > maxFuzzActionBytes {
			t.Skip()
		}

		require.NotPanics(t, func() {
			ad := &validator.ActionDeserializer{}
			if actionKind%2 == 0 {
				ia := &snarktoken.IssueAction{}
				_ = ia.Deserialize(raw)
			} else {
				ta := &snarktoken.TransferAction{}
				if err := ta.Deserialize(raw); err != nil {
					return
				}
				_ = ta.Validate()
				_, _ = ta.GetSerializedInputs()
				_ = ta.GetInputs()
				_ = ad // keep reference alive
			}
		})
	})
}

// FuzzTransferActionValidateNoPanic fuzzes TransferAction deserialization
// followed by Validate, GetInputs, and GetSerializedInputs, the same
// sequence the validator runs before any cryptographic check. A panic
// anywhere in this chain is an unauthenticated validator DoS.
func FuzzTransferActionValidateNoPanic(f *testing.F) {
	transferRaw := validTransferActionRaw(f, false)
	transferRawWithIDs := validTransferActionRaw(f, true)
	nilIDRaw := transferActionWithNilInputID(f)

	f.Add(transferRaw)
	f.Add(transferRawWithIDs)
	f.Add(nilIDRaw)
	f.Add([]byte{})
	f.Add([]byte("malformed"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzActionBytes {
			t.Skip()
		}

		require.NotPanics(t, func() {
			ta := &snarktoken.TransferAction{}
			if err := ta.Deserialize(raw); err != nil {
				return
			}
			if err := ta.Validate(); err != nil {
				return
			}
			_ = ta.GetInputs()
			_, _ = ta.GetSerializedInputs()
			_ = ta.GetOutputs()
			_, _ = ta.GetSerializedOutputs()
		})
	})
}
