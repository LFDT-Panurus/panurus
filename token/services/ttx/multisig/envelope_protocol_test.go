/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"context"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	utilsession "github.com/LFDT-Panurus/panurus/token/services/utils/session"
	"github.com/LFDT-Panurus/panurus/token/services/utils/session/mock"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/require"
)

func TestVersionedSpendRequestRoundTrip(t *testing.T) {
	original := &SpendRequest{
		Token: &token.UnspentToken{Type: "TOK", Id: token.ID{TxId: "tx1"}},
	}
	roundTripSpendMessage(t, ttx.TypeSpendRequest, original, &SpendRequest{})
}

func TestVersionedSpendResponseRoundTrip(t *testing.T) {
	original := &SpendResponse{}
	roundTripSpendMessage(t, ttx.TypeSpendResponse, original, &SpendResponse{})
}

func roundTripSpendMessage(t *testing.T, msgType string, sent, received any) {
	t.Helper()

	var wire []byte
	mockSession := &mock.Session{}
	mockSession.SendWithContextStub = func(_ context.Context, payload []byte) error {
		wire = append([]byte(nil), payload...)

		return nil
	}

	s := utilsession.New(mockSession, t.Context(), jsession.JSONMarshaller{})
	require.NoError(t, jsession.SendTyped(s, t.Context(), sent, msgType))

	ch := make(chan *view.Message, 1)
	ch <- &view.Message{Payload: wire, Status: int32(view.OK)}
	mockSession.ReceiveReturns(ch)

	require.NoError(t, jsession.ReceiveTypedWithTimeout(s, msgType, received, time.Second))
}
