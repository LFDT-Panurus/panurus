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

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	commonmetrics "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staleCandidateMetricsProvider hands out a countingCounter for each of the three outcomes a
// failed lock can have - StaleCandidates ("stale_candidates_total"), LockConflicts
// ("lock_conflicts_total") and LockStoreErrors ("lock_store_errors_total") - and discards
// everything else. All three are needed together: the defect this guards against is not a
// missing count but a *miscount*, a stale candidate booked as contention or as ill health, and
// only checking that the other two stayed at zero can tell those apart.
func staleCandidateMetricsProvider() (*mocks.FakeProvider, *countingCounter, *countingCounter, *countingCounter) {
	stale, conflicts, storeErrors := &countingCounter{}, &countingCounter{}, &countingCounter{}
	p := &mocks.FakeProvider{}
	p.NewCounterStub = func(opts commonmetrics.CounterOpts) commonmetrics.Counter {
		switch opts.Name {
		case "stale_candidates_total":
			return stale
		case "lock_conflicts_total":
			return conflicts
		case "lock_store_errors_total":
			return storeErrors
		default:
			return &countingCounter{}
		}
	}
	p.NewHistogramStub = func(commonmetrics.HistogramOpts) commonmetrics.Histogram {
		return discardHistogram{}
	}

	return p, stale, conflicts, storeErrors
}

// snapshotFetcher models the eager fetcher's defining property: it answers from a snapshot of
// the token store rather than from the store, and only re-reads the store when told that the
// snapshot is behind. That is what lets a spent token go on being offered as a candidate, which
// is the situation under test; a fetcher that read through on every call could not reproduce it.
//
// It starts out holding `stale` - a token the store has already moved past - and serves it until
// InvalidateCache is called, after which it serves `fresh`. Nothing else makes it advance: the
// test asserts recovery that the selector drives, so the fetcher must not quietly refresh on its
// own and hand the selector a correct answer the production path would not have had.
type snapshotFetcher struct {
	mu           sync.Mutex
	stale, fresh *token2.UnspentTokenInWallet
	invalidated  int
	fetches      int
}

func newSnapshotFetcher(stale, fresh *token2.UnspentTokenInWallet) *snapshotFetcher {
	return &snapshotFetcher{stale: stale, fresh: fresh}
}

func (f *snapshotFetcher) UnspentTokensIteratorBy(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.fetches++
	if f.invalidated == 0 {
		return &sliceIterator{items: []*token2.UnspentTokenInWallet{f.stale}}, nil
	}

	return &sliceIterator{items: []*token2.UnspentTokenInWallet{f.fresh}}, nil
}

// HasEnoughSpendableTokens reads the store, not the snapshot, exactly as the production
// fetchers do: the wallet can cover the request throughout, so an insufficient-funds answer
// here would be the selector's own doing.
func (f *snapshotFetcher) HasEnoughSpendableTokens(context.Context, string, token2.Type, *big.Int) (bool, error) {
	return true, nil
}

func (f *snapshotFetcher) InvalidateCache() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.invalidated++
}

func (f *snapshotFetcher) counts() (invalidated, fetches int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.invalidated, f.fetches
}

// spendabilityLocker is the single-token locker the sqlite-backed deployments use (it does not
// implement BatchTokenLocker, so selectInternal takes the one-token-at-a-time path). It refuses
// any token in `spent` with driver.ErrTokenNotSpendable, which is what the conditional lock
// insert does for a token that has been spent since the caller read it, and grants everything
// else.
type spendabilityLocker struct {
	mu       sync.Mutex
	spent    map[token2.ID]struct{}
	attempts []token2.ID
	locked   []token2.ID
}

func newSpendabilityLocker(spent ...token2.ID) *spendabilityLocker {
	s := make(map[token2.ID]struct{}, len(spent))
	for _, id := range spent {
		s[id] = struct{}{}
	}

	return &spendabilityLocker{spent: s}
}

func (l *spendabilityLocker) TryLock(_ context.Context, tokenID *token2.ID, _ string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.attempts = append(l.attempts, *tokenID)
	if _, isSpent := l.spent[*tokenID]; isSpent {
		return false, errors.Wrapf(driver.ErrTokenNotSpendable, "token %s is no longer spendable", tokenID)
	}
	l.locked = append(l.locked, *tokenID)

	return true, nil
}

func (l *spendabilityLocker) UnlockAll(context.Context) error { return nil }

func (l *spendabilityLocker) attempted() []token2.ID {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]token2.ID(nil), l.attempts...)
}

