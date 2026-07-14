/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package translator

import "github.com/LFDT-Panurus/panurus/token/token"

type SetupAction interface {
	GetSetupParameters() ([]byte, error)
}

//go:generate counterfeiter -o mock/issue_action.go -fake-name IssueAction . IssueAction

type IssueAction interface {
	ActionWithInputs
	Serialize() ([]byte, error)
	NumOutputs() int
	GetSerializedOutputs() ([][]byte, error)
	IsAnonymous() bool
	GetIssuer() []byte
	GetMetadata() map[string][]byte
	// IsGraphHiding returns true if the action is graph hiding
	IsGraphHiding() bool
}

//go:generate counterfeiter -o mock/transfer_action.go -fake-name TransferAction . TransferAction

// TransferAction is the action used to transfer tokens
type TransferAction interface {
	ActionWithInputs
	// Serialize returns the serialized version of the action
	Serialize() ([]byte, error)
	// NumOutputs returns the number of outputs of the action
	NumOutputs() int
	// GetSerializedOutputs returns the serialized outputs of the action
	GetSerializedOutputs() ([][]byte, error)
	// IsRedeemAt returns true if the output is a redeem output at the passed index
	IsRedeemAt(index int) bool
	// SerializeOutputAt returns the serialized output at the passed index
	SerializeOutputAt(index int) ([]byte, error)
	// IsGraphHiding returns true if the action is graph hiding
	IsGraphHiding() bool
	// GetMetadata returns the action's metadata
	GetMetadata() map[string][]byte
}

//go:generate counterfeiter -o mock/action_with_inputs.go -fake-name ActionWithInputs . ActionWithInputs

type ActionWithInputs interface {
	// GetInputs returns the identifiers of the inputs in the action.
	GetInputs() []*token.ID
	// GetSerializedInputs returns the serialized inputs of the action
	GetSerializedInputs() ([][]byte, error)
	// GetSerialNumbers returns the serial numbers of the inputs if this action supports graph hiding
	GetSerialNumbers() []string
}
