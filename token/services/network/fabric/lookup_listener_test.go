/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

const testKey = "transfer-metadata-key"

// TestLookupListenerWaitOnStatus verifies the happy path: the value reported by the
// lookup manager is returned to the waiter.
func TestLookupListenerWaitOnStatus(t *testing.T) {
	l := newLookupListener(testKey)

	go l.OnStatus(context.Background(), testKey, []byte("pre-image"))

	value, err := l.wait(5 * time.Second)
	require.NoError(t, err)
	assert.Equal(t, []byte("pre-image"), value)
}

// TestLookupListenerWaitOnError verifies that a failure reported by the lookup manager
// is surfaced to the waiter.
func TestLookupListenerWaitOnError(t *testing.T) {
	l := newLookupListener(testKey)
	expected := errors.New("scan failed")

	go l.OnError(context.Background(), testKey, expected)

	value, err := l.wait(5 * time.Second)
	require.ErrorIs(t, err, expected)
	assert.Nil(t, value)
}

// TestLookupListenerIgnoresOtherKeys verifies that notifications for a different key do
// not release the waiter, which must still time out.
func TestLookupListenerIgnoresOtherKeys(t *testing.T) {
	l := newLookupListener(testKey)

	l.OnStatus(context.Background(), "some-other-key", []byte("not mine"))
	l.OnError(context.Background(), "some-other-key", errors.New("not mine either"))

	_, err := l.wait(20 * time.Millisecond)
	require.ErrorContains(t, err, "timed out")
}

// TestLookupListenerFirstNotificationWins verifies that done is closed exactly once, so a
// duplicate or racing notification neither panics nor overwrites the reported result.
func TestLookupListenerFirstNotificationWins(t *testing.T) {
	l := newLookupListener(testKey)

	l.OnStatus(context.Background(), testKey, []byte("first"))
	l.OnStatus(context.Background(), testKey, []byte("second"))
	l.OnError(context.Background(), testKey, errors.New("late failure"))

	value, err := l.wait(5 * time.Second)
	require.NoError(t, err)
	assert.Equal(t, []byte("first"), value)
}

// TestLookupListenerConcurrentNotifications verifies that concurrent notifications for the
// same key are safe. Run with -race, this covers the close-once path under contention.
func TestLookupListenerConcurrentNotifications(t *testing.T) {
	l := newLookupListener(testKey)

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			if i%2 == 0 {
				l.OnStatus(context.Background(), testKey, []byte(strconv.Itoa(i)))
			} else {
				l.OnError(context.Background(), testKey, errors.New(strconv.Itoa(i)))
			}
		})
	}
	wg.Wait()

	_, err := l.wait(5 * time.Second)
	// Which notification won is undefined; not panicking and not blocking is the point.
	_ = err
}

// TestLookupListenerWaitTimeoutDoesNotLeak is the regression test for issue #2124.
//
// The waiter is abandoned exactly as LookupTransferMetadataKey abandons it in production:
// the timeout fires and the listener is never notified, because the deferred
// RemoveLookupListener has torn it down, so OnStatus/OnError will never be called.
//
// The test deliberately does NOT release the listener afterwards. That is what made the
// original tests vacuous — releasing the signal also let the leaked goroutine exit, so they
// passed against the buggy implementation. goleak fails here if anything is still parked.
func TestLookupListenerWaitTimeoutDoesNotLeak(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	l := newLookupListener(testKey)

	_, err := l.wait(20 * time.Millisecond)
	require.ErrorContains(t, err, "timed out")
	require.ErrorContains(t, err, testKey, "the timeout error should name the key being looked up")
}

// TestLookupListenerRepeatedTimeoutsDoNotLeak covers the accumulating case from #2124: every
// unrevealed HTLC pre-image used to strand one goroutine for the life of the process. As
// above, no listener is ever notified.
func TestLookupListenerRepeatedTimeoutsDoNotLeak(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	for range 32 {
		_, err := newLookupListener(testKey).wait(time.Millisecond)
		require.ErrorContains(t, err, "timed out")
	}
}