// TestSelector_DropsStaleCandidateAndRefreshes is the unit-level guard for the integration
// failure that #2395's phase 4b ordering made deterministic: two transfers of the same amount
// issued inside the eager cache's freshness interval both picked the same smallest-sufficient
// token, and the second one got a token the first had already spent - which the caller could
// then not load ("failed to load tokens: token not found for key ...").
//
// The selector must therefore never return a candidate whose lock was refused as not
// spendable. Dropping it is not enough on its own: the next scan would re-read the same stale
// snapshot and offer the same token again, so the candidate source has to be told it is behind
// the store. Both halves are asserted here, because either one alone still fails - without the
// refresh the selector would exhaust its retry budget on a wallet that can plainly pay.
func TestSelector_DropsStaleCandidateAndRefreshes(t *testing.T) {
	provider, stale, conflicts, storeErrors := staleCandidateMetricsProvider()
	metrics := sherdlock.NewMetrics(provider)

	spentToken := &token2.UnspentTokenInWallet{
		Id: token2.ID{TxId: "already-spent", Index: 0}, Type: "USD", Quantity: "110",
	}
	freshToken := &token2.UnspentTokenInWallet{
		Id: token2.ID{TxId: "change", Index: 0}, Type: "USD", Quantity: "60",
	}

	fetcher := newSnapshotFetcher(spentToken, freshToken)
	locker := newSpendabilityLocker(spentToken.Id)

	s := sherdlock.NewSelector(sherdlock.Logger(), fetcher, locker, 64, metrics)
	ids, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "issuer"}, "50", "USD")

	require.NoError(t, err,
		"the wallet can cover the request out of the fresh token, so a stale candidate must not fail the selection")
	require.Len(t, ids, 1)
	assert.Equal(t, freshToken.Id, *ids[0],
		"the selector returned the token that had already been spent; its caller cannot load it")
	assert.Equal(t, "60", sum.Decimal())

	assert.Contains(t, locker.attempted(), spentToken.Id,
		"the test is vacuous unless the stale candidate was actually offered and attempted")

	invalidated, fetches := fetcher.counts()
	assert.Positive(t, invalidated,
		"a candidate refused as not spendable is proof the snapshot is behind the store, so the cache must be invalidated")
	assert.GreaterOrEqual(t, fetches, 2,
		"the selector must re-read the candidate source after dropping the stale candidate")

	assert.InDelta(t, 1, stale.Total(), 0, "the dropped candidate must be counted as stale")
	assert.Zero(t, conflicts.Total(),
		"a spent token is not contention: counting it as a lock conflict hides the cache staleness it actually signals")
	assert.Zero(t, storeErrors.Total(),
		"a spent token is not a store error: the store answered correctly")
}

// spendabilityBatchLocker is the batch-capable locker the Postgres-backed deployments use
// (postgres.TokenLockStore.LockBatch). Its answer shape is the whole point of it: it returns the
// tokens it won and says nothing about the rest, exactly as the store's claim statement does,
// so a caller cannot tell a token another claimant holds from one that is no longer spendable.
// It embeds spendabilityLocker so it also satisfies TokenLocker, which is what makes
// selectInternal's s.locker.(BatchTokenLocker) assertion succeed and take the batch path.
type spendabilityBatchLocker struct {
	*spendabilityLocker
}

func newSpendabilityBatchLocker(spent ...token2.ID) *spendabilityBatchLocker {
	return &spendabilityBatchLocker{spendabilityLocker: newSpendabilityLocker(spent...)}
}

func (l *spendabilityBatchLocker) TryLockBatch(_ context.Context, ids []*token2.ID, _ string) ([]*token2.ID, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	won := make([]*token2.ID, 0, len(ids))
	for _, id := range ids {
		l.attempts = append(l.attempts, *id)
		if _, isSpent := l.spent[*id]; isSpent {
			continue
		}
		l.locked = append(l.locked, *id)
		won = append(won, id)
	}

	return won, nil
}

