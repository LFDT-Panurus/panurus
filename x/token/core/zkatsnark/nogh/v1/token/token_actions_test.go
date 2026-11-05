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

func makeOutputDescription(recipient []byte) snarktoken.OutputDescription {
	return snarktoken.OutputDescription{
		CommitmentOut:   make([]byte, 32),
		ValueCommitOutX: make([]byte, 32),
		ValueCommitOutY: make([]byte, 32),
		TypeCommitment:  make([]byte, 32),
		OutputProof:     make([]byte, 292),
		Recipient:       recipient,
	}
}

func makeSpendDescription() snarktoken.SpendDescription {
	return snarktoken.SpendDescription{
		CommitmentIn:   make([]byte, 32),
		ValueCommitInX: make([]byte, 32),
		ValueCommitInY: make([]byte, 32),
		TypeCommitment: make([]byte, 32),
		SpendProof:     make([]byte, 292),
	}
}

func makeValidIssueAction() *snarktoken.IssueAction {
	tc := make([]byte, 32)
	out := makeOutputDescription([]byte("alice"))
	out.TypeCommitment = tc

	return &snarktoken.IssueAction{
		Issuer:           []byte("issuer"),
		TokenType:        "USD",
		TypeCommitment:   tc,
		Outputs:          []snarktoken.OutputDescription{out},
		BindingSignature: make([]byte, 96),
		TotalValue:       make([]byte, 32),
	}
}

func makeValidTransferAction() *snarktoken.TransferAction {
	tc := make([]byte, 32)
	in := makeSpendDescription()
	in.TypeCommitment = tc
	out := makeOutputDescription([]byte("alice"))
	out.TypeCommitment = tc

	return &snarktoken.TransferAction{
		TypeCommitment:   tc,
		Inputs:           []snarktoken.SpendDescription{in},
		Outputs:          []snarktoken.OutputDescription{out},
		BindingSignature: make([]byte, 96),
	}
}

// ── OutputDescription ─────────────────────────────────────────────────────────

func TestOutputDescription_Validate(t *testing.T) {
	o := makeOutputDescription([]byte("alice"))
	require.NoError(t, o.Validate())

	// Empty recipient is valid (redeem)
	oRedeem := makeOutputDescription(nil)
	require.NoError(t, oRedeem.Validate())

	badCm := makeOutputDescription([]byte("alice"))
	badCm.CommitmentOut = make([]byte, 31)
	require.Error(t, badCm.Validate())

	badVx := makeOutputDescription([]byte("alice"))
	badVx.ValueCommitOutX = make([]byte, 33)
	require.Error(t, badVx.Validate())

	badVy := makeOutputDescription([]byte("alice"))
	badVy.ValueCommitOutY = nil
	require.Error(t, badVy.Validate())

	badTc := makeOutputDescription([]byte("alice"))
	badTc.TypeCommitment = make([]byte, 16)
	require.Error(t, badTc.Validate())

	badProof := makeOutputDescription([]byte("alice"))
	badProof.OutputProof = nil
	require.Error(t, badProof.Validate())
}

func TestSpendDescription_Validate(t *testing.T) {
	s := makeSpendDescription()
	require.NoError(t, s.Validate())

	badCm := makeSpendDescription()
	badCm.CommitmentIn = make([]byte, 31)
	require.Error(t, badCm.Validate())

	badVx := makeSpendDescription()
	badVx.ValueCommitInX = nil
	require.Error(t, badVx.Validate())

	badVy := makeSpendDescription()
	badVy.ValueCommitInY = make([]byte, 64)
	require.Error(t, badVy.Validate())

	badTc := makeSpendDescription()
	badTc.TypeCommitment = make([]byte, 30)
	require.Error(t, badTc.Validate())

	badProof := makeSpendDescription()
	badProof.SpendProof = nil
	require.Error(t, badProof.Validate())
}

