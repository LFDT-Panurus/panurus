/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package session_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	utilsession "github.com/LFDT-Panurus/panurus/token/services/utils/session"
	"github.com/LFDT-Panurus/panurus/token/services/utils/session/mock"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Local message-type strings for exercising the generic envelope mechanism.
// The session package no longer defines service-specific type constants, so
// the tests use neutral values; only their distinctness matters.
const (
	testTypeA = "test_type_a"
	testTypeB = "test_type_b"
	testTypeC = "test_type_c"
	testTypeD = "test_type_d"
)

// --- Envelope marshal / unmarshal ---

func TestWrapEnvelope(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	env, err := jsession.WrapEnvelope(&payload{Name: "alice"}, testTypeA)
	require.NoError(t, err)
	assert.Equal(t, jsession.CurrentVersion, env.Version)
	assert.Equal(t, testTypeA, env.Type)

	var p payload
	require.NoError(t, json.Unmarshal(env.Body, &p))
	assert.Equal(t, "alice", p.Name)
}

func TestWrapEnvelope_MarshalError(t *testing.T) {
	_, err := jsession.WrapEnvelope(make(chan int), testTypeA)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to marshal envelope body")
}

func TestUnwrapEnvelope_Valid(t *testing.T) {
	body, _ := json.Marshal(map[string]string{"key": "val"})
	raw, _ := json.Marshal(jsession.Envelope{
		Version: jsession.CurrentVersion,
		Type:    testTypeA,
		Body:    body,
	})

	env, err := jsession.UnwrapEnvelope(raw, testTypeA)
	require.NoError(t, err)
	assert.Equal(t, jsession.CurrentVersion, env.Version)
	assert.Equal(t, testTypeA, env.Type)
}

func TestUnwrapEnvelope_SkipTypeCheck(t *testing.T) {
	body, _ := json.Marshal("hello")
	raw, _ := json.Marshal(jsession.Envelope{
		Version: jsession.CurrentVersion,
		Type:    testTypeB,
		Body:    body,
	})

	env, err := jsession.UnwrapEnvelope(raw, "")
	require.NoError(t, err)
	assert.Equal(t, testTypeB, env.Type)
}

// --- Version validation ---

func TestUnwrapEnvelope_MissingVersion(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"t": "recipient_req", "b": "{}"})

	_, err := jsession.UnwrapEnvelope(raw, "")
	require.Error(t, err)
	require.ErrorIs(t, err, jsession.ErrMissingVersion)
}

func TestUnwrapEnvelope_FutureVersion(t *testing.T) {
	raw, _ := json.Marshal(jsession.Envelope{
		Version: 99,
		Type:    testTypeA,
		Body:    json.RawMessage(`{}`),
	})

	_, err := jsession.UnwrapEnvelope(raw, "")
	require.Error(t, err)
	require.ErrorIs(t, err, jsession.ErrVersionMismatch)

	var ve *jsession.VersionError
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, jsession.CurrentVersion, ve.Expected)
	assert.Equal(t, uint32(99), ve.Received)
}

// --- Type validation ---

func TestUnwrapEnvelope_EmptyType(t *testing.T) {
	raw, _ := json.Marshal(jsession.Envelope{
		Version: jsession.CurrentVersion,
		Type:    "",
		Body:    json.RawMessage(`{}`),
	})

	_, err := jsession.UnwrapEnvelope(raw, "")
	require.Error(t, err)
	require.ErrorIs(t, err, jsession.ErrInvalidEnvelope)
	assert.Contains(t, err.Error(), "type field is empty")
}

func TestUnwrapEnvelope_TypeMismatch(t *testing.T) {
	raw, _ := json.Marshal(jsession.Envelope{
		Version: jsession.CurrentVersion,
		Type:    testTypeB,
		Body:    json.RawMessage(`{}`),
	})

	_, err := jsession.UnwrapEnvelope(raw, testTypeA)
	require.Error(t, err)
	require.ErrorIs(t, err, jsession.ErrTypeMismatch)
}

// --- Malformed JSON ---

func TestUnwrapEnvelope_MalformedJSON(t *testing.T) {
	_, err := jsession.UnwrapEnvelope([]byte(`not json`), "")
	require.Error(t, err)
	require.ErrorIs(t, err, jsession.ErrInvalidEnvelope)
}

// --- UnwrapBody ---

func TestUnwrapBody(t *testing.T) {
	type msg struct {
		Value int `json:"value"`
	}
	body, _ := json.Marshal(msg{Value: 42})
	raw, _ := json.Marshal(jsession.Envelope{
		Version: jsession.CurrentVersion,
		Type:    testTypeC,
		Body:    body,
	})

	var dst msg
	require.NoError(t, jsession.UnwrapBody(raw, testTypeC, &dst))
	assert.Equal(t, 42, dst.Value)
}

