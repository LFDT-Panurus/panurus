/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"context"
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	commonmetrics "github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingCounter records the total added, so a test can assert on whether a
// metric was touched at all. Mirrors auditor_test.go's countingCounter, kept
// local to this package so this file does not need to import the auditor
// package just to name a two-method helper.
type countingCounter struct {
	mu    sync.Mutex
	total float64
}

func (c *countingCounter) With(...string) commonmetrics.Counter { return c }

func (c *countingCounter) Add(delta float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total += delta
}

func (c *countingCounter) Total() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.total
}

type discardHistogram struct{}

func (discardHistogram) With(...string) commonmetrics.Histogram { return discardHistogram{} }
func (discardHistogram) Observe(float64)                        {}

// recordingHistogram records every Observe call, so a test can assert on how many
// observations happened and what they were - unlike countingCounter's running total,
// DistinctTokensAttempted reports one value per Select() call (via a defer), not a
// cumulative count, so "was it observed at all, and with what value" is what matters.
type recordingHistogram struct {
	mu  sync.Mutex
	obs []float64
}

func (h *recordingHistogram) With(...string) commonmetrics.Histogram { return h }

func (h *recordingHistogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.obs = append(h.obs, v)
}

func (h *recordingHistogram) Observations() []float64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]float64(nil), h.obs...)
}

// lockMetricsProvider hands out a countingCounter for sherdlock's LockConflicts metric
// (name "lock_conflicts_total") and a recordingHistogram for DistinctTokensAttempted (name
// "distinct_tokens_attempted", see metrics.go), and discards everything else, so a test can
// assert on either metric without also having to attribute Adds/Observes against every other
// counter/histogram the selector touches.
func lockMetricsProvider() (*mocks.FakeProvider, *countingCounter, *recordingHistogram) {
	conflicts := &countingCounter{}
	distinct := &recordingHistogram{}
	p := &mocks.FakeProvider{}
	p.NewCounterStub = func(opts commonmetrics.CounterOpts) commonmetrics.Counter {
		if opts.Name == "lock_conflicts_total" {
			return conflicts
		}

		return &countingCounter{}
	}
	p.NewHistogramStub = func(opts commonmetrics.HistogramOpts) commonmetrics.Histogram {
		if opts.Name == "distinct_tokens_attempted" {
			return distinct
		}

		return discardHistogram{}
	}

	return p, conflicts, distinct
}

// lockConflictsProvider is lockMetricsProvider's LockConflicts-only convenience form, kept for
// the tests below that only care about that one metric.
func lockConflictsProvider() (*mocks.FakeProvider, *countingCounter) {
	p, conflicts, _ := lockMetricsProvider()

	return p, conflicts
}

// singleTokenIteratorStub returns a UnspentTokensIteratorByStub that hands back a fresh
// one-shot iterator over tok on every call, so a test can drive multiple fetch/refetch
// cycles (immediate retries) over the same candidate without it ever appearing to be
// exhausted for good.
func singleTokenIteratorStub(tok *token2.UnspentTokenInWallet) func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
	return func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
		it := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
		it.NextReturnsOnCall(0, tok, nil)
		it.NextReturnsOnCall(1, nil, nil)

		return it, nil
	}
}

