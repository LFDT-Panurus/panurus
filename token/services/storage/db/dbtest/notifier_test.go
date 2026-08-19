/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbtest

import (
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	"github.com/stretchr/testify/require"
)

// fakeTransportSubscriber is a subscriber that never delivers events but can
// report a transport-level failure, standing in for a Postgres notifier whose
// LISTEN connection dropped.
type fakeTransportSubscriber struct {
	transportErr chan error
}

func (f *fakeTransportSubscriber) Subscribe(func(operation driver.Operation, vals int)) error {
	return nil
}

func (f *fakeTransportSubscriber) TransportError() <-chan error { return f.transportErr }

// newLostCollector builds a collector whose transport channel already carries
// an error, so AssertSize observes the drop on its first select iteration.
func newLostCollector() *dbEventsCollector[int] {
	errCh := make(chan error, 1)
	errCh <- errors.New("waiting for notification: unexpected EOF")

	return &dbEventsCollector[int]{close: make(chan bool, 1), transportErr: errCh}
}

// TestAssertSizeReturnsListenerConnectionLost verifies that a transport failure
// during the wait yields the ErrListenerConnectionLost sentinel rather than a
// plain timeout, so callers can tell a transient infra fault apart from a real
// notifier defect. See issue #2270.
func TestAssertSizeReturnsListenerConnectionLost(t *testing.T) {
	c := newLostCollector()
	err := c.AssertSize(1)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrListenerConnectionLost), "expected ErrListenerConnectionLost, got %v", err)
}

// TestRequireSizeOrSkipSkipsOnConnectionLost verifies that requireSizeOrSkip
// skips (does not fail) the subtest when the transport connection was lost.
func TestRequireSizeOrSkipSkipsOnConnectionLost(t *testing.T) {
	c := newLostCollector()

	var reached, skipped bool
	failed := !t.Run("inner", func(st *testing.T) {
		defer func() { skipped = st.Skipped() }()
		requireSizeOrSkip(st, c, 1)
		reached = true // unreachable: Skipf calls runtime.Goexit
	})

	require.False(t, reached, "requireSizeOrSkip must not fall through on a lost connection")
	require.True(t, skipped, "subtest must be marked skipped")
	require.False(t, failed, "a skipped subtest must not be reported as failed")
}

// TestCollectDBEventsCapturesTransportChannel verifies the wiring: when a
// subscriber implements transportErrorReporter, collectDBEvents captures its
// channel so a later transport failure is surfaced through AssertSize.
func TestCollectDBEventsCapturesTransportChannel(t *testing.T) {
	errCh := make(chan error, 1)
	c, err := collectDBEvents[int](&fakeTransportSubscriber{transportErr: errCh})
	require.NoError(t, err)

	errCh <- errors.New("dial tcp 127.0.0.1:32831: connect: connection refused")
	require.True(t, errors.Is(c.AssertSize(1), ErrListenerConnectionLost))
}

// TestAssertSizeSucceedsWhenSizeReached is a sanity check that the happy path
// still returns nil once the expected number of events have been collected.
func TestAssertSizeSucceedsWhenSizeReached(t *testing.T) {
	c := &dbEventsCollector[int]{close: make(chan bool, 1)}
	c.Append(dbEvent[int]{Val: 1})

	require.NoError(t, c.AssertSize(1))
}