func TestOutputDescription_IsRedeem_EmptyRecipient(t *testing.T) {
	o := makeOutputDescription(nil)
	require.True(t, o.IsRedeem())
}

func TestOutputDescription_IsRedeem_NonEmptyRecipient(t *testing.T) {
	o := makeOutputDescription([]byte("alice"))
	require.False(t, o.IsRedeem())
}

func TestOutputDescription_GetOwner(t *testing.T) {
	o := makeOutputDescription([]byte("bob"))
	require.Equal(t, []byte("bob"), o.GetOwner())
}

func TestOutputDescription_Serialize(t *testing.T) {
	o := makeOutputDescription([]byte("charlie"))
	raw, err := o.Serialize()
	require.NoError(t, err)

	got, err := snarktoken.DeserializeOutputDescription(raw)
	require.NoError(t, err)
	require.Equal(t, o.Recipient, got.Recipient)
}

// ── IssueAction ───────────────────────────────────────────────────────────────

func TestIssueAction_Validate_Valid(t *testing.T) {
	a := makeValidIssueAction()
	require.NoError(t, a.Validate())
}

func TestIssueAction_Validate_FieldErrors(t *testing.T) {
	t.Run("no outputs", func(t *testing.T) {
		a := makeValidIssueAction()
		a.Outputs = nil
		require.Error(t, a.Validate())
	})

	t.Run("missing issuer", func(t *testing.T) {
		a := makeValidIssueAction()
		a.Issuer = nil
		require.Error(t, a.Validate())
	})

	t.Run("empty token type", func(t *testing.T) {
		a := makeValidIssueAction()
		a.TokenType = ""
		require.Error(t, a.Validate())
	})

	t.Run("invalid type commitment", func(t *testing.T) {
		a := makeValidIssueAction()
		a.TypeCommitment = make([]byte, 31)
		require.Error(t, a.Validate())
	})

	t.Run("invalid total value", func(t *testing.T) {
		a := makeValidIssueAction()
		a.TotalValue = make([]byte, 33)
		require.Error(t, a.Validate())
	})

	t.Run("invalid binding signature", func(t *testing.T) {
		a := makeValidIssueAction()
		a.BindingSignature = make([]byte, 95)
		require.Error(t, a.Validate())
	})

	t.Run("invalid output", func(t *testing.T) {
		a := makeValidIssueAction()
		a.Outputs[0].CommitmentOut = make([]byte, 31)
		require.Error(t, a.Validate())
	})

	t.Run("type commitment mismatch", func(t *testing.T) {
		a := makeValidIssueAction()
		a.Outputs[0].TypeCommitment = make([]byte, 32)
		a.Outputs[0].TypeCommitment[0] = 0xFF
		require.Error(t, a.Validate())
	})
}

func TestIssueAction_NumInputs_IsZero(t *testing.T) {
	a := &snarktoken.IssueAction{Outputs: []snarktoken.OutputDescription{makeOutputDescription(nil)}}
	require.Equal(t, 0, a.NumInputs())
}

func TestIssueAction_GetInputs_IsNil(t *testing.T) {
	a := &snarktoken.IssueAction{}
	require.Nil(t, a.GetInputs())
}

func TestIssueAction_GetSerializedInputs_IsNil(t *testing.T) {
	a := &snarktoken.IssueAction{}
	res, err := a.GetSerializedInputs()
	require.NoError(t, err)
	require.Nil(t, res)
}

func TestIssueAction_GetSerialNumbers_IsNil(t *testing.T) {
	a := &snarktoken.IssueAction{}
	require.Nil(t, a.GetSerialNumbers())
}

func TestIssueAction_IsGraphHiding_False(t *testing.T) {
	require.False(t, (&snarktoken.IssueAction{}).IsGraphHiding())
}

func TestIssueAction_GetMetadata_Nil(t *testing.T) {
	require.Nil(t, (&snarktoken.IssueAction{}).GetMetadata())
}

