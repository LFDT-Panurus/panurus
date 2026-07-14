/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

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

	var wire []byte
	mockSession := &mock.Session{}
	mockSession.SendWithContextStub = func(_ context.Context, payload []byte) error {
		wire = append([]byte(nil), payload...)

		return nil
	}

	s := utilsession.New(mockSession, t.Context(), jsession.JSONMarshaller{})
	require.NoError(t, jsession.SendTyped(s, t.Context(), original, ttx.TypeSpendRequest))

	ch := make(chan *view.Message, 1)
	ch <- &view.Message{Payload: wire, Status: int32(view.OK)}
	mockSession.ReceiveReturns(ch)

	dst := &SpendRequest{}
	require.NoError(t, jsession.ReceiveTypedWithTimeout(s, ttx.TypeSpendRequest, dst, time.Second))
	require.Equal(t, original.Token.Id, dst.Token.Id)
}
