/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role_test

import (
	"context"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// provisionGoroutineName is the symbol that appears in the stack trace of the
// background provisioning goroutine. Searching all stacks for it is a precise way to
// assert whether the goroutine is running, unlike a plain goroutine count.
const provisionGoroutineName = "role.(*RecipientDataCache).provisionIdentities"

// allStacks returns the stack traces of every goroutine in the process.
func allStacks() string {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return string(buf[:n])
		}
		buf = make([]byte, 2*len(buf))
	}
}

// provisioningGoroutines counts the running provisioning goroutines.
func provisioningGoroutines() int {
	return strings.Count(allStacks(), provisionGoroutineName)
}

// requireProvisioningGoroutines waits until exactly want provisioning goroutines are
// running in the process. Every test in this package closes the caches it creates, so
// the count is a reliable absolute number as long as tests wait for it to settle
// before measuring (goroutines closed by a previous test may still be winding down).
func requireProvisioningGoroutines(t *testing.T, want int, within time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		return provisioningGoroutines() == want
	}, within, 5*time.Millisecond,
		"expected [%d] provisioning goroutines, got [%d]", want, provisioningGoroutines())
}

// cacheFixture is a cache under test whose provisioning goroutine is guaranteed to be
// released when the test ends.
type cacheFixture struct {
	*role.RecipientDataCache
}

// requireProvisioning waits until exactly want provisioning goroutines are running.
func (f *cacheFixture) requireProvisioning(t *testing.T, want int, within time.Duration) {
	t.Helper()
	requireProvisioningGoroutines(t, want, within)
}

// assertNoProvisioning asserts no provisioning goroutine is running, without waiting.
func (f *cacheFixture) assertNoProvisioning(t *testing.T) {
	t.Helper()
	assert.Zero(t, provisioningGoroutines(), "no provisioning goroutine must be running")
}

func newRecipientData(id string) *driver.RecipientData {
	return &driver.RecipientData{
		Identity:  driver.Identity(id),
		AuditInfo: []byte("audit-info"),
	}
}

func newTestCacheWithMetrics(t *testing.T, size int, m *role.Metrics, backend role.RecipientDataBackendFunc) *cacheFixture {
	t.Helper()
	// Wait for goroutines released by earlier tests to actually exit, so this test can
	// count provisioning goroutines in absolute terms.
	requireProvisioningGoroutines(t, 0, 2*time.Second)
	f := &cacheFixture{
		RecipientDataCache: role.NewRecipientDataCache(logging.MustGetLogger("cache_test"), backend, size, m),
	}
	// No test may leave a provisioning goroutine behind, even if it fails early.
	t.Cleanup(func() {
		f.Close()
		f.requireProvisioning(t, 0, 2*time.Second)
	})

	return f
}

func newTestCache(t *testing.T, size int, backend role.RecipientDataBackendFunc) *cacheFixture {
	t.Helper()

	return newTestCacheWithMetrics(t, size, role.NewMetrics(&disabled.Provider{}), backend)
}

// TestRecipientDataCacheFromCache checks recipient data is served out of the
// pre-provisioned cache.
func TestRecipientDataCacheFromCache(t *testing.T) {
	c := newTestCache(t, 10, func(context.Context) (*driver.RecipientData, error) {
		return newRecipientData("cached"), nil
	})

	rd, err := c.RecipientData(t.Context())
	require.NoError(t, err)
	require.NotNil(t, rd)
	assert.Equal(t, driver.Identity("cached"), rd.Identity)
}

// TestRecipientDataCacheOnTheSpot checks that with caching disabled the recipient
// data is generated on demand and no background goroutine is started.
func TestRecipientDataCacheOnTheSpot(t *testing.T) {
	var calls atomic.Int32
	c := newTestCache(t, 0, func(context.Context) (*driver.RecipientData, error) {
		calls.Add(1)

		return newRecipientData("on-the-spot"), nil
	})

	rd, err := c.RecipientData(t.Context())
	require.NoError(t, err)
	assert.Equal(t, driver.Identity("on-the-spot"), rd.Identity)
	assert.Equal(t, int32(1), calls.Load())
	c.assertNoProvisioning(t)
}

