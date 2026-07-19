/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package actions

import (
	"strconv"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/protos-go/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/driver"
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
	limits := driver.DefaultResourceLimits()
	mk := func(n int) *IssueAction {
		a := baseValidIssueAction()
		a.Outputs = make([]*Output, n)
		for i := range a.Outputs {
			a.Outputs[i] = &Output{Owner: []byte("owner"), Type: "TYPE", Quantity: "0x1"}
		}

		return a
	}
	require.NoError(t, mk(limits.MaxOutputs-1).Validate())
	require.NoError(t, mk(limits.MaxOutputs).Validate())
	require.ErrorIs(t, mk(limits.MaxOutputs+1).Validate(), ErrTooManyOutputs)
}

func TestIssueAction_Validate_TooManyOutputs_CustomLimit(t *testing.T) {
	custom := driver.DefaultResourceLimits()
	custom.MaxOutputs = 2
	mk := func(n int) *IssueAction {
		a := baseValidIssueAction()
		a.Outputs = make([]*Output, n)
		for i := range a.Outputs {
			a.Outputs[i] = &Output{Owner: []byte("owner"), Type: "TYPE", Quantity: "0x1"}
		}
		a.SetLimits(custom)

		return a
	}
	require.NoError(t, mk(2).Validate())
	require.ErrorIs(t, mk(3).Validate(), ErrTooManyOutputs)
}

