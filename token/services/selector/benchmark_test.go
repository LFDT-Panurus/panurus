/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package selector

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	inmemory2 "github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/inmemory"
	selector "github.com/LFDT-Panurus/panurus/token/services/selector/simple"
	"github.com/LFDT-Panurus/panurus/token/services/selector/simple/inmemory"
	"github.com/LFDT-Panurus/panurus/token/services/selector/testutils"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	fscmetrics "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
)

// benchBackoff is the retry backoff used by the "+stubborn" settings below. It mirrors
// production's StubbornSelector (see sherdlock/manager.go, which always wires a configurable
// backoff > 0), kept short so contended benchmark iterations retry instead of ballooning
// benchmark runtime.
const benchBackoff = 2 * time.Millisecond

// classifyOutcome reports whether err is a contention-driven outcome (some tokens were locked
// by another process, or the wallet ran out of unlocked funds because of it) as opposed to a
// genuine bug. Only the latter should fail the benchmark: under concurrent settings, losing a
// lock race and ultimately reporting locked/insufficient funds is expected system behavior, not
// a broken benchmark.
func classifyOutcome(err error) (contended bool, unexpected bool) {
	if err == nil {
		return false, false
	}
	if errors.Is(err, token.SelectorInsufficientFunds) || errors.Is(err, token.SelectorSufficientButLockedFunds) {
		return true, false
	}

	return false, true
}

// benchMetricsProvider is a minimal, thread-safe fscmetrics.Provider that records every
// observation instead of discarding it like disabled.Provider does. Without this, none of
// sherdlock.Metrics (LockConflicts, ImmediateRetries, DistinctTokensAttempted, ...) could ever be
// asserted on or logged from a benchmark: disabled.Provider's counters/histograms are pure no-ops.
// It is not a general-purpose test double (no Gauge support beyond satisfying the interface,
// label combinations are joined into an opaque string key) - just enough for this file to report
// what sherdlock actually observed during a run.
type benchMetricsProvider struct {
	mu         sync.Mutex
	counters   map[string]*benchCounterVec
	histograms map[string]*benchHistogramVec
}

func newBenchMetricsProvider() *benchMetricsProvider {
	return &benchMetricsProvider{
		counters:   map[string]*benchCounterVec{},
		histograms: map[string]*benchHistogramVec{},
	}
}

func (p *benchMetricsProvider) NewCounter(opts fscmetrics.CounterOpts) fscmetrics.Counter {
	p.mu.Lock()
	defer p.mu.Unlock()
	cv := &benchCounterVec{vals: map[string]float64{}}
	p.counters[opts.Name] = cv

	return &benchCounter{cv: cv}
}

func (p *benchMetricsProvider) NewGauge(fscmetrics.GaugeOpts) fscmetrics.Gauge {
	return &benchGauge{}
}

func (p *benchMetricsProvider) NewHistogram(opts fscmetrics.HistogramOpts) fscmetrics.Histogram {
	p.mu.Lock()
	defer p.mu.Unlock()
	hv := &benchHistogramVec{count: map[string]float64{}, sum: map[string]float64{}}
	p.histograms[opts.Name] = hv

	return &benchHistogram{hv: hv}
}

// counterTotal sums every label combination recorded for the named counter.
func (p *benchMetricsProvider) counterTotal(name string) (float64, bool) {
	p.mu.Lock()
	cv, ok := p.counters[name]
	p.mu.Unlock()
	if !ok {
		return 0, false
	}

	cv.mu.Lock()
	defer cv.mu.Unlock()
	var total float64
	for _, v := range cv.vals {
		total += v
	}

	return total, true
}

