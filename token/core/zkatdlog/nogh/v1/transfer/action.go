/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package transfer

import (
	math "github.com/IBM/mathlib"
	factions "github.com/LFDT-Panurus/panurus/token/core/fabtoken/protos-go/v1/actions"
	fv1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/utils"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	protosv1 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	"github.com/LFDT-Panurus/panurus/token/services/utils/protos"
	"github.com/LFDT-Panurus/panurus/token/services/utils/slices"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
)

const ProtocolV1 = 1

// ActionInput represents a single input to a transfer action.
// It includes the token identifier, the token itself, and an optional upgrade witness.
type ActionInput struct {
	ID             *token2.ID
	Token          *token.Token
	UpgradeWitness *token.UpgradeWitness
}

// ToProtos converts the ActionInput to its protobuf representation.
func (a *ActionInput) ToProtos() (*actions.TransferActionInput, error) {
	var id *protosv1.TokenID
	if a.ID != nil {
		id = &protosv1.TokenID{
			TxId:  a.ID.TxId,
			Index: a.ID.Index,
		}
	}
	var input *actions.Token
	if a.Token != nil {
		data, err := utils.ToProtoG1(a.Token.Data)
		if err != nil {
			return nil, err
		}
		input = &actions.Token{
			Owner: a.Token.Owner,
			Data:  data,
		}
	}
	var witness *actions.TransferActionInputUpgradeWitness
	if a.UpgradeWitness != nil {
		blindingFactor, err := utils.ToProtoZr(a.UpgradeWitness.BlindingFactor)
		if err != nil {
			return nil, err
		}
		var fabtoken *factions.Token
		if a.UpgradeWitness.FabToken != nil {
			fabtoken = &factions.Token{
				Owner:    a.UpgradeWitness.FabToken.Owner,
				Type:     string(a.UpgradeWitness.FabToken.Type),
				Quantity: a.UpgradeWitness.FabToken.Quantity,
			}
		}
		witness = &actions.TransferActionInputUpgradeWitness{
			Output:         fabtoken,
			BlindingFactor: blindingFactor,
		}
	}

	return &actions.TransferActionInput{
		TokenId:        id,
		Input:          input,
		UpgradeWitness: witness,
	}, nil
}

// FromProtos populates the ActionInput from its protobuf representation.
func (a *ActionInput) FromProtos(input *actions.TransferActionInput) error {
	if input.TokenId != nil {
		a.ID = &token2.ID{
			TxId:  input.TokenId.TxId,
			Index: input.TokenId.Index,
		}
	}
	if input.Input != nil {
		data, err := utils.FromG1Proto(input.Input.Data)
		if err != nil {
			return err
		}
		a.Token = &token.Token{
			Owner: input.Input.Owner,
			Data:  data,
		}
	}
	if input.UpgradeWitness != nil {
		blindingFactor, err := utils.FromZrProto(input.UpgradeWitness.BlindingFactor)
		if err != nil {
			return err
		}
		var fabtoken *fv1.Output
		if input.UpgradeWitness.Output != nil {
			fabtoken = &fv1.Output{
				Owner:    input.UpgradeWitness.Output.Owner,
				Type:     token2.Type(input.UpgradeWitness.Output.Type),
				Quantity: input.UpgradeWitness.Output.Quantity,
			}
		}
		a.UpgradeWitness = &token.UpgradeWitness{
			FabToken:       fabtoken,
			BlindingFactor: blindingFactor,
		}
	}

	return nil
}

// Action specifies a transfer of one or more tokens
type Action struct {
	// Inputs specify the tokens to be spent
	Inputs []*ActionInput
	// Outputs are the new tokens resulting from the transfer
	Outputs []*token.Token
	// ProofType is the type of proof used by this action
	ProofType rp.ProofType
	// ZK Proof that shows that the transfer is correct
	Proof []byte
	// Metadata contains the transfer action's metadata
	Metadata map[string][]byte
	// Issuer contains the identity of the issuer to sign the transfer action
	Issuer driver.Identity
}

