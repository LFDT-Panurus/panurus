/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// SpendDescription is the public wire-format record for one token being
// consumed as an input. The commitment being spent, its value commitment,
// and its type commitment are all public; the value, token type, randomness,
// type randomness, and value-commitment randomness that open them are not.
type SpendDescription struct {
	CommitmentIn   []byte // 32 bytes
	ValueCommitInX []byte // 32 bytes
	ValueCommitInY []byte // 32 bytes
	TypeCommitment []byte // 32 bytes: MiMC(TokenType, TypeRandomness)
	SpendProof     []byte // 244 bytes: Groth16 proof for SpendCircuit
}

// TransferAction represents consuming one or more existing tokens and
// creating one or more new tokens, with value conserved across the two
// sets.
//
// It implements the driver.TransferAction interface so that the common
// validator infrastructure can be used.
type TransferAction struct {
	TypeCommitment   []byte // 32 bytes: MiMC(TokenType, TypeRandomness), shared across all inputs/outputs
	Inputs           []SpendDescription
	Outputs          []OutputDescription
	BindingSignature []byte // 96 bytes: R.X || R.Y || S
}

// ── driver.Action ──────────────────────────────────────────────────────────────

// Validate performs a basic structural self-check on the action.
func (a *TransferAction) Validate() error {
	if len(a.Inputs) == 0 && len(a.Outputs) == 0 {
		return errors.New("transfer action has no inputs and no outputs")
	}

	return nil
}

// ── driver.ActionWithInputs ────────────────────────────────────────────────────

// NumInputs returns the number of inputs in the action.
func (a *TransferAction) NumInputs() int {
	return len(a.Inputs)
}

// GetInputs returns nil; zkatsnark inputs are identified by commitment,
// not by ledger token IDs on the action itself.
func (a *TransferAction) GetInputs() []*token2.ID {
	return nil
}

// GetSerializedInputs returns the serialized inputs of the action.
func (a *TransferAction) GetSerializedInputs() ([][]byte, error) {
	res := make([][]byte, len(a.Inputs))
	for i, in := range a.Inputs {
		raw, err := json.Marshal(in)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to serialize input %d", i)
		}

		res[i] = raw
	}

	return res, nil
}

// GetSerialNumbers returns nil, zkatsnark does not use serial numbers.
func (a *TransferAction) GetSerialNumbers() []string {
	return nil
}

// IsGraphHiding returns false, zkatsnark does not support graph hiding yet.
func (a *TransferAction) IsGraphHiding() bool {
	return false
}

// GetMetadata returns nil, zkatsnark has no per-action metadata.
func (a *TransferAction) GetMetadata() map[string][]byte {
	return nil
}

// ExtraSigners returns nil, zkatsnark does not require extra signers.
func (a *TransferAction) ExtraSigners() []driver.Identity {
	return nil
}

// ── driver.TransferAction ──────────────────────────────────────────────────────

// Serialize converts the action to bytes using JSON encoding.
func (a *TransferAction) Serialize() ([]byte, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize transfer action")
	}

	return raw, nil
}

// Deserialize populates the action from bytes produced by Serialize.
func (a *TransferAction) Deserialize(raw []byte) error {
	return json.Unmarshal(raw, a)
}

// NumOutputs returns the number of outputs.
func (a *TransferAction) NumOutputs() int {
	return len(a.Outputs)
}

// GetSerializedOutputs returns each output serialized as JSON.
func (a *TransferAction) GetSerializedOutputs() ([][]byte, error) {
	res := make([][]byte, len(a.Outputs))
	for i, out := range a.Outputs {
		raw, err := json.Marshal(out)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to serialize output %d", i)
		}

		res[i] = raw
	}

	return res, nil
}

// GetOutputs returns the outputs as driver.Output interfaces.
func (a *TransferAction) GetOutputs() []driver.Output {
	res := make([]driver.Output, len(a.Outputs))
	for i := range a.Outputs {
		res[i] = &a.Outputs[i]
	}

	return res
}

// IsRedeemAt checks if the output at the given index is a redeem output.
func (a *TransferAction) IsRedeemAt(index int) bool {
	if index < 0 || index >= len(a.Outputs) {
		return false
	}

	return a.Outputs[index].IsRedeem()
}

// SerializeOutputAt serializes the output at the given index.
func (a *TransferAction) SerializeOutputAt(index int) ([]byte, error) {
	if index < 0 || index >= len(a.Outputs) {
		return nil, errors.Errorf("SerializeOutputAt: index [%d] out of bounds (len=%d)", index, len(a.Outputs))
	}

	return json.Marshal(a.Outputs[index])
}

// GetIssuer returns nil, transfers have no issuer.
func (a *TransferAction) GetIssuer() driver.Identity {
	return nil
}