// TestBatchLockRateLimit_HardAborts is TestSelectorRateLimit's counterpart for the batch
// path (selector.go:305-310): a mocks.FakeBatchTokenLocker denying TryLockBatch with an
// error wrapping token.SelectorRateLimited must abort immediately, the same as the
// single-token TryLock path already covered by TestSelectorRateLimit. This path was
// previously uncovered - TestSelectorRateLimit uses mocks.FakeTokenLocker, which is not a
// BatchTokenLocker, so selectInternal's `s.locker.(BatchTokenLocker)` assertion always
// misses there and the batch branch (selector.go:264) is never taken.
func TestBatchLockRateLimit_HardAborts(t *testing.T) {
	_, metrics := setupMetricsMocks()

	mockFetcher := &mocks.FakeTokenFetcher{}
	mockLocker := &mocks.FakeBatchTokenLocker{}

	mockIt := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
	mockIt.NextReturns(&token2.UnspentTokenInWallet{
		Id:       token2.ID{TxId: "tx1", Index: 0},
		Type:     "ABC",
		Quantity: "100",
	}, nil)
	mockFetcher.UnspentTokensIteratorByReturns(mockIt, nil)

	mockLocker.TryLockBatchReturns(nil, errors.Wrapf(token.SelectorRateLimited, "wallet alice throttled"))

	s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)
	_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
	require.Error(t, err)
	assert.True(t, errors.Is(err, token.SelectorRateLimited), "expected rate-limit error, got: %v", err)
	assert.Equal(t, 1, mockLocker.TryLockBatchCallCount(), "batch path must abort after the first rate-limited TryLockBatch, not retry")
}

// TestBatchLockGenericStoreError_RetriedNotBlacklisted pins the deliberate asymmetry
// documented at selector.go:311-320: a TryLockBatch call that fails with a real store error
// (not a rate limit, and not signalled by absence from the won set) must NOT blacklist any
// window token and must NOT count a LockConflicts. It logs a warning and lets the caller
// refetch and re-attempt the exact same token, because nothing here establishes that the
// token is actually contended - unlike a lost race, where TryLockBatch succeeds but the
// token is missing from won (covered by TestBatchLockLostRace_BlacklistsAndCountsConflict
// below). This test proves the "retried" half of that asymmetry: the same token, offered
// again on a refetch, is not skipped as blacklisted and does get a second TryLockBatch call,
// which this time succeeds.
func TestBatchLockGenericStoreError_RetriedNotBlacklisted(t *testing.T) {
	metricsProvider, conflicts := lockConflictsProvider()
	metrics := sherdlock.NewMetrics(metricsProvider)

	tok := &token2.UnspentTokenInWallet{
		Id:       token2.ID{TxId: "tx1", Index: 0},
		Type:     "ABC",
		Quantity: "100",
	}

	mockFetcher := &mocks.FakeTokenFetcher{}
	mockFetcher.UnspentTokensIteratorByStub = singleTokenIteratorStub(tok)
	mockFetcher.HasEnoughSpendableTokensReturns(true, nil)

	mockLocker := &mocks.FakeBatchTokenLocker{}
	mockLocker.TryLockBatchReturnsOnCall(0, nil, errors.New("db unavailable"))
	mockLocker.TryLockBatchReturnsOnCall(1, []*token2.ID{&tok.Id}, nil)

	s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)
	tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.Equal(t, "tx1", tokens[0].TxId)
	assert.Equal(t, "100", sum.Decimal())

	assert.Equal(t, 2, mockLocker.TryLockBatchCallCount(),
		"a generic store error must not blacklist the token: it must be re-attempted via TryLockBatch on the next refetch")
	assert.Zero(t, conflicts.Total(), "a generic store error is not a lock conflict and must not increment LockConflicts")
}