func TestIssueAction_ExtraSigners_Nil(t *testing.T) {
	require.Nil(t, (&snarktoken.IssueAction{}).ExtraSigners())
}

func TestIssueAction_IsAnonymous_False(t *testing.T) {
	require.False(t, (&snarktoken.IssueAction{}).IsAnonymous())
}

func TestIssueAction_GetIssuer(t *testing.T) {
	a := &snarktoken.IssueAction{Issuer: []byte("issuer-id")}
	require.Equal(t, []byte("issuer-id"), a.GetIssuer())
}

func TestIssueAction_NumOutputs(t *testing.T) {
	a := &snarktoken.IssueAction{
		Outputs: []snarktoken.OutputDescription{
			makeOutputDescription([]byte("alice")),
			makeOutputDescription([]byte("bob")),
		},
	}
	require.Equal(t, 2, a.NumOutputs())
}

func TestIssueAction_GetOutputs(t *testing.T) {
	a := &snarktoken.IssueAction{
		Outputs: []snarktoken.OutputDescription{makeOutputDescription([]byte("alice"))},
	}
	outs := a.GetOutputs()
	require.Len(t, outs, 1)
	require.Equal(t, []byte("alice"), outs[0].GetOwner())
}

func TestIssueAction_GetSerializedOutputs_RoundTrip(t *testing.T) {
	a := &snarktoken.IssueAction{
		Outputs: []snarktoken.OutputDescription{
			makeOutputDescription([]byte("alice")),
			makeOutputDescription([]byte("bob")),
		},
	}
	raws, err := a.GetSerializedOutputs()
	require.NoError(t, err)
	require.Len(t, raws, 2)

	got, err := snarktoken.DeserializeOutputDescription(raws[0])
	require.NoError(t, err)
	require.Equal(t, []byte("alice"), got.Recipient)
}

func TestIssueAction_Serialize_Deserialize_RoundTrip(t *testing.T) {
	orig := &snarktoken.IssueAction{
		Issuer:           []byte("issuer"),
		TokenType:        "USD",
		TypeCommitment:   make([]byte, 32),
		Outputs:          []snarktoken.OutputDescription{makeOutputDescription([]byte("alice"))},
		BindingSignature: make([]byte, 96),
		TotalValue:       make([]byte, 32),
	}
	raw, err := orig.Serialize()
	require.NoError(t, err)

	got := &snarktoken.IssueAction{}
	require.NoError(t, got.Deserialize(raw))
	require.Equal(t, orig.TokenType, got.TokenType)
	require.Equal(t, orig.Issuer, got.Issuer)
	require.Len(t, got.Outputs, 1)
}

// ── TransferAction ────────────────────────────────────────────────────────────

func TestTransferAction_Validate_Valid(t *testing.T) {
	a := makeValidTransferAction()
	require.NoError(t, a.Validate())
}

func TestTransferAction_Validate_WithInputOnly(t *testing.T) {
	a := makeValidTransferAction()
	a.Outputs = nil
	require.NoError(t, a.Validate())
}

func TestTransferAction_Validate_WithOutputOnly(t *testing.T) {
	a := makeValidTransferAction()
	a.Inputs = nil
	require.NoError(t, a.Validate())
}

