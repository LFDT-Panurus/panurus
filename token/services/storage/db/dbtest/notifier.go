/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbtest

import (
	"sync"
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	"github.com/stretchr/testify/require"
)

// ErrListenerConnectionLost is returned by AssertSize when the collector timed
// out AND the notifier reported a transport-level failure (e.g. a dropped
// Postgres LISTEN connection) during the wait. The expected notification was
// legitimately lost to a connectivity gap rather than to a notifier defect, so
// callers should skip rather than fail. See issue #2270.
var ErrListenerConnectionLost = errors.New("notifier listener connection lost")

type dbEvent[T any] struct {
	Op  driver.Operation
	Val T
}

type subscriber[T any] interface {
	Subscribe(func(operation driver.Operation, vals T)) error
}

// transportErrorReporter is optionally implemented by a subscriber whose
// underlying transport can fail independently of the subscription callback
// (e.g. a Postgres LISTEN/NOTIFY connection that drops and reconnects). While
// the transport is down, notifications are silently lost, so the collector can
// observe this channel to tell an infra-induced miss apart from a real defect.
type transportErrorReporter interface {
	TransportError() <-chan error
}

type dbEventsCollector[T any] struct {
	close chan bool
	mu    sync.RWMutex
	// result accumulates the events delivered through the subscription.
	result []dbEvent[T]
	// transportErr, when non-nil, surfaces transport-level failures of the
	// underlying notifier. A receive on a nil channel blocks forever, so
	// notifiers that do not report transport errors simply never trigger the
	// corresponding branch in AssertSize.
	transportErr <-chan error
}

func collectDBEvents[T any](db subscriber[T]) (*dbEventsCollector[T], error) {
	ch := make(chan dbEvent[T])
	closeCh := make(chan bool)
	err := db.Subscribe(func(operation driver.Operation, m T) {
		ch <- dbEvent[T]{Op: operation, Val: m}
	})
	if err != nil {
		return nil, err
	}
	collector := &dbEventsCollector[T]{close: closeCh}
	if r, ok := db.(transportErrorReporter); ok {
		collector.transportErr = r.TransportError()
	}
	go func(collector *dbEventsCollector[T]) {
		for {
			select {
			case e := <-ch:
				collector.Append(e)
			case <-closeCh:
				return
			}
		}
	}(collector)

	return collector, nil
}

func (c *dbEventsCollector[T]) AssertSize(size int) error {
	defer func() {
		c.close <- true
	}()

	// The overall timeout must be computed once: allocating time.After inside
	// the loop would reset it on every poll tick, so it could never elapse and
	// AssertSize would spin forever when the expected size is never reached.
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-c.close:
			return errors.Errorf("db events collector closed")
		case err := <-c.transportErr:
			// The notifier's transport dropped (e.g. the Postgres LISTEN
			// connection was lost). Any notification emitted during the outage
			// is gone, so waiting for it is pointless: return a distinguishable
			// error so the caller can skip a transient infra fault rather than
			// fail on it. See issue #2270.
			return errors.Wrapf(ErrListenerConnectionLost, "db events collector: %v", err)
		case <-timeout.C:
			return errors.Errorf("db events collector timeout")
		case <-ticker.C:
			c.mu.RLock()
			resultSize := len(c.result)
			c.mu.RUnlock()
			if resultSize == size {
				return nil
			}
		}
	}
}

// requireSizeOrSkip asserts the collector observed exactly size events. A
// dropped notifier transport (e.g. a transient Postgres LISTEN connection loss;
// see issue #2270) legitimately loses notifications, so the subtest is skipped
// rather than failed — the miss is an infrastructure fault, not a notifier
// defect. It must be given the subtest's *testing.T so the skip is attributed
// to the subtest and not to its parent.
func requireSizeOrSkip[T any](t *testing.T, result *dbEventsCollector[T], size int) {
	t.Helper()
	err := result.AssertSize(size)
	if errors.Is(err, ErrListenerConnectionLost) {
		t.Skipf("notifier transport dropped during test, skipping flaky assertion: %v", err)
	}
	require.NoError(t, err)
}

func (c *dbEventsCollector[T]) Append(e dbEvent[T]) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.result = append(c.result, e)
}

func (c *dbEventsCollector[T]) Values() []dbEvent[T] {
	c.mu.Lock()
	defer c.mu.Unlock()
	clone := make([]dbEvent[T], len(c.result))
	copy(clone, c.result)
	c.result = clone

	return clone
}