// TestBatchLockLostRace_BlacklistsAndCountsConflict is the other half of the asymmetry: when
// TryLockBatch succeeds but a window token is absent from the returned won set (selector.go:
// 326-336), that is a genuine lost race. It must count a LockConflicts and must blacklist the
// token so this same Select call does not immediately re-propose it. The scenario uses two
// tokens sized so a single window covers both in one TryLockBatch call: a small "hot" token
// that always loses its race, and a larger token that alone satisfies the remaining amount, so
// the lost race is visible without needing a second round trip.
func TestBatchLockLostRace_BlacklistsAndCountsConflict(t *testing.T) {
	metricsProvider, conflicts := lockConflictsProvider()
	metrics := sherdlock.NewMetrics(metricsProvider)

	hot := &token2.UnspentTokenInWallet{
		Id:       token2.ID{TxId: "tx-hot", Index: 0},
		Type:     "ABC",
		Quantity: "50",
	}
	cold := &token2.UnspentTokenInWallet{
		Id:       token2.ID{TxId: "tx-cold", Index: 0},
		Type:     "ABC",
		Quantity: "100",
	}

	mockFetcher := &mocks.FakeTokenFetcher{}
	mockIt := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
	mockIt.NextReturnsOnCall(0, hot, nil)
	mockIt.NextReturnsOnCall(1, cold, nil)
	mockIt.NextReturnsOnCall(2, nil, nil)
	mockFetcher.UnspentTokensIteratorByReturns(mockIt, nil)

	mockLocker := &mocks.FakeBatchTokenLocker{}
	// The window covering a 100 request starting from hot (50) grows to include cold (150 >=
	// 100), so both are claimed in one TryLockBatch call; only cold is returned as won.
	mockLocker.TryLockBatchReturns([]*token2.ID{&cold.Id}, nil)

	s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)
	tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
	require.NoError(t, err)
	require.Len(t, tokens, 1, "the lost-race token must not be selected")
	assert.Equal(t, "tx-cold", tokens[0].TxId)
	assert.Equal(t, "100", sum.Decimal())

	assert.Equal(t, 1, mockLocker.TryLockBatchCallCount(), "the covering window satisfies the request in a single round trip")
	assert.InDelta(t, 1, conflicts.Total(), 0, "a genuine lost race (absent from won) must increment LockConflicts exactly once")
}

// TestBlacklistClearsWhenScanSeesOnlyBlacklistedCandidates covers the escape hatch at
// selector.go:229-233: once a whole scan (since the last refetch) produced nothing but
// already-blacklisted candidates, the blacklist is cleared so a genuinely freed token can be
// retried, instead of a lost race turning into a permanent false insufficient-funds within the
// same Select call. The scenario is a single-token wallet where that token loses its lock race
// exactly once, then wins on a later attempt (simulating the other holder having released it):
// without the escape hatch, the token would stay blacklisted for the rest of this Select call
// and the request would exhaust its immediate-retry budget and fail with
// token.SelectorSufficientButLockedFunds instead of succeeding.
func TestBlacklistClearsWhenScanSeesOnlyBlacklistedCandidates(t *testing.T) {
	metricsProvider, conflicts := lockConflictsProvider()
	metrics := sherdlock.NewMetrics(metricsProvider)

	tok := &token2.UnspentTokenInWallet{
		Id:       token2.ID{TxId: "tx1", Index: 0},
		Type:     "ABC",
		Quantity: "100",
	}

	mockFetcher := &mocks.FakeTokenFetcher{}
	mockFetcher.UnspentTokensIteratorByStub = singleTokenIteratorStub(tok)
	mockFetcher.HasEnoughSpendableTokensReturns(true, nil)

	mockLocker := &mocks.FakeBatchTokenLocker{}
	// First attempt: a genuine lost race (won set does not contain tok), which blacklists it.
	// The next scan sees only that blacklisted candidate and nothing else, triggering the
	// escape hatch; the following scan re-offers the (now un-blacklisted) token and this
	// second TryLockBatch call wins it.
	mockLocker.TryLockBatchReturnsOnCall(0, []*token2.ID{}, nil)
	mockLocker.TryLockBatchReturnsOnCall(1, []*token2.ID{&tok.Id}, nil)

	s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)
	tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
	require.NoError(t, err, "the blacklist-clear escape hatch must let a freed token be retried within the same Select call")
	require.Len(t, tokens, 1)
	assert.Equal(t, "tx1", tokens[0].TxId)
	assert.Equal(t, "100", sum.Decimal())

	assert.Equal(t, 2, mockLocker.TryLockBatchCallCount())
	assert.InDelta(t, 1, conflicts.Total(), 0, "only the first, genuine lost race counts as a conflict")
}

