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
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

func newRecipientLogger() logging.Logger {
	return logging.MustGetLogger("cache_test")
}

// TestRecipientData_BoundsConcurrentGenerations asserts that a burst of callers never puts more than
// MaxConcurrentRecipientDataGenerations generations in flight at once. Without the bound, every
// caller in the burst would generate simultaneously.
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

	c := role.NewRecipientDataProvider(newRecipientLogger(), backend, nil)

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

// TestRecipientData_BoundIsSharedAcrossWallets asserts the bound applies to the node rather than to
// each wallet: two providers sharing one semaphore must not exceed the limit between them. A
// per-wallet bound would scale with the number of wallets and stop bounding anything.
func TestRecipientData_BoundIsSharedAcrossWallets(t *testing.T) {
	var inFlight, peak atomic.Int64
	release := make(chan struct{})
	entered := make(chan struct{}, role.MaxConcurrentRecipientDataGenerations*4)

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

	shared := semaphore.NewWeighted(role.MaxConcurrentRecipientDataGenerations)
	first := role.NewRecipientDataProvider(newRecipientLogger(), backend, shared)
	second := role.NewRecipientDataProvider(newRecipientLogger(), backend, shared)

	var wg sync.WaitGroup
	for i := range role.MaxConcurrentRecipientDataGenerations * 2 {
		provider := first
		if i%2 == 1 {
			provider = second
		}
		wg.Go(func() {
			_, err := provider.RecipientData(context.Background())
			require.NoError(t, err)
		})
	}

	for range role.MaxConcurrentRecipientDataGenerations {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for generations to start")
		}
	}
	select {
	case <-entered:
		t.Fatal("the two wallets together exceeded the shared limit")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	wg.Wait()
	require.LessOrEqual(t, peak.Load(), int64(role.MaxConcurrentRecipientDataGenerations),
		"concurrent generations across wallets exceeded the shared limit")
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
	c := role.NewRecipientDataProvider(newRecipientLogger(), backend, nil)

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

	// The caller must still be queued: no slot is free.
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

// TestRecipientData_GeneratesEveryCall asserts the provider does not cache: each call reaches the
// backend, so no identity is produced (and no binding row written) unless a caller asked for it.
func TestRecipientData_GeneratesEveryCall(t *testing.T) {
	var calls atomic.Int64
	backend := func(ctx context.Context) (*driver.RecipientData, error) {
		calls.Add(1)

		return &driver.RecipientData{Identity: []byte("id")}, nil
	}
	c := role.NewRecipientDataProvider(newRecipientLogger(), backend, nil)

	for range 5 {
		rd, err := c.RecipientData(context.Background())
		require.NoError(t, err)
		require.Equal(t, driver.Identity("id"), rd.Identity)
	}
	require.Equal(t, int64(5), calls.Load(), "expected one generation per call and no pre-provisioning")
}

// TestRecipientData_ReleasesSlotOnBackendFailure asserts a failing generation frees its slot, so a
// backend that errors cannot permanently exhaust the bound.
func TestRecipientData_ReleasesSlotOnBackendFailure(t *testing.T) {
	var calls atomic.Int64
	backend := func(ctx context.Context) (*driver.RecipientData, error) {
		calls.Add(1)

		return nil, errors.New("backend is down")
	}
	c := role.NewRecipientDataProvider(newRecipientLogger(), backend, nil)

	// More failures than there are slots: if a failed generation leaked its slot, the calls after
	// the first MaxConcurrentRecipientDataGenerations would block forever.
	for range role.MaxConcurrentRecipientDataGenerations * 3 {
		_, err := c.RecipientData(context.Background())
		require.Error(t, err)
	}
	require.Equal(t, int64(role.MaxConcurrentRecipientDataGenerations*3), calls.Load())
}