func TestTransferAction_Validate_FieldErrors(t *testing.T) {
	t.Run("both empty", func(t *testing.T) {
		a := &snarktoken.TransferAction{}
		require.Error(t, a.Validate())
	})

	t.Run("invalid type commitment", func(t *testing.T) {
		a := makeValidTransferAction()
		a.TypeCommitment = make([]byte, 31)
		require.Error(t, a.Validate())
	})

	t.Run("invalid binding signature", func(t *testing.T) {
		a := makeValidTransferAction()
		a.BindingSignature = make([]byte, 95)
		require.Error(t, a.Validate())
	})

	t.Run("invalid input", func(t *testing.T) {
		a := makeValidTransferAction()
		a.Inputs[0].CommitmentIn = make([]byte, 31)
		require.Error(t, a.Validate())
	})

	t.Run("input type commitment mismatch", func(t *testing.T) {
		a := makeValidTransferAction()
		a.Inputs[0].TypeCommitment = make([]byte, 32)
		a.Inputs[0].TypeCommitment[0] = 0xAA
		require.Error(t, a.Validate())
	})

	t.Run("invalid output", func(t *testing.T) {
		a := makeValidTransferAction()
		a.Outputs[0].CommitmentOut = make([]byte, 31)
		require.Error(t, a.Validate())
	})

	t.Run("output type commitment mismatch", func(t *testing.T) {
		a := makeValidTransferAction()
		a.Outputs[0].TypeCommitment = make([]byte, 32)
		a.Outputs[0].TypeCommitment[0] = 0xBB
		require.Error(t, a.Validate())
	})
}

func TestTransferAction_NumInputs(t *testing.T) {
	a := &snarktoken.TransferAction{
		Inputs: []snarktoken.SpendDescription{makeSpendDescription(), makeSpendDescription()},
	}
	require.Equal(t, 2, a.NumInputs())
}

func TestTransferAction_GetInputs_Nil(t *testing.T) {
	a := &snarktoken.TransferAction{
		Inputs: []snarktoken.SpendDescription{makeSpendDescription()},
	}
	require.Nil(t, a.GetInputs())
}

func TestTransferAction_GetSerializedInputs_WithInputTokens(t *testing.T) {
	outputDesc := makeOutputDescription([]byte("bob"))
	raw, err := outputDesc.Serialize()
	require.NoError(t, err)

	a := &snarktoken.TransferAction{
		Inputs:      []snarktoken.SpendDescription{makeSpendDescription()},
		InputTokens: [][]byte{raw},
	}
	raws, err := a.GetSerializedInputs()
	require.NoError(t, err)
	require.Len(t, raws, 1)

	got, err := snarktoken.DeserializeOutputDescription(raws[0])
	require.NoError(t, err)
	require.Equal(t, []byte("bob"), got.Recipient)
}

func TestTransferAction_GetSerializedInputs_ErrorWithoutInputTokens(t *testing.T) {
	a := &snarktoken.TransferAction{
		Inputs: []snarktoken.SpendDescription{makeSpendDescription()},
	}
	_, err := a.GetSerializedInputs()
	require.Error(t, err)
	require.Contains(t, err.Error(), "InputTokens not populated")
}

func TestTransferAction_GetSerializedInputs_NilWhenEmpty(t *testing.T) {
	a := &snarktoken.TransferAction{}
	raws, err := a.GetSerializedInputs()
	require.NoError(t, err)
	require.Nil(t, raws)
}

func TestTransferAction_GetSerialNumbers_Nil(t *testing.T) {
	require.Nil(t, (&snarktoken.TransferAction{}).GetSerialNumbers())
}

func TestTransferAction_IsGraphHiding_False(t *testing.T) {
	require.False(t, (&snarktoken.TransferAction{}).IsGraphHiding())
}

func TestTransferAction_GetMetadata_Nil(t *testing.T) {
	require.Nil(t, (&snarktoken.TransferAction{}).GetMetadata())
}

func TestTransferAction_ExtraSigners_Nil(t *testing.T) {
	require.Nil(t, (&snarktoken.TransferAction{}).ExtraSigners())
}

func TestTransferAction_GetIssuer_Nil(t *testing.T) {
	require.Nil(t, (&snarktoken.TransferAction{}).GetIssuer())
}

func TestTransferAction_NumOutputs(t *testing.T) {
	a := &snarktoken.TransferAction{
		Outputs: []snarktoken.OutputDescription{
			makeOutputDescription([]byte("a")),
			makeOutputDescription([]byte("b")),
			makeOutputDescription([]byte("c")),
		},
	}
	require.Equal(t, 3, a.NumOutputs())
}