// TestSingleLockLostRace_BlacklistsAndCountsConflict is
// TestBatchLockLostRace_BlacklistsAndCountsConflict's counterpart for the single-token path
// (selector.go:352-371): TryLock failing with an error wrapping driver.ErrTokenAlreadyLocked is
// a genuine lost race, and must count a LockConflicts and blacklist the token, exactly like the
// batch path's "absent from won" case. The token then wins on the refetch that follows losing
// the race, simulating the other holder having released it.
func TestSingleLockLostRace_BlacklistsAndCountsConflict(t *testing.T) {
	metricsProvider, conflicts := lockConflictsProvider()
	metrics := sherdlock.NewMetrics(metricsProvider)

	tok := &token2.UnspentTokenInWallet{
		Id:       token2.ID{TxId: "tx1", Index: 0},
		Type:     "ABC",
		Quantity: "100",
	}

	mockFetcher := &mocks.FakeTokenFetcher{}
	mockFetcher.UnspentTokensIteratorByStub = singleTokenIteratorStub(tok)
	mockFetcher.HasEnoughSpendableTokensReturns(true, nil)

	mockLocker := &mocks.FakeTokenLocker{}
	mockLocker.TryLockReturnsOnCall(0, false, errors.Wrapf(driver.ErrTokenAlreadyLocked, "already locked"))
	mockLocker.TryLockReturnsOnCall(1, true, nil)

	s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)
	tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.Equal(t, "tx1", tokens[0].TxId)
	assert.Equal(t, "100", sum.Decimal())

	assert.Equal(t, 2, mockLocker.TryLockCallCount(),
		"a lost race must be retried via TryLock on the next refetch, after the other holder released it")
	assert.InDelta(t, 1, conflicts.Total(), 0, "a genuine lost race (ErrTokenAlreadyLocked) must increment LockConflicts exactly once")
}

// TestSingleLockGenericStoreError_NotCountedAsConflict_Retried is
// TestBatchLockGenericStoreError_RetriedNotBlacklisted's counterpart for the single-token path:
// a TryLock error that does NOT wrap driver.ErrTokenAlreadyLocked (a real store error, not
// per-token contention) must not count a LockConflicts and must not blacklist the token, exactly
// mirroring the batch path's asymmetry documented at selector.go:359-370.
func TestSingleLockGenericStoreError_NotCountedAsConflict_Retried(t *testing.T) {
	metricsProvider, conflicts := lockConflictsProvider()
	metrics := sherdlock.NewMetrics(metricsProvider)

	tok := &token2.UnspentTokenInWallet{
		Id:       token2.ID{TxId: "tx1", Index: 0},
		Type:     "ABC",
		Quantity: "100",
	}

	mockFetcher := &mocks.FakeTokenFetcher{}
	mockFetcher.UnspentTokensIteratorByStub = singleTokenIteratorStub(tok)
	mockFetcher.HasEnoughSpendableTokensReturns(true, nil)

	mockLocker := &mocks.FakeTokenLocker{}
	mockLocker.TryLockReturnsOnCall(0, false, errors.New("db unavailable"))
	mockLocker.TryLockReturnsOnCall(1, true, nil)

	s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)
	tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.Equal(t, "tx1", tokens[0].TxId)
	assert.Equal(t, "100", sum.Decimal())

	assert.Equal(t, 2, mockLocker.TryLockCallCount(),
		"a generic store error must not blacklist the token: it must be re-attempted via TryLock on the next refetch")
	assert.Zero(t, conflicts.Total(), "a generic store error is not a lock conflict and must not increment LockConflicts")
}