// NewAction returns the Action that matches the passed arguments
func NewAction(
	tokenIDs []*token2.ID,
	inputToken []*token.Token,
	commitments []*math.G1,
	owners [][]byte,
	proof []byte,
	proofType rp.ProofType,
) (*Action, error) {
	if len(commitments) != len(owners) {
		return nil, errors.Wrapf(ErrMismatchedRecipientsOutputs, "number of recipients [%d] does not match number of outputs [%d]", len(commitments), len(owners))
	}
	if len(tokenIDs) != len(inputToken) {
		return nil, errors.Wrapf(ErrMismatchedInputsTokens, "number of inputs [%d] does not match number of input tokens [%d]", len(tokenIDs), len(inputToken))
	}

	inputs := make([]*ActionInput, len(tokenIDs))
	for i, id := range tokenIDs {
		inputs[i] = &ActionInput{
			ID:    id,
			Token: inputToken[i],
		}
	}

	tokens := make([]*token.Token, len(owners))
	for i, o := range commitments {
		tokens[i] = &token.Token{Data: o, Owner: owners[i]}
	}

	return &Action{
		Inputs:    inputs,
		Outputs:   tokens,
		Proof:     proof,
		Metadata:  map[string][]byte{},
		Issuer:    nil,
		ProofType: proofType,
	}, nil
}

// NewActionFromProtos creates a new action from protos
func NewActionFromProtos(raw []byte) (*Action, error) {
	action := &Action{}
	err := action.Deserialize(raw)
	if err != nil {
		return nil, err
	}

	return action, nil
}

// NumInputs returns the number of inputs in the Action
func (t *Action) NumInputs() int {
	return len(t.Inputs)
}

// GetInputs returns the identifiers of the tokens spent in the Action.
// A nil ActionInput entry produces a nil *token2.ID at the corresponding index,
// which is consistent with the slice-length contract used by callers.
func (t *Action) GetInputs() []*token2.ID {
	res := make([]*token2.ID, len(t.Inputs))
	for i, input := range t.Inputs {
		if input == nil {
			res[i] = nil

			continue
		}
		res[i] = input.ID
	}

	return res
}

// GetSerializedInputs returns the serialized tokens spent in the Action.
// Returns an error instead of panicking when an input's Token field is nil
// (which Validate() guards against, but some code paths skip Validate).
func (t *Action) GetSerializedInputs() ([][]byte, error) {
	var res [][]byte
	for i, input := range t.Inputs {
		if input == nil {
			res = append(res, nil)

			continue
		}
		if w := input.UpgradeWitness; w != nil {
			ser, err := w.FabToken.Serialize()
			if err != nil {
				return nil, err
			}
			res = append(res, ser)

			continue
		}
		if input.Token == nil {
			return nil, errors.Errorf("nil token in input at index [%d]", i)
		}
		r, err := input.Token.Serialize()
		if err != nil {
			return nil, err
		}
		res = append(res, r)
	}

	return res, nil
}

// GetSerialNumbers returns nil as zkatdlog doesn't use serial numbers for graph hiding
func (t *Action) GetSerialNumbers() []string {
	return nil
}

// NumOutputs returns the number of outputs in the Action
func (t *Action) NumOutputs() int {
	return len(t.Outputs)
}

// GetOutputs returns the outputs in the Action
func (t *Action) GetOutputs() []driver.Output {
	res := make([]driver.Output, len(t.Outputs))
	for i, outputToken := range t.Outputs {
		res[i] = outputToken
	}

	return res
}

// IsRedeemAt checks if output in the Action at the passed index is redeemed
func (t *Action) IsRedeemAt(index int) bool {
	if index < 0 || index >= len(t.Outputs) {
		return false
	}
	if t.Outputs[index] == nil {
		return false
	}

	return t.Outputs[index].IsRedeem()
}

// IsRedeem checks if this action contains any redeemed outputs
func (t *Action) IsRedeem() bool {
	for _, output := range t.Outputs {
		if output.IsRedeem() {
			return true
		}
	}

	return false
}

