/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbpool

import (
	"database/sql"
	"path"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// recordingGauge/recordingCounter capture the (label values, value) pairs a
// Sampler reports through Metrics, so tests can assert on what was observed
// without a real Prometheus registry.
type recordingProvider struct {
	mu       sync.Mutex
	gauges   map[string]*recordingGauge
	counters map[string]*recordingCounter
}

func newRecordingProvider() *recordingProvider {
	return &recordingProvider{gauges: map[string]*recordingGauge{}, counters: map[string]*recordingCounter{}}
}

//nolint:ireturn // implements metrics.Provider; the interface fixes the return type
func (p *recordingProvider) NewGauge(o metrics.GaugeOpts) metrics.Gauge {
	p.mu.Lock()
	defer p.mu.Unlock()
	g := &recordingGauge{values: map[string]float64{}}
	p.gauges[o.Name] = g

	return g
}

func (p *recordingProvider) NewCounter(o metrics.CounterOpts) metrics.Counter {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := &recordingCounter{values: map[string]float64{}}
	p.counters[o.Name] = c

	return c
}

//nolint:ireturn // implements metrics.Provider; the interface fixes the return type
func (p *recordingProvider) NewHistogram(metrics.HistogramOpts) metrics.Histogram {
	panic("not used by dbpool")
}

func (p *recordingProvider) gauge(name, label string) (float64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	g, ok := p.gauges[name]
	if !ok {
		return 0, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	v, ok := g.values[label]

	return v, ok
}

func (p *recordingProvider) counterTotal(name, label string) (float64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.counters[name]
	if !ok {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.values[label]

	return v, ok
}

type recordingGauge struct {
	mu     sync.Mutex
	lvs    []string
	values map[string]float64
}

//nolint:ireturn // implements metrics.Gauge; the interface fixes the return type
func (g *recordingGauge) With(lvs ...string) metrics.Gauge {
	return &recordingGauge{lvs: lvs, values: g.values}
}
func (g *recordingGauge) Add(delta float64) { g.Set(g.values[labelKeyFor(g.lvs)] + delta) }
func (g *recordingGauge) Set(value float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.values[labelKeyFor(g.lvs)] = value
}

type recordingCounter struct {
	mu     sync.Mutex
	lvs    []string
	values map[string]float64
}

func (c *recordingCounter) With(lvs ...string) metrics.Counter {
	return &recordingCounter{lvs: lvs, values: c.values}
}
func (c *recordingCounter) Add(delta float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[labelKeyFor(c.lvs)] += delta
}

// labelKeyFor turns the "name1","value1",... pair list into the label value
// alone: dbpool only ever uses a single "pool" label, so the second element is
// the key tests index on.
func labelKeyFor(lvs []string) string {
	if len(lvs) < 2 {
		return ""
	}

	return lvs[1]
}

func TestSamplerDeltaAccounting(t *testing.T) {
	db, mock := newStatsStub(sql.DBStats{InUse: 2, MaxOpenConnections: 10, WaitCount: 3, WaitDuration: 300 * time.Millisecond})
	p := newRecordingProvider()
	s := &Sampler{metrics: NewMetrics(p), pools: map[string]*pool{}}
	registerStub(s, "dsn", "host:5432/db", db, mock)

	s.Sample()
	v, ok := p.gauge("in_use", "host:5432/db")
	require.True(t, ok)
	assert.InDelta(t, 2.0, v, 1e-9)
	wc, ok := p.counterTotal("wait_count_total", "host:5432/db")
	require.True(t, ok)
	assert.InDelta(t, 3.0, wc, 1e-9)
	ws, ok := p.counterTotal("wait_seconds_total", "host:5432/db")
	require.True(t, ok)
	assert.InDelta(t, 0.3, ws, 1e-9)

	// A second sample must add only the increment, not the new cumulative total.
	mock.set(sql.DBStats{InUse: 5, MaxOpenConnections: 10, WaitCount: 7, WaitDuration: 700 * time.Millisecond})
	s.Sample()
	wc, _ = p.counterTotal("wait_count_total", "host:5432/db")
	assert.InDelta(t, 7.0, wc, 1e-9, "delta of 4 added to the previous total of 3")
	ws, _ = p.counterTotal("wait_seconds_total", "host:5432/db")
	assert.InDelta(t, 0.7, ws, 1e-9)
	v, _ = p.gauge("in_use", "host:5432/db")
	assert.InDelta(t, 5.0, v, 1e-9, "gauge is instantaneous, not accumulated")
}

func TestSamplerNegativeDeltaDropped(t *testing.T) {
	db, mock := newStatsStub(sql.DBStats{WaitCount: 10, WaitDuration: time.Second})
	p := newRecordingProvider()
	s := &Sampler{metrics: NewMetrics(p), pools: map[string]*pool{}}
	registerStub(s, "dsn", "pool-a", db, mock)
	s.Sample()

	// Simulate the pool's cumulative counters going backwards (e.g. reset).
	mock.set(sql.DBStats{WaitCount: 2, WaitDuration: 200 * time.Millisecond})
	s.Sample()

	wc, _ := p.counterTotal("wait_count_total", "pool-a")
	assert.InDelta(t, 10.0, wc, 1e-9, "a negative delta must not be added, and must not decrease the exported counter")
}

func TestSamplerRegisterDedupesOnKey(t *testing.T) {
	db1, _ := newStatsStub(sql.DBStats{InUse: 1})
	db2, _ := newStatsStub(sql.DBStats{InUse: 99})
	s := &Sampler{metrics: NewMetrics(nil), pools: map[string]*pool{}}

	s.Register("same-dsn", "label-a", db1)
	s.Register("same-dsn", "label-b", db2)

	assert.Len(t, s.pools, 1)
	assert.Equal(t, "label-a", s.pools["same-dsn"].label, "the first registration under a key wins")
}

func TestNewMetricsNilProviderDoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		m := NewMetrics(nil)
		m.InUse.With(poolLabel, "x").Set(1)
		m.WaitCountTotal.With(poolLabel, "x").Add(1)
	})
}