func TestUnwrapBody_VersionMismatch(t *testing.T) {
	raw, _ := json.Marshal(jsession.Envelope{
		Version: 2,
		Type:    testTypeC,
		Body:    json.RawMessage(`{}`),
	})

	var dst struct{}
	err := jsession.UnwrapBody(raw, testTypeC, &dst)
	require.Error(t, err)
	require.ErrorIs(t, err, jsession.ErrVersionMismatch)
}

// --- Envelope.Validate ---

func TestEnvelope_Validate(t *testing.T) {
	env := &jsession.Envelope{
		Version: jsession.CurrentVersion,
		Type:    testTypeD,
	}
	require.NoError(t, env.Validate(testTypeD))
	require.Error(t, env.Validate(testTypeA))
}

// --- VersionError ---

func TestVersionError_Error(t *testing.T) {
	ve := &jsession.VersionError{Expected: 1, Received: 5}
	assert.Contains(t, ve.Error(), "expected 1")
	assert.Contains(t, ve.Error(), "received 5")
}

func TestVersionError_Is(t *testing.T) {
	ve := &jsession.VersionError{Expected: 1, Received: 2}
	require.ErrorIs(t, ve, jsession.ErrVersionMismatch)
	assert.False(t, ve.Is(jsession.ErrInvalidEnvelope))
}

// --- SendTyped / ReceiveTyped over mock session ---

func TestSendTyped(t *testing.T) {
	mockSession := &mock.Session{}
	var captured []byte
	mockSession.SendWithContextStub = func(_ context.Context, payload []byte) error {
		captured = payload

		return nil
	}

	s := utilsession.New(mockSession, t.Context(), jsession.JSONMarshaller{})

	type req struct {
		ID string `json:"id"`
	}
	err := jsession.SendTyped(s, t.Context(), &req{ID: "abc"}, testTypeA)
	require.NoError(t, err)

	var env jsession.Envelope
	require.NoError(t, json.Unmarshal(captured, &env))
	assert.Equal(t, jsession.CurrentVersion, env.Version)
	assert.Equal(t, testTypeA, env.Type)

	var body req
	require.NoError(t, json.Unmarshal(env.Body, &body))
	assert.Equal(t, "abc", body.ID)
}

func TestReceiveTypedWithTimeout_Success(t *testing.T) {
	type resp struct {
		Name string `json:"name"`
	}

	body, _ := json.Marshal(resp{Name: "bob"})
	raw, _ := json.Marshal(jsession.Envelope{
		Version: jsession.CurrentVersion,
		Type:    testTypeB,
		Body:    body,
	})

	mockSession := &mock.Session{}
	ch := make(chan *view.Message, 1)
	ch <- &view.Message{Payload: raw, Status: int32(view.OK)}
	mockSession.ReceiveReturns(ch)

	s := utilsession.New(mockSession, t.Context(), jsession.JSONMarshaller{})

	var dst resp
	err := jsession.ReceiveTypedWithTimeout(s, testTypeB, &dst, 1*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "bob", dst.Name)
}

func TestReceiveTypedWithTimeout_VersionMismatch(t *testing.T) {
	raw, _ := json.Marshal(jsession.Envelope{
		Version: 99,
		Type:    testTypeB,
		Body:    json.RawMessage(`{}`),
	})

	mockSession := &mock.Session{}
	ch := make(chan *view.Message, 1)
	ch <- &view.Message{Payload: raw, Status: int32(view.OK)}
	mockSession.ReceiveReturns(ch)

	s := utilsession.New(mockSession, t.Context(), jsession.JSONMarshaller{})

	var dst struct{}
	err := jsession.ReceiveTypedWithTimeout(s, testTypeB, &dst, 1*time.Second)
	require.Error(t, err)
	require.ErrorIs(t, err, jsession.ErrVersionMismatch)
}

func TestReceiveTypedWithTimeout_TypeMismatch(t *testing.T) {
	raw, _ := json.Marshal(jsession.Envelope{
		Version: jsession.CurrentVersion,
		Type:    testTypeB,
		Body:    json.RawMessage(`{}`),
	})

	mockSession := &mock.Session{}
	ch := make(chan *view.Message, 1)
	ch <- &view.Message{Payload: raw, Status: int32(view.OK)}
	mockSession.ReceiveReturns(ch)

	s := utilsession.New(mockSession, t.Context(), jsession.JSONMarshaller{})

	var dst struct{}
	err := jsession.ReceiveTypedWithTimeout(s, testTypeA, &dst, 1*time.Second)
	require.Error(t, err)
	require.ErrorIs(t, err, jsession.ErrTypeMismatch)
}

