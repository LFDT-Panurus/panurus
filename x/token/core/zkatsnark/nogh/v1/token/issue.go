/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	snarkactions "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/protos-go/v1/actions"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
)

// OutputDescription is the public wire-format record for one newly created
// token. Everything here is safe to publish, the note secrets (value,
// type, randomness) that this commitment opens to are never included.
type OutputDescription struct {
	CommitmentOut   []byte // 32 bytes: MiMC(value, type, randomness)
	ValueCommitOutX []byte // 32 bytes: Jubjub X coordinate of cv
	ValueCommitOutY []byte // 32 bytes: Jubjub Y coordinate of cv
	TypeCommitment  []byte // 32 bytes: MiMC(TokenType, TypeRandomness)
	OutputProof     []byte // 292 bytes: Groth16 proof for OutputCircuit
	Recipient       []byte // intended recipient identity
}

// ── driver.Output interface ────────────────────────────────────────────────────

// Serialize converts the output description to bytes using protobuf encoding.
func (o *OutputDescription) Serialize() ([]byte, error) {
	raw, err := proto.Marshal(o.toProto())
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize output description")
	}

	return raw, nil
}

// toProto converts the OutputDescription to its protobuf representation.
func (o *OutputDescription) toProto() *snarkactions.OutputDescription {
	return &snarkactions.OutputDescription{
		CommitmentOut:   o.CommitmentOut,
		ValueCommitOutX: o.ValueCommitOutX,
		ValueCommitOutY: o.ValueCommitOutY,
		TypeCommitment:  o.TypeCommitment,
		OutputProof:     o.OutputProof,
		Recipient:       o.Recipient,
	}
}

// outputDescriptionFromProto creates an OutputDescription from its protobuf representation.
func outputDescriptionFromProto(pb *snarkactions.OutputDescription) OutputDescription {
	if pb == nil {
		return OutputDescription{}
	}

	return OutputDescription{
		CommitmentOut:   pb.CommitmentOut,
		ValueCommitOutX: pb.ValueCommitOutX,
		ValueCommitOutY: pb.ValueCommitOutY,
		TypeCommitment:  pb.TypeCommitment,
		OutputProof:     pb.OutputProof,
		Recipient:       pb.Recipient,
	}
}

// DeserializeOutputDescription reconstructs an OutputDescription from bytes
// produced by Serialize.
func DeserializeOutputDescription(raw []byte) (*OutputDescription, error) {
	pb := &snarkactions.OutputDescription{}
	if err := proto.Unmarshal(raw, pb); err != nil {
		return nil, errors.Wrap(err, "failed to deserialize output description")
	}
	od := outputDescriptionFromProto(pb)

	return &od, nil
}

// IsRedeem returns true if this output is a redeem (burn) output.
// In zkatsnark, a nil or empty Recipient signals a redeem.
func (o *OutputDescription) IsRedeem() bool {
	return len(o.Recipient) == 0
}

// GetOwner returns the recipient identity for this output.
func (o *OutputDescription) GetOwner() []byte {
	return o.Recipient
}

// IssueAction represents the creation of one or more new tokens.
//
// There are no SpendDescriptions — issuance consumes nothing. The binding
// signature's signing key is bsk = −Σrcv_out (no input RCVs to add), and it
// attests that every output's value commitment is correctly formed, not
// that any conservation equation holds — there is nothing to conserve
// against for freshly minted tokens.
//
// It implements the driver.IssueAction interface so that the common
// validator infrastructure can be used.
type IssueAction struct {
	Issuer           []byte
	TokenType        string // kept in cleartext for issuer authorization policy checks
	TypeCommitment   []byte // 32 bytes: MiMC(TokenType, TypeRandomness), shared across all outputs
	Outputs          []OutputDescription
	BindingSignature []byte // 96 bytes: R.X || R.Y || S
	TotalValue       []byte
	limits           *driver.ResourceLimits
	Metadata         map[string][]byte `json:"Metadata,omitempty"` // action metadata, set from the issue options
}

// ── driver.Action ──────────────────────────────────────────────────────────────