func TestTransferAction_GetOutputs(t *testing.T) {
	a := &snarktoken.TransferAction{
		Outputs: []snarktoken.OutputDescription{makeOutputDescription([]byte("x"))},
	}
	outs := a.GetOutputs()
	require.Len(t, outs, 1)
	require.Equal(t, []byte("x"), outs[0].GetOwner())
}

func TestTransferAction_GetSerializedOutputs_RoundTrip(t *testing.T) {
	a := &snarktoken.TransferAction{
		Outputs: []snarktoken.OutputDescription{makeOutputDescription([]byte("y"))},
	}
	raws, err := a.GetSerializedOutputs()
	require.NoError(t, err)
	require.Len(t, raws, 1)

	got, err := snarktoken.DeserializeOutputDescription(raws[0])
	require.NoError(t, err)
	require.Equal(t, []byte("y"), got.Recipient)
}

func TestTransferAction_IsRedeemAt_RedeemOutput(t *testing.T) {
	a := &snarktoken.TransferAction{
		Outputs: []snarktoken.OutputDescription{makeOutputDescription(nil)}, // nil = redeem
	}
	require.True(t, a.IsRedeemAt(0))
}

func TestTransferAction_IsRedeemAt_NonRedeemOutput(t *testing.T) {
	a := &snarktoken.TransferAction{
		Outputs: []snarktoken.OutputDescription{makeOutputDescription([]byte("owner"))},
	}
	require.False(t, a.IsRedeemAt(0))
}

func TestTransferAction_IsRedeemAt_OutOfBounds(t *testing.T) {
	a := &snarktoken.TransferAction{
		Outputs: []snarktoken.OutputDescription{makeOutputDescription([]byte("owner"))},
	}
	require.False(t, a.IsRedeemAt(-1))
	require.False(t, a.IsRedeemAt(1))
}

func TestTransferAction_SerializeOutputAt_InBounds(t *testing.T) {
	a := &snarktoken.TransferAction{
		Outputs: []snarktoken.OutputDescription{makeOutputDescription([]byte("z"))},
	}
	raw, err := a.SerializeOutputAt(0)
	require.NoError(t, err)

	got, err := snarktoken.DeserializeOutputDescription(raw)
	require.NoError(t, err)
	require.Equal(t, []byte("z"), got.Recipient)
}

func TestTransferAction_SerializeOutputAt_OutOfBounds(t *testing.T) {
	a := &snarktoken.TransferAction{
		Outputs: []snarktoken.OutputDescription{makeOutputDescription(nil)},
	}
	_, err := a.SerializeOutputAt(-1)
	require.Error(t, err)

	_, err = a.SerializeOutputAt(1)
	require.Error(t, err)
}

func TestTransferAction_Serialize_Deserialize_RoundTrip(t *testing.T) {
	orig := &snarktoken.TransferAction{
		TypeCommitment:   make([]byte, 32),
		Inputs:           []snarktoken.SpendDescription{makeSpendDescription()},
		Outputs:          []snarktoken.OutputDescription{makeOutputDescription([]byte("alice"))},
		BindingSignature: make([]byte, 96),
	}
	raw, err := orig.Serialize()
	require.NoError(t, err)

	got := &snarktoken.TransferAction{}
	require.NoError(t, got.Deserialize(raw))
	require.Len(t, got.Inputs, 1)
	require.Len(t, got.Outputs, 1)
}

// ── Input ─────────────────────────────────────────────────────────────────────

func TestInput_GetOwner(t *testing.T) {
	i := &snarktoken.Input{Owner: []byte("owner-bytes")}
	require.Equal(t, []byte("owner-bytes"), i.GetOwner())
}

// ── EncodeTokenType ───────────────────────────────────────────────────────────

