/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package session_test

import (
	"testing"
	"time"

	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	utilsession "github.com/LFDT-Panurus/panurus/token/services/utils/session"
	"github.com/LFDT-Panurus/panurus/token/services/utils/session/mock"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONMarshaller_Marshal(t *testing.T) {
	m := jsession.JSONMarshaller{}
	data, err := m.Marshal(map[string]string{"key": "value"})
	require.NoError(t, err)
	assert.Contains(t, string(data), "key")
}

func TestJSONMarshaller_Unmarshal(t *testing.T) {
	m := jsession.JSONMarshaller{}
	var result map[string]string
	err := m.Unmarshal([]byte(`{"key":"value"}`), &result)
	require.NoError(t, err)
	assert.Equal(t, "value", result["key"])
}

func TestJSON_ReceiveRawWithTimeout_Timeout(t *testing.T) {
	mockSession := &mock.Session{}
	ch := make(chan *view.Message)
	mockSession.ReceiveReturns(ch)
	mockSession.InfoReturns(view.SessionInfo{ID: "test-session"})

	sess := utilsession.New(mockSession, t.Context(), jsession.JSONMarshaller{})
	_, err := sess.ReceiveRawWithTimeout(1 * time.Millisecond)
	require.Error(t, err)
	assert.ErrorIs(t, err, utilsession.ErrTimeout)
}

func TestJSON_ReceiveRawWithTimeout_Success(t *testing.T) {
	mockSession := &mock.Session{}
	ch := make(chan *view.Message, 1)
	payload := []byte(`"hello"`)
	ch <- &view.Message{Payload: payload, Status: int32(view.OK)}
	mockSession.ReceiveReturns(ch)

	sess := utilsession.New(mockSession, t.Context(), jsession.JSONMarshaller{})
	raw, err := sess.ReceiveRawWithTimeout(1 * time.Second)
	require.NoError(t, err)
	assert.Equal(t, payload, raw)
}
