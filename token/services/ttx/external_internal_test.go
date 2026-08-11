/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This white-box (package ttx) file covers StreamExternalWalletSignerClient: the errors that
// its init goroutine reports on the unexported err channel must reach the caller of Respond
// instead of being swallowed until the timeout elapses.
package ttx

import (
	"bytes"
	"encoding/json"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recvStep is a single scripted outcome of a call to fakeStream.Recv: either the message to
// deliver, or the error to return. When after is set, Recv waits for it to be closed before
// producing the outcome, which lets a test order the stream against Respond's progress.
type recvStep struct {
	msg   *StreamExternalWalletMsg
	err   error
	after chan struct{}
}

// fakeStream is a view2.Stream whose Recv calls are scripted. Once the script is exhausted,
// Recv blocks until the test finishes, mimicking a stream that simply goes quiet. It is used
// from both the init goroutine (Recv) and the Respond caller (Send), hence the mutex.
type fakeStream struct {
	mu      sync.Mutex
	steps   []recvStep
	recvIdx int
	sent    []*StreamExternalWalletMsg
	sendErr error
	quiet   chan struct{}
}

func newFakeStream(t *testing.T, steps ...recvStep) *fakeStream {
	t.Helper()
	s := &fakeStream{steps: steps, quiet: make(chan struct{})}
	// release any Recv still parked on the exhausted script so the init goroutine does not
	// outlive the test.
	t.Cleanup(func() { close(s.quiet) })

	return s
}

func (s *fakeStream) Recv(m any) error {
	s.mu.Lock()
	if s.recvIdx >= len(s.steps) {
		s.mu.Unlock()
		<-s.quiet

		return errors.New("stream closed")
	}
	step := s.steps[s.recvIdx]
	s.recvIdx++
	s.mu.Unlock()

	if step.after != nil {
		select {
		case <-step.after:
		case <-s.quiet:
			return errors.New("stream closed")
		}
	}
	if step.err != nil {
		return step.err
	}
	msg, ok := m.(*StreamExternalWalletMsg)
	if !ok {
		return errors.Errorf("unexpected receive target [%T]", m)
	}
	*msg = *step.msg

	return nil
}

func (s *fakeStream) Send(m any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sendErr != nil {
		return s.sendErr
	}
	msg, ok := m.(*StreamExternalWalletMsg)
	if !ok {
		return errors.Errorf("unexpected send value [%T]", m)
	}
	s.sent = append(s.sent, msg)

	return nil
}

func (s *fakeStream) sentMessages() []*StreamExternalWalletMsg {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]*StreamExternalWalletMsg(nil), s.sent...)
}

// fakeSigner returns a fixed signature, or a fixed error.
type fakeSigner struct {
	sigma []byte
	err   error
}

func (f *fakeSigner) Sign(_ []byte) ([]byte, error) { return f.sigma, f.err }

// fakeSignerProvider hands out the same signer for every party.
type fakeSignerProvider struct {
	signer token.Signer
	err    error
}

func (f *fakeSignerProvider) GetSigner(_ view.Identity) (token.Signer, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.signer, nil
}

func sigRequestMsg(t *testing.T, party view.Identity, message []byte) *StreamExternalWalletMsg {
	t.Helper()
	msg, err := NewStreamExternalWalletMsg(SigRequest, &StreamExternalWalletSignRequest{
		Party:   party,
		Message: message,
	})
	require.NoError(t, err)

	return msg
}

// respondWithin runs Respond and fails the test if it does not return in time. Every case here
// is expected to return well before the client timeout; a hang means the error path is broken.
func respondWithin(t *testing.T, c *StreamExternalWalletSignerClient, d time.Duration) error {
	t.Helper()
	res := make(chan error, 1)
	go func() { res <- c.Respond() }()
	select {
	case err := <-res:
		return err
	case <-time.After(d):
		t.Fatal("Respond did not return in time")

		return nil
	}
}

// TestStreamExternalWalletSignerClientErrChannelIsBuffered guards the fix for the goroutine
// leak: with an unbuffered channel the init goroutine blocks forever on its send.
func TestStreamExternalWalletSignerClientErrChannelIsBuffered(t *testing.T) {
	c := NewStreamExternalWalletSignerClientWithTimeout(
		&fakeSignerProvider{signer: &fakeSigner{sigma: []byte("sigma")}},
		newFakeStream(t),
		time.Hour,
	)
	assert.Equal(t, 1, cap(c.err), "err channel must be buffered so init can always exit")
}

