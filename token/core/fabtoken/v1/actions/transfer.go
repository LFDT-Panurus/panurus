/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package actions

import (
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/protos-go/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/driver"
	driverv1 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	"github.com/LFDT-Panurus/panurus/token/services/utils/protos"
	"github.com/LFDT-Panurus/panurus/token/services/utils/slices"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
)

const ProtocolV1 = 1

type TransferActionInput struct {
	// identifier of token to be transferred
	ID *token.ID
	// Output is the input transferred
	Input *Output
}

func (a *TransferActionInput) ToProtos() (*actions.TransferActionInput, error) {
	var id *driverv1.TokenID
	if a.ID != nil {
		id = &driverv1.TokenID{
			TxId:  a.ID.TxId,
			Index: a.ID.Index,
		}
	}
	var input *actions.Token
	if a.Input != nil {
		input = &actions.Token{
			Owner:    a.Input.Owner,
			Type:     string(a.Input.Type),
			Quantity: a.Input.Quantity,
		}
	}

	return &actions.TransferActionInput{
		TokenId: id,
		Input:   input,
	}, nil
}

func (a *TransferActionInput) FromProtos(input *actions.TransferActionInput) error {
	if input.TokenId != nil {
		a.ID = &token.ID{
			TxId:  input.TokenId.TxId,
			Index: input.TokenId.Index,
		}
	}
	if input.Input != nil {
		a.Input = &Output{
			Owner:    input.Input.Owner,
			Type:     token.Type(input.Input.Type),
			Quantity: input.Input.Quantity,
		}
	}

	return nil
}

// TransferAction encodes a fabtoken transfer
type TransferAction struct {
	Inputs []*TransferActionInput
	// outputs to be created as a result of the transfer
	Outputs []*Output
	// Metadata contains the transfer action's metadata
	Metadata map[string][]byte
	// Issuer contains the identity of the issuer to sign the transfer action
	Issuer driver.Identity
}

func (t *TransferAction) NumInputs() int {
	return len(t.Inputs)
}

// NumOutputs returns the number of outputs in an TransferAction
func (t *TransferAction) NumOutputs() int {
	return len(t.Outputs)
}

// GetSerializedOutputs returns the serialization of the outputs in a TransferAction
func (t *TransferAction) GetSerializedOutputs() ([][]byte, error) {
	var res [][]byte
	for k, output := range t.Outputs {
		if output == nil {
			return nil, errors.Errorf("cannot serialize transfer action outputs: nil output at index [%d]", k)
		}
		ser, err := output.Serialize()
		if err != nil {
			return nil, errors.Errorf("failed to serialize output [%d] in transfer action", k)
		}
		res = append(res, ser)
	}

	return res, nil
}

// GetOutputs returns the outputs in a TransferAction
func (t *TransferAction) GetOutputs() []driver.Output {
	var res []driver.Output
	for _, output := range t.Outputs {
		res = append(res, output)
	}

	return res
}

// IsRedeemAt returns true if the output at the specified index is a redeemed output
// todo update interface to account for nil t.outputs[index]
func (t *TransferAction) IsRedeemAt(index int) bool {
	return t.Outputs[index].IsRedeem()
}

// IsRedeem checks if this action is a Redeem Transfer
func (t *TransferAction) IsRedeem() bool {
	for _, output := range t.Outputs {
		if output.IsRedeem() {
			return true
		}
	}

	return false
}

// IsGraphHiding returns false, indicating that fabtoken does not hide the transaction graph
func (t *TransferAction) IsGraphHiding() bool {
	return false
}

// SerializeOutputAt marshals the output at the specified index in TransferAction
func (t *TransferAction) SerializeOutputAt(index int) ([]byte, error) {
	if index >= len(t.Outputs) {
		return nil, errors.Errorf("failed to serialize output in transfer action: it does not exist")
	}
	if t.Outputs[index] == nil {
		return nil, errors.Errorf("failed to serialize output in transfer action: nil output at index [%d]", index)
	}

	return t.Outputs[index].Serialize()
}

// GetInputs returns inputs of the TransferAction
func (t *TransferAction) GetInputs() []*token.ID {
	res := make([]*token.ID, len(t.Inputs))
	for i, input := range t.Inputs {
		res[i] = input.ID
	}

	return res
}

func (t *TransferAction) GetSerializedInputs() ([][]byte, error) {
	var res [][]byte
	for _, input := range t.Inputs {
		if input == nil {
			res = append(res, nil)

			continue
		}
		r, err := input.Input.Serialize()
		if err != nil {
			return nil, err
		}
		res = append(res, r)
	}

	return res, nil
}

