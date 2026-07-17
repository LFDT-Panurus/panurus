/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	depmock "github.com/LFDT-Panurus/panurus/token/services/ttx/dep/mock"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRespondExchangeRecipientIdentitiesRejectsMissingRecipientDataWithoutPanicking(t *testing.T) {
	session := &depmock.Session{}
	messages := make(chan *view.Message, 1)
	messages <- &view.Message{Payload: mustEnvelopeBytes(t, ttx.TypeExchangeRecipientRequest, &ttx.ExchangeRecipientRequest{Nonce: []byte("nonce")})}
	session.ReceiveReturns(messages)
	ctx := &depmock.Context{}
	ctx.ContextReturns(t.Context())
	ctx.SessionReturns(session)
	ctx.GetServiceReturns(nil, assert.AnError)

	_, err := (&ttx.RespondExchangeRecipientIdentitiesView{}).Call(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing recipient data")
}

func TestRespondExchangeRecipientIdentitiesRejectsUnauthenticatedInitiator(t *testing.T) {
	session := &depmock.Session{}
	messages := make(chan *view.Message, 1)
	messages <- &view.Message{Payload: mustEnvelopeBytes(t, ttx.TypeExchangeRecipientRequest, &ttx.ExchangeRecipientRequest{
		Nonce:         []byte("nonce"),
		RecipientData: &ttx.RecipientData{Identity: view.Identity("victim")},
	})}
	session.ReceiveReturns(messages)
	ctx := &depmock.Context{}
	ctx.ContextReturns(t.Context())
	ctx.SessionReturns(session)
	ctx.GetServiceReturns(nil, assert.AnError)

	_, err := (&ttx.RespondExchangeRecipientIdentitiesView{}).Call(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing initiator signature")
	assert.Equal(t, 0, session.SendWithContextCallCount(), "unauthenticated initiator must receive no recipient data")
}
