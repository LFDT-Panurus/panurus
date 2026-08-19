/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cache

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIdentityCache verifies basic cache functionality and identity retrieval.
func TestIdentityCache(t *testing.T) {
	c := NewIdentityCache(func(context.Context, []byte) (*idriver.IdentityDescriptor, error) {
		return &idriver.IdentityDescriptor{
			Identity:  []byte("hello world"),
			AuditInfo: []byte("audit"),
		}, nil
	}, 100, nil, NewMetrics(&disabled.Provider{}))
	t.Cleanup(c.Close)
	identityDescriptor, err := c.Identity(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, driver.Identity([]byte("hello world")), identityDescriptor.Identity)
	assert.Equal(t, []byte("audit"), identityDescriptor.AuditInfo)

	identityDescriptor, err = c.Identity(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, driver.Identity([]byte("hello world")), identityDescriptor.Identity)
	assert.Equal(t, []byte("audit"), identityDescriptor.AuditInfo)
}

// TestIdentityCacheForRace tests concurrent cache access for thread-safety.
func TestIdentityCacheForRace(t *testing.T) {
	c := NewIdentityCache(func(context.Context, []byte) (*idriver.IdentityDescriptor, error) {
		return &idriver.IdentityDescriptor{
			Identity:  []byte("hello world"),
			AuditInfo: []byte("audit"),
		}, nil
	}, 10000, nil, NewMetrics(&disabled.Provider{}))
	t.Cleanup(c.Close)

	numRoutines := 4
	wg := sync.WaitGroup{}
	wg.Add(numRoutines)
	for range numRoutines {
		go func() {
			defer wg.Done()

			for range 100 {
				id, err := c.Identity(t.Context(), nil)
				assert.NoError(t, err)
				assert.Equal(t, driver.Identity("hello world"), id.Identity)
				assert.Equal(t, []byte("audit"), id.AuditInfo)
			}
		}()
	}
	wg.Wait()
}

// TestFetchIdentityFromBackend verifies backend fetch when audit info doesn't match.
func TestFetchIdentityFromBackend(t *testing.T) {
	expectedIdentity := &idriver.IdentityDescriptor{
		Identity:  []byte("backend identity"),
		AuditInfo: []byte("backend audit"),
	}

	c := NewIdentityCache(func(ctx context.Context, auditInfo []byte) (*idriver.IdentityDescriptor, error) {
		return expectedIdentity, nil
	}, 10, []byte("cache audit"), NewMetrics(&disabled.Provider{}))
	t.Cleanup(c.Close)

	// Call with different audit info to trigger backend fetch
	identityDescriptor, err := c.Identity(context.Background(), []byte("different audit"))
	require.NoError(t, err)
	assert.Equal(t, expectedIdentity.Identity, identityDescriptor.Identity)
	assert.Equal(t, expectedIdentity.AuditInfo, identityDescriptor.AuditInfo)
}

// TestFetchIdentityFromBackendError verifies error propagation from backend failures.
func TestFetchIdentityFromBackendError(t *testing.T) {
	expectedErr := errors.New("backend error")

	c := NewIdentityCache(func(ctx context.Context, auditInfo []byte) (*idriver.IdentityDescriptor, error) {
		return nil, expectedErr
	}, 10, []byte("cache audit"), NewMetrics(&disabled.Provider{}))
	t.Cleanup(c.Close)

	// Call with different audit info to trigger backend fetch
	_, err := c.Identity(context.Background(), []byte("different audit"))
	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
}

// TestFetchIdentityFromCacheTimeout verifies on-demand generation after cache timeout.
func TestFetchIdentityFromCacheTimeout(t *testing.T) {
	var callCount atomic.Int32
	c := NewIdentityCache(func(ctx context.Context, auditInfo []byte) (*idriver.IdentityDescriptor, error) {
		callCount.Add(1)
		// Simulate slow backend - not strictly needed for the test
		// time.Sleep(10 * time.Millisecond)
		return &idriver.IdentityDescriptor{
			Identity:  []byte("timeout identity"),
			AuditInfo: []byte("timeout audit"),
		}, nil
	}, 0, nil, NewMetrics(&disabled.Provider{})) // cache size 0 to force timeout
	t.Cleanup(c.Close)

	// Set short timeout to trigger timeout path
	c.cacheTimeout = 1 * time.Millisecond

	identityDescriptor, err := c.Identity(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, driver.Identity([]byte("timeout identity")), identityDescriptor.Identity)
	assert.Equal(t, []byte("timeout audit"), identityDescriptor.AuditInfo)
	assert.Equal(t, int32(1), callCount.Load())
}