// SerializeOutputAt marshals the output in the Action at the passed index.
// Returns an error when index is out of bounds or the output entry is nil
// instead of panicking.
func (t *Action) SerializeOutputAt(index int) ([]byte, error) {
	if index < 0 || index >= len(t.Outputs) {
		return nil, errors.Errorf("SerializeOutputAt: index [%d] out of bounds (len=%d)", index, len(t.Outputs))
	}
	if t.Outputs[index] == nil {
		return nil, errors.Errorf("SerializeOutputAt: nil output at index [%d]", index)
	}

	return t.Outputs[index].Serialize()
}

// GetSerializedOutputs returns the outputs in the Action serialized
func (t *Action) GetSerializedOutputs() ([][]byte, error) {
	res := make([][]byte, len(t.Outputs))
	var err error
	for i, token := range t.Outputs {
		if token == nil {
			return nil, errors.Errorf("nil output entry at index %d", i)
		}
		res[i], err = token.Serialize()
		if err != nil {
			return nil, err
		}
	}

	return res, nil
}

// IsGraphHiding returns false as zkatdlog is not graph hiding
func (t *Action) IsGraphHiding() bool {
	return false
}

// GetMetadata returns the metadata of the Action
func (t *Action) GetMetadata() map[string][]byte {
	return t.Metadata
}

// GetIssuer returns the identity of the issuer who must sign the transaction
func (t *Action) GetIssuer() driver.Identity {
	return t.Issuer
}

// Validate ensures the Action is well-formed
func (t *Action) Validate() error {
	if len(t.Inputs) == 0 {
		return ErrInvalidInputs
	}
	for i, in := range t.Inputs {
		if in == nil {
			return errors.Wrapf(ErrEmptyInput, "invalid input at index [%d], empty input", i)
		}
		if in.ID == nil {
			return errors.Wrapf(ErrEmptyInputID, "invalid input's ID at index [%d], it is empty", i)
		}
		if len(in.ID.TxId) == 0 {
			return errors.Wrapf(ErrEmptyInputTxID, "invalid input's ID at index [%d], tx id is empty", i)
		}
		if in.Token == nil {
			return errors.Wrapf(ErrEmptyInputToken, "invalid input's token at index [%d], empty token", i)
		}
		if err := in.Token.Validate(true); err != nil {
			return errors.Wrapf(err, "invalid input token at index [%d]", i)
		}

		if in.UpgradeWitness != nil {
			if err := in.UpgradeWitness.Validate(); err != nil {
				return errors.Wrapf(err, "invalid input's upgrade witness at index [%d]", i)
			}
		}
	}
	if len(t.Outputs) == 0 {
		return ErrInvalidOutputs
	}
	for i, out := range t.Outputs {
		if out == nil {
			return errors.Wrapf(ErrEmptyOutputToken, "invalid output token at index [%d]", i)
		}
		if err := out.Validate(false); err != nil {
			return errors.Wrapf(err, "invalid output at index [%d]", i)
		}
	}
	// The following check must happen only if the public parameters contain issuers.
	// The validator enforces the signature of an issuer only in that case.
	// if t.IsRedeem() && (t.Issuer == nil) {
	// 	return ErrMissingIssuer
	// }
	if t.ProofType != rp.RangeProofType && t.ProofType != rp.CSPRangeProofType {
		return ErrInvalidProofType
	}
	if len(t.Proof) == 0 {
		return ErrEmptyProof
	}

	return nil
}

// ExtraSigners returns nil as zkatdlog doesn't require extra signers
func (t *Action) ExtraSigners() []driver.Identity {
	return nil
}

