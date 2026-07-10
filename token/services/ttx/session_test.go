/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests session.go which provides local bidirectional channels
// for simulating sessions between views running in the same process.
package ttx_test

import (
	"context"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const (
	testKey      contextKey = "key"
	testErrorKey contextKey = "error-key"
)

// TestNewLocalBidirectionalChannel_Success verifies successful creation of a bidirectional channel.
func TestNewLocalBidirectionalChannel_Success(t *testing.T) {
	ctx := t.Context()
	caller := "test-caller"
	contextID := "test-context-id"
	endpoint := "test-endpoint"
	pkid := []byte("test-pkid")

	channel, err := ttx.NewLocalBidirectionalChannel(ctx, caller, contextID, endpoint, pkid)

	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.NotNil(t, channel.LeftSession())
	assert.NotNil(t, channel.RightSession())
}

// TestNewLocalBidirectionalChannel_SessionInfo verifies session info is properly set.
func TestNewLocalBidirectionalChannel_SessionInfo(t *testing.T) {
	ctx := t.Context()
	caller := "test-caller"
	contextID := "test-context-id"
	endpoint := "test-endpoint"
	pkid := []byte("test-pkid")

	channel, err := ttx.NewLocalBidirectionalChannel(ctx, caller, contextID, endpoint, pkid)
	require.NoError(t, err)

	leftInfo := channel.LeftSession().Info()
	rightInfo := channel.RightSession().Info()

	// Both sessions should have the same session ID
	assert.Equal(t, leftInfo.ID, rightInfo.ID)
	assert.NotEmpty(t, leftInfo.ID)

	// Both should have the same endpoint info
	assert.Equal(t, endpoint, leftInfo.RemoteEndpoint)
	assert.Equal(t, endpoint, rightInfo.RemoteEndpoint)
	assert.Equal(t, pkid, leftInfo.RemotePKID)
	assert.Equal(t, pkid, rightInfo.RemotePKID)

	// Both should not be closed initially
	assert.False(t, leftInfo.Closed)
	assert.False(t, rightInfo.Closed)
}

// TestLocalBidirectionalChannel_SendReceive verifies basic send/receive functionality.
func TestLocalBidirectionalChannel_SendReceive(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	// Send from left to right
	payload := []byte("test-payload")
	err = leftSession.Send(payload)
	require.NoError(t, err)

	// Receive on right
	select {
	case msg := <-rightSession.Receive():
		assert.Equal(t, payload, msg.Payload)
		assert.Equal(t, int32(view.OK), msg.Status)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

// TestLocalBidirectionalChannel_BidirectionalCommunication verifies two-way communication.
func TestLocalBidirectionalChannel_BidirectionalCommunication(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	// Send from left to right
	leftPayload := []byte("left-to-right")
	err = leftSession.Send(leftPayload)
	require.NoError(t, err)

	// Receive on right
	select {
	case msg := <-rightSession.Receive():
		assert.Equal(t, leftPayload, msg.Payload)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for left-to-right message")
	}

	// Send from right to left
	rightPayload := []byte("right-to-left")
	err = rightSession.Send(rightPayload)
	require.NoError(t, err)

	// Receive on left
	select {
	case msg := <-leftSession.Receive():
		assert.Equal(t, rightPayload, msg.Payload)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for right-to-left message")
	}
}

// TestLocalBidirectionalChannel_SendWithContext verifies SendWithContext functionality.
func TestLocalBidirectionalChannel_SendWithContext(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	payload := []byte("test-payload")
	sendCtx := context.WithValue(ctx, testKey, "value")
	err = leftSession.SendWithContext(sendCtx, payload)
	require.NoError(t, err)

	select {
	case msg := <-rightSession.Receive():
		assert.Equal(t, payload, msg.Payload)
		assert.Equal(t, int32(view.OK), msg.Status)
		assert.NotNil(t, msg.Ctx)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

// TestLocalBidirectionalChannel_SendError verifies error message sending.
func TestLocalBidirectionalChannel_SendError(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	errorPayload := []byte("error-message")
	err = leftSession.SendError(errorPayload)
	require.NoError(t, err)

	select {
	case msg := <-rightSession.Receive():
		assert.Equal(t, errorPayload, msg.Payload)
		assert.Equal(t, int32(view.ERROR), msg.Status)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for error message")
	}
}

// TestLocalBidirectionalChannel_SendErrorWithContext verifies error message with context.
func TestLocalBidirectionalChannel_SendErrorWithContext(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	errorPayload := []byte("error-with-context")
	sendCtx := context.WithValue(ctx, testErrorKey, "error-value")
	err = leftSession.SendErrorWithContext(sendCtx, errorPayload)
	require.NoError(t, err)

	select {
	case msg := <-rightSession.Receive():
		assert.Equal(t, errorPayload, msg.Payload)
		assert.Equal(t, int32(view.ERROR), msg.Status)
		assert.NotNil(t, msg.Ctx)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for error message")
	}
}

// TestLocalBidirectionalChannel_MultipleMessages verifies multiple message exchange.
func TestLocalBidirectionalChannel_MultipleMessages(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	// Send multiple messages from left to right
	messages := [][]byte{
		[]byte("message-1"),
		[]byte("message-2"),
		[]byte("message-3"),
	}

	for _, msg := range messages {
		err = leftSession.Send(msg)
		require.NoError(t, err)
	}

	// Receive all messages on right
	for i, expectedMsg := range messages {
		select {
		case msg := <-rightSession.Receive():
			assert.Equal(t, expectedMsg, msg.Payload, "message %d mismatch", i)
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for message %d", i)
		}
	}
}

// TestLocalBidirectionalChannel_Close verifies session closure.
func TestLocalBidirectionalChannel_Close(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()

	// Verify session is not closed initially
	assert.False(t, leftSession.Info().Closed)

	// Close the session
	leftSession.Close()

	// Verify session is closed
	assert.True(t, leftSession.Info().Closed)
}

// TestLocalBidirectionalChannel_SendAfterClose verifies error when sending after close.
func TestLocalBidirectionalChannel_SendAfterClose(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()

	// Close the session
	leftSession.Close()

	// Try to send after close
	err = leftSession.Send([]byte("test"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session is closed")
}

// TestLocalBidirectionalChannel_ReceiveAfterClose verifies receive returns nil after close.
func TestLocalBidirectionalChannel_ReceiveAfterClose(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()

	// Close the session
	leftSession.Close()

	// Try to receive after close
	receiveChan := leftSession.Receive()
	assert.Nil(t, receiveChan)
}

// TestLocalBidirectionalChannel_MessageFields verifies all message fields are set correctly.
func TestLocalBidirectionalChannel_MessageFields(t *testing.T) {
	ctx := t.Context()
	caller := "test-caller"
	contextID := "test-context-id"
	endpoint := "test-endpoint"
	pkid := []byte("test-pkid")

	channel, err := ttx.NewLocalBidirectionalChannel(ctx, caller, contextID, endpoint, pkid)
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	payload := []byte("test-payload")
	err = leftSession.Send(payload)
	require.NoError(t, err)

	select {
	case msg := <-rightSession.Receive():
		assert.Equal(t, payload, msg.Payload)
		assert.Equal(t, leftSession.Info().ID, msg.SessionID)
		assert.Equal(t, contextID, msg.ContextID)
		assert.Equal(t, caller, msg.Caller)
		assert.Equal(t, endpoint, msg.FromEndpoint)
		assert.Equal(t, pkid, msg.FromPKID)
		assert.Equal(t, int32(view.OK), msg.Status)
		assert.NotNil(t, msg.Ctx)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

// TestLocalBidirectionalChannel_EmptyPayload verifies empty payload handling.
func TestLocalBidirectionalChannel_EmptyPayload(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	// Send empty payload
	err = leftSession.Send([]byte{})
	require.NoError(t, err)

	select {
	case msg := <-rightSession.Receive():
		assert.Empty(t, msg.Payload)
		assert.Equal(t, int32(view.OK), msg.Status)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

// TestLocalBidirectionalChannel_NilPayload verifies nil payload handling.
func TestLocalBidirectionalChannel_NilPayload(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	// Send nil payload
	err = leftSession.Send(nil)
	require.NoError(t, err)

	select {
	case msg := <-rightSession.Receive():
		assert.Nil(t, msg.Payload)
		assert.Equal(t, int32(view.OK), msg.Status)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

// TestLocalBidirectionalChannel_LargePayload verifies large payload handling.
func TestLocalBidirectionalChannel_LargePayload(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	// Send large payload (1MB)
	largePayload := make([]byte, 1024*1024)
	for i := range largePayload {
		largePayload[i] = byte(i % 256)
	}

	err = leftSession.Send(largePayload)
	require.NoError(t, err)

	select {
	case msg := <-rightSession.Receive():
		assert.Equal(t, largePayload, msg.Payload)
		assert.Equal(t, int32(view.OK), msg.Status)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for large message")
	}
}

// TestLocalBidirectionalChannel_ConcurrentSend verifies concurrent sending.
func TestLocalBidirectionalChannel_ConcurrentSend(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	numMessages := 10
	done := make(chan bool)

	// Send messages concurrently
	go func() {
		for i := range numMessages {
			payload := []byte{byte(i)}
			err := leftSession.Send(payload)
			assert.NoError(t, err)
		}
		done <- true
	}()

	// Receive all messages
	received := 0
	timeout := time.After(2 * time.Second)
	for received < numMessages {
		select {
		case <-rightSession.Receive():
			received++
		case <-timeout:
			t.Fatalf("timeout: received %d/%d messages", received, numMessages)
		}
	}

	<-done
	assert.Equal(t, numMessages, received)
}

// TestLocalBidirectionalChannel_UniqueSessionIDs verifies each channel gets unique session ID.
func TestLocalBidirectionalChannel_UniqueSessionIDs(t *testing.T) {
	ctx := t.Context()

	channel1, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	channel2, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	id1 := channel1.LeftSession().Info().ID
	id2 := channel2.LeftSession().Info().ID

	assert.NotEqual(t, id1, id2, "session IDs should be unique")
}
