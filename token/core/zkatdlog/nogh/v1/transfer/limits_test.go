/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package transfer_test

import (
	"strconv"
	"testing"
	"time"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	token2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/require"
)

func baseValidTransferAction() *transfer.Action {
	curve := math.Curves[math.BN254]

	return &transfer.Action{
		Inputs: []*transfer.ActionInput{
			{
				ID:    &token.ID{TxId: "txid1", Index: 0},
				Token: &token2.Token{Owner: []byte("owner1"), Data: curve.GenG1},
			},
		},
		Outputs: []*token2.Token{
			{Owner: []byte("owner2"), Data: curve.GenG1},
		},
		ProofType: rp.RangeProofType,
		Proof:     []byte("proof"),
		Metadata:  map[string][]byte{},
	}
}

func TestTransferAction_Validate_TooManyInputs(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	curve := math.Curves[math.BN254]
	mk := func(n int) *transfer.Action {
		a := baseValidTransferAction()
		a.Inputs = make([]*transfer.ActionInput, n)
		for i := range a.Inputs {
			a.Inputs[i] = &transfer.ActionInput{
				ID:    &token.ID{TxId: "txid", Index: uint64(i)},
				Token: &token2.Token{Owner: []byte("owner"), Data: curve.GenG1},
			}
		}

		return a
	}
	require.NoError(t, mk(limits.MaxInputs-1).Validate())
	require.NoError(t, mk(limits.MaxInputs).Validate())
	require.ErrorIs(t, mk(limits.MaxInputs+1).Validate(), transfer.ErrTooManyInputs)
}

func TestTransferAction_Validate_TooManyInputs_CustomLimit(t *testing.T) {
	custom := driver.DefaultResourceLimits()
	custom.MaxInputs = 2
	curve := math.Curves[math.BN254]
	mk := func(n int) *transfer.Action {
		a := baseValidTransferAction()
		a.Inputs = make([]*transfer.ActionInput, n)
		for i := range a.Inputs {
			a.Inputs[i] = &transfer.ActionInput{
				ID:    &token.ID{TxId: "txid", Index: uint64(i)},
				Token: &token2.Token{Owner: []byte("owner"), Data: curve.GenG1},
			}
		}
		a.SetLimits(custom)

		return a
	}
	require.NoError(t, mk(2).Validate())
	require.ErrorIs(t, mk(3).Validate(), transfer.ErrTooManyInputs)
}

func TestTransferAction_Validate_TooManyOutputs(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	curve := math.Curves[math.BN254]
	mk := func(n int) *transfer.Action {
		a := baseValidTransferAction()
		a.Outputs = make([]*token2.Token, n)
		for i := range a.Outputs {
			a.Outputs[i] = &token2.Token{Owner: []byte("owner"), Data: curve.GenG1}
		}

		return a
	}
	require.NoError(t, mk(limits.MaxOutputs-1).Validate())
	require.NoError(t, mk(limits.MaxOutputs).Validate())
	require.ErrorIs(t, mk(limits.MaxOutputs+1).Validate(), transfer.ErrTooManyOutputs)
}

func TestTransferAction_Validate_ProofTooLarge(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	mk := func(n int) *transfer.Action {
		a := baseValidTransferAction()
		a.Proof = make([]byte, n)

		return a
	}
	require.NoError(t, mk(limits.MaxProofBytes-1).Validate())
	require.NoError(t, mk(limits.MaxProofBytes).Validate())
	require.ErrorIs(t, mk(limits.MaxProofBytes+1).Validate(), transfer.ErrProofTooLarge)
}

func TestTransferAction_Validate_ProofTooLarge_CustomLimit(t *testing.T) {
	custom := driver.DefaultResourceLimits()
	custom.MaxProofBytes = 16
	mk := func(n int) *transfer.Action {
		a := baseValidTransferAction()
		a.Proof = make([]byte, n)
		a.SetLimits(custom)

		return a
	}
	require.NoError(t, mk(16).Validate())
	require.ErrorIs(t, mk(17).Validate(), transfer.ErrProofTooLarge)
}