// counterByLabel returns a copy of the named counter's per-label-combination totals, keyed by
// the label pairs joined into a human-readable string (e.g. "outcome=success").
func (p *benchMetricsProvider) counterByLabel(name string) map[string]float64 {
	p.mu.Lock()
	cv, ok := p.counters[name]
	p.mu.Unlock()
	if !ok {
		return nil
	}

	cv.mu.Lock()
	defer cv.mu.Unlock()
	out := make(map[string]float64, len(cv.vals))
	for k, v := range cv.vals {
		out[k] = v
	}

	return out
}

// histogramAvg reports the mean observed value and observation count for the named histogram,
// summed across every label combination.
func (p *benchMetricsProvider) histogramAvg(name string) (avg float64, count int, ok bool) {
	p.mu.Lock()
	hv, exists := p.histograms[name]
	p.mu.Unlock()
	if !exists {
		return 0, 0, false
	}

	hv.mu.Lock()
	defer hv.mu.Unlock()
	var totalCount, totalSum float64
	for k, c := range hv.count {
		totalCount += c
		totalSum += hv.sum[k]
	}
	if totalCount == 0 {
		return 0, 0, true
	}

	return totalSum / totalCount, int(totalCount), true
}

type benchCounterVec struct {
	mu   sync.Mutex
	vals map[string]float64
}

// benchCounter mirrors prometheus.counter's With()-chaining shape (see fabric-smart-client's
// metrics/prometheus/provider.go): With returns a new benchCounter that shares the same
// underlying vec but accumulates label pairs, so repeated .With(...).Add(...) calls with the
// same labels land in the same bucket.
type benchCounter struct {
	cv  *benchCounterVec
	lvs []string
}

func (c *benchCounter) With(labelValues ...string) fscmetrics.Counter {
	return &benchCounter{cv: c.cv, lvs: append(append([]string{}, c.lvs...), labelValues...)}
}

func (c *benchCounter) Add(delta float64) {
	key := labelKey(c.lvs)
	c.cv.mu.Lock()
	c.cv.vals[key] += delta
	c.cv.mu.Unlock()
}

type benchHistogramVec struct {
	mu    sync.Mutex
	count map[string]float64
	sum   map[string]float64
}

type benchHistogram struct {
	hv  *benchHistogramVec
	lvs []string
}

func (h *benchHistogram) With(labelValues ...string) fscmetrics.Histogram {
	return &benchHistogram{hv: h.hv, lvs: append(append([]string{}, h.lvs...), labelValues...)}
}

func (h *benchHistogram) Observe(value float64) {
	key := labelKey(h.lvs)
	h.hv.mu.Lock()
	h.hv.count[key]++
	h.hv.sum[key] += value
	h.hv.mu.Unlock()
}

// benchGauge satisfies fscmetrics.Gauge without tracking anything: no sherdlock metric is a
// Gauge today, this only exists so benchMetricsProvider implements the Provider interface.
type benchGauge struct{}

func (g *benchGauge) With(...string) fscmetrics.Gauge { return g }
func (g *benchGauge) Add(float64)                     {}
func (g *benchGauge) Set(float64)                     {}

// labelKey turns a "name1", "value1", "name2", "value2", ... pair list (the convention
// fscmetrics.Counter/Histogram.With uses - see makeLabels in fabric-smart-client's prometheus
// provider) into a stable, human-readable map key such as "outcome=success".
func labelKey(pairs []string) string {
	if len(pairs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(pairs)/2+1)
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, pairs[i]+"="+pairs[i+1])
	}

	return strings.Join(parts, ",")
}

