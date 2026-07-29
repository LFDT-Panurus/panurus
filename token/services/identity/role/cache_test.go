/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/require"
)

func newCacheMetrics() *role.Metrics {
	return role.NewMetrics(&disabled.Provider{})
}

func TestNewRecipientDataCache_ClampsSize(t *testing.T) {
	backend := func(context.Context) (*driver.RecipientData, error) {
		return &driver.RecipientData{Identity: []byte("id")}, nil
	}

	for _, tc := range []struct {
		name     string
		size     int
		expected int
	}{
		{"negative is clamped to zero", -10, 0},
		{"zero is preserved", 0, 0},
		{"below the maximum is preserved", 7, 7},
		{"at the maximum is preserved", role.MaxRecipientDataCacheSize, role.MaxRecipientDataCacheSize},
		{"above the maximum is clamped", role.MaxRecipientDataCacheSize + 1, role.MaxRecipientDataCacheSize},
		{"far above the maximum is clamped", 100_000_000, role.MaxRecipientDataCacheSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := role.NewRecipientDataCache(logging.MustGetLogger("cache_test"), backend, tc.size, newCacheMetrics())
			require.Equal(t, tc.expected, c.Capacity())
		})
	}
}

// TestRecipientData_BoundsConcurrentGenerations asserts that a burst of cache-missing callers never
// puts more than MaxConcurrentRecipientDataGenerations generations in flight at once. Without the
// bound, every caller in the burst would generate simultaneously.
func TestRecipientData_BoundsConcurrentGenerations(t *testing.T) {
	const callers = role.MaxConcurrentRecipientDataGenerations * 4

	var inFlight, peak atomic.Int64
	release := make(chan struct{})
	entered := make(chan struct{}, callers)

	backend := func(ctx context.Context) (*driver.RecipientData, error) {
		current := inFlight.Add(1)
		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		inFlight.Add(-1)

		return &driver.RecipientData{Identity: []byte("id")}, nil
	}

	// Size 0 keeps the background provisioner out of the way, so every caller must generate.
	c := role.NewRecipientDataCache(logging.MustGetLogger("cache_test"), backend, 0, newCacheMetrics())

	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			_, err := c.RecipientData(context.Background())
			require.NoError(t, err)
		})
	}

	// Wait until the admitted callers are all parked inside the backend, then confirm no more than
	// the limit got in.
	for range role.MaxConcurrentRecipientDataGenerations {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for generations to start")
		}
	}
	require.Equal(t, int64(role.MaxConcurrentRecipientDataGenerations), inFlight.Load(),
		"expected exactly the limit to be admitted")
	select {
	case <-entered:
		t.Fatal("a caller beyond the limit was admitted")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	wg.Wait()
	require.LessOrEqual(t, peak.Load(), int64(role.MaxConcurrentRecipientDataGenerations),
		"concurrent generations exceeded the limit")
}

// TestRecipientData_QueuedCallerHonoursContextCancellation asserts a caller waiting for a
// generation slot is released when its context is cancelled, rather than blocking indefinitely.
func TestRecipientData_QueuedCallerHonoursContextCancellation(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{}, role.MaxConcurrentRecipientDataGenerations)

	backend := func(ctx context.Context) (*driver.RecipientData, error) {
		entered <- struct{}{}
		<-release

		return &driver.RecipientData{Identity: []byte("id")}, nil
	}
	c := role.NewRecipientDataCache(logging.MustGetLogger("cache_test"), backend, 0, newCacheMetrics())

	// Occupy every slot.
	for range role.MaxConcurrentRecipientDataGenerations {
		go func() { _, _ = c.RecipientData(context.Background()) }()
	}
	for range role.MaxConcurrentRecipientDataGenerations {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for generations to start")
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.RecipientData(ctx)
		done <- err
	}()

	// The caller must still be queued: no slot is free and nothing has been cached.
	select {
	case <-done:
		t.Fatal("queued caller returned before its context was cancelled")
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		require.Error(t, err, "a cancelled caller must report an error rather than a recipient")
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled caller was not released")
	}
}

// TestProvisionIdentities_BacksOffOnFailure asserts the background provisioning loop does not spin
// on a persistently failing backend. Before the backoff, this loop retried with no pause at all and
// burned a core per wallet.
func TestProvisionIdentities_BacksOffOnFailure(t *testing.T) {
	var calls atomic.Int64
	backend := func(ctx context.Context) (*driver.RecipientData, error) {
		calls.Add(1)

		return nil, errors.New("backend is down")
	}

	// A non-zero size starts the background provisioner.
	c := role.NewRecipientDataCache(logging.MustGetLogger("cache_test"), backend, 1, newCacheMetrics())

	// RecipientData starts the provisioner and, since the backend fails, returns an error itself.
	_, err := c.RecipientData(context.Background())
	require.Error(t, err)

	time.Sleep(250 * time.Millisecond)

	// With exponential backoff from provisionRetryBackoff (50ms) the loop can only have attempted a
	// handful of times in this window. An unthrottled loop would reach many thousands.
	require.Less(t, calls.Load(), int64(50), "provisioning loop is spinning instead of backing off")
}

// TestRecipientData_ServesFromCacheWithoutGenerating asserts a pre-provisioned entry is returned
// without touching the backend on the request path.
func TestRecipientData_ServesFromCacheWithoutGenerating(t *testing.T) {
	var calls atomic.Int64
	backend := func(ctx context.Context) (*driver.RecipientData, error) {
		calls.Add(1)

		return &driver.RecipientData{Identity: []byte("id")}, nil
	}
	c := role.NewRecipientDataCache(logging.MustGetLogger("cache_test"), backend, 4, newCacheMetrics())

	rd, err := c.RecipientData(context.Background())
	require.NoError(t, err)
	require.Equal(t, driver.Identity("id"), rd.Identity)
}
