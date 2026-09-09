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

// LocalBidirectionalChannel is a bidirectional channel that is used to simulate
// a session between two views (let's call them L and R) running in the same process.
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
	lr := make(chan *view.Message, 10)
	rl := make(chan *view.Message, 10)

	info := view.SessionInfo{
		ID:             base64.StdEncoding.EncodeToString(ID),
		Caller:         nil,
		CallerViewID:   "",
		RemoteEndpoint: endpoint,
		RemotePKID:     pkid,
		Closed:         false,
	}

	return &LocalBidirectionalChannel{
		left: &localSession{
			name:         "left",
			contextID:    contextID,
			caller:       caller,
			info:         info,
			readChannel:  rl,
			writeChannel: lr,
			closedChan:   make(chan struct{}),
		},
		right: &localSession{
			name:         "right",
			contextID:    contextID,
			caller:       caller,
			info:         info,
			readChannel:  lr,
			writeChannel: rl,
			closedChan:   make(chan struct{}),
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

// localSession is a local session that is used to simulate a session between two views.
// It has a read channel and a write channel.
type localSession struct {
	name         string
	contextID    string
	caller       string
	info         view.SessionInfo
	readChannel  chan *view.Message
	writeChannel chan *view.Message

	mu          sync.RWMutex
	closed      bool
	closedChan  chan struct{}
	inFlight    int
	writeClosed bool
}

func (s *localSession) Info() view.SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.info
}

func (s *localSession) Send(ctx context.Context, payload []byte) error {
	return s.send(ctx, payload, view.OK)
}

func (s *localSession) SendError(ctx context.Context, payload []byte) error {
	return s.send(ctx, payload, view.ERROR)
}

func (s *localSession) send(ctx context.Context, payload []byte, status int32) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()

		return errors.New("session is closed")
	}
	s.inFlight++
	info := s.info
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.inFlight--
		if s.inFlight == 0 && s.closed && !s.writeClosed {
			s.writeClosed = true
			close(s.writeChannel)
		}
		s.mu.Unlock()
	}()

	msg := &view.Message{
		SessionID:    info.ID,
		ContextID:    s.contextID,
		Caller:       s.caller,
		FromEndpoint: info.RemoteEndpoint,
		FromPKID:     info.RemotePKID,
		Status:       status,
		Payload:      payload,
		Ctx:          ctx,
	}

	select {
	case s.writeChannel <- msg:
		return nil
	case <-s.closedChan:
		return errors.New("session is closed")
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "context cancelled while sending message")
	}
}

func (s *localSession) Receive() <-chan *view.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil
	}

	return s.readChannel
}

func (s *localSession) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()

		return
	}
	s.closed = true
	s.info.Closed = true
	close(s.closedChan)
	if s.inFlight == 0 && !s.writeClosed {
		s.writeClosed = true
		close(s.writeChannel)
	}
	s.mu.Unlock()
}