// reportSherdlockMetrics logs a snapshot of every sherdlock.Metrics value observed during the
// subtest, so behavior classifyOutcome's pass/fail counters cannot show - e.g. how many
// *distinct* tokens a losing Select call tried before giving up, not just that it lost - is
// visible in `go test -v` output instead of being silently discarded. No-op when mp is nil
// (settings that don't build a sherdlock selector, e.g. the legacy "selector+*" ones).
func reportSherdlockMetrics(b *testing.B, mp *benchMetricsProvider) {
	if mp == nil {
		return
	}
	if v, ok := mp.counterTotal("lock_conflicts_total"); ok {
		b.Logf("metrics: lock_conflicts_total=%.0f", v)
	}
	for label, v := range mp.counterByLabel("selection_outcome_total") {
		b.Logf("metrics: selection_outcome_total[%s]=%.0f", label, v)
	}
	if avg, count, ok := mp.histogramAvg("selection_immediate_retries"); ok && count > 0 {
		b.Logf("metrics: selection_immediate_retries avg=%.2f (n=%d)", avg, count)
	}
	if avg, count, ok := mp.histogramAvg("distinct_tokens_attempted"); ok && count > 0 {
		b.Logf("metrics: distinct_tokens_attempted avg=%.2f (n=%d)", avg, count)
	}
	if avg, count, ok := mp.histogramAvg("selection_duration_seconds"); ok && count > 0 {
		b.Logf("metrics: selection_duration_seconds avg=%.6f (n=%d)", avg, count)
	}
	for label, v := range mp.counterByLabel("unspent_tokens_invocations") {
		b.Logf("metrics: unspent_tokens_invocations[%s]=%.0f", label, v)
	}
}

type WalletIDByRawIdentityFunc func(rawIdentity []byte) string

type Locker interface {
	Lock(ctx context.Context, owner string, id *token2.ID, txID string, reclaim bool) (string, error)
	UnlockIDs(ctx context.Context, owner string, ids ...*token2.ID) []*token2.ID
	UnlockByTxID(ctx context.Context, txID string)
	IsLocked(id *token2.ID) bool
}

type extendedSelector struct {
	Selector token.Selector
	Lock     Locker
	// Metrics is non-nil for sherdlock-backed selectors, giving benchmarks a way to read back
	// what sherdlock.Metrics actually observed (see reportSherdlockMetrics). nil for the legacy
	// "selector+*" settings, which don't build a sherdlock.Metrics at all.
	Metrics *benchMetricsProvider
}

func (s *extendedSelector) Select(ctx context.Context, ownerFilter token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	return s.Selector.Select(ctx, ownerFilter, q, tokenType)
}
func (s *extendedSelector) Close() error { return s.Selector.Close() }

func (s *extendedSelector) BenchMetrics() *benchMetricsProvider { return s.Metrics }

// Unselect releases the tokens Select returned. For the "simple" selector (s.Lock != nil) this
// unlocks by ID. sherdlock selectors (s.Lock == nil, since sherdlock owns locking internally via
// its TokenLocker) instead need UnlockAll: without this branch, Unselect silently did nothing
// for every sherdlock-based setting, so locked tokens accumulated across benchmark iterations
// and never came back, growing the live candidate scan on every subsequent Select until the
// wallet looked contended or exhausted. That is a correctness bug in this harness, not evidence
// of anything wrong in the selector under test.
func (s *extendedSelector) Unselect(id ...*token2.ID) {
	if s.Lock != nil {
		s.Lock.UnlockIDs(context.Background(), "", id...)

		return
	}
	if u, ok := s.Selector.(interface {
		UnlockAll(ctx context.Context) error
	}); ok {
		if err := u.UnlockAll(context.Background()); err != nil {
			panic("benchmark harness: UnlockAll failed: " + err.Error())
		}
	}
}