// TestFetchIdentityFromCacheTimeoutError verifies error handling after cache timeout.
func TestFetchIdentityFromCacheTimeoutError(t *testing.T) {
	expectedErr := errors.New("timeout backend error")

	c := NewIdentityCache(func(ctx context.Context, auditInfo []byte) (*idriver.IdentityDescriptor, error) {
		return nil, expectedErr
	}, 0, nil, NewMetrics(&disabled.Provider{}))
	t.Cleanup(c.Close)

	// Set short timeout to trigger timeout path
	c.cacheTimeout = 1 * time.Millisecond

	_, err := c.Identity(context.Background(), nil)
	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
}

// TestProvisionIdentitiesError verifies provisioning retries after errors.
func TestProvisionIdentitiesError(t *testing.T) {
	var callCount atomic.Int32
	maxCalls := int32(3)

	c := NewIdentityCache(func(ctx context.Context, auditInfo []byte) (*idriver.IdentityDescriptor, error) {
		// Fail 3 times then succeed
		current := callCount.Add(1)
		if current <= maxCalls {
			return nil, errors.New("provision error")
		}

		return &idriver.IdentityDescriptor{
			Identity:  []byte("success identity"),
			AuditInfo: []byte("success audit"),
		}, nil
	}, 10, nil, NewMetrics(&disabled.Provider{}))
	defer c.Close()

	// Trigger provisioning and wait for success
	assert.Eventually(t, func() bool {
		_, err := c.Identity(context.Background(), nil)

		return err == nil
	}, time.Second, 10*time.Millisecond)

	// Wait a bit for provisioning to attempt multiple times
	time.Sleep(50 * time.Millisecond)

	// Verify that provisioning continued after errors
	assert.Greater(t, callCount.Load(), maxCalls)
}

// TestFetchIdentityFromCacheNilEntry verifies backend fallback for nil cache entries.
func TestFetchIdentityFromCacheNilEntry(t *testing.T) {
	var backendCallCount atomic.Int32

	c := NewIdentityCache(func(ctx context.Context, auditInfo []byte) (*idriver.IdentityDescriptor, error) {
		backendCallCount.Add(1)

		return &idriver.IdentityDescriptor{
			Identity:  []byte("backend fallback"),
			AuditInfo: []byte("backend audit"),
		}, nil
	}, 10, nil, NewMetrics(&disabled.Provider{}))
	t.Cleanup(c.Close)

	// Pre-populate the cache with nil before calling Identity()
	// Since cache is buffered, this completes immediately
	c.cache <- nil

	// Small delay to ensure the nil is in the buffer before Identity() reads
	time.Sleep(10 * time.Millisecond)

	identityDescriptor, err := c.Identity(context.Background(), nil)
	require.NoError(t, err)
	assert.Eventually(t, func() bool {
		return backendCallCount.Load() > 0
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, driver.Identity([]byte("backend fallback")), identityDescriptor.Identity)
}

// TestIdentityCache_Close verifies that Close() stops the background provisioning.
func TestIdentityCache_Close(t *testing.T) {
	var callCount atomic.Int32
	// Backend function that just increments a counter
	backend := func(ctx context.Context, auditInfo []byte) (*idriver.IdentityDescriptor, error) {
		callCount.Add(1)

		return &idriver.IdentityDescriptor{
			Identity:  []byte("id"),
			AuditInfo: []byte("ai"),
		}, nil
	}

	c := NewIdentityCache(backend, 10, nil, NewMetrics(&disabled.Provider{}))
	t.Cleanup(c.Close)
	// Set a very short timeout so we don't wait long if the cache is empty
	c.cacheTimeout = 1 * time.Millisecond

	// Trigger provisioning
	_, err := c.Identity(context.Background(), nil)
	require.NoError(t, err)

	// Wait for some provisioning to happen
	assert.Eventually(t, func() bool {
		return callCount.Load() > 0
	}, time.Second, 10*time.Millisecond)

	// Close the cache
	c.Close()

	// Close must stop the background provisioning goroutine. Assert on the goroutine
	// actually exiting rather than on a fixed bound of extra iterations: the fast
	// provisioning loop (1ms timeout) may complete an arbitrary number of in-flight
	// iterations before observing the stop signal on a loaded/slow runner (notably
	// under -race), so any fixed margin is inherently timing-dependent and flaky.
	assert.Eventually(t, func() bool {
		return provisioningGoroutines() == 0
	}, time.Second, 10*time.Millisecond)

	// Once the goroutine has exited, the call count must be stable: no further calls
	// happen after provisioning has stopped.
	countAfterStop := callCount.Load()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, countAfterStop, callCount.Load())
}

