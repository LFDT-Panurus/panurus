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
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
)

// IssueAction encodes a fabtoken Issue
type IssueAction struct {
	// issuer's public key
	Issuer driver.Identity
	// new tokens to be issued
	Outputs []*Output
	// metadata of the issue action
	Metadata map[string][]byte
}

func (i *IssueAction) NumInputs() int {
	return 0
}

func (i *IssueAction) GetInputs() []*token.ID {
	return nil
}

func (i *IssueAction) GetSerializedInputs() ([][]byte, error) {
	return nil, nil
}

func (i *IssueAction) GetSerialNumbers() []string {
	return nil
}

// Serialize marshal IssueAction
func (i *IssueAction) Serialize() ([]byte, error) {
	// outputs
	outputs, err := protos.ToProtosSliceFunc(i.Outputs, func(output *Output) (*actions.IssueActionOutput, error) {
		return &actions.IssueActionOutput{
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

	issueAction := &actions.IssueAction{
		Version: ProtocolV1,
		Issuer: &driverv1.Identity{
			Raw: i.Issuer,
		},
		Outputs:  outputs,
		Metadata: i.Metadata,
	}

	return proto.Marshal(issueAction)
}

// Deserialize un-marshals IssueAction
func (i *IssueAction) Deserialize(raw []byte) error {
	issueAction := &actions.IssueAction{}
	err := proto.Unmarshal(raw, issueAction)
	if err != nil {
		return errors.Wrap(err, "failed to deserialize issue action")
	}

	// assert version
	if issueAction.Version != ProtocolV1 {
		return errors.Errorf("invalid issue version, expected [%d], got [%d]", ProtocolV1, issueAction.Version)
	}

	// outputs
	i.Outputs = make([]*Output, len(issueAction.Outputs))
	for j, output := range issueAction.Outputs {
		if output == nil || output.Token == nil {
			continue
		}
		i.Outputs[j] = &Output{
			Owner:    output.Token.Owner,
			Type:     token.Type(output.Token.Type),
			Quantity: output.Token.Quantity,
		}
	}

	if issueAction.Issuer != nil {
		i.Issuer = issueAction.Issuer.Raw
	}
	i.Metadata = issueAction.Metadata

	return nil
}

// NumOutputs returns the number of outputs in an IssueAction
func (i *IssueAction) NumOutputs() int {
	return len(i.Outputs)
}

// GetSerializedOutputs returns the serialization of the outputs in an IssueAction
func (i *IssueAction) GetSerializedOutputs() ([][]byte, error) {
	var res [][]byte
	for k, output := range i.Outputs {
		if output == nil {
			return nil, errors.Errorf("cannot serialize issue action outputs: nil output at index [%d]", k)
		}
		ser, err := output.Serialize()
		if err != nil {
			return nil, errors.Errorf("failed to serialize output [%d] in issue action", k)
		}
		res = append(res, ser)
	}

	return res, nil
}

// GetOutputs returns the outputs in an IssueAction
func (i *IssueAction) GetOutputs() []driver.Output {
	var res []driver.Output
	for _, output := range i.Outputs {
		res = append(res, output)
	}

	return res
}

// IsAnonymous returns false, indicating that the identity of issuers in fabtoken
// is revealed during issue
func (i *IssueAction) IsAnonymous() bool {
	return false
}

// GetIssuer returns the issuer encoded in IssueAction
func (i *IssueAction) GetIssuer() []byte {
	return i.Issuer
}

// GetMetadata returns the IssueAction metadata
func (i *IssueAction) GetMetadata() map[string][]byte {
	return i.Metadata
}

// IsGraphHiding returns false, indicating that fabtoken does not hide the transaction graph
func (i *IssueAction) IsGraphHiding() bool {
	return false
}

func (i *IssueAction) Validate() error {
	if i.Issuer.IsNone() {
		return errors.Errorf("issuer is not set")
	}
	if len(i.Outputs) == 0 {
		return errors.Errorf("no outputs in issue action")
	}
	for i, output := range i.Outputs {
		if output == nil {
			return errors.Errorf("nil output in issue action")
		}
		if len(output.Type) == 0 {
			return errors.Errorf("invalid output's type at index [%d], output type is empty", i)
		}
		if len(output.Quantity) == 0 {
			return errors.Errorf("invalid output's quantity at index [%d], output quantity is empty", i)
		}
	}

	return nil
}

func (i *IssueAction) ExtraSigners() []driver.Identity {
	return nil
}
