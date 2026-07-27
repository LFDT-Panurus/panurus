/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"
	"errors"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"go.opentelemetry.io/otel/trace"
)

// errNoService is returned by fakeContext.GetService so callers that probe for optional services
// (envelope metrics) fall back gracefully instead of hitting a panic.
var errNoService = errors.New("no service in fake context")

// pipeSession is one end of an in-memory, bidirectional session between two views. Send delivers to
// the peer's inbox; Receive drains this end's inbox. It implements view.Session, so the real
// Initiator.Call and Responder.Call paths run over it in the gate test.
type pipeSession struct {
	caller  view.Identity
	inbox   chan *view.Message
	peerBox chan *view.Message
}

// newPipe returns the two ends of a session. Each end reports its peer's identity as the caller, so
// the responder end authorizes against the initiator's authenticated identity.
func newPipe(initiatorID, endorserID view.Identity) (initiatorEnd, endorserEnd *pipeSession) {
	a := make(chan *view.Message, 4)
	b := make(chan *view.Message, 4)
	initiatorEnd = &pipeSession{caller: endorserID, inbox: a, peerBox: b}
	endorserEnd = &pipeSession{caller: initiatorID, inbox: b, peerBox: a}

	return initiatorEnd, endorserEnd
}

func (s *pipeSession) Info() view.SessionInfo { return view.SessionInfo{ID: "pipe", Caller: s.caller} }

func (s *pipeSession) Send(payload []byte) error {
	return s.SendWithContext(context.Background(), payload)
}

func (s *pipeSession) SendWithContext(_ context.Context, payload []byte) error {
	s.peerBox <- &view.Message{Status: view.OK, Payload: payload}

	return nil
}

func (s *pipeSession) SendError(payload []byte) error {
	return s.SendErrorWithContext(context.Background(), payload)
}

func (s *pipeSession) SendErrorWithContext(_ context.Context, payload []byte) error {
	s.peerBox <- &view.Message{Status: view.ERROR, Payload: payload}

	return nil
}

func (s *pipeSession) Receive() <-chan *view.Message { return s.inbox }

func (s *pipeSession) Close() {}

// fakeContext is a minimal view.Context for the endorsement tests: it carries a context.Context, and
// (for the gate test) hands the initiator a session per party via GetSession and the responder its
// own session via Session. Everything the endorsement views do not touch panics, so an unexpected
// dependency surfaces loudly rather than silently.
type fakeContext struct {
	ctx      context.Context
	me       view.Identity
	sessions map[string]view.Session // keyed by party UniqueID, for GetSession (initiator side)
	own      view.Session            // for Session() (responder side)
}

func (c *fakeContext) Context() context.Context { return c.ctx }
func (c *fakeContext) ID() string               { return "fake" }
func (c *fakeContext) Me() view.Identity        { return c.me }

func (c *fakeContext) GetSession(_ view.View, party view.Identity, _ ...view.View) (view.Session, error) {
	return c.sessions[party.UniqueID()], nil
}

func (c *fakeContext) Session() view.Session { return c.own }

// unused view.Context surface: these must never be called by the endorsement views under test.
func (c *fakeContext) StartSpanFrom(ctx context.Context, _ string, _ ...trace.SpanStartOption) (context.Context, trace.Span) {
	return ctx, trace.SpanFromContext(ctx)
}

// GetService returns an error rather than a service: the typed session resolves optional envelope
// metrics through it and treats an error as "no metrics", which is what the tests want (no metrics
// registered). Panicking here would abort the session setup.
func (c *fakeContext) GetService(any) (any, error) {
	return nil, errNoService
}

func (c *fakeContext) RunView(view.View, ...view.RunViewOption) (any, error) {
	panic("RunView not supported in fake context")
}
func (c *fakeContext) IsMe(view.Identity) bool { return false }
func (c *fakeContext) Initiator() view.View    { return nil }

func (c *fakeContext) GetSessionByID(string, view.Identity) (view.Session, error) {
	panic("GetSessionByID not supported in fake context")
}
func (c *fakeContext) OnError(func()) {}

// compile-time checks that the fakes satisfy the FSC contracts.
var (
	_ view.Session = (*pipeSession)(nil)
	_ view.Context = (*fakeContext)(nil)
)
