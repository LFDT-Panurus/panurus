/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newLimitedWallet returns a key manager for w's wallet with the given options.
func newLimitedWallet(t *testing.T, w *testWallet, opts ...KeyManagerOption) *KeyManager {
	t.Helper()
	conf := &Config{Version: ProtocolVersionV1, Name: "alice", IPK: w.ipk, Signer: w.signer}
	km, err := NewKeyManager(conf, w.keystore, w.store, opts...)
	require.NoError(t, err)
	t.Cleanup(km.Close)

	return km
}

// TestProverLimiterParallelIdentities generates identities and signs with them
// from more goroutines than the limiter lets through; every presentation must
// verify and no limiter slot may leak.
func TestProverLimiterParallelIdentities(t *testing.T) {
	w := newTestWallet(t, "alice")
	km := newLimitedWallet(t, w, WithProverLimiter(NewProverLimiter(3)))

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
	assert.Empty(t, km.limiter.slots, "limiter slots leaked")
}

// TestProverLimiterShared checks that a limiter bounds the key managers
// sharing it: with its one slot taken, the other key manager waits.
func TestProverLimiterShared(t *testing.T) {
	w := newTestWallet(t, "alice")
	limiter := NewProverLimiter(1)
	newLimitedWallet(t, w, WithProverLimiter(limiter))
	km2 := newLimitedWallet(t, w, WithProverLimiter(limiter))

	require.NoError(t, limiter.acquire(t.Context(), nil))
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, err := km2.Identity(ctx, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded, "the shared limiter did not bound the second key manager")
	limiter.release()
	_, err = km2.Identity(t.Context(), nil)
	require.NoError(t, err)
}

// TestProverLimiterWaitersReleased checks that a caller waiting for the
// limiter is released by the cancellation of its context and by Close.
func TestProverLimiterWaitersReleased(t *testing.T) {
	w := newTestWallet(t, "alice")
	limiter := NewProverLimiter(1)
	km := newLimitedWallet(t, w, WithProverLimiter(limiter))
	require.NoError(t, limiter.acquire(t.Context(), nil))
	defer limiter.release()

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
}

// TestKeyManagerCloseWaitsForPresentations closes a key manager while several
// presentations run at once on its user state: Close waits for all of them,
// each one either completes with a valid identity or fails as closed, and
// none runs on a released state.
func TestKeyManagerCloseWaitsForPresentations(t *testing.T) {
	w := newTestWallet(t, "alice")
	km := newLimitedWallet(t, w, WithProverLimiter(NewProverLimiter(4)))

	const workers = 4
	start := make(chan struct{})
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			<-start
			d, err := km.Identity(t.Context(), nil)
			if err != nil {
				errs[i] = err

				return
			}
			sigma, err := d.Signer.Sign([]byte("m"))
			if err == nil {
				err = d.Verifier.Verify([]byte("m"), sigma)
			}
			if err != nil && !km.isClosed() {
				errs[i] = err
			}
		})
	}
	close(start)
	time.Sleep(30 * time.Millisecond)
	km.Close()
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			require.ErrorIs(t, err, errClosed)
		}
	}
	_, err := km.Identity(t.Context(), nil)
	require.Error(t, err, "identity after Close")
	assert.Empty(t, km.limiter.slots, "limiter slots leaked")
	km.Close()
}

// TestDefaultProverLimit checks that DefaultProverLimit is min(GOMAXPROCS, 4)
// and that NewProverLimiter(n <= 0) uses it. Not parallel: it sets GOMAXPROCS.
func TestDefaultProverLimit(t *testing.T) {
	prev := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(prev) })
	assert.Equal(t, 4, maxDefaultProvers)
	for _, tc := range []struct {
		procs, want int
	}{
		{1, 1},
		{2, 2},
		{3, 3},
		{4, 4},
		{5, 4},
		{8, 4},
		{64, 4},
	} {
		runtime.GOMAXPROCS(tc.procs)
		assert.Equal(t, tc.want, DefaultProverLimit(), "GOMAXPROCS=%d", tc.procs)
		for _, n := range []int{0, -1} {
			assert.Equal(t, tc.want, cap(NewProverLimiter(n).slots), "GOMAXPROCS=%d, NewProverLimiter(%d)", tc.procs, n)
		}
		assert.Equal(t, 7, cap(NewProverLimiter(7).slots), "GOMAXPROCS=%d, NewProverLimiter(7)", tc.procs)
	}
}

// BenchmarkKmIdentityParallel benchmarks identity generation from parallel
// goroutines with a limiter of DefaultProverLimit.
func BenchmarkKmIdentityParallel(b *testing.B) {
	w := newTestWallet(b, "alice")
	conf := &Config{Version: ProtocolVersionV1, Name: "alice", IPK: w.ipk, Signer: w.signer}
	km, err := NewKeyManager(conf, w.keystore, w.store)
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
