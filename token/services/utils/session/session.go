/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package session

//go:generate counterfeiter -o mock/session.go -fake-name Session . Session
//go:generate counterfeiter -o mock/marshaller.go -fake-name Marshaller . Marshaller

import (
	"context"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

const DefaultReceiveTimeout = 10 * time.Second

// DefaultMaxRecvMessageSize is the default upper bound, in bytes, on the size
// of a single inbound message accepted by a session. It is generous enough for
// legitimate token transactions (matching common gRPC/Fabric maximums) while
// still bounding the memory a remote party can force the receiver to allocate
// before any deserialization happens. Override per session with
// WithMaxRecvMessageSize.
const DefaultMaxRecvMessageSize = 100 * 1024 * 1024 // 100 MiB

// ErrTimeout is returned when a session receive operation times out.
var ErrTimeout = errors.New("session timeout")

// ErrNilMessage is returned when a nil message is received.
var ErrNilMessage = errors.New("received message is nil")

// ErrContextDone is returned when the context is done.
var ErrContextDone = errors.New("context done")

// ErrMessageTooLarge is returned when an inbound message exceeds the session's
// configured maximum size. The oversized payload is rejected before it is
// deserialized or otherwise processed.
var ErrMessageTooLarge = errors.New("received message exceeds maximum allowed size")

var logger = logging.MustGetLogger()

type Session = view.Session

// Marshaller is a generic interface for marshalling and unmarshalling data.
type Marshaller interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

type S struct {
	s           Session
	ctx         context.Context
	marshaller  Marshaller
	maxRecvSize int
}

// Option customizes a session created with New.
type Option func(*S)

// WithMaxRecvMessageSize sets the maximum size, in bytes, of a single inbound
// message. Messages larger than this are rejected with ErrMessageTooLarge
// before any deserialization. A value <= 0 disables the limit.
func WithMaxRecvMessageSize(maxBytes int) Option {
	return func(s *S) { s.maxRecvSize = maxBytes }
}

func New(s Session, ctx context.Context, m Marshaller, opts ...Option) *S {
	logger.DebugfContext(ctx, "Open session to [%s]", logging.Eval(s.Info))

	sess := &S{s: s, ctx: ctx, marshaller: m, maxRecvSize: DefaultMaxRecvMessageSize}
	for _, opt := range opts {
		opt(sess)
	}

	return sess
}

func (j *S) Receive(state any) error {
	return j.ReceiveWithTimeout(state, DefaultReceiveTimeout)
}

func (j *S) ReceiveWithTimeout(state any, d time.Duration) error {
	raw, err := j.ReceiveRawWithTimeout(d)
	if err != nil {
		return err
	}

	err = j.marshaller.Unmarshal(raw, state)
	if err != nil {
		return errors.Wrapf(err, "failed unmarshalling state, len [%d]", len(raw))
	}

	return nil
}

func (j *S) ReceiveRaw() ([]byte, error) {
	return j.ReceiveRawWithTimeout(DefaultReceiveTimeout)
}

func (j *S) ReceiveRawWithTimeout(d time.Duration) ([]byte, error) {
	timeout := time.NewTimer(d)
	defer timeout.Stop()

	logger.DebugfContext(j.ctx, "Wait to receive")
	ch := j.s.Receive()
	select {
	case msg := <-ch:
		if msg == nil {
			logger.ErrorfContext(j.ctx, "Received nil message")

			return nil, ErrNilMessage
		}
		if msg.Status == view.ERROR {
			logger.ErrorfContext(j.ctx, "Received error message")

			return nil, errors.Errorf("received error from remote [%s]", string(msg.Payload))
		}
		// Reject oversized payloads before any deserialization so a remote party
		// cannot force the receiver to allocate unbounded memory.
		if j.maxRecvSize > 0 && len(msg.Payload) > j.maxRecvSize {
			logger.ErrorfContext(j.ctx, "received message of size [%d] exceeds maximum [%d] on session [%s]", len(msg.Payload), j.maxRecvSize, j.Info().ID)

			return nil, errors.Join(errors.Errorf("message size [%d] exceeds maximum [%d]", len(msg.Payload), j.maxRecvSize), ErrMessageTooLarge)
		}
		logger.DebugfContext(j.ctx, "session, received message [%s]", logging.SHA256Base64(msg.Payload))

		return msg.Payload, nil
	case <-timeout.C:
		logger.ErrorfContext(j.ctx, "timeout reached")

		return nil, errors.Join(errors.Errorf("time out reached on session [%s]", j.Info().ID), ErrTimeout)
	case <-j.ctx.Done():
		logger.ErrorfContext(j.ctx, "ctx done: %w", j.ctx.Err())

		return nil, errors.Join(errors.Errorf("ctx done [%s]", j.ctx.Err()), ErrContextDone)
	}
}

func (j *S) Send(state any) error {
	return j.SendWithContext(j.ctx, state)
}

func (j *S) SendWithContext(ctx context.Context, state any) error {
	v, err := j.marshaller.Marshal(state)
	if err != nil {
		return err
	}

	logger.DebugfContext(ctx, "session, send message [%s]", logging.SHA256Base64(v))

	return j.s.SendWithContext(ctx, v)
}

func (j *S) SendRaw(ctx context.Context, raw []byte) error {
	logger.DebugfContext(ctx, "session, send raw message [%s]", logging.SHA256Base64(raw))

	return j.s.SendWithContext(ctx, raw)
}

func (j *S) SendError(err string) error {
	return j.SendErrorWithContext(j.ctx, err)
}

func (j *S) SendErrorWithContext(ctx context.Context, err string) error {
	logger.ErrorfContext(ctx, "session, send error: %w", err)

	return j.s.SendErrorWithContext(ctx, []byte(err))
}

func (j *S) Session() Session {
	return j.s
}

func (j *S) Info() view.SessionInfo {
	return j.s.Info()
}