// TestRecipientDataCacheBackendError checks the error returned by the backend is
// wrapped and propagated to the caller.
func TestRecipientDataCacheBackendError(t *testing.T) {
	c := newTestCache(t, 0, func(context.Context) (*driver.RecipientData, error) {
		return nil, errors.New("backend is down")
	})

	rd, err := c.RecipientData(t.Context())
	require.Error(t, err)
	assert.Nil(t, rd)
	require.ErrorContains(t, err, "failed fetching wallet identity")
	require.ErrorContains(t, err, "backend is down")
}

// TestRecipientDataCacheContextDone checks a cancelled caller context aborts the
// fetch instead of falling back to the backend.
func TestRecipientDataCacheContextDone(t *testing.T) {
	c := newTestCache(t, 0, func(context.Context) (*driver.RecipientData, error) {
		return newRecipientData("never"), nil
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	rd, err := c.RecipientData(ctx)
	require.Error(t, err)
	assert.Nil(t, rd)
	require.ErrorContains(t, err, "context is done")
}

// TestRecipientDataCacheCloseStopsProvisioning is the regression test for the
// uncancellable provisioning goroutine: after Close the goroutine must terminate and
// stop calling the backend.
func TestRecipientDataCacheCloseStopsProvisioning(t *testing.T) {
	var calls atomic.Int32
	c := newTestCache(t, 10, func(context.Context) (*driver.RecipientData, error) {
		calls.Add(1)

		return newRecipientData("provisioned"), nil
	})

	// Trigger provisioning and wait for the goroutine to be up and working.
	_, err := c.RecipientData(t.Context())
	require.NoError(t, err)
	c.requireProvisioning(t, 1, 2*time.Second)
	require.Eventually(t, func() bool {
		return calls.Load() > 0
	}, 2*time.Second, 5*time.Millisecond)

	c.Close()
	c.requireProvisioning(t, 0, 2*time.Second)

	// Once the goroutine is gone the backend must not be called any more.
	callsAfterClose := calls.Load()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, callsAfterClose, calls.Load(), "the backend was called after Close")
}

// TestRecipientDataCacheCloseUnblocksSend checks Close releases a goroutine that is
// blocked handing an entry to a full cache channel. Before the fix the send had no
// cancellation branch, so the goroutine stayed parked there for the process lifetime.
func TestRecipientDataCacheCloseUnblocksSend(t *testing.T) {
	// A cache of size 1 fills up immediately, and the test never drains it again, so
	// the goroutine ends up parked on the channel send.
	c := newTestCache(t, 1, func(context.Context) (*driver.RecipientData, error) {
		return newRecipientData("provisioned"), nil
	})

	_, err := c.RecipientData(t.Context())
	require.NoError(t, err)
	c.requireProvisioning(t, 1, 2*time.Second)

	// Let it settle into the blocking send, then confirm it is parked and still there.
	time.Sleep(50 * time.Millisecond)
	require.Contains(t, allStacks(), provisionGoroutineName)

	c.Close()
	c.requireProvisioning(t, 0, 2*time.Second)
}

// TestRecipientDataCacheCloseIsIdempotent checks Close can be called repeatedly.
func TestRecipientDataCacheCloseIsIdempotent(t *testing.T) {
	c := newTestCache(t, 10, func(context.Context) (*driver.RecipientData, error) {
		return newRecipientData("provisioned"), nil
	})

	assert.NotPanics(t, func() {
		c.Close()
		c.Close()
		c.Close()
	})
}

// TestRecipientDataCacheCloseBeforeFirstUse checks a cache closed before its first
// use never starts a provisioning goroutine, while still serving recipient data.
func TestRecipientDataCacheCloseBeforeFirstUse(t *testing.T) {
	c := newTestCache(t, 10, func(context.Context) (*driver.RecipientData, error) {
		return newRecipientData("on-the-spot"), nil
	})

	c.Close()

	rd, err := c.RecipientData(t.Context())
	require.NoError(t, err)
	assert.Equal(t, driver.Identity("on-the-spot"), rd.Identity)
	c.assertNoProvisioning(t)
}

// TestRecipientDataCacheFailingBackendDoesNotSpin checks a persistently failing
// backend makes the provisioning loop back off instead of burning a core on a bare
// continue.
func TestRecipientDataCacheFailingBackendDoesNotSpin(t *testing.T) {
	var calls atomic.Int32
	c := newTestCache(t, 10, func(context.Context) (*driver.RecipientData, error) {
		calls.Add(1)

		return nil, errors.New("backend is down")
	})

	// The caller gets the error, and the provisioning loop keeps retrying in the
	// background.
	_, err := c.RecipientData(t.Context())
	require.Error(t, err)

	time.Sleep(300 * time.Millisecond)
	// With a one second backoff only a handful of attempts can have happened. Without
	// any backoff this counter would be orders of magnitude higher.
	assert.Less(t, calls.Load(), int32(10), "the failing backend is being retried in a busy loop")
}

// TestRecipientDataCacheCloseDuringBackoff checks Close interrupts the retry backoff
// instead of waiting for it to elapse.
func TestRecipientDataCacheCloseDuringBackoff(t *testing.T) {
	c := newTestCache(t, 10, func(context.Context) (*driver.RecipientData, error) {
		return nil, errors.New("backend is down")
	})

	_, err := c.RecipientData(t.Context())
	require.Error(t, err)
	c.requireProvisioning(t, 1, 2*time.Second)

	c.Close()
	// Well below the one second backoff: the goroutine must not sit out the timer.
	c.requireProvisioning(t, 0, 500*time.Millisecond)
}

// TestRecipientDataCacheGaugeTracksCacheLevel checks the cache level gauge is only
// incremented for entries that actually made it into the cache, so that a Close
// racing a blocked send cannot leave the reported level permanently too high.
func TestRecipientDataCacheGaugeTracksCacheLevel(t *testing.T) {
	gauge := &countingGauge{}
	c := newTestCacheWithMetrics(t, 1, &role.Metrics{CacheLevelGauge: gauge, ProvisionFailuresCount: &countingCounter{}},
		func(context.Context) (*driver.RecipientData, error) {
			return newRecipientData("provisioned"), nil
		})

	_, err := c.RecipientData(t.Context())
	require.NoError(t, err)
	c.requireProvisioning(t, 1, 2*time.Second)

	// The goroutine is now parked on the send of an entry the cache will never take.
	time.Sleep(50 * time.Millisecond)
	c.Close()
	c.requireProvisioning(t, 0, 2*time.Second)

	// The gauge must equal the number of entries actually sitting in the channel: the
	// entry lost to the cancelled send must not have been counted.
	assert.InDelta(t, 1.0, gauge.value(), 0.001)
}

// countingGauge is a minimal metrics.Gauge that keeps a running total.
type countingGauge struct {
	total atomic.Int64
}

//nolint:ireturn // implements metrics.Gauge; the interface fixes the return type
func (g *countingGauge) With(...string) metrics.Gauge { return g }

func (g *countingGauge) Add(delta float64) { g.total.Add(int64(delta)) }

func (g *countingGauge) Set(value float64) { g.total.Store(int64(value)) }

func (g *countingGauge) value() float64 { return float64(g.total.Load()) }

// countingCounter is a minimal metrics.Counter that keeps a running total.
type countingCounter struct {
	total atomic.Int64
}

func (c *countingCounter) With(...string) metrics.Counter { return c }

func (c *countingCounter) Add(delta float64) { c.total.Add(int64(delta)) }

func (c *countingCounter) value() float64 { return float64(c.total.Load()) }

// TestRecipientDataCacheCountsProvisionFailures checks a failing backend is reported on a
// counter, not only in the log. A log line alone cannot be alerted on, which is what made
// this failure mode invisible in production.
func TestRecipientDataCacheCountsProvisionFailures(t *testing.T) {
	failures := &countingCounter{}
	c := newTestCacheWithMetrics(t, 10, &role.Metrics{CacheLevelGauge: &countingGauge{}, ProvisionFailuresCount: failures},
		func(context.Context) (*driver.RecipientData, error) {
			return nil, errors.New("backend is down")
		})

	_, err := c.RecipientData(t.Context())
	require.Error(t, err)

	// The background loop reports every failed attempt.
	require.Eventually(t, func() bool {
		return failures.value() >= 1
	}, 2*time.Second, 5*time.Millisecond, "the failed provisioning attempt was not counted")
}
