/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue

import (
	math "github.com/IBM/mathlib"
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

// ActionInput represents a token that is being redeemed by an issue action.
type ActionInput struct {
	// ID is the unique identifier of the token.
	ID token2.ID
	// Token is the serialized representation of the token.
	Token []byte
}

// ToProtos converts ActionInput to its protobuf representation.
func (i *ActionInput) ToProtos() (*actions.IssueActionInput, error) {
	return &actions.IssueActionInput{
		Id: &protosv1.TokenID{
			TxId:  i.ID.TxId,
			Index: i.ID.Index,
		},
		Token: i.Token,
	}, nil
}

// FromProtos populates ActionInput from its protobuf representation.
func (i *ActionInput) FromProtos(p *actions.IssueActionInput) error {
	if p.Id != nil {
		i.ID.TxId = p.Id.TxId
		i.ID.Index = p.Id.Index
	}
	i.Token = p.Token

	return nil
}

// Action specifies an issue of one or more tokens.
// It includes the issuer's identity, inputs, outputs, a zero-knowledge proof of validity, and metadata.
type Action struct {
	// Issuer is the identity of the issuer.
	Issuer driver.Identity
	// Inputs are the tokens to be redeemed by this issue action.
	Inputs []*ActionInput
	// Outputs are the newly issued tokens.
	Outputs []*token.Token
	// ProofType is the type of proof
	ProofType rp.ProofType
	// Proof carries the ZKP of IssueAction validity.
	Proof []byte
	// Metadata of the issue action.
	Metadata map[string][]byte
}

// NewAction instantiates an Action given the issuer's identity, token commitments, owners, and a proof.
func NewAction(issuer []byte, coms []*math.G1, owners [][]byte, proof []byte) (*Action, error) {
	if len(owners) != len(coms) {
		return nil, ErrOwnerTokenMismatch
	}

	outputs := make([]*token.Token, len(coms))
	for i, c := range coms {
		outputs[i] = &token.Token{Owner: owners[i], Data: c}
	}

	return &Action{
		Issuer:    issuer,
		Outputs:   outputs,
		Proof:     proof,
		ProofType: rp.RangeProofType,
	}, nil
}

// NumInputs returns the number of inputs in the Action.
func (i *Action) NumInputs() int {
	return len(i.Inputs)
}

// GetInputs returns the identifiers of the tokens redeemed by the Action.
func (i *Action) GetInputs() []*token2.ID {
	res := make([]*token2.ID, len(i.Inputs))
	for i, input := range i.Inputs {
		if input == nil {
			res[i] = nil

			continue
		}
		res[i] = &input.ID
	}

	return res
}

// GetSerializedInputs returns the serialized tokens redeemed by the Action.
func (i *Action) GetSerializedInputs() ([][]byte, error) {
	res := make([][]byte, len(i.Inputs))
	for i, input := range i.Inputs {
		if input == nil {
			res[i] = nil

			continue
		}
		res[i] = input.Token
	}

	return res, nil
}

// GetSerialNumbers returns the serial numbers of the tokens (not used for issue).
func (i *Action) GetSerialNumbers() []string {
	return nil
}

// GetMetadata returns IssueAction metadata if there is any.
func (i *Action) GetMetadata() map[string][]byte {
	return i.Metadata
}

// IsAnonymous returns a Boolean. True if IssueAction is anonymous, and False otherwise.
func (i *Action) IsAnonymous() bool {
	return false
}

// NumOutputs returns the number of outputs in IssueAction
func (i *Action) NumOutputs() int {
	return len(i.Outputs)
}

// GetOutputs returns the Outputs in IssueAction
func (i *Action) GetOutputs() []driver.Output {
	res := make([]driver.Output, len(i.Outputs))
	for i, tok := range i.Outputs {
		res[i] = tok
	}

	return res
}

// GetSerializedOutputs returns the serialization of Outputs
func (i *Action) GetSerializedOutputs() ([][]byte, error) {
	res := make([][]byte, len(i.Outputs))
	for i, tok := range i.Outputs {
		if tok == nil {
			return nil, ErrNilOutput
		}
		var err error
		res[i], err = tok.Serialize()
		if err != nil {
			return nil, errors.Join(ErrSerializeOutputFailed, err)
		}
	}

	return res, nil
}

// GetIssuer returns the Issuer of IssueAction
func (i *Action) GetIssuer() []byte {
	return i.Issuer
}

// IsGraphHiding returns false, this driver does not hide the transaction graph.
func (i *Action) IsGraphHiding() bool {
	return false
}

// Validate ensures the Action is well-formed.
func (i *Action) Validate() error {
	if i.Issuer.IsNone() {
		return ErrIssuerNotSet
	}
	for _, input := range i.Inputs {
		if input == nil {
			return ErrNilInput
		}
		if len(input.Token) == 0 {
			return ErrNilInputToken
		}
		if len(input.ID.TxId) == 0 {
			return ErrNilInputID
		}
	}
	if len(i.Outputs) == 0 {
		return ErrNoOutputs
	}
	for _, output := range i.Outputs {
		if output == nil {
			return ErrNilOutput
		}
	}
	if i.ProofType != rp.RangeProofType && i.ProofType != rp.CSPRangeProofType {
		return ErrInvalidProofType
	}
	if len(i.Proof) == 0 {
		return ErrEmptyProof
	}

	return nil
}

// ExtraSigners returns additional identities that must sign the transaction (none for issue).
func (i *Action) ExtraSigners() []driver.Identity {
	return nil
}

// Serialize marshals the Action into its protobuf-encoded byte representation.
func (i *Action) Serialize() ([]byte, error) {
	// inputs
	inputs, err := protos.ToProtosSlice[actions.IssueActionInput, *ActionInput](i.Inputs)
	if err != nil {
		return nil, errors.Join(ErrSerializeInputsFailed, err)
	}

	// outputs
	outputs, err := protos.ToProtosSliceFunc(i.Outputs, func(output *token.Token) (*actions.IssueActionOutput, error) {
		data, err := utils.ToProtoG1(output.Data)
		if err != nil {
			return nil, errors.Join(ErrSerializeOutputFailed, err)
		}

		return &actions.IssueActionOutput{
			Token: &actions.Token{
				Owner: output.Owner,
				Data:  data,
			},
		}, nil
	})
	if err != nil {
		return nil, errors.Join(ErrSerializeOutputsFailed, err)
	}

	var proof *actions.Proof
	switch i.ProofType {
	case rp.RangeProofType:
		proof = &actions.Proof{
			ProofType: &actions.Proof_Proof{
				Proof: i.Proof,
			},
		}
	case rp.CSPRangeProofType:
		proof = &actions.Proof{
			ProofType: &actions.Proof_CspBasedProof{
				CspBasedProof: i.Proof,
			},
		}
	default:
		return nil, ErrInvalidProofType
	}

	issueAction := &actions.IssueAction{
		Version: ProtocolV1,
		Issuer: &protosv1.Identity{
			Raw: i.Issuer,
		},
		Inputs:   inputs,
		Outputs:  outputs,
		Proof:    proof,
		Metadata: i.Metadata,
	}

	return proto.Marshal(issueAction)
}

// Deserialize unmarshals the Action from its protobuf-encoded byte representation.
func (i *Action) Deserialize(raw []byte) error {
	issueAction := &actions.IssueAction{}
	err := proto.Unmarshal(raw, issueAction)
	if err != nil {
		return errors.Join(ErrDeserializeIssueActionFailed, err)
	}

	// assert version
	if issueAction.Version != ProtocolV1 {
		return errors.Join(ErrInvalidProtocolVersion, errors.Errorf("expected [%d], got [%d]", ProtocolV1, issueAction.Version))
	}

	// inputs
	i.Inputs = make([]*ActionInput, len(issueAction.Inputs))
	i.Inputs = slices.GenericSliceOfPointers[ActionInput](len(issueAction.Inputs))
	if err := protos.FromProtosSlice(issueAction.Inputs, i.Inputs); err != nil {
		return errors.Join(ErrUnmarshalReceiversMetadataFailed, err)
	}

	// outputs
	i.Outputs = make([]*token.Token, len(issueAction.Outputs))
	for j, output := range issueAction.Outputs {
		if output == nil {
			return errors.Errorf("invalid issue action: output at index [%d] is nil", j)
		}
		if output.Token == nil {
			return errors.Errorf("invalid issue action: token of output at index [%d] is nil", j)
		}
		data, err := utils.FromG1Proto(output.Token.Data)
		if err != nil {
			return errors.Join(ErrDeserializeOutputFailed, err)
		}
		i.Outputs[j] = &token.Token{
			Owner: output.Token.Owner,
			Data:  data,
		}
	}

	if issueAction.Proof != nil {
		switch issueAction.Proof.GetProofType().(type) {
		case *actions.Proof_CspBasedProof:
			i.ProofType = rp.CSPRangeProofType
			i.Proof = issueAction.Proof.GetCspBasedProof()
		case *actions.Proof_Proof:
			i.ProofType = rp.RangeProofType
			i.Proof = issueAction.Proof.GetProof()
		default:
			return ErrInvalidProofType
		}
	}
	if issueAction.Issuer != nil {
		i.Issuer = issueAction.Issuer.Raw
	}
	i.Metadata = issueAction.Metadata

	return nil
}

// GetCommitments returns the Pedersen commitments of (type, value) for each output.
func (i *Action) GetCommitments() ([]*math.G1, error) {
	com := make([]*math.G1, len(i.Outputs))
	for j := range com {
		if i.Outputs[j] == nil {
			return nil, ErrNilOutput
		}
		com[j] = i.Outputs[j].Data
	}

	return com, nil
}

// GetProof returns the zero-knowledge proof of the Action's validity.
func (i *Action) GetProof() []byte {
	return i.Proof
}
