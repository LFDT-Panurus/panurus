/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	protosv1 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	snarkactions "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/protos-go/v1/actions"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
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
	SpendProof     []byte // 292 bytes: Groth16 proof for SpendCircuit
}

// toProto converts the SpendDescription to its protobuf representation.
func (s *SpendDescription) toProto() *snarkactions.SpendDescription {
	return &snarkactions.SpendDescription{
		CommitmentIn:   s.CommitmentIn,
		ValueCommitInX: s.ValueCommitInX,
		ValueCommitInY: s.ValueCommitInY,
		TypeCommitment: s.TypeCommitment,
		SpendProof:     s.SpendProof,
	}
}

// spendDescriptionFromProto creates a SpendDescription from its protobuf representation.
func spendDescriptionFromProto(pb *snarkactions.SpendDescription) SpendDescription {
	if pb == nil {
		return SpendDescription{}
	}

	return SpendDescription{
		CommitmentIn:   pb.CommitmentIn,
		ValueCommitInX: pb.ValueCommitInX,
		ValueCommitInY: pb.ValueCommitInY,
		TypeCommitment: pb.TypeCommitment,
		SpendProof:     pb.SpendProof,
	}
}

// TransferAction represents consuming one or more existing tokens and
// creating one or more new tokens, with value conserved across the two
// sets.
//
// It implements the driver.TransferAction interface so that the common
// validator infrastructure can be used.
type TransferAction struct {
	TypeCommitment   []byte       // 32 bytes: MiMC(TokenType, TypeRandomness), shared across all inputs/outputs
	InputIDs         []*token2.ID `json:"InputIDs,omitempty"`    // ledger token IDs of spent tokens; populated by TransferService
	InputTokens      [][]byte     `json:"InputTokens,omitempty"` // serialized OutputDescriptions of spent tokens
	Inputs           []SpendDescription
	Outputs          []OutputDescription
	BindingSignature []byte // 96 bytes: R.X || R.Y || S
	Issuer           []byte `json:"Issuer,omitempty"` // issuer identity for redeem actions
	limits           *driver.ResourceLimits
	Metadata         map[string][]byte `json:"Metadata,omitempty"` // action metadata, set from the transfer options
}

// ── driver.Action ──────────────────────────────────────────────────────────────

// SetLimits configures the resource limits enforced by Validate and Deserialize.
func (a *TransferAction) SetLimits(limits driver.ResourceLimits) {
	a.limits = &limits
}

// Validate performs a basic structural self-check on the action.
func (a *TransferAction) Validate() error {
	if len(a.Inputs) == 0 && len(a.Outputs) == 0 {
		return errors.New("transfer action has no inputs and no outputs")
	}

	return a.checkLimits()
}

// ── driver.ActionWithInputs ────────────────────────────────────────────────────

// NumInputs returns the number of inputs in the action.
func (a *TransferAction) NumInputs() int {
	return len(a.Inputs)
}

// GetInputs returns the ledger token IDs of the tokens being spent.
func (a *TransferAction) GetInputs() []*token2.ID {
	return a.InputIDs
}

// GetSerializedInputs returns the serialized inputs of the action.
// It returns the InputTokens (serialized OutputDescriptions) that were
// populated by TransferService. If InputTokens is empty and no Inputs
// exist, it returns (nil, nil). If Inputs exist but InputTokens was
// never populated, it returns an error, falling back to serializing
// SpendDescriptions would produce OutputSN keys that can never match
// the on-ledger token keys.
func (a *TransferAction) GetSerializedInputs() ([][]byte, error) {
	if len(a.InputTokens) > 0 {
		return a.InputTokens, nil
	}
	if len(a.Inputs) == 0 {
		return nil, nil
	}

	return nil, errors.New("InputTokens not populated: cannot derive serialized inputs from SpendDescriptions alone")
}

// GetSerialNumbers returns nil, zkatsnark does not use serial numbers.
func (a *TransferAction) GetSerialNumbers() []string {
	return nil
}

