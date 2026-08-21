/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests session.go which provides local bidirectional channels
// for simulating sessions between views running in the same process.
package ttx_test

import (
	"context"
	"sync"
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
	err = leftSession.Send(ctx, payload)
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
	err = leftSession.Send(ctx, leftPayload)
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
	err = rightSession.Send(ctx, rightPayload)
	require.NoError(t, err)

	// Receive on left
	select {
	case msg := <-leftSession.Receive():
		assert.Equal(t, rightPayload, msg.Payload)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for right-to-left message")
	}
}

// TestLocalBidirectionalChannel_SendPropagatesContext verifies the context passed to
// Send is propagated to the received message.
func TestLocalBidirectionalChannel_SendPropagatesContext(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	payload := []byte("test-payload")
	sendCtx := context.WithValue(ctx, testKey, "value")
	err = leftSession.Send(sendCtx, payload)
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
	err = leftSession.SendError(ctx, errorPayload)
	require.NoError(t, err)

	select {
	case msg := <-rightSession.Receive():
		assert.Equal(t, errorPayload, msg.Payload)
		assert.Equal(t, int32(view.ERROR), msg.Status)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for error message")
	}
}

// TestLocalBidirectionalChannel_SendErrorPropagatesContext verifies the context passed
// to SendError is propagated to the received message.
func TestLocalBidirectionalChannel_SendErrorPropagatesContext(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	errorPayload := []byte("error-with-context")
	sendCtx := context.WithValue(ctx, testErrorKey, "error-value")
	err = leftSession.SendError(sendCtx, errorPayload)
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
		err = leftSession.Send(ctx, msg)
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
	err = leftSession.Send(ctx, []byte("test"))
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
	err = leftSession.Send(ctx, payload)
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
	err = leftSession.Send(ctx, []byte{})
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
	err = leftSession.Send(ctx, nil)
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

	err = leftSession.Send(ctx, largePayload)
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
			err := leftSession.Send(ctx, payload)
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

// TestLocalBidirectionalChannel_CloseIsIdempotent verifies that closing a session twice does not panic.
func TestLocalBidirectionalChannel_CloseIsIdempotent(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()

	assert.NotPanics(t, func() {
		leftSession.Close()
		leftSession.Close()
	})
	assert.True(t, leftSession.Info().Closed)
}

// TestLocalBidirectionalChannel_CloseBothSides verifies that both ends of the same channel can be closed.
func TestLocalBidirectionalChannel_CloseBothSides(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	assert.NotPanics(t, func() {
		channel.LeftSession().Close()
		channel.RightSession().Close()
	})
}

// TestLocalBidirectionalChannel_ClosePeerSendStillSafe verifies that closing one side leaves the peer able to send.
func TestLocalBidirectionalChannel_ClosePeerSendStillSafe(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	leftSession.Close()

	assert.NotPanics(t, func() {
		assert.NoError(t, rightSession.Send([]byte("still-open")))
	})
	assert.False(t, rightSession.Info().Closed, "closing one side must not close the other")
}

// TestLocalBidirectionalChannel_CloseUnblocksPeerReceive verifies that Close releases a peer blocked on Receive.
func TestLocalBidirectionalChannel_CloseUnblocksPeerReceive(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	received := make(chan *view.Message, 1)
	go func() {
		received <- <-rightSession.Receive()
	}()

	leftSession.Close()

	select {
	case msg := <-received:
		assert.Nil(t, msg, "a receive on a closed peer channel should yield a nil message")
	case <-time.After(time.Second):
		t.Fatal("timeout: Close did not unblock the peer's Receive")
	}
}

// TestLocalBidirectionalChannel_BufferedMessagesSurviveClose verifies that already sent messages are still delivered after close.
func TestLocalBidirectionalChannel_BufferedMessagesSurviveClose(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	require.NoError(t, leftSession.Send([]byte("buffered-1")))
	require.NoError(t, leftSession.Send([]byte("buffered-2")))

	receive := rightSession.Receive()
	leftSession.Close()

	for _, expected := range []string{"buffered-1", "buffered-2"} {
		select {
		case msg := <-receive:
			require.NotNil(t, msg, "buffered message should survive the close")
			assert.Equal(t, []byte(expected), msg.Payload)
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for buffered message %s", expected)
		}
	}
}

// TestLocalBidirectionalChannel_SendFullBufferContextDone verifies that a send blocked on a full buffer gives up when its context is done.
func TestLocalBidirectionalChannel_SendFullBufferContextDone(t *testing.T) {
	channel, err := ttx.NewLocalBidirectionalChannel(t.Context(), "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()

	// Fill the buffer; nothing reads from the right side.
	sendCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for i := range 10 {
		require.NoError(t, leftSession.SendWithContext(sendCtx, []byte{byte(i)}))
	}

	sendErr := make(chan error, 1)
	go func() {
		sendErr <- leftSession.SendWithContext(sendCtx, []byte("overflow"))
	}()

	// The send is blocked on the full buffer until the context is cancelled.
	select {
	case err := <-sendErr:
		t.Fatalf("send returned before the buffer drained or the context was cancelled: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-sendErr:
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("timeout: send did not return after its context was cancelled")
	}
}

// TestLocalBidirectionalChannel_ConcurrentSendAndClose verifies that Close racing with concurrent sends neither panics nor races.
func TestLocalBidirectionalChannel_ConcurrentSendAndClose(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()
	rightSession := channel.RightSession()

	// Drain the peer so senders are never blocked on a full buffer.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range rightSession.Receive() {
		}
	}()

	// Let the senders get going first, so the close lands while sends are in flight.
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 2000 {
				if err := leftSession.Send([]byte("concurrent")); err != nil {
					assert.Contains(t, err.Error(), "session is closed")
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		time.Sleep(time.Millisecond)
		leftSession.Close()
	}()

	close(start)
	wg.Wait()
	<-drained

	assert.True(t, leftSession.Info().Closed)
	// A further close must still be safe once the racing sends are done.
	assert.NotPanics(t, leftSession.Close)
}

// TestLocalBidirectionalChannel_ConcurrentInfoAndClose verifies that reading Info while another goroutine closes is race free.
func TestLocalBidirectionalChannel_ConcurrentInfoAndClose(t *testing.T) {
	ctx := t.Context()
	channel, err := ttx.NewLocalBidirectionalChannel(ctx, "caller", "ctx-id", "endpoint", []byte("pkid"))
	require.NoError(t, err)

	leftSession := channel.LeftSession()

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				_ = leftSession.Info()
				_ = leftSession.Receive()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		leftSession.Close()
	}()

	wg.Wait()
	assert.True(t, leftSession.Info().Closed)
}
