/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
)

// TestNonceRecoversFromChain is the restart story: the manager holds no persisted state, so the first
// allocation after a start re-derives the sequence from the node's pending nonce.
func TestNonceRecoversFromChain(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(42, nil)
	n := NewNonceManager(evm, client.Address{})

	var got uint64
	err := n.WithNonce(t.Context(), func(nonce uint64) error {
		got = nonce

		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(42), got, "the first nonce must come from the chain, not from zero")
}

// TestNonceIsSequentialWithoutRefetching checks the manager does not ask the node for every
// transaction: doing so would hand the same nonce to two callers racing between the query and the
// send.
func TestNonceIsSequentialWithoutRefetching(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(7, nil)
	n := NewNonceManager(evm, client.Address{})

	for want := uint64(7); want < 12; want++ {
		err := n.WithNonce(t.Context(), func(nonce uint64) error {
			assert.Equal(t, want, nonce)

			return nil
		})
		require.NoError(t, err)
	}
	assert.Equal(t, 1, evm.PendingNonceAtCallCount(), "the node is consulted once, not per transaction")
}

// TestNonceIsNotAdvancedOnFailure is the fix for a real bug. The manager used to have a separate
// Reset that walked the whole sequence back and re-derived it from the chain's current mempool view,
// which does not include a nonce a different, still in-flight call already holds and has not
// broadcast yet: one call's failure could hand that nonce to somebody else. WithNonce closes the gap
// by holding the lock for the whole allocate-and-use step, so a failed attempt simply leaves the
// nonce where it was: nothing else could have raced in while use ran, so reusing the same value on
// the next call needs no round trip to the chain to be safe.
func TestNonceIsNotAdvancedOnFailure(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(5, nil)
	n := NewNonceManager(evm, client.Address{})

	err := n.WithNonce(t.Context(), func(nonce uint64) error {
		assert.Equal(t, uint64(5), nonce)

		return assert.AnError
	})
	require.Error(t, err)

	err = n.WithNonce(t.Context(), func(nonce uint64) error {
		assert.Equal(t, uint64(5), nonce, "a failed attempt must not consume the nonce")

		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, evm.PendingNonceAtCallCount(), "recovering from a failure needs no round trip to the chain")

	next, initialized := n.Cached()
	assert.True(t, initialized)
	assert.Equal(t, uint64(6), next, "the sequence advances only past the attempt that actually succeeded")
}

func TestNonceSurfacesRecoveryFailure(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(0, errors.New("node unavailable"))
	n := NewNonceManager(evm, client.Address{})

	called := false
	err := n.WithNonce(t.Context(), func(uint64) error {
		called = true

		return nil
	})
	require.Error(t, err)
	assert.False(t, called, "use must not run when the nonce could not be recovered")

	_, initialized := n.Cached()
	assert.False(t, initialized, "a failed recovery must not leave the manager initialized")
}

// TestNonceConcurrentAllocationIsUnique is the property Ethereum requires: every concurrent caller
// must get a distinct nonce, or all but one transaction is rejected.
func TestNonceConcurrentAllocationIsUnique(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(100, nil)
	n := NewNonceManager(evm, client.Address{})

	const callers = 50
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		got = make(map[uint64]struct{}, callers)
	)
	for range callers {
		wg.Go(func() {
			err := n.WithNonce(t.Context(), func(nonce uint64) error {
				mu.Lock()
				got[nonce] = struct{}{}
				mu.Unlock()

				return nil
			})
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	assert.Len(t, got, callers, "every concurrent caller must receive a distinct nonce")
}

// TestNonceHandlesConcurrentFailuresWithoutDuplicating drives many goroutines against one manager,
// roughly half of them failing, and checks every nonce a goroutine actually succeeded with is unique.
// This is the scenario the old Reset-based design got wrong: a failure from one caller must not be
// able to hand a still in-flight caller's nonce to somebody else.
func TestNonceHandlesConcurrentFailuresWithoutDuplicating(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(0, nil)
	n := NewNonceManager(evm, client.Address{})

	const callers = 200
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		got = make(map[uint64]struct{}, callers)
	)
	for i := range callers {
		wg.Go(func() {
			_ = n.WithNonce(t.Context(), func(nonce uint64) error {
				if i%2 == 0 {
					return assert.AnError
				}
				mu.Lock()
				got[nonce] = struct{}{}
				mu.Unlock()

				return nil
			})
		})
	}
	wg.Wait()

	assert.Len(t, got, callers/2, "every successful attempt must have consumed a distinct nonce")
}

// TestNonceReReadsAfterAnAmbiguousBroadcast covers the one failure that is not proof the chain never
// saw the transaction.
//
// Everything before the broadcast (gas estimation, fee suggestion, signing) either never reaches the
// node or cannot consume a nonce, so those failures leave the sequence provably untouched and the
// manager reuses the nonce with no round trip, which TestNonceIsNotAdvancedOnFailure pins. A broadcast
// that times out or loses its connection is different: the node may have accepted and mined the
// transaction and only the reply was lost, in which case the nonce is spent.
//
// Reusing it there wedges the account for good. Every later send fails "nonce too low", and since that
// is also a failed broadcast, nothing ever re-reads the chain to notice. Only a restart recovers.
func TestNonceReReadsAfterAnAmbiguousBroadcast(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(5, nil)
	n := NewNonceManager(evm, client.Address{})

	// The broadcast fails, but the chain took the transaction: its pending nonce has moved on.
	err := n.WithNonce(t.Context(), func(nonce uint64) error {
		assert.Equal(t, uint64(5), nonce)

		return errors.Wrapf(ErrNonceMayBeConsumed, "the reply was lost")
	})
	require.Error(t, err)
	evm.PendingNonceAtReturns(6, nil)

	err = n.WithNonce(t.Context(), func(nonce uint64) error {
		assert.Equal(t, uint64(6), nonce, "the manager must take the chain's word, not its own stale count")

		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, evm.PendingNonceAtCallCount(), "an ambiguous broadcast has to cost one re-read")

	next, initialized := n.Cached()
	assert.True(t, initialized)
	assert.Equal(t, uint64(7), next)
}

// TestNonceKeepsItsSequenceWhenTheChainDidNotTakeIt is the other half: the same ambiguous failure when
// the transaction really was not accepted must not skip a nonce, or the account is left with a gap
// that stalls every transaction queued behind it.
func TestNonceKeepsItsSequenceWhenTheChainDidNotTakeIt(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(5, nil)
	n := NewNonceManager(evm, client.Address{})

	err := n.WithNonce(t.Context(), func(uint64) error {
		return errors.Wrapf(ErrNonceMayBeConsumed, "the reply was lost")
	})
	require.Error(t, err)

	// The chain never took it, so its pending nonce is unchanged and the re-read returns the same value.
	err = n.WithNonce(t.Context(), func(nonce uint64) error {
		assert.Equal(t, uint64(5), nonce, "a nonce the chain never consumed must be reused, not skipped")

		return nil
	})
	require.NoError(t, err)
}

// fakeClock is a settable time source for the gap checks.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func gapTestManager(evm *mock.EVMClient) (*NonceManager, *fakeClock) {
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	n := NewNonceManager(evm, client.Address{})
	n.now = clock.now

	return n, clock
}

func allocate(t *testing.T, n *NonceManager) uint64 {
	t.Helper()
	var got uint64
	require.NoError(t, n.WithNonce(t.Context(), func(nonce uint64) error {
		got = nonce

		return nil
	}))

	return got
}

// TestNonceGapIsFilled is a transaction that left the node's pool unmined after a successful
// broadcast. Every later nonce queues behind the hole, and nothing failed to make the manager re-read
// the chain, so it used to keep handing out nonces that would never mine.
func TestNonceGapIsFilled(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(5, nil)
	n, clock := gapTestManager(evm)
	for want := uint64(5); want < 9; want++ {
		require.Equal(t, want, allocate(t, n))
	}

	// nonce 5 is gone from the pool: the node keeps answering 5 while the local sequence is at 9
	clock.advance(DefaultNonceGapCheckInterval)
	assert.Equal(t, uint64(9), allocate(t, n), "one sighting is not enough to call it a gap")
	clock.advance(DefaultNonceGapGrace)
	assert.Equal(t, uint64(5), allocate(t, n), "the hole is filled once the gap has lasted the grace period")

	// with 5 filled, 6..9 are minable again and the node's pending nonce moves past them
	evm.PendingNonceAtReturns(10, nil)
	assert.Equal(t, uint64(10), allocate(t, n), "after filling the hole the chain is re-read, not 6 reissued")
}

// TestNonceBriefLagIsNotAGap checks a node that answers below the local sequence once, and then
// catches up, does not make the manager reuse a nonce: that would replace a live transaction.
func TestNonceBriefLagIsNotAGap(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(5, nil)
	n, clock := gapTestManager(evm)
	for want := uint64(5); want < 9; want++ {
		require.Equal(t, want, allocate(t, n))
	}

	clock.advance(DefaultNonceGapCheckInterval)
	assert.Equal(t, uint64(9), allocate(t, n))
	evm.PendingNonceAtReturns(10, nil)
	clock.advance(DefaultNonceGapGrace)
	assert.Equal(t, uint64(10), allocate(t, n))
	evm.PendingNonceAtReturns(5, nil)
	clock.advance(DefaultNonceGapCheckInterval)
	assert.Equal(t, uint64(11), allocate(t, n), "a new sighting starts the grace period over")
}

// TestNonceGapCheckIsThrottled checks the gap check costs at most one read per interval, and that a
// failed read leaves the sequence alone.
func TestNonceGapCheckIsThrottled(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(5, nil)
	n, clock := gapTestManager(evm)
	for range 5 {
		allocate(t, n)
	}
	assert.Equal(t, 1, evm.PendingNonceAtCallCount())

	evm.PendingNonceAtReturns(0, errors.New("connection refused"))
	clock.advance(DefaultNonceGapCheckInterval)
	assert.Equal(t, uint64(10), allocate(t, n))
	assert.Equal(t, uint64(11), allocate(t, n))
	assert.Equal(t, 2, evm.PendingNonceAtCallCount())
}

// TestNonceReReadClearsAGapSighting checks a re-read starts the grace period over. A sighting kept
// from before it would let a later sighting of the same value, from a lagging replica, move the
// sequence back at once and replace a live transaction.
func TestNonceReReadClearsAGapSighting(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(5, nil)
	n, clock := gapTestManager(evm)
	for want := uint64(5); want < 9; want++ {
		require.Equal(t, want, allocate(t, n))
	}

	// a first sighting of 5 below the local 9 starts the grace period
	clock.advance(DefaultNonceGapCheckInterval)
	assert.Equal(t, uint64(9), allocate(t, n))

	// an ambiguous broadcast makes the next allocation re-read the chain, which has moved on
	require.Error(t, n.WithNonce(t.Context(), func(uint64) error {
		return errors.Wrapf(ErrNonceMayBeConsumed, "the reply was lost")
	}))
	evm.PendingNonceAtReturns(10, nil)
	assert.Equal(t, uint64(10), allocate(t, n))

	// past the old sighting's grace, a lagging replica answers 5 again: that is a new first sighting
	evm.PendingNonceAtReturns(5, nil)
	clock.advance(DefaultNonceGapGrace)
	assert.Equal(t, uint64(11), allocate(t, n), "a sighting from before the re-read must not skip the grace period")
}

// hangingPendingNonce makes PendingNonceAt block until its context is done, like a node that never
// answers.
func hangingPendingNonce(evm *mock.EVMClient) {
	evm.PendingNonceAtCalls(func(ctx context.Context, _ client.Address) (uint64, error) {
		<-ctx.Done()

		return 0, ctx.Err()
	})
}

// TestNonceRecoveryReadIsBounded checks a node that never answers the first read fails the allocation
// after the read timeout, instead of holding the lock and blocking every submitter on the account.
func TestNonceRecoveryReadIsBounded(t *testing.T) {
	evm := &mock.EVMClient{}
	hangingPendingNonce(evm)
	n := NewNonceManager(evm, client.Address{})
	n.readTimeout = 20 * time.Millisecond

	err := n.WithNonce(t.Context(), func(uint64) error {
		t.Fatal("use must not run without a nonce")

		return nil
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestNonceGapCheckReadIsBounded checks a gap check against a node that never answers gives up after
// the read timeout and goes ahead with the cached nonce.
func TestNonceGapCheckReadIsBounded(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(5, nil)
	n, clock := gapTestManager(evm)
	n.readTimeout = 20 * time.Millisecond
	require.Equal(t, uint64(5), allocate(t, n))

	hangingPendingNonce(evm)
	clock.advance(DefaultNonceGapCheckInterval)
	assert.Equal(t, uint64(6), allocate(t, n))
	assert.Equal(t, 2, evm.PendingNonceAtCallCount())
}