// Serialize marshals the TransferAction to bytes
func (t *Action) Serialize() ([]byte, error) {
	// inputs
	inputs, err := protos.ToProtosSlice[actions.TransferActionInput, *ActionInput](t.Inputs)
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize inputs")
	}

	// outputs
	outputs, err := protos.ToProtosSliceFunc(t.Outputs, func(output *token.Token) (*actions.TransferActionOutput, error) {
		data, err := utils.ToProtoG1(output.Data)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to serialize output")
		}

		return &actions.TransferActionOutput{
			Token: &actions.Token{
				Owner: output.Owner,
				Data:  data,
			},
		}, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize outputs")
	}

	var issuer *protosv1.Identity
	if t.Issuer != nil {
		issuer = &protosv1.Identity{
			Raw: t.Issuer.Bytes(),
		}
	}

	var proof *actions.Proof
	switch t.ProofType {
	case rp.RangeProofType:
		proof = &actions.Proof{
			ProofType: &actions.Proof_Proof{
				Proof: t.Proof,
			},
		}
	case rp.CSPRangeProofType:
		proof = &actions.Proof{
			ProofType: &actions.Proof_CspBasedProof{
				CspBasedProof: t.Proof,
			},
		}
	default:
		return nil, ErrInvalidProofType
	}

	action := &actions.TransferAction{
		Version:  ProtocolV1,
		Inputs:   inputs,
		Outputs:  outputs,
		Proof:    proof,
		Metadata: t.Metadata,
		Issuer:   issuer,
	}

	return proto.Marshal(action)
}

// Deserialize un-marshals a TransferAction from bytes
func (t *Action) Deserialize(raw []byte) error {
	action := &actions.TransferAction{}
	err := proto.Unmarshal(raw, action)
	if err != nil {
		return errors.Wrap(err, "failed to deserialize transfer action")
	}

	// assert version
	if action.Version != ProtocolV1 {
		return errors.Wrapf(ErrInvalidVersion, "expected [%d], got [%d]", ProtocolV1, action.Version)
	}

	// inputs
	t.Inputs = make([]*ActionInput, len(action.Inputs))
	t.Inputs = slices.GenericSliceOfPointers[ActionInput](len(action.Inputs))
	if err := protos.FromProtosSlice(action.Inputs, t.Inputs); err != nil {
		return errors.Wrap(err, "failed unmarshalling inputs")
	}

	// outputs
	t.Outputs = make([]*token.Token, len(action.Outputs))
	for j, output := range action.Outputs {
		if output == nil {
			return errors.Errorf("invalid transfer action: output at index [%d] is nil", j)
		}
		if output.Token == nil {
			return errors.Errorf("invalid transfer action: token of output at index [%d] is nil", j)
		}
		data, err := utils.FromG1Proto(output.Token.Data)
		if err != nil {
			return errors.Wrapf(err, "failed to deserialize output")
		}
		t.Outputs[j] = &token.Token{
			Owner: output.Token.Owner,
			Data:  data,
		}
	}

	if action.Proof != nil {
		switch action.Proof.GetProofType().(type) {
		case *actions.Proof_CspBasedProof:
			t.ProofType = rp.CSPRangeProofType
			t.Proof = action.Proof.GetCspBasedProof()
		case *actions.Proof_Proof:
			t.ProofType = rp.RangeProofType
			t.Proof = action.Proof.GetProof()
		default:
			return ErrInvalidProofType
		}
	}
	t.Metadata = action.Metadata

	if action.Issuer != nil {
		t.Issuer = driver.Identity(action.Issuer.Raw)
	} else {
		t.Issuer = nil
	}

	return nil
}

// GetProof returns the zero-knowledge proof in the Action
func (t *Action) GetProof() []byte {
	return t.Proof
}

// GetOutputCommitments returns the cryptographic commitments of the outputs.
// It skips nil output entries defensively; callers (validators) ensure all outputs
// are non-nil via Action.Validate() before this method is reached.
func (t *Action) GetOutputCommitments() []*math.G1 {
	com := make([]*math.G1, 0, len(t.Outputs))
	for _, out := range t.Outputs {
		if out == nil {
			continue
		}
		com = append(com, out.Data)
	}

	return com
}

// InputTokens returns the tokens spent in the Action
func (t *Action) InputTokens() []*token.Token {
	tokens := make([]*token.Token, len(t.Inputs))
	for i, in := range t.Inputs {
		tokens[i] = in.Token
	}

	return tokens
}