func TestEncodeTokenType_Empty(t *testing.T) {
	// Empty string must not panic, and must not collide with the zero
	// element or with "\x00" (the former SetBytes encoding mapped both to 0).
	for _, curve := range supportedCurves {
		elem, err := snarktoken.EncodeTokenTypeBytes("", curve)
		require.NoError(t, err)
		require.Len(t, elem, 32)
		require.NotEqual(t, make([]byte, 32), elem)
		nul, err := snarktoken.EncodeTokenTypeBytes("\x00", curve)
		require.NoError(t, err)
		require.NotEqual(t, nul, elem)
	}
}

func TestEncodeTokenType_Deterministic(t *testing.T) {
	for _, curve := range supportedCurves {
		e1, err := snarktoken.EncodeTokenTypeBytes("EUR", curve)
		require.NoError(t, err)
		e2, err := snarktoken.EncodeTokenTypeBytes("EUR", curve)
		require.NoError(t, err)
		require.Equal(t, e1, e2)
	}
}

func TestEncodeTokenType_DifferentTypes(t *testing.T) {
	for _, curve := range supportedCurves {
		eUSD, err := snarktoken.EncodeTokenTypeBytes("USD", curve)
		require.NoError(t, err)
		eEUR, err := snarktoken.EncodeTokenTypeBytes("EUR", curve)
		require.NoError(t, err)
		require.NotEqual(t, eUSD, eEUR)
	}
}

// ── ComputeTypeCommitment ─────────────────────────────────────────────────────

func TestComputeTypeCommitment_Deterministic(t *testing.T) {
	for _, curve := range []ecc.ID{ecc.BLS12_381, ecc.BN254} {
		t.Run(curve.String(), func(t *testing.T) {
			note, err := snarktoken.NewRandomNote(100, "USD", curve)
			require.NoError(t, err)

			tc1, err := snarktoken.ComputeTypeCommitment("USD", note.Randomness, curve)
			require.NoError(t, err)

			tc2, err := snarktoken.ComputeTypeCommitment("USD", note.Randomness, curve)
			require.NoError(t, err)

			require.Equal(t, tc1, tc2)
		})
	}
}

func TestComputeTypeCommitment_DifferentTypesGiveDifferentCommitments(t *testing.T) {
	for _, curve := range []ecc.ID{ecc.BLS12_381, ecc.BN254} {
		t.Run(curve.String(), func(t *testing.T) {
			note, err := snarktoken.NewRandomNote(100, "USD", curve)
			require.NoError(t, err)

			tcUSD, err := snarktoken.ComputeTypeCommitment("USD", note.Randomness, curve)
			require.NoError(t, err)

			tcEUR, err := snarktoken.ComputeTypeCommitment("EUR", note.Randomness, curve)
			require.NoError(t, err)

			require.NotEqual(t, tcUSD, tcEUR)
		})
	}
}

// ── Note with issuer ──────────────────────────────────────────────────────────

func TestNote_Serialize_WithIssuer_RoundTrip(t *testing.T) {
	note, err := snarktoken.NewRandomNote(500, "USD", ecc.BLS12_381)
	require.NoError(t, err)
	note.Issuer = []byte("issuer-abc")

	raw, err := note.Serialize()
	require.NoError(t, err)

	got, err := snarktoken.Deserialize(raw)
	require.NoError(t, err)

	require.Equal(t, note.Value, got.Value)
	require.Equal(t, note.TokenType, got.TokenType)
	require.Equal(t, note.Issuer, got.Issuer)
}

func TestNote_Deserialize_TooShort(t *testing.T) {
	_, err := snarktoken.Deserialize([]byte{1, 2, 3})
	require.Error(t, err)
}

func TestNote_Deserialize_LengthMismatch(t *testing.T) {
	// 16 bytes header, but typeLen says much more data is coming.
	buf := make([]byte, 16)
	// typeLen = 1000 (big-endian at bytes 8-16)
	buf[15] = 0xE8 // 1000 in big-endian LSB
	_, err := snarktoken.Deserialize(buf)
	require.Error(t, err)
}