// SetLimits configures the resource limits enforced by Validate and Deserialize.
func (a *IssueAction) SetLimits(limits driver.ResourceLimits) {
	a.limits = &limits
}

// Validate performs a basic structural self-check on the action.
func (a *IssueAction) Validate() error {
	if len(a.Outputs) == 0 {
		return errors.New("issue action has no outputs")
	}

	return a.checkLimits()
}

// ── driver.ActionWithInputs ────────────────────────────────────────────────────

// NumInputs returns 0, issuance has no inputs.
func (a *IssueAction) NumInputs() int {
	return 0
}

// GetInputs returns nil, issuance has no inputs.
func (a *IssueAction) GetInputs() []*token2.ID {
	return nil
}

// GetSerializedInputs returns nil, issuance has no inputs.
func (a *IssueAction) GetSerializedInputs() ([][]byte, error) {
	return nil, nil
}

// GetSerialNumbers returns nil, zkatsnark does not use serial numbers.
func (a *IssueAction) GetSerialNumbers() []string {
	return nil
}

// IsGraphHiding returns false, zkatsnark does not support graph hiding.
func (a *IssueAction) IsGraphHiding() bool {
	return false
}

// GetMetadata returns the metadata of the action.
func (a *IssueAction) GetMetadata() map[string][]byte {
	return a.Metadata
}

// ExtraSigners returns nil, zkatsnark does not require extra signers.
func (a *IssueAction) ExtraSigners() []driver.Identity {
	return nil
}

// ── driver.IssueAction ─────────────────────────────────────────────────────────

// Serialize converts the action to bytes using protobuf encoding.
func (a *IssueAction) Serialize() ([]byte, error) {
	outputs := make([]*snarkactions.OutputDescription, len(a.Outputs))
	for i := range a.Outputs {
		outputs[i] = a.Outputs[i].toProto()
	}

	return proto.Marshal(&snarkactions.IssueAction{
		Issuer:           a.Issuer,
		TokenType:        a.TokenType,
		TypeCommitment:   a.TypeCommitment,
		Outputs:          outputs,
		BindingSignature: a.BindingSignature,
		TotalValue:       a.TotalValue,
		Metadata:         a.Metadata,
	})
}

// Deserialize populates the action from bytes produced by Serialize.
func (a *IssueAction) Deserialize(raw []byte) error {
	pb := &snarkactions.IssueAction{}
	if err := proto.Unmarshal(raw, pb); err != nil {
		return errors.Wrap(err, "failed to deserialize issue action")
	}

	a.Issuer = pb.Issuer
	a.TokenType = pb.TokenType
	a.TypeCommitment = pb.TypeCommitment
	a.BindingSignature = pb.BindingSignature
	a.TotalValue = pb.TotalValue
	a.Metadata = pb.Metadata

	a.Outputs = make([]OutputDescription, len(pb.Outputs))
	for i, out := range pb.Outputs {
		a.Outputs[i] = outputDescriptionFromProto(out)
	}

	return a.checkLimits()
}

// checkLimits enforces the configured resource limits on the action.
func (a *IssueAction) checkLimits() error {
	limits := effectiveLimits(a.limits)
	if err := checkCountLimits(0, len(a.Outputs), limits); err != nil {
		return err
	}

	return checkMetadataLimits(a.Metadata, limits)
}

// NumOutputs returns the number of outputs.
func (a *IssueAction) NumOutputs() int {
	return len(a.Outputs)
}

// GetSerializedOutputs returns each output serialized as protobuf.
func (a *IssueAction) GetSerializedOutputs() ([][]byte, error) {
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
func (a *IssueAction) GetOutputs() []driver.Output {
	res := make([]driver.Output, len(a.Outputs))
	for i := range a.Outputs {
		res[i] = &a.Outputs[i]
	}

	return res
}

// IsAnonymous returns false, zkatsnark issuers are not anonymous.
func (a *IssueAction) IsAnonymous() bool {
	return false
}

// GetIssuer returns the issuer identity.
func (a *IssueAction) GetIssuer() []byte {
	return a.Issuer
}
