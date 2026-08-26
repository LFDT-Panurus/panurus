/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"encoding/base64"
	"sync"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// messageBufferSize is the capacity of each direction of a LocalBidirectionalChannel.
const messageBufferSize = 10

// LocalBidirectionalChannel is a bidirectional channel that is used to simulate
// a session between two views (let's call them L and R) running in the same process.
//
// Closing either end terminates the conversation for both: the two message channels
// are closed, so a peer parked on Receive is released, and any further Send returns
// an error instead of writing into a channel nobody drains.
type LocalBidirectionalChannel struct {
	left  view.Session
	right view.Session
}

// NewLocalBidirectionalChannel creates a new bidirectional channel
func NewLocalBidirectionalChannel(ctx context.Context, caller string, contextID string, endpoint string, pkid []byte) (*LocalBidirectionalChannel, error) {
	ID, err := GetRandomNonce()
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate session ID")
	}
	lr := make(chan *view.Message, messageBufferSize)
	rl := make(chan *view.Message, messageBufferSize)

	info := view.SessionInfo{
		ID:             base64.StdEncoding.EncodeToString(ID),
		Caller:         nil,
		CallerViewID:   "",
		RemoteEndpoint: endpoint,
		RemotePKID:     pkid,
		Closed:         false,
	}

	state := &channelState{
		done:     make(chan struct{}),
		finished: make(chan struct{}),
		channels: [2]chan *view.Message{lr, rl},
	}

	return &LocalBidirectionalChannel{
		left: &localSession{
			name:         "left",
			contextID:    contextID,
			caller:       caller,
			info:         info,
			readChannel:  rl,
			writeChannel: lr,
			state:        state,
		},
		right: &localSession{
			name:         "right",
			contextID:    contextID,
			caller:       caller,
			info:         info,
			readChannel:  lr,
			writeChannel: rl,
			state:        state,
		},
	}, nil
}

// LeftSession returns the session from the L to R
func (c *LocalBidirectionalChannel) LeftSession() view.Session {
	return c.left
}

// RightSession returns the session from the R to L
func (c *LocalBidirectionalChannel) RightSession() view.Session {
	return c.right
}

// channelState is the state shared by the two ends of a LocalBidirectionalChannel.
// Both message channels are owned here because closing one end closes both of them.
type channelState struct {
	// mu guards closed. It is never held across a channel send, so Close never
	// blocks behind a sender parked on a full buffer.
	mu     sync.RWMutex
	closed bool
	// done is closed by close to release senders parked on a full buffer.
	done chan struct{}
	// finished is closed once the message channels are closed, so that a concurrent
	// second close returns only when the close is complete.
	finished chan struct{}
	// senders counts the sends that are in flight, so that the message channels
	// are closed only once no send can still be selecting on them.
	senders  sync.WaitGroup
	channels [2]chan *view.Message
}

// isClosed reports whether either end of the channel has been closed.
func (s *channelState) isClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.closed
}

// beginSend registers an in-flight send. It reports false if the channel is already
// closed, in which case no matching endSend call must be made.
func (s *channelState) beginSend() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return false
	}
	// Registered under the read lock, so it is ordered before close takes the write
	// lock and, therefore, before the Wait in close.
	s.senders.Add(1)

	return true
}

// endSend deregisters an in-flight send.
func (s *channelState) endSend() {
	s.senders.Done()
}

// close terminates the conversation for both ends. It marks the channel closed,
// releases the senders parked on a full buffer, and closes both message channels
// once the in-flight sends have left. It is idempotent and never blocks on a send.
func (s *channelState) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		// Another goroutine is closing: return only once it is done, so that a
		// returned close always means the message channels are closed.
		<-s.finished

		return
	}
	s.closed = true
	// Senders parked on a full buffer bail out as soon as this is closed.
	close(s.done)
	// The lock is dropped before the wait below: holding it there would queue Info,
	// the sends that are still starting, and the peer's close behind the senders
	// that are still leaving.
	s.mu.Unlock()

	// No lock is held here: the parked senders observe done and return immediately,
	// and no new send can register, so the wait is bounded.
	s.senders.Wait()
	for _, ch := range s.channels {
		close(ch)
	}
	close(s.finished)
}

// localSession is a local session that is used to simulate a session between two views.
// It has a read channel and a write channel.
type localSession struct {
	name      string
	contextID string
	caller    string
	// info is immutable; the Closed flag reported by Info comes from the shared state.
	info         view.SessionInfo
	readChannel  chan *view.Message
	writeChannel chan *view.Message
	state        *channelState
}

func (s *localSession) Info() view.SessionInfo {
	info := s.info
	info.Closed = s.state.isClosed()

	return info
}

func (s *localSession) Send(ctx context.Context, payload []byte) error {
	return s.send(ctx, payload, view.OK)
}

func (s *localSession) SendError(ctx context.Context, payload []byte) error {
	return s.send(ctx, payload, view.ERROR)
}

func (s *localSession) send(ctx context.Context, payload []byte, status int32) error {
	if !s.state.beginSend() {
		return errors.New("session is closed")
	}
	defer s.state.endSend()

	msg := &view.Message{
		SessionID:    s.info.ID,
		ContextID:    s.contextID,
		Caller:       s.caller,
		FromEndpoint: s.info.RemoteEndpoint,
		FromPKID:     s.info.RemotePKID,
		Status:       status,
		Payload:      payload,
		Ctx:          ctx,
	}

	select {
	case s.writeChannel <- msg:
		return nil
	default:
	}

	select {
	case s.writeChannel <- msg:
		return nil
	case <-s.state.done:
		return errors.New("session is closed")
	case <-ctx.Done():
		return errors.Wrapf(ctx.Err(), "failed sending message on session [%s]", s.info.ID)
	}
}

func (s *localSession) Receive() <-chan *view.Message {
	return s.readChannel
}

func (s *localSession) Close() {
	s.state.close()
}
