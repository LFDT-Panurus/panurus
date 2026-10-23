/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"context"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	commonmetrics "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contendedStore is a sherdlock.TokenDB over a fixed wallet with lock rows, modelling the two
// query semantics this recovery depends on:
//
//   - SpendableTokensIteratorBy applies the anti-join (#2395 mechanism 3): a token locked by
//     *anyone*, this caller included, is hidden from the candidate scan. That is what makes a
//     released lock invisible to a snapshot taken while it was held, which is the whole
//     situation under test.
//   - HasEnoughSpendableTokens ignores locks, as the real query does, so the wallet's balance
//     keeps reporting that the request can be covered throughout.
//
// Unlike the hand-rolled fetcher fakes elsewhere in this package it is a *store*, so the
// tests below can drive the real cachedFetcher and mixedFetcher over it and assert what those
// two strategies actually do.
type contendedStore struct {
	mu     sync.Mutex
	tokens []*token2.UnspentTokenInWallet
	locks  map[token2.ID]string
	scans  int
	// onScan runs after the n-th scan has read its rows, so a test can change the world
	// between the snapshot a fetcher took and the next read of it - releasing a lock, say.
	onScan func(scan int)
}

func newContendedStore(tokens ...*token2.UnspentTokenInWallet) *contendedStore {
	return &contendedStore{tokens: tokens, locks: map[token2.ID]string{}}
}

// SpendableTokensIteratorBy returns the tokens no one holds a lock on, ascending by amount as
// the real ORDER BY guarantees (the caller passes them in that order). An empty walletID and
// type match everything, which is how the eager fetcher reads the whole store.
func (s *contendedStore) SpendableTokensIteratorBy(_ context.Context, walletID string, typ token2.Type) (driver.SpendableTokensIterator, error) {
	s.mu.Lock()
	s.scans++
	scan := s.scans
	out := make([]*token2.UnspentTokenInWallet, 0, len(s.tokens))
	for _, t := range s.tokens {
		if _, locked := s.locks[t.Id]; locked {
			continue
		}
		if (walletID != "" && t.WalletID != walletID) || (typ != "" && t.Type != typ) {
			continue
		}
		out = append(out, t)
	}
	onScan := s.onScan
	s.mu.Unlock()

	if onScan != nil {
		onScan(scan)
	}

	return &sliceIterator{items: out}, nil
}

// HasEnoughSpendableTokens sums the wallet's whole balance, locks and all: a lock is a row in
// TokenLocks and leaves the token spendable in Tokens until the transaction commits.
func (s *contendedStore) HasEnoughSpendableTokens(_ context.Context, walletID string, typ token2.Type, target *big.Int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	total := big.NewInt(0)
	for _, t := range s.tokens {
		if t.WalletID != walletID || t.Type != typ {
			continue
		}
		q, err := token2.ToQuantity(t.Quantity, 64)
		if err != nil {
			return false, err
		}
		total.Add(total, q.ToBigInt())
	}

	return total.Cmp(target) >= 0, nil
}

func (s *contendedStore) lock(id token2.ID, holder string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.locks[id] = holder
}

func (s *contendedStore) release(id token2.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.locks, id)
}

func (s *contendedStore) scanCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.scans
}

// storeLocker is the sherdlock.TokenLocker over a contendedStore: a claim on a token someone
// already holds - including this very selector, whose own locks the anti-join hides from later
// scans - loses the race with driver.ErrTokenAlreadyLocked, exactly as the conditional lock
// insert does.
type storeLocker struct {
	store *contendedStore
	txID  string
}

func (l *storeLocker) TryLock(_ context.Context, id *token2.ID, _ string) (bool, error) {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()

	if holder, locked := l.store.locks[*id]; locked {
		return false, errors.Wrapf(dbdriver.ErrTokenAlreadyLocked, "token %s is locked by %s", id, holder)
	}
	l.store.locks[*id] = l.txID

	return true, nil
}

func (l *storeLocker) UnlockAll(context.Context) error { return nil }

// invalidationMetrics hands out a countingCounter for cache_invalidations_total and discards
// the rest, so a test can assert that the forced refresh was actually what drove the recovery.
func invalidationMetrics() (*sherdlock.Metrics, *countingCounter) {
	invalidations := &countingCounter{}
	p := &mocks.FakeProvider{}
	p.NewCounterStub = func(opts commonmetrics.CounterOpts) commonmetrics.Counter {
		if opts.Name == "cache_invalidations_total" {
			return invalidations
		}

		return &countingCounter{}
	}
	p.NewHistogramStub = func(commonmetrics.HistogramOpts) commonmetrics.Histogram {
		return discardHistogram{}
	}

	return sherdlock.NewMetrics(p), invalidations
}

// aliceToken builds one of alice's USD tokens.
func aliceToken(txID, quantity string) *token2.UnspentTokenInWallet {
	return &token2.UnspentTokenInWallet{
		Id:       token2.ID{TxId: txID, Index: 0},
		WalletID: "alice",
		Type:     "USD",
		Quantity: quantity,
	}
}