// BenchmarkSelectorSingle measures pure, uncontended Select() latency and throughput: a single
// goroutine, no concurrent callers. Unselect always completes (synchronously, with the timer
// paused) before the next Select starts, so no iteration can ever race a prior iteration's
// still-in-flight unlock. That in-flight race is exactly what a fire-and-forget
// `go s.selector.Unselect(ids...)` produced here before: each iteration re-scanned the same
// ascending-by-amount token order (testutils.WarmupCache sorts it to mirror production's
// `ORDER BY amount ASC`), so a slow unlock let the next Select collide with the very token the
// previous iteration was still releasing — a self-inflicted repeat of the #2395 hot-token
// collision, not a property of the selector under test.
func BenchmarkSelectorSingle(b *testing.B) {
	settings := []Setting{
		{name: "sherdlock", clients: 1, tokens: testutils.NumTokensPerWallet, selectorProvider: NewSherdSelector, lockProvider: NewNoLocker},
		{name: "sherdlock+lock", clients: 1, tokens: testutils.NumTokensPerWallet, selectorProvider: NewSherdSelector, lockProvider: NewLocker},
		// Fetcher strategy variants: same lazy-fetcher workload, swapped for the eager/cached
		// snapshot (sherdlock.Cached) and the try-eager-then-lazy hybrid (sherdlock.Mixed), so
		// this benchmark can compare all three FetcherStrategy code paths head to head.
		{name: "sherdlock+cached", clients: 1, tokens: testutils.NumTokensPerWallet, selectorProvider: NewCachedSherdSelector, lockProvider: NewNoLocker},
		{name: "sherdlock+mixed", clients: 1, tokens: testutils.NumTokensPerWallet, selectorProvider: NewMixedSherdSelector, lockProvider: NewNoLocker},
		// Exercises selectInternal's batch-locking branch (see benchBatchLocker) instead of the
		// single-token Lock path every other sherdlock setting above uses.
		{name: "sherdlock+batchlock", clients: 1, tokens: testutils.NumTokensPerWallet, selectorProvider: NewBatchLockSherdSelector, lockProvider: NewNoLocker},
		{name: "selector+nolock", clients: 1, tokens: testutils.NumTokensPerWallet, selectorProvider: NewSelector, lockProvider: NewNoLocker},
		{name: "selector+lock", clients: 1, tokens: testutils.NumTokensPerWallet, selectorProvider: NewSelector, lockProvider: NewLocker},
	}

	for _, s := range settings {
		setup(&s)
		b.ResetTimer()
		b.Run(s.name, func(b *testing.B) {
			var contended int
			for range b.N {
				ids, _, err := s.selector.Select(b.Context(), s.filter, testutils.SelectQuantity, testutils.TokenType)
				if isContended, isUnexpected := classifyOutcome(err); isUnexpected {
					b.Fatalf("unexpected error: %v", err)
				} else if isContended {
					contended++

					continue
				}

				// Release synchronously so the next iteration never contends against this
				// iteration's own tokens. Excluded from the timed selection latency, since
				// unlock cost is not what this benchmark measures.
				b.StopTimer()
				s.selector.Unselect(ids...)
				b.StartTimer()
			}
			b.StopTimer()
			b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "selects/sec")
			if contended > 0 {
				b.Logf("%d/%d selects hit contention (locked/insufficient funds)", contended, b.N)
			}
			reportSherdlockMetrics(b, s.selector.BenchMetrics())
		})
		cleanup(&s)
	}
}