// provisionGoroutineName is the symbol appearing in the stack trace of the background
// provisioning goroutine.
const provisionGoroutineName = "cache.(*IdentityCache).provisionIdentities"

// provisioningGoroutines counts the running provisioning goroutines.
func provisioningGoroutines() int {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Count(string(buf[:n]), provisionGoroutineName)
		}
		buf = make([]byte, 2*len(buf))
	}
}

// TestIdentityCache_CloseRacesFirstUse checks Close is safe to call concurrently with
// the first Identity call, and that the cancellation is never missed.
//
// The cancel function used to be assigned inside once.Do, so Close read it without
// synchronisation: a data race, and worse, a Close that observed the old nil value
// silently skipped the cancellation and leaked the goroutine. Building the context in
// the constructor makes the field write-once.
func TestIdentityCache_CloseRacesFirstUse(t *testing.T) {
	require.Eventually(t, func() bool {
		return provisioningGoroutines() == 0
	}, 5*time.Second, 10*time.Millisecond, "a previous test left a provisioning goroutine behind")

	for range 300 {
		c := NewIdentityCache(func(context.Context, []byte) (*idriver.IdentityDescriptor, error) {
			return &idriver.IdentityDescriptor{Identity: []byte("id")}, nil
		}, 4, nil, NewMetrics(&disabled.Provider{}))

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = c.Identity(context.Background(), nil)
		}()
		go func() {
			defer wg.Done()
			c.Close()
		}()
		wg.Wait()
	}

	// Every cache was closed, so no provisioning goroutine may survive.
	require.Eventually(t, func() bool {
		return provisioningGoroutines() == 0
	}, 5*time.Second, 10*time.Millisecond, "Close missed the cancellation and leaked a goroutine")
}

// countingCounter is a minimal metrics.Counter that keeps a running total.
type countingCounter struct {
	total atomic.Int64
}

func (c *countingCounter) With(...string) metrics.Counter { return c }

func (c *countingCounter) Add(delta float64) { c.total.Add(int64(delta)) }

func (c *countingCounter) value() float64 { return float64(c.total.Load()) }

// TestIdentityCache_CountsProvisionFailures checks a failing key manager is reported on a
// counter, not only in the log, so the condition can be alerted on.
func TestIdentityCache_CountsProvisionFailures(t *testing.T) {
	failures := &countingCounter{}
	c := NewIdentityCache(func(context.Context, []byte) (*idriver.IdentityDescriptor, error) {
		return nil, errors.New("key manager is down")
	}, 10, nil, &Metrics{CacheLevelGauge: &noopGauge{}, ProvisionFailuresCount: failures})
	t.Cleanup(c.Close)

	_, err := c.Identity(context.Background(), nil)
	require.Error(t, err)

	require.Eventually(t, func() bool {
		return failures.value() >= 1
	}, 2*time.Second, 5*time.Millisecond, "the failed provisioning attempt was not counted")
}

// noopGauge is a metrics.Gauge that discards everything.
type noopGauge struct{}

func (g *noopGauge) With(...string) metrics.Gauge { return g }

func (g *noopGauge) Add(float64) {}

func (g *noopGauge) Set(float64) {}