// TestEagerFetcher_RecoversWithinTheRetryBudgetWhenALockIsReleased is the confirmation asked
// for on #2410: that the caching strategies actually recover from the case the immediate-retry
// budget exists for.
//
// The budget is spent waiting for another process to release a lock. But the candidate query
// excludes locked tokens (#2395 mechanism 3), so a token released after the eager snapshot was
// taken is *not in that snapshot*, and re-reading the snapshot can never show it. With a
// freshness interval long relative to the retry loop - the loop has no sleeps at all - every
// refetch used to hand back the identical empty set, and a wallet whose only token had just
// been freed failed with SelectorSufficientButLockedFunds while sitting on the funds.
//
// Telling the candidate source it is behind the store before refetching is what closes that,
// and nothing else in this test can: the fetcher is built with an hour-long freshness interval
// and a query budget it cannot reach, so neither staleness nor overuse can refresh the cache.
func TestEagerFetcher_RecoversWithinTheRetryBudgetWhenALockIsReleased(t *testing.T) {
	contended := aliceToken("contended", "100")
	store := newContendedStore(contended)
	store.lock(contended.Id, "another-process")
	// The release lands after the snapshot that hid it was taken, which is precisely the
	// ordering that makes the refetch necessary and the snapshot useless.
	store.onScan = func(scan int) {
		if scan == 1 {
			store.release(contended.Id)
		}
	}

	metrics, invalidations := invalidationMetrics()
	fetcher := sherdlock.NewCachedFetcher(store, 0, time.Hour, 1_000_000)
	s := sherdlock.NewSelector(sherdlock.Logger(), fetcher, &storeLocker{store: store, txID: "mine"}, 64, metrics)

	ids, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "USD")

	require.NoError(t, err,
		"the lock was released while the selection was still retrying, so the request can be covered")
	require.Len(t, ids, 1)
	assert.Equal(t, contended.Id, *ids[0])
	assert.Equal(t, "100", sum.Decimal())

	assert.Positive(t, invalidations.Total(),
		"the recovery must come from the candidate source being told it is behind the store; nothing "+
			"else in this test can refresh it")
	assert.LessOrEqual(t, store.scanCount(), 2,
		"one scan for the snapshot that hid the locked token and one after the invalidation is all "+
			"this needs: more means the refetch is re-reading the store without cause")
}

// TestMixedFetcher_RecoversWithinTheRetryBudgetWhenALockIsReleased is the same scenario on the
// default (mixed) strategy. Mixed already recovered here before the fix, by a different route:
// its eager snapshot held nothing at all for the wallet, so the peek failed and it fell through
// to the lazy half, which reads the store. That fallback is exactly what
// TestMixedFetcher_RecoversWhenItsStaleSnapshotIsNotEmpty takes away, so this case is pinned
// separately - it is the half of the strategy that was never broken, and a change that recovers
// the other half by disabling this one would otherwise look like an improvement.
func TestMixedFetcher_RecoversWithinTheRetryBudgetWhenALockIsReleased(t *testing.T) {
	contended := aliceToken("contended", "100")
	store := newContendedStore(contended)
	store.lock(contended.Id, "another-process")
	store.onScan = func(scan int) {
		if scan == 1 {
			store.release(contended.Id)
		}
	}

	metrics, _ := invalidationMetrics()
	fetcher := sherdlock.NewMixedFetcher(store, metrics, 0, time.Hour, 1_000_000)
	s := sherdlock.NewSelector(sherdlock.Logger(), fetcher, &storeLocker{store: store, txID: "mine"}, 64, metrics)

	ids, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "USD")

	require.NoError(t, err, "the mixed strategy must recover from a released lock too")
	require.Len(t, ids, 1)
	assert.Equal(t, contended.Id, *ids[0])
	assert.Equal(t, "100", sum.Decimal())
}

// TestMixedFetcher_RecoversWhenItsStaleSnapshotIsNotEmpty is the mixed case that the lazy
// fallback does *not* cover, and the one that was broken: the eager snapshot is not empty for
// the wallet, so the peek succeeds and the lazy half is never consulted. The snapshot's one
// token is the dust this call has already locked itself, which the store hides from every
// later scan while the cache goes on offering it, so each refetch re-proposed a token the
// selector already held, lost the race against its own lock, and spent the retry budget doing
// it - with the 100 that had just been freed never once considered.
func TestMixedFetcher_RecoversWhenItsStaleSnapshotIsNotEmpty(t *testing.T) {
	dust := aliceToken("dust", "10")
	contended := aliceToken("contended", "100")
	store := newContendedStore(dust, contended)
	store.lock(contended.Id, "another-process")
	store.onScan = func(scan int) {
		if scan == 1 {
			store.release(contended.Id)
		}
	}

	metrics, invalidations := invalidationMetrics()
	fetcher := sherdlock.NewMixedFetcher(store, metrics, 0, time.Hour, 1_000_000)
	s := sherdlock.NewSelector(sherdlock.Logger(), fetcher, &storeLocker{store: store, txID: "mine"}, 64, metrics)

	ids, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "USD")

	require.NoError(t, err,
		"the dust alone cannot cover the request, but the released token can, and a non-empty stale "+
			"snapshot must not hide it")
	// Select returns its selection from a set, in no particular order, so compare the
	// elements rather than the order they were locked in.
	assert.ElementsMatch(t, []token2.ID{dust.Id, contended.Id}, idSet(ids),
		"the dust was locked first and stays selected; the released token is what completes the request")
	assert.Equal(t, "110", sum.Decimal())
	assert.Positive(t, invalidations.Total(),
		"with a non-empty snapshot the lazy half is never reached, so only the invalidation can "+
			"surface the released token")
}