// BenchmarkSelectorParallel measures throughput and per-op latency under concurrent callers.
// Note that b.SetParallelism(p) runs p*GOMAXPROCS goroutines (testing.B docs), so even the
// "clients: 1" settings below are actually GOMAXPROCS-way concurrent on this machine, not
// single-threaded: use BenchmarkSelectorSingle for a genuinely uncontended baseline.
//
// Unselect is synchronous here too (see BenchmarkSelectorSingle's doc comment for why the
// former fire-and-forget goroutine was a correctness bug, not just an unideal measurement), so
// ns/op is the full select+release round trip and reflects sustainable throughput under
// sustained load, not just selection latency.
func BenchmarkSelectorParallel(b *testing.B) {
	settings := []Setting{
		{name: "sherdlock", clients: 1, tokens: testutils.NumTokensPerWallet, selectorProvider: NewSherdSelector, lockProvider: NewNoLocker},
		{name: "sherdlock+lock", clients: 1, tokens: testutils.NumTokensPerWallet, selectorProvider: NewSherdSelector, lockProvider: NewLocker},
		{name: "sherdlock+lock+parallelism", clients: 10, tokens: 10 * testutils.NumTokensPerWallet, selectorProvider: NewSherdSelector, lockProvider: NewLocker},
		// No-backoff contention: mirrors production's Selector (not StubbornSelector) hitting a
		// hot, mostly-locked wallet. Expected to report a non-trivial contention rate, not zero.
		{name: "sherdlock+lock+contention", clients: 8, tokens: testutils.NumTokensPerWallet / 1000, selectorProvider: NewSherdSelector, lockProvider: NewLocker},
		// Same contention profile, but through the backoff-retrying StubbornSelector that
		// production actually wires up (sherdlock/manager.go always passes backoff > 0). This is
		// the gap the no-backoff case above cannot cover: whether retrying resolves contention
		// that an immediate attempt reports as locked/insufficient funds.
		{name: "sherdlock+lock+contention+stubborn", clients: 8, tokens: testutils.NumTokensPerWallet / 1000, selectorProvider: NewStubbornSherdSelector, lockProvider: NewLocker},
		// Batch-locking under the same hot-wallet contention profile as
		// "sherdlock+lock+contention", to compare claiming a covering window of candidates per
		// round trip against the single-lock path there.
		{name: "sherdlock+batchlock+contention", clients: 8, tokens: testutils.NumTokensPerWallet / 1000, selectorProvider: NewBatchLockSherdSelector, lockProvider: NewLocker},
		{name: "selector+nolock", clients: 1, tokens: testutils.NumTokensPerWallet, selectorProvider: NewSelector, lockProvider: NewNoLocker},
		{name: "selector+lock", clients: 1, tokens: testutils.NumTokensPerWallet, selectorProvider: NewSelector, lockProvider: NewLocker},
	}

	for _, s := range settings {
		setup(&s)
		b.ResetTimer()
		b.Run(s.name, func(b *testing.B) {
			var contended, unexpectedCount int32
			b.SetParallelism(s.clients)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					ids, _, err := s.selector.Select(b.Context(), s.filter, testutils.SelectQuantity, testutils.TokenType)
					if isContended, isUnexpected := classifyOutcome(err); isUnexpected {
						atomic.AddInt32(&unexpectedCount, 1)
						b.Errorf("unexpected error: %v", err)

						continue
					} else if isContended {
						atomic.AddInt32(&contended, 1)

						continue
					}
					s.selector.Unselect(ids...)
				}
			})
			b.StopTimer()
			b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "selects/sec")
			if contended > 0 {
				b.Logf("%d/%d selects hit contention (locked/insufficient funds)", contended, b.N)
			}
			reportSherdlockMetrics(b, s.selector.BenchMetrics())
		})
		cleanup(&s)
	}
}

func setup(s *Setting) {
	walletID := "wallet0"
	walletOwner := []byte(walletID)

	walletIDByRawIdentity := func(rawIdentity []byte) string {
		return string(rawIdentity)
	}

	s.filter = &testutils.TokenFilter{
		Wallet:   walletOwner,
		WalletID: walletID,
	}

	// populate walletOwner
	qs := testutils.NewMockQueryService()
	for i := range s.tokens {
		q := token2.NewOneQuantity(testutils.TokenQuantityPrecision)
		t := &token2.UnspentToken{
			Id:       token2.ID{TxId: strconv.Itoa(i), Index: 0},
			Owner:    walletOwner,
			Type:     testutils.TokenType,
			Quantity: q.Decimal(),
		}

		k := fmt.Sprintf("etoken.%s.%s.%s.%d", string(walletOwner), testutils.TokenType, t.Id.TxId, t.Id.Index)
		qs.Add(k, t)
	}

	// create lockCache to mimic efficient range queries
	qs.WarmupCache(walletID, testutils.TokenType)
	s.selector, s.cleanup = s.selectorProvider(qs, walletIDByRawIdentity, s.lockProvider())

	runtime.GC()
}