func TestTransferAction_Validate_MetadataLimits(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	t.Run("entries", func(t *testing.T) {
		mk := func(n int) *transfer.Action {
			a := baseValidTransferAction()
			a.Metadata = make(map[string][]byte, n)
			for i := range n {
				a.Metadata["k"+strconv.Itoa(i)] = []byte("v")
			}

			return a
		}
		require.NoError(t, mk(limits.MaxMetadataEntries-1).Validate())
		require.NoError(t, mk(limits.MaxMetadataEntries).Validate())
		require.ErrorIs(t, mk(limits.MaxMetadataEntries+1).Validate(), transfer.ErrTooManyMetadataEntries)
	})
	t.Run("key bytes", func(t *testing.T) {
		mk := func(n int) *transfer.Action {
			a := baseValidTransferAction()
			a.Metadata = map[string][]byte{string(make([]byte, n)): []byte("v")}

			return a
		}
		require.NoError(t, mk(limits.MaxMetadataKeyBytes-1).Validate())
		require.NoError(t, mk(limits.MaxMetadataKeyBytes).Validate())
		require.ErrorIs(t, mk(limits.MaxMetadataKeyBytes+1).Validate(), transfer.ErrMetadataKeyTooLarge)
	})
	t.Run("value bytes", func(t *testing.T) {
		mk := func(n int) *transfer.Action {
			a := baseValidTransferAction()
			a.Metadata = map[string][]byte{"k": make([]byte, n)}

			return a
		}
		require.NoError(t, mk(limits.MaxMetadataValueBytes-1).Validate())
		require.NoError(t, mk(limits.MaxMetadataValueBytes).Validate())
		require.ErrorIs(t, mk(limits.MaxMetadataValueBytes+1).Validate(), transfer.ErrMetadataValueTooLarge)
	})
}

func marshalTransferAction(t *testing.T, inputs, outputs, proofLen int) []byte {
	t.Helper()
	ta := &actions.TransferAction{
		Version: transfer.ProtocolV1,
	}
	for range inputs {
		ta.Inputs = append(ta.Inputs, &actions.TransferActionInput{Input: &actions.Token{Owner: []byte("o")}})
	}
	for range outputs {
		ta.Outputs = append(ta.Outputs, &actions.TransferActionOutput{Token: &actions.Token{Owner: []byte("o")}})
	}
	if proofLen > 0 {
		ta.Proof = &actions.Proof{ProofType: &actions.Proof_Proof{Proof: make([]byte, proofLen)}}
	}
	raw, err := proto.Marshal(ta)
	require.NoError(t, err)

	return raw
}

func TestTransferAction_Deserialize_TooManyInputs(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	a := &transfer.Action{}
	require.NoError(t, a.Deserialize(marshalTransferAction(t, limits.MaxInputs, 1, 1)))
	require.ErrorIs(t, a.Deserialize(marshalTransferAction(t, limits.MaxInputs+1, 1, 1)), transfer.ErrTooManyInputs)
}

func TestTransferAction_Deserialize_TooManyOutputs(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	a := &transfer.Action{}
	require.NoError(t, a.Deserialize(marshalTransferAction(t, 1, limits.MaxOutputs, 1)))
	require.ErrorIs(t, a.Deserialize(marshalTransferAction(t, 1, limits.MaxOutputs+1, 1)), transfer.ErrTooManyOutputs)
}

func TestTransferAction_Deserialize_ProofTooLarge(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	a := &transfer.Action{}
	require.ErrorIs(t, a.Deserialize(marshalTransferAction(t, 1, 1, limits.MaxProofBytes+1)), transfer.ErrProofTooLarge)
}

func TestTransferAction_Deserialize_ManyTinyInputs(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	a := &transfer.Action{}
	require.ErrorIs(t, a.Deserialize(marshalTransferAction(t, limits.MaxInputs+1, 1, 1)), transfer.ErrTooManyInputs)
}

func TestTransferAction_Deserialize_TooManyInputs_CustomLimit(t *testing.T) {
	custom := driver.DefaultResourceLimits()
	custom.MaxInputs = 2
	a := &transfer.Action{}
	a.SetLimits(custom)
	require.NoError(t, a.Deserialize(marshalTransferAction(t, 2, 1, 1)))
	require.ErrorIs(t, a.Deserialize(marshalTransferAction(t, 3, 1, 1)), transfer.ErrTooManyInputs)
}

// TestTransferAction_Deserialize_RejectsBeforeCryptographicWork proves that an oversized
// proof is rejected by the resource-limit check before any cryptographic verifier work would
// run: Deserialize returns near-instantly and never reaches proof-specific unmarshalling.
func TestTransferAction_Deserialize_RejectsBeforeCryptographicWork(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	raw := marshalTransferAction(t, 1, 1, limits.MaxProofBytes+1)

	start := time.Now()
	a := &transfer.Action{}
	err := a.Deserialize(raw)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, transfer.ErrProofTooLarge)
	require.Less(t, elapsed, 50*time.Millisecond, "oversized proof must be rejected near-instantly, before any cryptographic work")
}
