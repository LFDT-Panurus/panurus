/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package actions

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/protos-go/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransferAction_Validate(t *testing.T) {
	tests := []struct {
		name          string
		action        *TransferAction
		wantErr       bool
		expectedError string
	}{
		{
			name:          "",
			action:        &TransferAction{},
			wantErr:       true,
			expectedError: "invalid number of token inputs, expected at least 1",
		},
		{
			name: "",
			action: &TransferAction{
				Inputs: []*TransferActionInput{},
			},
			wantErr:       true,
			expectedError: "invalid number of token inputs, expected at least 1",
		},
		{
			name: "",
			action: &TransferAction{
				Inputs: []*TransferActionInput{
					nil,
				},
			},
			wantErr:       true,
			expectedError: "invalid input at index [0], empty input",
		},
		{
			name: "",
			action: &TransferAction{
				Inputs: []*TransferActionInput{
					{
						ID:    nil,
						Input: nil,
					},
				},
			},
			wantErr:       true,
			expectedError: "invalid input's ID at index [0], it is empty",
		},
		{
			name: "",
			action: &TransferAction{
				Inputs: []*TransferActionInput{
					{
						ID:    &token.ID{},
						Input: nil,
					},
				},
			},
			wantErr:       true,
			expectedError: "invalid input's ID at index [0], tx id is empty",
		},
		{
			name: "",
			action: &TransferAction{
				Inputs: []*TransferActionInput{
					{
						ID:    &token.ID{TxId: "txid"},
						Input: nil,
					},
				},
			},
			wantErr:       true,
			expectedError: "invalid input's token at index [0], empty token",
		},
		{
			name: "",
			action: &TransferAction{
				Inputs: []*TransferActionInput{
					{
						ID:    &token.ID{TxId: "txid"},
						Input: &Output{},
					},
				},
			},
			wantErr:       true,
			expectedError: "invalid input token at index [0]: token owner cannot be empty",
		},
		{
			name: "",
			action: &TransferAction{
				Inputs: []*TransferActionInput{
					{
						ID: &token.ID{TxId: "txid"},
						Input: &Output{
							Owner:    []byte("owner"),
							Type:     "",
							Quantity: "",
						},
					},
				},
			},
			wantErr:       true,
			expectedError: "invalid input token at index [0]: token quantity cannot be empty",
		},
		{
			name: "",
			action: &TransferAction{
				Inputs: []*TransferActionInput{
					{
						ID: &token.ID{TxId: "txid"},
						Input: &Output{
							Owner:    []byte("owner"),
							Type:     "type",
							Quantity: "",
						},
					},
				},
			},
			wantErr:       true,
			expectedError: "invalid input token at index [0]: token quantity cannot be empty",
		},
		{
			name: "",
			action: &TransferAction{
				Inputs: []*TransferActionInput{
					{
						ID: &token.ID{TxId: "txid"},
						Input: &Output{
							Owner:    []byte("owner"),
							Type:     "type",
							Quantity: "11",
						},
					},
				},
				Outputs: []*Output{
					nil,
				},
			},
			wantErr:       true,
			expectedError: "invalid output at index [0], empty output",
		},
		{
			name: "",
			action: &TransferAction{
				Inputs: []*TransferActionInput{
					{
						ID: &token.ID{TxId: "txid"},
						Input: &Output{
							Owner:    []byte("owner"),
							Type:     "type",
							Quantity: "11",
						},
					},
				},
				Outputs: []*Output{
					{},
				},
			},
			wantErr:       true,
			expectedError: "invalid output's type at index [0], output type is empty",
		},
		{
			name: "",
			action: &TransferAction{
				Inputs: []*TransferActionInput{
					{
						ID: &token.ID{TxId: "txid"},
						Input: &Output{
							Owner:    []byte("owner"),
							Type:     "type",
							Quantity: "11",
						},
					},
				},
				Outputs: []*Output{
					{
						Type: "type",
					},
				},
			},
			wantErr:       true,
			expectedError: "invalid output's quantity at index [0], output quantity is empty",
		},
		{
			name: "",
			action: &TransferAction{
				Inputs: []*TransferActionInput{
					{
						ID: &token.ID{TxId: "txid"},
						Input: &Output{
							Owner:    []byte("owner"),
							Type:     "type",
							Quantity: "11",
						},
					},
				},
				Outputs: []*Output{
					{
						Owner:    []byte("owner"),
						Type:     "type",
						Quantity: "11",
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.action.Validate()
			if tt.wantErr {
				require.Error(t, err)
				require.EqualError(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestTransferAction_Serialization(t *testing.T) {
	action := &TransferAction{
		Inputs: []*TransferActionInput{
			{
				ID: &token.ID{
					TxId:  "txid",
					Index: 0,
				},
				Input: &Output{
					Owner:    []byte("owner"),
					Type:     "type",
					Quantity: "11",
				},
			},
		},
		Outputs: []*Output{
			{
				Type:     "type",
				Quantity: "11",
				Owner:    []byte("owner"),
			},
		},
		Metadata: map[string][]byte{
			"metadata": []byte("{\"foo\":\"bar\"}"),
		},
		Issuer: nil,
	}
	raw, err := action.Serialize()
	require.NoError(t, err, "failed to serialize a new transfer action")

	action2 := &TransferAction{}
	err = action2.Deserialize(raw)
	require.NoError(t, err, "failed to deserialize a new transfer action")
	assert.Equal(t, action, action2, "deserialized action is not equal to the original one")

	raw2, err := action2.Serialize()
	require.NoError(t, err, "failed to serialize a new transfer action")

	action3 := &TransferAction{}
	err = action3.Deserialize(raw2)
	require.NoError(t, err, "failed to deserialize a new transfer action")
	assert.Equal(t, action2, action3, "deserialized action is not equal to the original one")
}

func TestTransferAction_GetIssuer(t *testing.T) {
	issuerId := []byte("issuer")
	action := &TransferAction{
		Issuer: issuerId,
	}
	issuer := action.GetIssuer()
	assert.True(t, issuer.Equal(issuerId), "unexpected issuer id in TransferAction")
}

func TestTransferAction_RemainingMethods(t *testing.T) {
	action := &TransferAction{
		Inputs: []*TransferActionInput{
			{
				ID: &token.ID{
					TxId:  "txid",
					Index: 0,
				},
				Input: &Output{
					Owner:    []byte("owner"),
					Type:     "type",
					Quantity: "11",
				},
			},
		},
		Outputs: []*Output{
			{
				Type:     "type",
				Quantity: "11",
				Owner:    []byte("owner"),
			},
			{
				Type:     "type",
				Quantity: "11",
				Owner:    nil, // Redeem
			},
		},
		Metadata: map[string][]byte{
			"foo": []byte("bar"),
		},
		Issuer: []byte("issuer"),
	}

	assert.Equal(t, 1, action.NumInputs())
	assert.Equal(t, 2, action.NumOutputs())
	serOutputs, err := action.GetSerializedOutputs()
	require.NoError(t, err)
	assert.Len(t, serOutputs, 2)
	assert.Len(t, action.GetOutputs(), 2)
	assert.False(t, action.IsRedeemAt(0))
	assert.True(t, action.IsRedeemAt(1))
	assert.True(t, action.IsRedeem())
	assert.False(t, action.IsGraphHiding())
	serOutput, err := action.SerializeOutputAt(0)
	require.NoError(t, err)
	assert.NotNil(t, serOutput)
	_, err = action.SerializeOutputAt(2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to serialize output in transfer action: it does not exist")

	inputs := action.GetInputs()
	assert.Len(t, inputs, 1)
	assert.Equal(t, "txid", inputs[0].TxId)

	serInputs, err := action.GetSerializedInputs()
	require.NoError(t, err)
	assert.Len(t, serInputs, 1)
	assert.NotNil(t, serInputs[0])

	assert.Nil(t, action.GetSerialNumbers())
	assert.Equal(t, map[string][]byte{"foo": []byte("bar")}, action.GetMetadata())
	assert.Nil(t, action.ExtraSigners())

	// Test GetSerializedOutputs with nil output
	action.Outputs = append(action.Outputs, nil)
	_, err = action.GetSerializedOutputs()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot serialize transfer action outputs: nil output at index [2]")

	// Test GetSerializedInputs with nil input
	action.Inputs = append(action.Inputs, nil)
	serInputs, err = action.GetSerializedInputs()
	require.NoError(t, err)
	assert.Len(t, serInputs, 2)
	assert.Nil(t, serInputs[1])
}

func TestTransferAction_DeserializeError(t *testing.T) {
	action := &TransferAction{}

	// Invalid bytes
	err := action.Deserialize([]byte("invalid"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize issue action")

	// Version mismatch
	invalidVersionAction := &actions.TransferAction{
		Version: 0,
	}
	raw, err := proto.Marshal(invalidVersionAction)
	require.NoError(t, err)
	err = action.Deserialize(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid issue version, expected [1], got [0]")

	// Nil output in proto
	nilOutputAction := &actions.TransferAction{
		Version: ProtocolV1,
		Outputs: []*actions.TransferActionOutput{
			nil,
			{Token: nil},
		},
	}
	raw, err = proto.Marshal(nilOutputAction)
	require.NoError(t, err)
	err = action.Deserialize(raw)
	require.NoError(t, err)
	assert.Len(t, action.Outputs, 2)
	assert.Nil(t, action.Outputs[0])
	assert.Nil(t, action.Outputs[1])
}