func cleanup(s *Setting) {
	if s.cleanup != nil {
		s.cleanup()
	}
}

func NewSelector(qs *testutils.MockQueryService, walletIDByRawIdentity WalletIDByRawIdentityFunc, lock selector.Locker) (ExtendedSelector, CleanupFunction) {
	qf := func() selector.QueryService {
		return qs
	}

	s, _ := selector.NewManager(lock, qf, testutils.SelectorNumRetries, testutils.SelectorTimeout, false, testutils.TokenQuantityPrecision).NewSelector(testutils.TxID)

	return &extendedSelector{
		Selector: s,
		Lock:     lock,
	}, nil
}

func NewSherdSelector(qs *testutils.MockQueryService, _ WalletIDByRawIdentityFunc, lock selector.Locker) (ExtendedSelector, CleanupFunction) {
	mp := newBenchMetricsProvider()

	return &extendedSelector{
		Selector: sherdlock.NewSherdSelector(
			testutils.TxID,
			sherdlock.NewLazyFetcher(qs),
			inmemory2.NewLocker(lock),
			testutils.TokenQuantityPrecision,
			sherdlock.NoBackoff,
			testutils.SelectorNumRetries,
			sherdlock.NewMetrics(mp),
		),
		Lock:    nil,
		Metrics: mp,
	}, nil
}

// NewStubbornSherdSelector wires the same lazy sherdlock stack as NewSherdSelector, but through
// StubbornSelector (backoff = benchBackoff) instead of NoBackoff, matching what
// sherdlock/manager.go actually constructs in production. NewSherdSelector's hardcoded
// sherdlock.NoBackoff means none of the settings using it ever exercise the retry-with-backoff
// path that real deployments rely on to ride out contention.
func NewStubbornSherdSelector(qs *testutils.MockQueryService, _ WalletIDByRawIdentityFunc, lock selector.Locker) (ExtendedSelector, CleanupFunction) {
	mp := newBenchMetricsProvider()

	return &extendedSelector{
		Selector: sherdlock.NewSherdSelector(
			testutils.TxID,
			sherdlock.NewLazyFetcher(qs),
			inmemory2.NewLocker(lock),
			testutils.TokenQuantityPrecision,
			benchBackoff,
			testutils.SelectorNumRetries,
			sherdlock.NewMetrics(mp),
		),
		Lock:    nil,
		Metrics: mp,
	}, nil
}

// NewCachedSherdSelector wires sherdlock's "eager"/cached fetcher strategy (sherdlock.Cached,
// backed by NewCachedFetcher: a periodically refreshed snapshot of the whole wallet) instead of
// the lazy, on-demand fetcher every other provider in this file uses. Production selects this
// strategy via sherdlock.NewFetcherProvider(..., sherdlock.Cached, ...); exercising it here is
// the only way this benchmark can compare "refetch on every cache miss" against "serve from a
// periodically refreshed snapshot" for the same selection workload.
func NewCachedSherdSelector(qs *testutils.MockQueryService, _ WalletIDByRawIdentityFunc, lock selector.Locker) (ExtendedSelector, CleanupFunction) {
	mp := newBenchMetricsProvider()
	m := sherdlock.NewMetrics(mp)

	return &extendedSelector{
		Selector: sherdlock.NewSherdSelector(
			testutils.TxID,
			sherdlock.NewCachedFetcher(qs, 0, 0, 0),
			inmemory2.NewLocker(lock),
			testutils.TokenQuantityPrecision,
			sherdlock.NoBackoff,
			testutils.SelectorNumRetries,
			m,
		),
		Lock:    nil,
		Metrics: mp,
	}, nil
}

