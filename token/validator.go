/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Ledger models a read-only ledger
type Ledger = driver.ValidatorLedger

// Validator validates a token request
type Validator struct {
	backend driver.Validator
}

func NewValidator(backend driver.Validator) *Validator {
	return &Validator{backend: backend}
}

// UnmarshalActions returns the actions contained in the serialized token request
func (c *Validator) UnmarshalActions(raw []byte) ([]any, error) {
	if c == nil || utils.IsNil(c.backend) {
		return nil, errors.New("validator backend is nil")
	}

	return c.backend.UnmarshalActions(raw)
}

// UnmarshallAndVerify unmarshalls the token request and verifies it against the passed ledger and anchor
func (c *Validator) UnmarshallAndVerify(ctx context.Context, ledger Ledger, anchor RequestAnchor, raw []byte) ([]any, error) {
	if c == nil || utils.IsNil(c.backend) {
		return nil, errors.New("validator backend is nil")
	}
	var getState driver.GetStateFnc
	if !utils.IsNil(ledger) {
		getState = ledger.GetState
	}
	actions, _, err := c.backend.VerifyTokenRequestFromRaw(ctx, getState, anchor, raw)
	if err != nil {
		return nil, err
	}

	res := make([]any, len(actions))
	copy(res, actions)

	return res, nil
}

// UnmarshallAndVerifyWithMetadata behaves as UnmarshallAndVerify. In addition, it returns the metadata extracts from the token request
// in the form of map.
func (c *Validator) UnmarshallAndVerifyWithMetadata(ctx context.Context, ledger Ledger, anchor RequestAnchor, raw []byte) ([]any, map[string][]byte, error) {
	if c == nil || utils.IsNil(c.backend) {
		return nil, nil, errors.New("validator backend is nil")
	}
	var getState driver.GetStateFnc
	if !utils.IsNil(ledger) {
		getState = ledger.GetState
	}
	actions, meta, err := c.backend.VerifyTokenRequestFromRaw(ctx, getState, anchor, raw)
	if err != nil {
		return nil, nil, err
	}

	res := make([]any, len(actions))
	copy(res, actions)

	return res, meta, nil
}

type stateGetter struct {
	f driver.GetStateFnc
}

func NewLedgerFromGetter(f driver.GetStateFnc) *stateGetter {
	return &stateGetter{f: f}
}

func (g *stateGetter) GetState(id token.ID) ([]byte, error) {
	if g == nil || g.f == nil {
		return nil, errors.New("ledger not available")
	}

	return g.f(id)
}
