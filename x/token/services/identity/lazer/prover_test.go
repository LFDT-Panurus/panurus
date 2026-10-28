/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPoolWallet returns a key manager for w's wallet with the given options.
func newPoolWallet(t *testing.T, w *testWallet, opts ...KeyManagerOption) *KeyManager {
	t.Helper()
	conf := &Config{Version: ProtocolVersionV1, Name: "alice", IPK: w.ipk, Signer: w.signer}
	km, err := NewKeyManager(conf, w.keystore, w.store, opts...)
	require.NoError(t, err)
	t.Cleanup(km.Close)

	return km
}

// TestProverPoolParallelIdentities generates identities and signs with them
// from several goroutines at once, more than the limiter lets through; every
// presentation must verify.
func TestProverPoolParallelIdentities(t *testing.T) {
	w := newTestWallet(t, "alice")
	km := newPoolWallet(t, w, WithProverLimiter(NewProverLimiter(3)), WithIdleProvers(2))

	const workers = 6
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			for range 2 {
				d, err := km.Identity(t.Context(), nil)
				if err != nil {
					errs[i] = err

					return
				}
				sigma, err := d.Signer.Sign([]byte("m"))
				if err != nil {
					errs[i] = err

					return
				}
				if err := d.Verifier.Verify([]byte("m"), sigma); err != nil {
					errs[i] = err

					return
				}
			}
		})
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	km.provers.mu.Lock()
	assert.LessOrEqual(t, len(km.provers.idle), 2, "more idle states than configured")
	km.provers.mu.Unlock()
	assert.Empty(t, km.provers.limiter.slots, "limiter slots leaked")
}

// TestProverPoolSharedLimiter checks that a limiter bounds the key managers
// sharing it: with one slot taken, the other key manager waits.
func TestProverPoolSharedLimiter(t *testing.T) {
	w := newTestWallet(t, "alice")
	limiter := NewProverLimiter(1)
	km1 := newPoolWallet(t, w, WithProverLimiter(limiter))
	km2 := newPoolWallet(t, w, WithProverLimiter(limiter))

	u, err := km1.provers.get(t.Context())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, err = km2.Identity(ctx, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded, "the shared limiter did not bound the second key manager")
	km1.provers.put(u)
	_, err = km2.Identity(t.Context(), nil)
	require.NoError(t, err)
}

// TestProverPoolWaitersReleased checks that a caller waiting for a prover is
// released by the cancellation of its context and by Close.
func TestProverPoolWaitersReleased(t *testing.T) {
	w := newTestWallet(t, "alice")
	km := newPoolWallet(t, w, WithProverLimiter(NewProverLimiter(1)))
	u, err := km.provers.get(t.Context())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	res := make(chan error, 1)
	go func() {
		_, err := km.Identity(ctx, nil)
		res <- err
	}()
	cancel()
	select {
	case err := <-res:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("waiter not released by its context")
	}

	go func() {
		_, err := km.Identity(t.Context(), nil)
		res <- err
	}()
	time.Sleep(50 * time.Millisecond)
	km.Close()
	select {
	case err := <-res:
		require.ErrorIs(t, err, errClosed)
	case <-time.After(10 * time.Second):
		t.Fatal("waiter not released by Close")
	}

	// a state put back after Close is cleared, not pooled, and its slot freed
	km.provers.put(u)
	km.provers.mu.Lock()
	assert.Empty(t, km.provers.idle)
	km.provers.mu.Unlock()
	assert.Empty(t, km.provers.limiter.slots)
	_, err = km.provers.get(t.Context())
	require.ErrorIs(t, err, errClosed)
}

// TestProverPoolNoIdle checks a key manager that keeps no prover state.
func TestProverPoolNoIdle(t *testing.T) {
	w := newTestWallet(t, "alice")
	km := newPoolWallet(t, w, WithIdleProvers(0))
	km.provers.mu.Lock()
	assert.Empty(t, km.provers.idle)
	km.provers.mu.Unlock()
	d, err := km.Identity(t.Context(), nil)
	require.NoError(t, err)
	sigma, err := d.Signer.Sign([]byte("m"))
	require.NoError(t, err)
	require.NoError(t, d.Verifier.Verify([]byte("m"), sigma))
	km.provers.mu.Lock()
	assert.Empty(t, km.provers.idle)
	km.provers.mu.Unlock()
}

// BenchmarkKmIdentityParallel benchmarks identity generation from parallel
// goroutines with a limiter of DefaultProverLimit.
func BenchmarkKmIdentityParallel(b *testing.B) {
	w := newTestWallet(b, "alice")
	conf := &Config{Version: ProtocolVersionV1, Name: "alice", IPK: w.ipk, Signer: w.signer}
	km, err := NewKeyManager(conf, w.keystore, w.store, WithIdleProvers(DefaultProverLimit()))
	require.NoError(b, err)
	b.Cleanup(km.Close)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := km.Identity(context.Background(), nil); err != nil {
				b.Error(err)

				return
			}
		}
	})
}