// TestSamplerAgainstRealSQLitePool exercises (*sql.DB).Stats()'s real contract
// against a real pool, rather than only the injected-stats seam above: InUse
// tracks held connections, and a checkout blocked by SetMaxOpenConns advances
// WaitCount.
func TestSamplerAgainstRealSQLitePool(t *testing.T) {
	dsn := "file:" + path.Join(t.TempDir(), "pool.sqlite")
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(2)

	ctx := t.Context()
	c1, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c1.Close() })
	c2, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c2.Close() })

	assert.Equal(t, 2, db.Stats().InUse)

	blocked := make(chan struct{})
	go func() {
		close(blocked)
		c3, err := db.Conn(ctx)
		if err == nil {
			_ = c3.Close()
		}
	}()
	<-blocked

	require.Eventually(t, func() bool {
		return db.Stats().WaitCount > 0
	}, time.Second, 5*time.Millisecond, "a third checkout against MaxOpenConns=2 must block and increment WaitCount")

	p := newRecordingProvider()
	s := &Sampler{metrics: NewMetrics(p), pools: map[string]*pool{}}
	s.Register(dsn, "sqlite-pool", db)
	s.Sample()

	v, ok := p.gauge("in_use", "sqlite-pool")
	require.True(t, ok)
	assert.InDelta(t, 2.0, v, 1e-9)
	wc, ok := p.counterTotal("wait_count_total", "sqlite-pool")
	require.True(t, ok)
	assert.Positive(t, wc)

	require.NoError(t, c1.Close())
}

func TestSamplerStartStopIdempotentAndLeakFree(t *testing.T) {
	db, _ := newStatsStub(sql.DBStats{})
	s := &Sampler{metrics: NewMetrics(nil), interval: 5 * time.Millisecond, pools: map[string]*pool{}}
	s.Register("dsn", "pool", db)

	before := runtime.NumGoroutine()

	require.NoError(t, s.Start())
	require.NoError(t, s.Start(), "a second Start must be a no-op, not a second goroutine")

	// Give the ticker a chance to actually fire at least once.
	time.Sleep(20 * time.Millisecond)

	require.NoError(t, s.Stop())
	require.NoError(t, s.Stop(), "a second Stop must be a no-op")

	require.Eventually(t, func() bool {
		return runtime.NumGoroutine() <= before+1 // allow for scheduler noise
	}, time.Second, 10*time.Millisecond, "Stop must join the sampling goroutine, not leak it")
}

func TestSamplerZeroIntervalDisablesStart(t *testing.T) {
	s := &Sampler{metrics: NewMetrics(nil), interval: 0, pools: map[string]*pool{}}
	require.NoError(t, s.Start())
	assert.Nil(t, s.ticker, "a zero interval must not start a ticker/goroutine")
	require.NoError(t, s.Stop())
}

// statsStub lets a test script the sql.DBStats a pool reports, since Sample()
// reads it through pool.statsFn rather than always calling (*sql.DB).Stats()
// directly. db is a real (if otherwise idle) *sql.DB purely so pool.db is
// never nil; its own Stats() is not what the sampler sees here.
type statsStub struct {
	mu    sync.Mutex
	stats sql.DBStats
}

func (s *statsStub) set(stats sql.DBStats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats = stats
}

func (s *statsStub) current() sql.DBStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.stats
}

func newStatsStub(initial sql.DBStats) (*sql.DB, *statsStub) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		panic(err)
	}

	return db, &statsStub{stats: initial}
}

// registerStub registers db with s under key/label, and immediately swaps in
// stub's Stats() so Sample() is deterministic regardless of what the
// underlying (idle, otherwise-unused) *sql.DB would itself report.
func registerStub(s *Sampler, key, label string, db *sql.DB, stub *statsStub) {
	s.Register(key, label, db)
	s.mu.Lock()
	s.pools[key].statsFn = stub.current
	s.mu.Unlock()
}
