/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type Input struct {
	*token.Input
	isHTLC bool
}

func ToInput(i *token.Input) (*Input, error) {
	owner, err := identity.UnmarshalTypedIdentity(i.Owner)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal owner")
	}

	return &Input{
		Input:  i,
		isHTLC: owner.Type == ScriptType,
	}, nil
}

func (i *Input) IsHTLC() bool {
	return i.isHTLC
}

func (i *Input) Script() (*Script, error) {
	if !i.isHTLC {
		return nil, errors.New("this input does not refer to an HTLC script")
	}

	owner, err := identity.UnmarshalTypedIdentity(i.Owner)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal owner")
	}
	if owner.Type != ScriptType {
		return nil, errors.Errorf("invalid identity type, expected [%s], got [%s]", ScriptType, owner.Type)
	}
	script := &Script{}
	err = json.Unmarshal(owner.Identity, script)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal HTLC script")
	}

	return script, nil
}

type Output struct {
	*token.Output
	isHTLC bool
}

func ToOutput(i *token.Output) (*Output, error) {
	owner, err := identity.UnmarshalTypedIdentity(i.Owner)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal owner")
	}

	return &Output{
		Output: i,
		isHTLC: owner.Type == ScriptType,
	}, nil
}

func (o *Output) IsHTLC() bool {
	return o.isHTLC
}

func (o *Output) Script() (*Script, error) {
	if !o.isHTLC {
		return nil, errors.New("this output does not refer to an HTLC script")
	}

	owner, err := identity.UnmarshalTypedIdentity(o.Owner)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal owner")
	}
	if owner.Type != ScriptType {
		return nil, errors.Errorf("invalid identity type, expected [%s], got [%s]", ScriptType, owner.Type)
	}
	script := &Script{}
	err = json.Unmarshal(owner.Identity, script)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal HTLC script")
	}

	return script, nil
}