// TestDistinctTokensAttempted_ObservesAttemptedCount pins that Select's deferred report
// (selector.go:169-171) observes the number of distinct tokens actually tried, won or lost,
// not just the number selected. The scenario has two tokens on the single-token path: a "hot"
// one that loses its race (attempted, blacklisted, not selected) and a "cold" one that wins
// alone (attempted, selected) - so the attempted count (2) differs from the selected count (1).
func TestDistinctTokensAttempted_ObservesAttemptedCount(t *testing.T) {
	metricsProvider, _, distinct := lockMetricsProvider()
	metrics := sherdlock.NewMetrics(metricsProvider)

	hot := &token2.UnspentTokenInWallet{
		Id:       token2.ID{TxId: "tx-hot", Index: 0},
		Type:     "ABC",
		Quantity: "50",
	}
	cold := &token2.UnspentTokenInWallet{
		Id:       token2.ID{TxId: "tx-cold", Index: 0},
		Type:     "ABC",
		Quantity: "100",
	}

	mockFetcher := &mocks.FakeTokenFetcher{}
	mockIt := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
	mockIt.NextReturnsOnCall(0, hot, nil)
	mockIt.NextReturnsOnCall(1, cold, nil)
	mockIt.NextReturnsOnCall(2, nil, nil)
	mockFetcher.UnspentTokensIteratorByReturns(mockIt, nil)

	mockLocker := &mocks.FakeTokenLocker{}
	mockLocker.TryLockReturnsOnCall(0, false, errors.Wrapf(driver.ErrTokenAlreadyLocked, "already locked"))
	mockLocker.TryLockReturnsOnCall(1, true, nil)

	s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)
	tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
	require.NoError(t, err)
	require.Len(t, tokens, 1, "the lost-race hot token must not be selected")
	assert.Equal(t, "tx-cold", tokens[0].TxId)
	assert.Equal(t, "100", sum.Decimal())

	obs := distinct.Observations()
	require.Len(t, obs, 1, "Select observes DistinctTokensAttempted exactly once, in its deferred reporter")
	assert.InDelta(t, 2, obs[0], 0, "both the lost-race hot token and the winning cold token count as attempted")
}

// TestDistinctTokensAttempted_NotObservedOnClosedSelector pins the other half of the
// DistinctTokensAttempted contract: the two earliest error returns in selectInternal
// (closed-selector, invalid quantity) happen before `attempted` is initialized
// (selector.go:158-171), so its deferred Observe is never registered and DistinctTokensAttempted
// must not be observed at all for those calls - not even with a zero value.
func TestDistinctTokensAttempted_NotObservedOnClosedSelector(t *testing.T) {
	metricsProvider, _, distinct := lockMetricsProvider()
	metrics := sherdlock.NewMetrics(metricsProvider)

	mockFetcher := &mocks.FakeTokenFetcher{}
	mockLocker := &mocks.FakeTokenLocker{}

	s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)
	require.NoError(t, s.Close())

	_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
	require.Error(t, err)
	assert.Empty(t, distinct.Observations(),
		"a closed selector returns before attempted is initialized, so DistinctTokensAttempted must not be observed")
}

// TestDistinctTokensAttempted_NotObservedOnInvalidQuantity is
// TestDistinctTokensAttempted_NotObservedOnClosedSelector's counterpart for the other early
// return ahead of attempted's initialization: an invalid quantity string fails token2.ToQuantity
// (selector.go:162-165), before the selector ever touches the fetcher or locker.
func TestDistinctTokensAttempted_NotObservedOnInvalidQuantity(t *testing.T) {
	metricsProvider, _, distinct := lockMetricsProvider()
	metrics := sherdlock.NewMetrics(metricsProvider)

	mockFetcher := &mocks.FakeTokenFetcher{}
	mockLocker := &mocks.FakeTokenLocker{}

	s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)
	_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "not-a-number", "ABC")
	require.Error(t, err)
	assert.Empty(t, distinct.Observations(),
		"an invalid quantity string returns before attempted is initialized, so DistinctTokensAttempted must not be observed")
}
