/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	protosv1 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	snarkactions "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/protos-go/v1/actions"
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
// There are no SpendDescriptions: a plain issuance consumes nothing. An
// upgrade issuance (burn-and-re-issue of legacy fabtoken outputs) lists the
// redeemed fabtokens in Inputs and reveals TypeRandomness, so that the
// validator can bind the minted type and value to the redeemed ones. The binding
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
	// Inputs are the legacy fabtoken outputs redeemed by an upgrade issuance; empty otherwise.
	Inputs []*IssueActionInput `json:"Inputs,omitempty"`
	// TypeRandomness opens TypeCommitment. It is set if and only if Inputs is
	// non-empty: the token type is then already public through the inputs.
	TypeRandomness []byte `json:"TypeRandomness,omitempty"`
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
	if err := a.validateInputs(); err != nil {
		return err
	}

	return a.checkLimits()
}

// validateInputs checks the upgrade inputs are complete and pairwise distinct,
// and that TypeRandomness is present exactly when there are inputs.
func (a *IssueAction) validateInputs() error {
	if len(a.Inputs) == 0 {
		if len(a.TypeRandomness) != 0 {
			return errors.New("issue action reveals type randomness but has no inputs")
		}

		return nil
	}
	if len(a.TypeRandomness) == 0 {
		return errors.New("upgrade issue action must reveal its type randomness")
	}
	seen := make(map[token2.ID]struct{}, len(a.Inputs))
	for i, in := range a.Inputs {
		if in == nil || len(in.Token) == 0 || len(in.ID.TxId) == 0 {
			return errors.Wrapf(ErrInvalidUpgradeInput, "input [%d] is nil or incomplete", i)
		}
		if _, ok := seen[in.ID]; ok {
			return errors.Wrapf(ErrDuplicateUpgradeInput, "input [%d] [%s]", i, in.ID)
		}
		seen[in.ID] = struct{}{}
	}

	return nil
}

// ── driver.ActionWithInputs ────────────────────────────────────────────────────

// NumInputs returns the number of redeemed fabtoken outputs.
func (a *IssueAction) NumInputs() int {
	return len(a.Inputs)
}

// GetInputs returns the ledger IDs of the redeemed fabtoken outputs.
func (a *IssueAction) GetInputs() []*token2.ID {
	if len(a.Inputs) == 0 {
		return nil
	}
	res := make([]*token2.ID, len(a.Inputs))
	for i, in := range a.Inputs {
		if in != nil {
			res[i] = &in.ID
		}
	}

	return res
}

// GetSerializedInputs returns the redeemed fabtoken outputs exactly as stored
// on the ledger.
func (a *IssueAction) GetSerializedInputs() ([][]byte, error) {
	if len(a.Inputs) == 0 {
		return nil, nil
	}
	res := make([][]byte, len(a.Inputs))
	for i, in := range a.Inputs {
		if in == nil {
			return nil, errors.Errorf("nil input at index [%d]", i)
		}
		res[i] = in.Token
	}

	return res, nil
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

// ExtraSigners returns nil: as in zkatdlog, an upgrade issuance is authorized
// by the issuer signature alone. The owners consent off-chain, through the
// upgrade proof the issuer verifies before assembling the issuance.
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
	var inputs []*snarkactions.IssueActionInput
	for i, in := range a.Inputs {
		if in == nil {
			return nil, errors.Errorf("nil input at index [%d]", i)
		}
		inputs = append(inputs, &snarkactions.IssueActionInput{
			Id:    &protosv1.TokenID{TxId: in.ID.TxId, Index: in.ID.Index},
			Token: in.Token,
		})
	}

	return proto.Marshal(&snarkactions.IssueAction{
		Issuer:           a.Issuer,
		TokenType:        a.TokenType,
		TypeCommitment:   a.TypeCommitment,
		Outputs:          outputs,
		BindingSignature: a.BindingSignature,
		TotalValue:       a.TotalValue,
		Metadata:         a.Metadata,
		Inputs:           inputs,
		TypeRandomness:   a.TypeRandomness,
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

	a.TypeRandomness = pb.TypeRandomness

	a.Outputs = make([]OutputDescription, len(pb.Outputs))
	for i, out := range pb.Outputs {
		a.Outputs[i] = outputDescriptionFromProto(out)
	}

	a.Inputs = nil
	if len(pb.Inputs) > 0 {
		a.Inputs = make([]*IssueActionInput, len(pb.Inputs))
		for i, in := range pb.Inputs {
			if in == nil || in.Id == nil {
				return errors.Wrapf(ErrInvalidUpgradeInput, "input [%d] has no id", i)
			}
			a.Inputs[i] = &IssueActionInput{
				ID:    token2.ID{TxId: in.Id.TxId, Index: in.Id.Index},
				Token: in.Token,
			}
		}
	}

	return a.checkLimits()
}

// checkLimits enforces the configured resource limits on the action.
func (a *IssueAction) checkLimits() error {
	limits := effectiveLimits(a.limits)
	if err := checkCountLimits(len(a.Inputs), len(a.Outputs), limits); err != nil {
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