func (t *TransferAction) GetSerialNumbers() []string {
	return nil
}

// GetMetadata returns the transfer action's metadata
func (t *TransferAction) GetMetadata() map[string][]byte {
	return t.Metadata
}

// GetIssuer returns the issuer to sign the transaction
func (t *TransferAction) GetIssuer() driver.Identity {
	return t.Issuer
}

func (t *TransferAction) Validate() error {
	if len(t.Inputs) == 0 {
		return errors.Errorf("invalid number of token inputs, expected at least 1")
	}
	for i, in := range t.Inputs {
		if in == nil {
			return errors.Errorf("invalid input at index [%d], empty input", i)
		}
		if in.ID == nil {
			return errors.Errorf("invalid input's ID at index [%d], it is empty", i)
		}
		if len(in.ID.TxId) == 0 {
			return errors.Errorf("invalid input's ID at index [%d], tx id is empty", i)
		}
		if in.Input == nil {
			return errors.Errorf("invalid input's token at index [%d], empty token", i)
		}
		if err := in.Input.Validate(true); err != nil {
			return errors.Wrapf(err, "invalid input token at index [%d]", i)
		}
	}
	for i, out := range t.Outputs {
		if out == nil {
			return errors.Errorf("invalid output at index [%d], empty output", i)
		}
		if len(out.Type) == 0 {
			return errors.Errorf("invalid output's type at index [%d], output type is empty", i)
		}
		if len(out.Quantity) == 0 {
			return errors.Errorf("invalid output's quantity at index [%d], output quantity is empty", i)
		}
	}
	// The following check must happen only if the public parameters contain issuers.
	// The validator enforces the signature of an issuer only in that case.
	// if t.IsRedeem() && (t.Issuer == nil) {
	// 	return ErrMissingIssuer
	// }

	return nil
}

func (t *TransferAction) ExtraSigners() []driver.Identity {
	return nil
}

// Serialize marshals TransferAction
func (t *TransferAction) Serialize() ([]byte, error) {
	// inputs
	inputs, err := protos.ToProtosSlice[actions.TransferActionInput, *TransferActionInput](t.Inputs)
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize inputs")
	}

	// outputs
	outputs, err := protos.ToProtosSliceFunc(t.Outputs, func(output *Output) (*actions.TransferActionOutput, error) {
		return &actions.TransferActionOutput{
			Token: &actions.Token{
				Owner:    output.Owner,
				Type:     string(output.Type),
				Quantity: output.Quantity,
			},
		}, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize outputs")
	}

	var issuer *driverv1.Identity
	if t.Issuer != nil {
		issuer = &driverv1.Identity{
			Raw: t.Issuer.Bytes(),
		}
	}

	action := &actions.TransferAction{
		Version:  ProtocolV1,
		Inputs:   inputs,
		Outputs:  outputs,
		Metadata: t.Metadata,
		Issuer:   issuer,
	}

	return proto.Marshal(action)
}

// Deserialize un-marshals TransferAction
func (t *TransferAction) Deserialize(raw []byte) error {
	action := &actions.TransferAction{}
	err := proto.Unmarshal(raw, action)
	if err != nil {
		return errors.Wrap(err, "failed to deserialize issue action")
	}

	// assert version
	if action.Version != ProtocolV1 {
		return errors.Errorf("invalid issue version, expected [%d], got [%d]", ProtocolV1, action.Version)
	}

	// inputs
	t.Inputs = make([]*TransferActionInput, len(action.Inputs))
	t.Inputs = slices.GenericSliceOfPointers[TransferActionInput](len(action.Inputs))
	if err := protos.FromProtosSlice(action.Inputs, t.Inputs); err != nil {
		return errors.Wrap(err, "failed unmarshalling receivers metadata")
	}

	// outputs
	t.Outputs = make([]*Output, len(action.Outputs))
	for j, output := range action.Outputs {
		if output == nil || output.Token == nil {
			continue
		}
		t.Outputs[j] = &Output{
			Owner:    output.Token.Owner,
			Type:     token.Type(output.Token.Type),
			Quantity: output.Token.Quantity,
		}
	}

	t.Metadata = action.Metadata
	t.Issuer = nil
	if action.Issuer != nil {
		t.Issuer = action.Issuer.Raw
	}

	return nil
}