// NewMixedSherdSelector wires sherdlock's "mixed" fetcher strategy (sherdlock.Mixed): try the
// eager/cached snapshot first, fall back to the lazy fetcher only if it comes back empty. This
// is the strategy sherdlock/fetcher.go's fetchers map actually registers for FetcherStrategy
// "mixed" - none of this file's other providers exercise it.
func NewMixedSherdSelector(qs *testutils.MockQueryService, _ WalletIDByRawIdentityFunc, lock selector.Locker) (ExtendedSelector, CleanupFunction) {
	mp := newBenchMetricsProvider()
	m := sherdlock.NewMetrics(mp)

	return &extendedSelector{
		Selector: sherdlock.NewSherdSelector(
			testutils.TxID,
			sherdlock.NewMixedFetcher(qs, m, 0, 0, 0),
			inmemory2.NewLocker(lock),
			testutils.TokenQuantityPrecision,
			sherdlock.NoBackoff,
			testutils.SelectorNumRetries,
			m,
		),
		Lock:    nil,
		Metrics: mp,
	}, nil
}

// benchBatchLocker adds sherdlock.BatchLocker to any sherdlock.Locker by looping single-token
// Lock calls under one call. Every production BatchLocker implementation is Postgres-specific
// (token/services/storage/db/sql/postgres/tokenlock.go, #2395 Phase 6), so without this wrapper
// this in-memory benchmark could never exercise selectInternal's batch-locking branch (see
// selector.go's `batchLocker, supportsBatch := s.locker.(BatchTokenLocker)`) at all.
type benchBatchLocker struct {
	sherdlock.Locker
}

func (l *benchBatchLocker) LockBatch(ctx context.Context, tokenIDs []*token2.ID, consumerTxID, walletID string) ([]*token2.ID, error) {
	won := make([]*token2.ID, 0, len(tokenIDs))
	for _, id := range tokenIDs {
		if err := l.Lock(ctx, id, consumerTxID, walletID); err == nil {
			won = append(won, id)
		}
	}

	return won, nil
}

// NewBatchLockSherdSelector wires the lazy sherdlock stack through benchBatchLocker, so the
// benchmark can measure selectInternal's batch-locking branch - claiming a covering window of
// candidates per round trip instead of one token at a time - against the plain single-lock path
// NewSherdSelector exercises, under the same contention profile.
func NewBatchLockSherdSelector(qs *testutils.MockQueryService, _ WalletIDByRawIdentityFunc, lock selector.Locker) (ExtendedSelector, CleanupFunction) {
	mp := newBenchMetricsProvider()

	return &extendedSelector{
		Selector: sherdlock.NewSherdSelector(
			testutils.TxID,
			sherdlock.NewLazyFetcher(qs),
			&benchBatchLocker{Locker: inmemory2.NewLocker(lock)},
			testutils.TokenQuantityPrecision,
			sherdlock.NoBackoff,
			testutils.SelectorNumRetries,
			sherdlock.NewMetrics(mp),
		),
		Lock:    nil,
		Metrics: mp,
	}, nil
}

type Setting struct {
	name             string
	clients          int
	tokens           int
	selectorProvider SelectorProviderFunction
	lockProvider     LockerProviderFunction
	selector         ExtendedSelector
	cleanup          CleanupFunction
	filter           token.OwnerFilter
}

type CleanupFunction func()
type SelectorProviderFunction func(qs *testutils.MockQueryService, walletIDByRawIdentity WalletIDByRawIdentityFunc, lock selector.Locker) (ExtendedSelector, CleanupFunction)
type LockerProviderFunction func() selector.Locker

type ExtendedSelector interface {
	token.Selector
	Unselect(id ...*token2.ID)
	BenchMetrics() *benchMetricsProvider
}

type MockTokenIterator struct {
	*testutils.MockQueryService
	*testutils.NoLock
}

func NewLocker() selector.Locker {
	return inmemory.NewLocker(&testutils.MockVault{}, testutils.LockSleepTimeout, testutils.LockValidTxEvictionTimeout)
}

func NewNoLocker() selector.Locker {
	return &testutils.NoLock{}
}