// TestStreamExternalWalletSignerClientRespondReturnsRecvError checks that a transport failure
// surfaces as the real error and not as a timeout.
func TestStreamExternalWalletSignerClientRespondReturnsRecvError(t *testing.T) {
	stream := newFakeStream(t, recvStep{err: errors.New("connection reset")})
	c := NewStreamExternalWalletSignerClientWithTimeout(
		&fakeSignerProvider{signer: &fakeSigner{sigma: []byte("sigma")}},
		stream,
		time.Hour,
	)

	err := respondWithin(t, c, 5*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to receive signature request [0]")
	assert.Contains(t, err.Error(), "connection reset")
	assert.NotContains(t, err.Error(), "Timeout waiting for stream input")
}

// TestStreamExternalWalletSignerClientRespondReturnsUnmarshalError checks the same for a
// malformed SigRequest payload.
func TestStreamExternalWalletSignerClientRespondReturnsUnmarshalError(t *testing.T) {
	stream := newFakeStream(t, recvStep{msg: &StreamExternalWalletMsg{
		Type: SigRequest,
		Raw:  []byte("not-json"),
	}})
	c := NewStreamExternalWalletSignerClientWithTimeout(
		&fakeSignerProvider{signer: &fakeSigner{sigma: []byte("sigma")}},
		stream,
		time.Hour,
	)

	err := respondWithin(t, c, 5*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get unmarshal msg type SigRequest")
	assert.NotContains(t, err.Error(), "Timeout waiting for stream input")
}

// TestStreamExternalWalletSignerClientRespondSignsUntilDone covers the happy path: every
// request is signed and answered, and Done terminates the exchange without error.
func TestStreamExternalWalletSignerClientRespondSignsUntilDone(t *testing.T) {
	party := view.Identity("alice")
	stream := newFakeStream(t,
		recvStep{msg: sigRequestMsg(t, party, []byte("msg-0"))},
		recvStep{msg: sigRequestMsg(t, party, []byte("msg-1"))},
		recvStep{msg: &StreamExternalWalletMsg{Type: Done}},
	)
	c := NewStreamExternalWalletSignerClientWithTimeout(
		&fakeSignerProvider{signer: &fakeSigner{sigma: []byte("sigma")}},
		stream,
		time.Hour,
	)

	require.NoError(t, respondWithin(t, c, 5*time.Second))

	sent := stream.sentMessages()
	require.Len(t, sent, 2)
	for _, msg := range sent {
		assert.Equal(t, SignResponse, msg.Type)
		response := &StreamExternalWalletSignResponse{}
		require.NoError(t, json.Unmarshal(msg.Raw, response))
		assert.Equal(t, []byte("sigma"), response.Sigma)
	}
}

// TestStreamExternalWalletSignerClientRespondReturnsSignError checks that a signing failure is
// reported to the caller.
func TestStreamExternalWalletSignerClientRespondReturnsSignError(t *testing.T) {
	stream := newFakeStream(t, recvStep{msg: sigRequestMsg(t, view.Identity("alice"), []byte("msg"))})
	c := NewStreamExternalWalletSignerClientWithTimeout(
		&fakeSignerProvider{signer: &fakeSigner{err: errors.New("hsm offline")}},
		stream,
		time.Hour,
	)

	err := respondWithin(t, c, 5*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hsm offline")
}

// requireInitExited waits until no goroutine is parked inside the client's receive loop. It
// inspects the stack rather than a goroutine count so that unrelated goroutines cannot make it
// pass or fail by accident.
func requireInitExited(t *testing.T, timeout time.Duration) {
	t.Helper()
	const frame = "(*StreamExternalWalletSignerClient).init"
	deadline := time.Now().Add(timeout)
	for {
		buf := make([]byte, 1<<20)
		dump := buf[:runtime.Stack(buf, true)]
		if !bytes.Contains(dump, []byte(frame)) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("receive goroutine still parked after %s:\n%s", timeout, dump)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestStreamExternalWalletSignerClientInitExitsWhenResponderStopped covers the other way the
// receive goroutine could be stranded: a request decoded after Respond has already returned has
// nobody to receive it, so the goroutine must give up instead of blocking on the handover.
//
// The gate makes the ordering deterministic: the second request is only delivered to the
// receive goroutine once Respond has returned.
func TestStreamExternalWalletSignerClientInitExitsWhenResponderStopped(t *testing.T) {
	party := view.Identity("alice")
	gate := make(chan struct{})
	stream := newFakeStream(t,
		recvStep{msg: sigRequestMsg(t, party, []byte("msg-0"))},
		recvStep{msg: sigRequestMsg(t, party, []byte("msg-1")), after: gate},
	)
	c := NewStreamExternalWalletSignerClientWithTimeout(
		&fakeSignerProvider{err: errors.New("hsm offline")},
		stream,
		time.Hour,
	)

	// the first request is consumed, then signing fails and Respond gives up
	err := respondWithin(t, c, 5*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hsm offline")

	close(gate)
	requireInitExited(t, 5*time.Second)
}

// TestStreamExternalWalletSignerClientRespondIsIdempotentOnRepeatedCalls checks that calling
// Respond again after it returned does not panic on the already-signalled done channel.
func TestStreamExternalWalletSignerClientRespondIsIdempotentOnRepeatedCalls(t *testing.T) {
	stream := newFakeStream(t, recvStep{msg: &StreamExternalWalletMsg{Type: Done}})
	c := NewStreamExternalWalletSignerClientWithTimeout(
		&fakeSignerProvider{signer: &fakeSigner{sigma: []byte("sigma")}},
		stream,
		10*time.Millisecond,
	)

	require.NoError(t, respondWithin(t, c, 5*time.Second))
	// s.input is closed by now, so the second call returns nil straight away
	require.NoError(t, respondWithin(t, c, 5*time.Second))
}

// TestStreamExternalWalletSignerClientRespondTimesOut checks that the timeout still applies
// when the remote wallet stops talking without failing the stream.
func TestStreamExternalWalletSignerClientRespondTimesOut(t *testing.T) {
	c := NewStreamExternalWalletSignerClientWithTimeout(
		&fakeSignerProvider{signer: &fakeSigner{sigma: []byte("sigma")}},
		newFakeStream(t),
		10*time.Millisecond,
	)

	err := respondWithin(t, c, 5*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Timeout waiting for stream input")
}