func TestIssueAction_Validate_MetadataLimits(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	t.Run("entries", func(t *testing.T) {
		mk := func(n int) *IssueAction {
			a := baseValidIssueAction()
			a.Metadata = make(map[string][]byte, n)
			for i := range n {
				a.Metadata["k"+strconv.Itoa(i)] = []byte("v")
			}

			return a
		}
		require.NoError(t, mk(limits.MaxMetadataEntries-1).Validate())
		require.NoError(t, mk(limits.MaxMetadataEntries).Validate())
		require.ErrorIs(t, mk(limits.MaxMetadataEntries+1).Validate(), ErrTooManyMetadataEntries)
	})
	t.Run("key bytes", func(t *testing.T) {
		mk := func(n int) *IssueAction {
			a := baseValidIssueAction()
			a.Metadata = map[string][]byte{string(make([]byte, n)): []byte("v")}

			return a
		}
		require.NoError(t, mk(limits.MaxMetadataKeyBytes-1).Validate())
		require.NoError(t, mk(limits.MaxMetadataKeyBytes).Validate())
		require.ErrorIs(t, mk(limits.MaxMetadataKeyBytes+1).Validate(), ErrMetadataKeyTooLarge)
	})
	t.Run("value bytes", func(t *testing.T) {
		mk := func(n int) *IssueAction {
			a := baseValidIssueAction()
			a.Metadata = map[string][]byte{"k": make([]byte, n)}

			return a
		}
		require.NoError(t, mk(limits.MaxMetadataValueBytes-1).Validate())
		require.NoError(t, mk(limits.MaxMetadataValueBytes).Validate())
		require.ErrorIs(t, mk(limits.MaxMetadataValueBytes+1).Validate(), ErrMetadataValueTooLarge)
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
	limits := driver.DefaultResourceLimits()
	a := &IssueAction{}
	require.NoError(t, a.Deserialize(marshalIssueAction(t, limits.MaxOutputs)))
	require.ErrorIs(t, a.Deserialize(marshalIssueAction(t, limits.MaxOutputs+1)), ErrTooManyOutputs)
}

func TestIssueAction_Deserialize_TooManyOutputs_CustomLimit(t *testing.T) {
	custom := driver.DefaultResourceLimits()
	custom.MaxOutputs = 2
	a := &IssueAction{}
	a.SetLimits(custom)
	require.NoError(t, a.Deserialize(marshalIssueAction(t, 2)))
	require.ErrorIs(t, a.Deserialize(marshalIssueAction(t, 3)), ErrTooManyOutputs)
}

func TestTransferAction_Validate_TooManyInputs(t *testing.T) {
	limits := driver.DefaultResourceLimits()
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
	require.NoError(t, mk(limits.MaxInputs-1).Validate())
	require.NoError(t, mk(limits.MaxInputs).Validate())
	require.ErrorIs(t, mk(limits.MaxInputs+1).Validate(), ErrTooManyInputs)
}

func TestTransferAction_Validate_TooManyInputs_CustomLimit(t *testing.T) {
	custom := driver.DefaultResourceLimits()
	custom.MaxInputs = 2
	mk := func(n int) *TransferAction {
		a := baseValidTransferAction()
		a.Inputs = make([]*TransferActionInput, n)
		for i := range a.Inputs {
			a.Inputs[i] = &TransferActionInput{
				ID:    &token.ID{TxId: "txid", Index: uint64(i)},
				Input: &Output{Owner: []byte("owner"), Type: "TYPE", Quantity: "0x1"},
			}
		}
		a.SetLimits(custom)

		return a
	}
	require.NoError(t, mk(2).Validate())
	require.ErrorIs(t, mk(3).Validate(), ErrTooManyInputs)
}

func TestTransferAction_Validate_TooManyOutputs(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	mk := func(n int) *TransferAction {
		a := baseValidTransferAction()
		a.Outputs = make([]*Output, n)
		for i := range a.Outputs {
			a.Outputs[i] = &Output{Owner: []byte("owner"), Type: "TYPE", Quantity: "0x1"}
		}

		return a
	}
	require.NoError(t, mk(limits.MaxOutputs-1).Validate())
	require.NoError(t, mk(limits.MaxOutputs).Validate())
	require.ErrorIs(t, mk(limits.MaxOutputs+1).Validate(), ErrTooManyOutputs)
}

func TestTransferAction_Validate_MetadataLimits(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	mk := func(n int) *TransferAction {
		a := baseValidTransferAction()
		a.Metadata = make(map[string][]byte, n)
		for i := range n {
			a.Metadata["k"+strconv.Itoa(i)] = []byte("v")
		}

		return a
	}
	require.NoError(t, mk(limits.MaxMetadataEntries-1).Validate())
	require.NoError(t, mk(limits.MaxMetadataEntries).Validate())
	require.ErrorIs(t, mk(limits.MaxMetadataEntries+1).Validate(), ErrTooManyMetadataEntries)
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
	limits := driver.DefaultResourceLimits()
	a := &TransferAction{}
	require.NoError(t, a.Deserialize(marshalTransferAction(t, limits.MaxInputs, 1)))
	require.ErrorIs(t, a.Deserialize(marshalTransferAction(t, limits.MaxInputs+1, 1)), ErrTooManyInputs)
}

func TestTransferAction_Deserialize_TooManyOutputs(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	a := &TransferAction{}
	require.NoError(t, a.Deserialize(marshalTransferAction(t, 1, limits.MaxOutputs)))
	require.ErrorIs(t, a.Deserialize(marshalTransferAction(t, 1, limits.MaxOutputs+1)), ErrTooManyOutputs)
}

func TestTransferAction_Deserialize_ManyTinyInputs(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	a := &TransferAction{}
	require.ErrorIs(t, a.Deserialize(marshalTransferAction(t, limits.MaxInputs+1, 1)), ErrTooManyInputs)
}

func TestTransferAction_Deserialize_TooManyInputs_CustomLimit(t *testing.T) {
	custom := driver.DefaultResourceLimits()
	custom.MaxInputs = 2
	a := &TransferAction{}
	a.SetLimits(custom)
	require.NoError(t, a.Deserialize(marshalTransferAction(t, 2, 1)))
	require.ErrorIs(t, a.Deserialize(marshalTransferAction(t, 3, 1)), ErrTooManyInputs)
}
