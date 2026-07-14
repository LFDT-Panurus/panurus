/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import "github.com/LFDT-Panurus/panurus/token/token"

// SetupAction defines the interface for actions that update the driver's public parameters.
type SetupAction interface {
	// GetSetupParameters returns the serialized public parameters from the setup action.
	GetSetupParameters() ([]byte, error)
}

//go:generate counterfeiter -o mock/ia.go -fake-name IssueAction . IssueAction

// IssueAction represents a single token issuance event on the ledger.
type IssueAction interface {
	Action
	ActionWithInputs
	// Serialize converts the action into its serialized byte representation.
	Serialize() ([]byte, error)
	// NumOutputs returns the total number of tokens created by this issuance.
	NumOutputs() int
	// GetSerializedOutputs retrieves the serialized representation of each created token.
	GetSerializedOutputs() ([][]byte, error)
	// GetOutputs returns the list of created tokens as Output interfaces.
	GetOutputs() []Output
	// IsAnonymous indicates whether the issuer's identity is hidden.
	IsAnonymous() bool
	// GetIssuer returns the identifier of the party that issued the tokens.
	GetIssuer() []byte
	// GetMetadata returns any additional driver-specific metadata associated with the issuance.
	GetMetadata() map[string][]byte
	// IsGraphHiding indicates whether the link between inputs and outputs is obfuscated.
	IsGraphHiding() bool
	// ExtraSigners returns any additional identities that must sign this action.
	ExtraSigners() []Identity
}

// Input represents a specific token that is being spent in a transaction.
type Input interface {
	// GetOwner returns the cryptographic owner of the token.
	GetOwner() []byte
}

// Output represents a token that is being created as a result of a transaction.
type Output interface {
	// Serialize converts the output into its serialized byte representation.
	Serialize() ([]byte, error)
	// IsRedeem indicates whether the output is being redeemed (burned) rather than assigned to an owner.
	IsRedeem() bool
	// GetOwner returns the cryptographic owner assigned to the created token.
	GetOwner() []byte
}

//go:generate counterfeiter -o mock/ta.go -fake-name TransferAction . TransferAction

// TransferAction represents a token transfer event on the ledger.
type TransferAction interface {
	Action
	ActionWithInputs
	// Serialize converts the action into its serialized byte representation.
	Serialize() ([]byte, error)
	// NumOutputs returns the total number of tokens created by this transfer.
	NumOutputs() int
	// GetSerializedOutputs retrieves the serialized representation of each created token.
	GetSerializedOutputs() ([][]byte, error)
	// GetOutputs returns the list of created tokens as Output interfaces.
	GetOutputs() []Output
	// IsRedeemAt checks if a specific output, by its index, is a redeem output.
	IsRedeemAt(index int) bool
	// SerializeOutputAt returns the serialized representation of a specific output.
	SerializeOutputAt(index int) ([]byte, error)
	// IsGraphHiding indicates whether the link between inputs and outputs is obfuscated.
	IsGraphHiding() bool
	// GetMetadata returns any additional driver-specific metadata associated with the transfer.
	GetMetadata() map[string][]byte
	// GetIssuer returns the identity of the issuer in cases where the transfer includes redemption.
	GetIssuer() Identity
}

//go:generate counterfeiter -o mock/action_with_inputs.go -fake-name ActionWithInputs . ActionWithInputs

// ActionWithInputs models an action with inputs
type ActionWithInputs interface {
	// NumInputs returns the number of inputs in the action
	NumInputs() int
	// GetInputs returns the identifiers of the inputs in the action.
	GetInputs() []*token.ID
	// GetSerializedInputs returns the serialized inputs of the action
	GetSerializedInputs() ([][]byte, error)
	// GetSerialNumbers returns the serial numbers of the inputs if this action supports graph hiding
	GetSerialNumbers() []string
	// IsGraphHiding returns true if the action is graph hiding
	IsGraphHiding() bool
	// GetMetadata returns the action's metadata
	GetMetadata() map[string][]byte
	// ExtraSigners returns the extra signers of the action
	ExtraSigners() []Identity
}

type Action interface {
	Validate() error
}

//go:generate counterfeiter -o mock/action_deserializer.go -fake-name ActionDeserializer . ActionDeserializer

type ActionDeserializer[TA TransferAction, IA IssueAction] interface {
	DeserializeActions(tr *TokenRequest) ([]IA, []TA, error)
}