// IsGraphHiding returns false, zkatsnark does not support graph hiding yet.
func (a *TransferAction) IsGraphHiding() bool {
	return false
}

// GetMetadata returns the metadata of the action.
func (a *TransferAction) GetMetadata() map[string][]byte {
	return a.Metadata
}

// ExtraSigners returns nil, zkatsnark does not require extra signers.
func (a *TransferAction) ExtraSigners() []driver.Identity {
	return nil
}

// ── driver.TransferAction ──────────────────────────────────────────────────────

// Serialize converts the action to bytes using protobuf encoding.
func (a *TransferAction) Serialize() ([]byte, error) {
	inputs := make([]*snarkactions.SpendDescription, len(a.Inputs))
	for i := range a.Inputs {
		inputs[i] = a.Inputs[i].toProto()
	}

	outputs := make([]*snarkactions.OutputDescription, len(a.Outputs))
	for i := range a.Outputs {
		outputs[i] = a.Outputs[i].toProto()
	}

	inputIDs := make([]*protosv1.TokenID, len(a.InputIDs))
	for i, id := range a.InputIDs {
		if id != nil {
			inputIDs[i] = &protosv1.TokenID{
				TxId:  id.TxId,
				Index: id.Index,
			}
		}
	}

	return proto.Marshal(&snarkactions.TransferAction{
		TypeCommitment:   a.TypeCommitment,
		InputIds:         inputIDs,
		InputTokens:      a.InputTokens,
		Inputs:           inputs,
		Outputs:          outputs,
		BindingSignature: a.BindingSignature,
		Issuer:           a.Issuer,
		Metadata:         a.Metadata,
	})
}

// Deserialize populates the action from bytes produced by Serialize.
func (a *TransferAction) Deserialize(raw []byte) error {
	pb := &snarkactions.TransferAction{}
	if err := proto.Unmarshal(raw, pb); err != nil {
		return errors.Wrap(err, "failed to deserialize transfer action")
	}

	a.TypeCommitment = pb.TypeCommitment
	a.BindingSignature = pb.BindingSignature
	a.Issuer = pb.Issuer
	a.Metadata = pb.Metadata
	a.InputTokens = pb.InputTokens

	a.InputIDs = make([]*token2.ID, len(pb.InputIds))
	for i, id := range pb.InputIds {
		if id != nil {
			a.InputIDs[i] = &token2.ID{
				TxId:  id.TxId,
				Index: id.Index,
			}
		}
	}

	a.Inputs = make([]SpendDescription, len(pb.Inputs))
	for i, in := range pb.Inputs {
		a.Inputs[i] = spendDescriptionFromProto(in)
	}

	a.Outputs = make([]OutputDescription, len(pb.Outputs))
	for i, out := range pb.Outputs {
		a.Outputs[i] = outputDescriptionFromProto(out)
	}

	return a.checkLimits()
}

// checkLimits enforces the configured resource limits on the action.
func (a *TransferAction) checkLimits() error {
	limits := effectiveLimits(a.limits)
	if err := checkCountLimits(len(a.Inputs), len(a.Outputs), limits); err != nil {
		return err
	}

	return checkMetadataLimits(a.Metadata, limits)
}

// NumOutputs returns the number of outputs.
func (a *TransferAction) NumOutputs() int {
	return len(a.Outputs)
}

// GetSerializedOutputs returns each output serialized as protobuf.
func (a *TransferAction) GetSerializedOutputs() ([][]byte, error) {
	res := make([][]byte, len(a.Outputs))
	for i := range a.Outputs {
		raw, err := a.Outputs[i].Serialize()
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

	return a.Outputs[index].Serialize()
}

// GetIssuer returns the issuer identity for redeem actions, or nil for
// regular transfers.
func (a *TransferAction) GetIssuer() driver.Identity {
	if len(a.Issuer) == 0 {
		return nil
	}

	return a.Issuer
}
