/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package session_test

import (
	"context"
	"testing"
	"time"

	utilsession "github.com/LFDT-Panurus/panurus/token/services/utils/session"
	"github.com/LFDT-Panurus/panurus/token/services/utils/session/mock"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSession(t *testing.T, s utilsession.Session, m utilsession.Marshaller) *utilsession.S {
	t.Helper()

	return utilsession.New(s, t.Context(), m)
}

func TestReceiveRawWithTimeout_Timeout(t *testing.T) {
	mockSession := &mock.Session{}
	ch := make(chan *view.Message)
	mockSession.ReceiveReturns(ch)
	mockSession.InfoReturns(view.SessionInfo{ID: "test-session"})

	m := &mock.Marshaller{}
	sess := newSession(t, mockSession, m)

	_, err := sess.ReceiveRawWithTimeout(1 * time.Millisecond)
	require.Error(t, err)
	assert.ErrorIs(t, err, utilsession.ErrTimeout)
}

func TestReceiveRawWithTimeout_NilMessage(t *testing.T) {
	mockSession := &mock.Session{}
	ch := make(chan *view.Message, 1)
	ch <- nil
	mockSession.ReceiveReturns(ch)

	m := &mock.Marshaller{}
	sess := newSession(t, mockSession, m)

	_, err := sess.ReceiveRawWithTimeout(1 * time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, utilsession.ErrNilMessage)
}

func TestReceiveRawWithTimeout_ContextDone(t *testing.T) {
	mockSession := &mock.Session{}
	ch := make(chan *view.Message)
	mockSession.ReceiveReturns(ch)

	m := &mock.Marshaller{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sess := utilsession.New(mockSession, ctx, m)
	_, err := sess.ReceiveRawWithTimeout(5 * time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, utilsession.ErrContextDone)
}

func TestReceiveRawWithTimeout_Success(t *testing.T) {
	mockSession := &mock.Session{}
	ch := make(chan *view.Message, 1)
	payload := []byte("hello")
	ch <- &view.Message{Payload: payload, Status: int32(view.OK)}
	mockSession.ReceiveReturns(ch)

	m := &mock.Marshaller{}
	sess := newSession(t, mockSession, m)

	raw, err := sess.ReceiveRawWithTimeout(1 * time.Second)
	require.NoError(t, err)
	assert.Equal(t, payload, raw)
}

func TestReceiveRawWithTimeout_ErrorMessage(t *testing.T) {
	mockSession := &mock.Session{}
	ch := make(chan *view.Message, 1)
	ch <- &view.Message{Payload: []byte("remote error"), Status: int32(view.ERROR)}
	mockSession.ReceiveReturns(ch)

	m := &mock.Marshaller{}
	sess := newSession(t, mockSession, m)

	_, err := sess.ReceiveRawWithTimeout(1 * time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote error")
}

func TestReceiveRawWithTimeout_TooLarge(t *testing.T) {
	mockSession := &mock.Session{}
	ch := make(chan *view.Message, 1)
	ch <- &view.Message{Payload: make([]byte, 100), Status: int32(view.OK)}
	mockSession.ReceiveReturns(ch)
	mockSession.InfoReturns(view.SessionInfo{ID: "test-session"})

	m := &mock.Marshaller{}
	sess := utilsession.New(mockSession, t.Context(), m, utilsession.WithMaxRecvMessageSize(10))

	_, err := sess.ReceiveRawWithTimeout(1 * time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, utilsession.ErrMessageTooLarge)
}

func TestReceiveRawWithTimeout_AtLimit(t *testing.T) {
	mockSession := &mock.Session{}
	ch := make(chan *view.Message, 1)
	payload := make([]byte, 10)
	ch <- &view.Message{Payload: payload, Status: int32(view.OK)}
	mockSession.ReceiveReturns(ch)

	m := &mock.Marshaller{}
	sess := utilsession.New(mockSession, t.Context(), m, utilsession.WithMaxRecvMessageSize(10))

	raw, err := sess.ReceiveRawWithTimeout(1 * time.Second)
	require.NoError(t, err)
	assert.Len(t, raw, 10)
}

func TestReceiveRawWithTimeout_UnlimitedWhenZero(t *testing.T) {
	mockSession := &mock.Session{}
	ch := make(chan *view.Message, 1)
	ch <- &view.Message{Payload: make([]byte, 1024), Status: int32(view.OK)}
	mockSession.ReceiveReturns(ch)

	m := &mock.Marshaller{}
	sess := utilsession.New(mockSession, t.Context(), m, utilsession.WithMaxRecvMessageSize(0))

	raw, err := sess.ReceiveRawWithTimeout(1 * time.Second)
	require.NoError(t, err)
	assert.Len(t, raw, 1024)
}

// TestReceiveWithTimeout_TooLargeRejectedBeforeUnmarshal asserts the oversized
// payload is dropped before any deserialization is attempted.
func TestReceiveWithTimeout_TooLargeRejectedBeforeUnmarshal(t *testing.T) {
	mockSession := &mock.Session{}
	ch := make(chan *view.Message, 1)
	ch <- &view.Message{Payload: make([]byte, 100), Status: int32(view.OK)}
	mockSession.ReceiveReturns(ch)
	mockSession.InfoReturns(view.SessionInfo{ID: "test-session"})

	m := &mock.Marshaller{}
	sess := utilsession.New(mockSession, t.Context(), m, utilsession.WithMaxRecvMessageSize(10))

	var result string
	err := sess.ReceiveWithTimeout(&result, 1*time.Second)
	require.ErrorIs(t, err, utilsession.ErrMessageTooLarge)
	assert.Equal(t, 0, m.UnmarshalCallCount(), "payload must be rejected before deserialization")
}

func TestSend_Success(t *testing.T) {
	mockSession := &mock.Session{}
	mockSession.SendWithContextReturns(nil)

	m := &mock.Marshaller{}
	m.MarshalReturns([]byte(`"data"`), nil)

	sess := newSession(t, mockSession, m)
	err := sess.Send("data")
	require.NoError(t, err)
}

func TestReceiveWithTimeout_Success(t *testing.T) {
	mockSession := &mock.Session{}
	ch := make(chan *view.Message, 1)
	ch <- &view.Message{Payload: []byte(`"hello"`), Status: int32(view.OK)}
	mockSession.ReceiveReturns(ch)

	m := &mock.Marshaller{}
	m.UnmarshalReturns(nil)

	sess := newSession(t, mockSession, m)
	var result string
	err := sess.ReceiveWithTimeout(&result, 1*time.Second)
	require.NoError(t, err)
}
