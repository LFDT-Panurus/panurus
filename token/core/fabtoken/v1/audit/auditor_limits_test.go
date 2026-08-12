/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package audit

import (
	"testing"

	protoactions "github.com/LFDT-Panurus/panurus/token/core/fabtoken/protos-go/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/require"
)

// marshalIssueAction builds a serialized fabtoken issue action with the given
// number of outputs.
func marshalIssueAction(t *testing.T, outputs int) []byte {
	t.Helper()
	ia := &protoactions.IssueAction{
		Version: actions.ProtocolV1,
		Issuer:  nil,
	}
	for range outputs {
		ia.Outputs = append(ia.Outputs, &protoactions.IssueActionOutput{Token: &protoactions.Token{Owner: []byte("o"), Type: "TYPE", Quantity: "0x1"}})
	}
	raw, err := proto.Marshal(ia)
	require.NoError(t, err)

	return raw
}

// marshalTransferAction builds a serialized fabtoken transfer action with the
// given number of inputs and outputs.
func marshalTransferAction(t *testing.T, inputs, outputs int) []byte {
	t.Helper()
	ta := &protoactions.TransferAction{
		Version: actions.ProtocolV1,
	}
	for range inputs {
		ta.Inputs = append(ta.Inputs, &protoactions.TransferActionInput{Input: &protoactions.Token{Owner: []byte("o"), Type: "TYPE", Quantity: "0x1"}})
	}
	for range outputs {
		ta.Outputs = append(ta.Outputs, &protoactions.TransferActionOutput{Token: &protoactions.Token{Owner: []byte("o"), Type: "TYPE", Quantity: "0x1"}})
	}
	raw, err := proto.Marshal(ta)
	require.NoError(t, err)

	return raw
}

func issueRequest(raw []byte) *driver.TokenRequest {
	return &driver.TokenRequest{
		Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: raw}},
	}
}

func transferRequest(raw []byte) *driver.TokenRequest {
	return &driver.TokenRequest{
		Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: raw}},
	}
}

// TestActionDeserializer_AppliesConfiguredLimits is a regression test for issue
// #2028: the audit path must enforce the operator-configured resource limits,
// not silently fall back to driver.DefaultResourceLimits(). It exercises a limit
// tighter than the defaults so that a missing SetLimits call (the original bug)
// would let the oversized action through and fail the test.
func TestActionDeserializer_AppliesConfiguredLimits(t *testing.T) {
	custom := driver.DefaultResourceLimits()
	custom.MaxOutputs = 2
	custom.MaxInputs = 2
	d := &ActionDeserializer{Limits: custom}

	t.Run("issue within limit", func(t *testing.T) {
		_, _, err := d.DeserializeActions(issueRequest(marshalIssueAction(t, 2)))
		require.NoError(t, err)
	})
	t.Run("issue exceeds configured limit", func(t *testing.T) {
		_, _, err := d.DeserializeActions(issueRequest(marshalIssueAction(t, 3)))
		require.ErrorIs(t, err, actions.ErrTooManyOutputs)
	})

	t.Run("transfer within limit", func(t *testing.T) {
		_, _, err := d.DeserializeActions(transferRequest(marshalTransferAction(t, 2, 2)))
		require.NoError(t, err)
	})
	t.Run("transfer exceeds configured input limit", func(t *testing.T) {
		_, _, err := d.DeserializeActions(transferRequest(marshalTransferAction(t, 3, 1)))
		require.ErrorIs(t, err, actions.ErrTooManyInputs)
	})
	t.Run("transfer exceeds configured output limit", func(t *testing.T) {
		_, _, err := d.DeserializeActions(transferRequest(marshalTransferAction(t, 1, 3)))
		require.ErrorIs(t, err, actions.ErrTooManyOutputs)
	})
}
