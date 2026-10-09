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
// (postgres.TokenLockStore.LockBatch). Its answer shape is the whole point of it: it reports the
// tokens it won *and* the ones it refused as no longer spendable, exactly as the store's claim
// statement does, leaving "another claimant holds it" as the unreported remainder. It embeds
// spendabilityLocker so it also satisfies TokenLocker, which is what makes selectInternal's
// s.locker.(BatchTokenLocker) assertion succeed and take the batch path.
type spendabilityBatchLocker struct {
	*spendabilityLocker
	// classify is false for a backend that wins or does not win a candidate but cannot say
	// why, which dbdriver.BatchLockOutcome explicitly permits. Such a store leaves Stale
	// empty and every unwon candidate reads as a lost race.
	classify bool
}

func newSpendabilityBatchLocker(spent ...token2.ID) *spendabilityBatchLocker {
	return &spendabilityBatchLocker{spendabilityLocker: newSpendabilityLocker(spent...), classify: true}
}

// newUnclassifyingBatchLocker is the same locker with the classification withheld.
func newUnclassifyingBatchLocker(spent ...token2.ID) *spendabilityBatchLocker {
	return &spendabilityBatchLocker{spendabilityLocker: newSpendabilityLocker(spent...), classify: false}
}

func (l *spendabilityBatchLocker) TryLockBatch(_ context.Context, ids []*token2.ID, _ string) (driver.BatchLockOutcome, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	outcome := driver.BatchLockOutcome{Won: make([]*token2.ID, 0, len(ids))}
	for _, id := range ids {
		l.attempts = append(l.attempts, *id)
		if _, isSpent := l.spent[*id]; isSpent {
			if l.classify {
				outcome.Stale = append(outcome.Stale, id)
			}

			continue
		}
		l.locked = append(l.locked, *id)
		outcome.Won = append(outcome.Won, id)
	}

	return outcome, nil
}

// TestBatchLockStaleCandidate_DropsStaleCandidateAndRefreshes is the batch-path counterpart of
// TestSelector_DropsStaleCandidateAndRefreshes, and asserts the same behaviour, which is the
// point: the batch claim classifies each candidate it did not win (dbdriver.BatchLockOutcome),
// so a stale candidate is recognised as stale on this path too and recovery no longer depends
// on which backend is underneath.
//
// It used to assert deliberately weaker behaviour - the drop booked as contention, the cache
// never told it was behind the store, and the selection giving up with
// SelectorSufficientButLockedFunds after spending its whole retry budget re-reading the same
// stale snapshot - because LockBatch reported only its winners and an unwon token carried no
// reason. That is what these expectations were written to flip to once it reported why.
func TestBatchLockStaleCandidate_DropsStaleCandidateAndRefreshes(t *testing.T) {
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
		"a candidate the batch claim refused as not spendable is proof the snapshot is behind the "+
			"store, so the cache must be invalidated on this path too")
	assert.GreaterOrEqual(t, fetches, 2,
		"the selector must re-read the candidate source after dropping the stale candidate")

	assert.InDelta(t, 1, stale.Total(), 0,
		"the dropped candidate must be counted as stale on the batch path, not left to read zero")
	assert.Zero(t, conflicts.Total(),
		"a spent token is not contention: counting it as a lock conflict hides the cache staleness it actually signals")
	assert.Zero(t, storeErrors.Total(),
		"a spent token is not a store error: the store answered correctly")
}

// TestBatchLockStaleCandidate_UnclassifiedFallsBackToConflict pins the degraded mode
// dbdriver.BatchLockOutcome permits: a batch-capable store that does not populate Stale. Every
// unwon candidate then reads as a lost race, which is what the whole batch path did before it
// classified, so this is also the regression guard for the behaviour the previous shape had.
//
// The invariant that matters survives regardless - the spent token is never handed to a caller
// that could not load it (#2395) - but the recovery does not: the drop is booked as contention,
// the cache is never told it is behind the store, and the selection spends its retry budget
// re-reading the same stale snapshot before giving up with SelectorSufficientButLockedFunds.
// Asserting it keeps "classification is what buys the recovery" a tested claim rather than a
// comment, so a store that silently stops classifying is a failing test and not a quiet
// regression in production.
func TestBatchLockStaleCandidate_UnclassifiedFallsBackToConflict(t *testing.T) {
	provider, stale, conflicts, storeErrors := staleCandidateMetricsProvider()
	metrics := sherdlock.NewMetrics(provider)

	spentToken := &token2.UnspentTokenInWallet{
		Id: token2.ID{TxId: "already-spent", Index: 0}, Type: "USD", Quantity: "110",
	}
	freshToken := &token2.UnspentTokenInWallet{
		Id: token2.ID{TxId: "change", Index: 0}, Type: "USD", Quantity: "60",
	}

	fetcher := newSnapshotFetcher(spentToken, freshToken)
	locker := newUnclassifyingBatchLocker(spentToken.Id)
	require.Implements(t, (*sherdlock.BatchTokenLocker)(nil), locker,
		"the test is vacuous unless the selector actually takes the batch path")

	s := sherdlock.NewSelector(sherdlock.Logger(), fetcher, locker, 64, metrics)
	ids, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "issuer"}, "50", "USD")

	assert.NotContains(t, idSet(ids), spentToken.Id,
		"whatever it is classified as, a token refused by the store must never reach the caller (#2395)")
	assert.True(t, errors.Is(err, token.SelectorSufficientButLockedFunds),
		"with no reason reported, the batch path treats the stale candidate as contention and "+
			"exhausts its retry budget instead of recovering within the call, got: %v", err)

	assert.Contains(t, locker.attempted(), spentToken.Id,
		"the test is vacuous unless the stale candidate was actually offered and attempted")

	invalidated, _ := fetcher.counts()
	assert.Zero(t, invalidated,
		"an unclassified unwon token carries no spendability information, so the snapshot cannot be "+
			"told it is behind the store")

	assert.Zero(t, stale.Total(),
		"a store that does not populate Stale cannot have its stale candidates counted")
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