func TestReceiveTypedWithTimeout_Timeout(t *testing.T) {
	mockSession := &mock.Session{}
	ch := make(chan *view.Message)
	mockSession.ReceiveReturns(ch)
	mockSession.InfoReturns(view.SessionInfo{ID: "test"})

	s := utilsession.New(mockSession, t.Context(), jsession.JSONMarshaller{})

	var dst struct{}
	err := jsession.ReceiveTypedWithTimeout(s, testTypeA, &dst, 1*time.Millisecond)
	require.Error(t, err)
	require.ErrorIs(t, err, utilsession.ErrTimeout)
}

func TestReceiveTyped_UsesDefaultTimeout(t *testing.T) {
	mockSession := &mock.Session{}
	ch := make(chan *view.Message)
	mockSession.ReceiveReturns(ch)
	mockSession.InfoReturns(view.SessionInfo{ID: "test"})

	s := utilsession.New(mockSession, t.Context(), jsession.JSONMarshaller{})

	var dst struct{}
	err := jsession.ReceiveTyped(s, testTypeA, &dst)
	require.Error(t, err)
	require.ErrorIs(t, err, utilsession.ErrTimeout)
}

// --- Large body ---

func TestRoundTrip_LargeBody(t *testing.T) {
	large := make([]byte, 64*1024)
	for i := range large {
		large[i] = byte(i % 256)
	}

	env, err := jsession.WrapEnvelope(large, testTypeC)
	require.NoError(t, err)

	raw, err := json.Marshal(env)
	require.NoError(t, err)

	var dst []byte
	require.NoError(t, jsession.UnwrapBody(raw, testTypeC, &dst))
	assert.Equal(t, large, dst)
}

// --- VersionError with Message ---

func TestVersionError_WithMessage(t *testing.T) {
	ve := &jsession.VersionError{Expected: 1, Received: 3, Message: "upgrade required"}
	assert.Contains(t, ve.Error(), "expected 1")
	assert.Contains(t, ve.Error(), "received 3")
	assert.Contains(t, ve.Error(), "upgrade required")
}

// --- IsCompatible ---

func TestIsCompatible(t *testing.T) {
	assert.True(t, jsession.IsCompatible(1, 1))
	assert.False(t, jsession.IsCompatible(1, 2))
	assert.False(t, jsession.IsCompatible(2, 1))
	assert.False(t, jsession.IsCompatible(0, 0))
}

// --- Concurrent send/receive ---

func TestConcurrentSendReceive(t *testing.T) {
	const goroutines = 20

	mockSession := &mock.Session{}
	mockSession.SendWithContextStub = func(_ context.Context, _ []byte) error {
		return nil
	}

	body, _ := json.Marshal(map[string]string{"k": "v"})
	raw, _ := json.Marshal(jsession.Envelope{
		Version: jsession.CurrentVersion,
		Type:    testTypeA,
		Body:    body,
	})

	errs := make(chan error, goroutines*2)
	for range goroutines {
		go func() {
			s := utilsession.New(mockSession, t.Context(), jsession.JSONMarshaller{})
			errs <- jsession.SendTyped(s, t.Context(), map[string]string{"k": "v"}, testTypeA)
		}()
		go func() {
			_, err := jsession.UnwrapEnvelope(raw, testTypeA)
			errs <- err
		}()
	}
	for range goroutines * 2 {
		assert.NoError(t, <-errs)
	}
}

// --- Performance benchmarks ---

func BenchmarkWrapEnvelope(b *testing.B) {
	payload := map[string]string{"name": "alice", "role": "owner"}
	for b.Loop() {
		_, _ = jsession.WrapEnvelope(payload, testTypeA)
	}
}

func BenchmarkUnwrapEnvelope(b *testing.B) {
	body, _ := json.Marshal(map[string]string{"name": "alice"})
	raw, _ := json.Marshal(jsession.Envelope{
		Version: jsession.CurrentVersion,
		Type:    testTypeA,
		Body:    body,
	})
	for b.Loop() {
		_, _ = jsession.UnwrapEnvelope(raw, testTypeA)
	}
}

func BenchmarkRoundTrip(b *testing.B) {
	type msg struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	for b.Loop() {
		env, _ := jsession.WrapEnvelope(&msg{Name: "alice", Value: 42}, testTypeA)
		raw, _ := json.Marshal(env)
		var dst msg
		_ = jsession.UnwrapBody(raw, testTypeA, &dst)
	}
}
