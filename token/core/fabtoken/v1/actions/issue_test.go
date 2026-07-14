/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package actions

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/protos-go/v1/actions"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssueAction_Serialization(t *testing.T) {
	action := &IssueAction{
		Issuer: []byte("issuer"),
		Outputs: []*Output{
			{
				Owner:    []byte("owner"),
				Type:     "a type",
				Quantity: "a quantity",
			},
		},
		Metadata: map[string][]byte{
			"foo": []byte("bar"),
		},
	}
	raw, err := action.Serialize()
	require.NoError(t, err, "failed to serialize a new transfer action")

	action2 := &IssueAction{}
	err = action2.Deserialize(raw)
	require.NoError(t, err, "failed to deserialize a new transfer action")
	assert.Equal(t, action, action2, "deserialized action is not equal to the original one")

	raw2, err := action2.Serialize()
	require.NoError(t, err, "failed to serialize a new transfer action")

	action3 := &IssueAction{}
	err = action3.Deserialize(raw2)
	require.NoError(t, err, "failed to deserialize a new transfer action")
	assert.Equal(t, action2, action3, "deserialized action is not equal to the original one")
}

func TestIssueAction_Validate(t *testing.T) {
	tests := []struct {
		name          string
		action        *IssueAction
		wantErr       bool
		expectedError string
	}{
		{
			name:          "",
			action:        &IssueAction{},
			wantErr:       true,
			expectedError: "issuer is not set",
		},
		{
			name: "",
			action: &IssueAction{
				Issuer: []byte("issuer"),
			},
			wantErr:       true,
			expectedError: "no outputs in issue action",
		},
		{
			name: "",
			action: &IssueAction{
				Issuer: []byte("issuer"),
				Outputs: []*Output{
					nil,
				},
			},
			wantErr:       true,
			expectedError: "nil output in issue action",
		},
		{
			name: "",
			action: &IssueAction{
				Issuer: []byte("issuer"),
				Outputs: []*Output{
					{},
				},
			},
			wantErr:       true,
			expectedError: "invalid output's type at index [0], output type is empty",
		},
		{
			name: "",
			action: &IssueAction{
				Issuer: []byte("issuer"),
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
			action: &IssueAction{
				Issuer: []byte("issuer"),
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

func TestIssueAction_RemainingMethods(t *testing.T) {
	action := &IssueAction{
		Issuer: []byte("issuer"),
		Outputs: []*Output{
			{
				Owner:    []byte("owner"),
				Type:     "type",
				Quantity: "11",
			},
		},
		Metadata: map[string][]byte{
			"foo": []byte("bar"),
		},
	}

	assert.Equal(t, 0, action.NumInputs())
	assert.Nil(t, action.GetInputs())
	serInputs, err := action.GetSerializedInputs()
	require.NoError(t, err)
	assert.Nil(t, serInputs)
	assert.Nil(t, action.GetSerialNumbers())
	assert.Equal(t, 1, action.NumOutputs())
	serOutputs, err := action.GetSerializedOutputs()
	require.NoError(t, err)
	assert.Len(t, serOutputs, 1)
	assert.Len(t, action.GetOutputs(), 1)
	assert.False(t, action.IsAnonymous())
	assert.Equal(t, []byte("issuer"), action.GetIssuer())
	assert.Equal(t, map[string][]byte{"foo": []byte("bar")}, action.GetMetadata())
	assert.False(t, action.IsGraphHiding())
	assert.Nil(t, action.ExtraSigners())

	// Test GetSerializedOutputs with nil output
	action.Outputs = append(action.Outputs, nil)
	serOutputs, err = action.GetSerializedOutputs()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot serialize issue action outputs: nil output at index [1]")
	assert.Nil(t, serOutputs)
}

func TestIssueAction_DeserializeError(t *testing.T) {
	action := &IssueAction{}

	// Invalid bytes
	err := action.Deserialize([]byte("invalid"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize issue action")

	// Version mismatch
	// To test this we need a valid proto with a different version.
	// We can manually create one if we have access to the proto struct.
	// Since we are in the same package, we can use actions.IssueAction
	invalidVersionAction := &actions.IssueAction{
		Version: 0,
	}
	raw, err := proto.Marshal(invalidVersionAction)
	require.NoError(t, err)
	err = action.Deserialize(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid issue version, expected [1], got [0]")

	// Nil output in proto
	nilOutputAction := &actions.IssueAction{
		Version: ProtocolV1,
		Outputs: []*actions.IssueActionOutput{
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
