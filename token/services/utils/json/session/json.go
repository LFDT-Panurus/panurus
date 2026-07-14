/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package session

//go:generate counterfeiter -o mock/json_session.go -fake-name JsonSession . JsonSession

import (
	"encoding/json"

	session "github.com/LFDT-Panurus/panurus/token/services/utils/session"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// JSONMarshaller is the default JSON marshaller.
type JSONMarshaller struct{}

func (JSONMarshaller) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (JSONMarshaller) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func NewJSON(viewCtx view.Context, caller view.View, party view.Identity) (*session.S, error) {
	s, err := viewCtx.GetSession(caller, party)
	if err != nil {
		return nil, err
	}

	return NewFromSession(viewCtx, s), nil
}

func NewFromInitiator(viewCtx view.Context, party view.Identity) (*session.S, error) {
	s, err := viewCtx.GetSession(viewCtx.Initiator(), party)
	if err != nil {
		return nil, err
	}

	return NewFromSession(viewCtx, s), nil
}

func NewFromSession(viewCtx view.Context, s view.Session) *session.S {
	return session.New(s, viewCtx.Context(), JSONMarshaller{})
}

func JSON(viewCtx view.Context) *session.S {
	return session.New(viewCtx.Session(), viewCtx.Context(), JSONMarshaller{})
}