// TestLazyFetcher_NeedsNoInvalidationToRecover pins the other end of the same rule: the lazy
// strategy reads through on every call, so it recovers by construction and must not be asked
// to invalidate anything - it implements no CacheInvalidator, and a refetch that tried to
// would be counted as a forced refresh that never happens.
func TestLazyFetcher_NeedsNoInvalidationToRecover(t *testing.T) {
	contended := aliceToken("contended", "100")
	store := newContendedStore(contended)
	store.lock(contended.Id, "another-process")
	store.onScan = func(scan int) {
		if scan == 1 {
			store.release(contended.Id)
		}
	}

	metrics, invalidations := invalidationMetrics()
	fetcher := sherdlock.NewLazyFetcher(store)
	s := sherdlock.NewSelector(sherdlock.Logger(), fetcher, &storeLocker{store: store, txID: "mine"}, 64, metrics)

	ids, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "USD")

	require.NoError(t, err)
	require.Len(t, ids, 1)
	assert.Equal(t, contended.Id, *ids[0])
	assert.Zero(t, invalidations.Total(),
		"a read-through fetcher has no snapshot to invalidate, so no forced refresh may be counted "+
			"against it")
}

// TestSelectorRecovery_BudgetIsNotSpentOnAnUnchangedStore is the cost side of the fix. A
// refetch now forces the eager strategy to re-read the whole store, so a request that genuinely
// cannot be served must not turn into one full refresh per retry on top of the retries
// themselves: the invalidations have to collapse into the reads that follow them, one each,
// rather than multiply.
//
// The wallet here holds a token locked by someone who never releases it, so the selection
// legitimately exhausts its budget. What is asserted is the bound: one store scan per refetch,
// and the whole thing bounded by the retry budget rather than by anything the fetcher does.
func TestSelectorRecovery_BudgetIsNotSpentOnAnUnchangedStore(t *testing.T) {
	contended := aliceToken("contended", "100")
	store := newContendedStore(contended)
	store.lock(contended.Id, "another-process")

	metrics, invalidations := invalidationMetrics()
	fetcher := sherdlock.NewCachedFetcher(store, 0, time.Hour, 1_000_000)
	s := sherdlock.NewSelector(sherdlock.Logger(), fetcher, &storeLocker{store: store, txID: "mine"}, 64, metrics)

	_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "USD")

	require.True(t, errors.Is(err, token.SelectorSufficientButLockedFunds),
		"the wallet can pay but its only token stays locked, so this must report locked funds, got: %v", err)
	assert.LessOrEqual(t, store.scanCount(), int(invalidations.Total())+1,
		"each forced refresh must be paid for once, in the read that follows it")
	assert.LessOrEqual(t, store.scanCount(), 8,
		"the store reads are bounded by the immediate-retry budget (maxImmediateRetries + the first "+
			"fetch), not by how often the cache is invalidated")
}

// TestEagerFetcher_FirstFetchForcesNoRefresh is the guard on the exemption that keeps the
// eager strategy a cache at all. A selection starts with an empty candidate cache, so it
// always reaches refreshCandidates once, before anything is known to be stale. Forcing the
// refresh there - the obvious simplification of the rule above - would re-read the whole token
// store on every Select and leave the eager and mixed strategies doing strictly more work than
// the lazy one they exist to avoid, while every test that asserts recovery would still pass.
func TestEagerFetcher_FirstFetchForcesNoRefresh(t *testing.T) {
	free := aliceToken("free", "100")
	store := newContendedStore(free)

	metrics, invalidations := invalidationMetrics()
	fetcher := sherdlock.NewCachedFetcher(store, 0, time.Hour, 1_000_000)
	s := sherdlock.NewSelector(sherdlock.Logger(), fetcher, &storeLocker{store: store, txID: "mine"}, 64, metrics)

	ids, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "USD")

	require.NoError(t, err)
	require.Len(t, ids, 1)
	assert.Zero(t, invalidations.Total(),
		"a selection served by its first scan has observed nothing stale, so it must not force a "+
			"full refresh of the cache")
	assert.Equal(t, 1, store.scanCount(),
		"one store read is all a cache hit may cost")
}
