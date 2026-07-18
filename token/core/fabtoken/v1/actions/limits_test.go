/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package actions

import (
	"strconv"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/protos-go/v1/actions"
	driverv1 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/require"
)

func baseValidIssueAction() *IssueAction {
	return &IssueAction{
		Issuer: []byte("issuer"),
		Outputs: []*Output{
			{Owner: []byte("owner1"), Type: "TYPE", Quantity: "0x1"},
		},
		Metadata: map[string][]byte{},
	}
}

func baseValidTransferAction() *TransferAction {
	return &TransferAction{
		Inputs: []*TransferActionInput{
			{
				ID:    &token.ID{TxId: "txid1", Index: 0},
				Input: &Output{Owner: []byte("owner1"), Type: "TYPE", Quantity: "0x1"},
			},
		},
		Outputs: []*Output{
			{Owner: []byte("owner2"), Type: "TYPE", Quantity: "0x1"},
		},
		Metadata: map[string][]byte{},
	}
}

func TestIssueAction_Validate_TooManyOutputs(t *testing.T) {
	mk := func(n int) *IssueAction {
		a := baseValidIssueAction()
		a.Outputs = make([]*Output, n)
		for i := range a.Outputs {
			a.Outputs[i] = &Output{Owner: []byte("owner"), Type: "TYPE", Quantity: "0x1"}
		}

		return a
	}
	require.NoError(t, mk(MaxOutputs-1).Validate())
	require.NoError(t, mk(MaxOutputs).Validate())
	require.ErrorIs(t, mk(MaxOutputs+1).Validate(), ErrTooManyOutputs)
}

func TestIssueAction_Validate_MetadataLimits(t *testing.T) {
	t.Run("entries", func(t *testing.T) {
		mk := func(n int) *IssueAction {
			a := baseValidIssueAction()
			a.Metadata = make(map[string][]byte, n)
			for i := range n {
				a.Metadata["k"+strconv.Itoa(i)] = []byte("v")
			}

			return a
		}
		require.NoError(t, mk(MaxMetadataEntries-1).Validate())
		require.NoError(t, mk(MaxMetadataEntries).Validate())
		require.ErrorIs(t, mk(MaxMetadataEntries+1).Validate(), ErrTooManyMetadataEntries)
	})
	t.Run("key bytes", func(t *testing.T) {
		mk := func(n int) *IssueAction {
			a := baseValidIssueAction()
			a.Metadata = map[string][]byte{string(make([]byte, n)): []byte("v")}

			return a
		}
		require.NoError(t, mk(MaxMetadataKeyBytes-1).Validate())
		require.NoError(t, mk(MaxMetadataKeyBytes).Validate())
		require.ErrorIs(t, mk(MaxMetadataKeyBytes+1).Validate(), ErrMetadataKeyTooLarge)
	})
	t.Run("value bytes", func(t *testing.T) {
		mk := func(n int) *IssueAction {
			a := baseValidIssueAction()
			a.Metadata = map[string][]byte{"k": make([]byte, n)}

			return a
		}
		require.NoError(t, mk(MaxMetadataValueBytes-1).Validate())
		require.NoError(t, mk(MaxMetadataValueBytes).Validate())
		require.ErrorIs(t, mk(MaxMetadataValueBytes+1).Validate(), ErrMetadataValueTooLarge)
	})
}

func marshalIssueAction(t *testing.T, outputs int) []byte {
	t.Helper()
	ia := &actions.IssueAction{
		Version: ProtocolV1,
		Issuer:  &driverv1.Identity{Raw: []byte("issuer")},
	}
	for range outputs {
		ia.Outputs = append(ia.Outputs, &actions.IssueActionOutput{Token: &actions.Token{Owner: []byte("o"), Type: "TYPE", Quantity: "0x1"}})
	}
	raw, err := proto.Marshal(ia)
	require.NoError(t, err)

	return raw
}