// TestBatchLockStaleCandidate_CountedAsConflictNotStale is the batch-path counterpart of
// TestSelector_DropsStaleCandidateAndRefreshes, and it deliberately asserts *weaker* behaviour,
// because that is what the batch path actually delivers: LockBatch reports only winners, so a
// stale candidate arrives as an unwon token and is indistinguishable from a lost race.
//
// What survives is the invariant that matters - the spent token is never returned, so no caller
// is handed a token it cannot load (#2395). What does not survive is the recovery the
// single-token path gained: the drop is booked as contention, the cache is never told it is
// behind the store, and so this selection spends its whole immediate-retry budget re-reading the
// same stale snapshot and gives up with SelectorSufficientButLockedFunds, where the single-token
// path finds the fresh token within the same call. In production the eager cache also refreshes
// on its own freshnessInterval (fetcher.go), so the gap closes on a later call rather than
// staying stuck - but it does not close on this one.
//
// This test exists to make that asymmetry visible instead of merely true. When LockBatch learns
// to report *why* a token was not won, the expectations below should flip to
// TestSelector_DropsStaleCandidateAndRefreshes' - a passing stale counter and a successful
// selection - rather than this gap being rediscovered from a production incident.
func TestBatchLockStaleCandidate_CountedAsConflictNotStale(t *testing.T) {
	provider, stale, conflicts, storeErrors := staleCandidateMetricsProvider()
	metrics := sherdlock.NewMetrics(provider)

	spentToken := &token2.UnspentTokenInWallet{
		Id: token2.ID{TxId: "already-spent", Index: 0}, Type: "USD", Quantity: "110",
	}
	freshToken := &token2.UnspentTokenInWallet{
		Id: token2.ID{TxId: "change", Index: 0}, Type: "USD", Quantity: "60",
	}

	fetcher := newSnapshotFetcher(spentToken, freshToken)
	locker := newSpendabilityBatchLocker(spentToken.Id)
	require.Implements(t, (*sherdlock.BatchTokenLocker)(nil), locker,
		"the test is vacuous unless the selector actually takes the batch path")

	s := sherdlock.NewSelector(sherdlock.Logger(), fetcher, locker, 64, metrics)
	ids, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "issuer"}, "50", "USD")

	assert.NotContains(t, idSet(ids), spentToken.Id,
		"whatever it is classified as, a token refused by the store must never reach the caller (#2395)")
	assert.True(t, errors.Is(err, token.SelectorSufficientButLockedFunds),
		"with no reason to distinguish, the batch path treats the stale candidate as contention and "+
			"exhausts its retry budget instead of recovering within the call, got: %v", err)

	assert.Contains(t, locker.attempted(), spentToken.Id,
		"the test is vacuous unless the stale candidate was actually offered and attempted")

	invalidated, _ := fetcher.counts()
	assert.Zero(t, invalidated,
		"documents the gap: an unwon token carries no spendability information, so the batch path "+
			"cannot tell the snapshot it is behind the store the way the single-token path does")

	assert.Zero(t, stale.Total(),
		"documents the gap: StaleCandidates reads zero on a batch-capable backend even during a "+
			"stale-candidate episode, which is why its doc comment says so (metrics.go)")
	assert.Positive(t, conflicts.Total(),
		"the drop is booked as contention instead - a rising LockConflicts with no real contention "+
			"is the only signal an operator gets on this path")
	assert.Zero(t, storeErrors.Total(),
		"a spent token is not a store error: the store answered correctly")
}

// idSet is a small readability helper: it turns the selector's result into a comparable set so
// an assertion can say "this token must not be in here" without indexing into a slice that may
// legitimately be empty.
func idSet(ids []*token2.ID) []token2.ID {
	out := make([]token2.ID, 0, len(ids))
	for _, id := range ids {
		out = append(out, *id)
	}

	return out
}

// TestRealFetchersImplementCacheInvalidator guards the type assertion the selector relies on.
// CacheInvalidator is optional, so a fetcher that stops satisfying it degrades silently - the
// selector just never refreshes - and TestSelector_DropsStaleCandidateAndRefreshes above would
// not catch it, because it supplies its own fetcher. Only the two caching strategies are
// expected to satisfy it: Lazy reads through on every call and has nothing to invalidate.
func TestRealFetchersImplementCacheInvalidator(t *testing.T) {
	tokenDB := &mocks.FakeTokenDB{}
	_, metrics := setupMetricsMocks()

	assert.Implements(t, (*sherdlock.CacheInvalidator)(nil), sherdlock.NewCachedFetcher(tokenDB, 0, 0, 0),
		"the eager fetcher serves candidates from a snapshot, so the selector must be able to invalidate it")
	assert.Implements(t, (*sherdlock.CacheInvalidator)(nil), sherdlock.NewMixedFetcher(tokenDB, metrics, 0, 0, 0),
		"the default (mixed) fetcher has an eager half, so it must forward invalidation to it")
	assert.NotImplements(t, (*sherdlock.CacheInvalidator)(nil), sherdlock.NewLazyFetcher(tokenDB),
		"the lazy fetcher reads through, so it must not advertise a cache to invalidate")
}