func TestIssueAction_Deserialize_TooManyOutputs(t *testing.T) {
	a := &IssueAction{}
	require.NoError(t, a.Deserialize(marshalIssueAction(t, MaxOutputs)))
	require.ErrorIs(t, a.Deserialize(marshalIssueAction(t, MaxOutputs+1)), ErrTooManyOutputs)
}

func TestTransferAction_Validate_TooManyInputs(t *testing.T) {
	mk := func(n int) *TransferAction {
		a := baseValidTransferAction()
		a.Inputs = make([]*TransferActionInput, n)
		for i := range a.Inputs {
			a.Inputs[i] = &TransferActionInput{
				ID:    &token.ID{TxId: "txid", Index: uint64(i)},
				Input: &Output{Owner: []byte("owner"), Type: "TYPE", Quantity: "0x1"},
			}
		}

		return a
	}
	require.NoError(t, mk(MaxInputs-1).Validate())
	require.NoError(t, mk(MaxInputs).Validate())
	require.ErrorIs(t, mk(MaxInputs+1).Validate(), ErrTooManyInputs)
}

func TestTransferAction_Validate_TooManyOutputs(t *testing.T) {
	mk := func(n int) *TransferAction {
		a := baseValidTransferAction()
		a.Outputs = make([]*Output, n)
		for i := range a.Outputs {
			a.Outputs[i] = &Output{Owner: []byte("owner"), Type: "TYPE", Quantity: "0x1"}
		}

		return a
	}
	require.NoError(t, mk(MaxOutputs-1).Validate())
	require.NoError(t, mk(MaxOutputs).Validate())
	require.ErrorIs(t, mk(MaxOutputs+1).Validate(), ErrTooManyOutputs)
}

func TestTransferAction_Validate_MetadataLimits(t *testing.T) {
	mk := func(n int) *TransferAction {
		a := baseValidTransferAction()
		a.Metadata = make(map[string][]byte, n)
		for i := range n {
			a.Metadata["k"+strconv.Itoa(i)] = []byte("v")
		}

		return a
	}
	require.NoError(t, mk(MaxMetadataEntries-1).Validate())
	require.NoError(t, mk(MaxMetadataEntries).Validate())
	require.ErrorIs(t, mk(MaxMetadataEntries+1).Validate(), ErrTooManyMetadataEntries)
}

func marshalTransferAction(t *testing.T, inputs, outputs int) []byte {
	t.Helper()
	ta := &actions.TransferAction{
		Version: ProtocolV1,
	}
	for range inputs {
		ta.Inputs = append(ta.Inputs, &actions.TransferActionInput{Input: &actions.Token{Owner: []byte("o"), Type: "TYPE", Quantity: "0x1"}})
	}
	for range outputs {
		ta.Outputs = append(ta.Outputs, &actions.TransferActionOutput{Token: &actions.Token{Owner: []byte("o"), Type: "TYPE", Quantity: "0x1"}})
	}
	raw, err := proto.Marshal(ta)
	require.NoError(t, err)

	return raw
}

func TestTransferAction_Deserialize_TooManyInputs(t *testing.T) {
	a := &TransferAction{}
	require.NoError(t, a.Deserialize(marshalTransferAction(t, MaxInputs, 1)))
	require.ErrorIs(t, a.Deserialize(marshalTransferAction(t, MaxInputs+1, 1)), ErrTooManyInputs)
}

func TestTransferAction_Deserialize_TooManyOutputs(t *testing.T) {
	a := &TransferAction{}
	require.NoError(t, a.Deserialize(marshalTransferAction(t, 1, MaxOutputs)))
	require.ErrorIs(t, a.Deserialize(marshalTransferAction(t, 1, MaxOutputs+1)), ErrTooManyOutputs)
}

func TestTransferAction_Deserialize_ManyTinyInputs(t *testing.T) {
	a := &TransferAction{}
	require.ErrorIs(t, a.Deserialize(marshalTransferAction(t, MaxInputs+1, 1)), ErrTooManyInputs)
}
