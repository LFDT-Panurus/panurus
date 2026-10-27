/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ratelimit

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorder is a metrics.Provider remembering what was reported.
type recorder struct {
	mu     sync.Mutex
	denied map[string]float64
	gauge  float64
	opts   []metrics.CounterOpts
}

func newRecorder() *recorder { return &recorder{denied: map[string]float64{}} }

func (r *recorder) NewCounter(o metrics.CounterOpts) metrics.Counter {
	r.opts = append(r.opts, o)

	return &recCounter{r: r}
}
func (r *recorder) NewGauge(metrics.GaugeOpts) metrics.Gauge { return &recGauge{r: r} }
func (r *recorder) NewHistogram(metrics.HistogramOpts) metrics.Histogram {
	panic("unexpected histogram")
}

func (r *recorder) deniedFor(scope string) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.denied[scope]
}

func (r *recorder) buckets() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.gauge
}

type recCounter struct {
	r     *recorder
	scope string
}

func (c *recCounter) With(kv ...string) metrics.Counter {
	if len(kv) != 2 || kv[0] != "tms" {
		panic("unexpected labels")
	}

	return &recCounter{r: c.r, scope: kv[1]}
}

func (c *recCounter) Add(v float64) {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()
	c.r.denied[c.scope] += v
}

type recGauge struct{ r *recorder }

func (g *recGauge) With(...string) metrics.Gauge { return g }
func (g *recGauge) Add(float64)                  {}
func (g *recGauge) Set(v float64) {
	g.r.mu.Lock()
	defer g.r.mu.Unlock()
	g.r.gauge = v
}

func TestMetrics_DeniedIsCountedPerScope(t *testing.T) {
	rec := newRecorder()
	l := New(Config{Rate: 0.001, Burst: 1, Metrics: rec})

	require.NoError(t, l.Allow(context.Background(), "a", "w"))
	require.Error(t, l.Allow(context.Background(), "a", "w"))
	require.Error(t, l.Allow(context.Background(), "a", "w"))
	require.NoError(t, l.Allow(context.Background(), "b", "w"))
	require.Error(t, l.Allow(context.Background(), "b", "w"))
	require.NoError(t, l.Allow(context.Background(), "b", ""))

	assert.InDelta(t, 2, rec.deniedFor("a"), 0)
	assert.InDelta(t, 1, rec.deniedFor("b"), 0)
	require.Len(t, rec.opts, 1)
	assert.Equal(t, []string{"tms"}, rec.opts[0].LabelNames)
}

func TestMetrics_BucketsActiveTracksCount(t *testing.T) {
	rec := newRecorder()
	clock := &fakeClock{now: time.Unix(1000, 0)}
	l := New(Config{Rate: 1, Burst: 1, MaxBuckets: 3, IdleTimeout: time.Minute, Metrics: rec})
	l.now = clock.Now

	for i := range 3 {
		require.NoError(t, l.Allow(context.Background(), "s", strconv.Itoa(i)))
		assert.InDelta(t, float64(i+1), rec.buckets(), 0)
	}

	// At the cap: a new wallet evicts one, the count must stay at MaxBuckets.
	require.NoError(t, l.Allow(context.Background(), "s", "new"))
	assert.InDelta(t, 3, rec.buckets(), 0)
	assert.Equal(t, l.BucketCount(), int(rec.buckets()))

	// Idle sweep on the way to a new bucket.
	clock.Advance(2 * time.Hour)
	require.NoError(t, l.Allow(context.Background(), "s", "later"))
	assert.InDelta(t, 1, rec.buckets(), 0)

	l.Stop()
	assert.InDelta(t, 0, rec.buckets(), 0)
}

func TestMetrics_DenialAndEvictionAtCapPublishCount(t *testing.T) {
	rec := newRecorder()
	// A new bucket always starts full, so a request creating one is never denied: check that a
	// denial and an eviction at the cap leave the gauge at the real count.
	l := New(Config{Rate: 0.001, Burst: 1, MaxBuckets: 2, Metrics: rec})
	require.NoError(t, l.Allow(context.Background(), "s", "a"))
	require.Error(t, l.Allow(context.Background(), "s", "a"))
	require.NoError(t, l.Allow(context.Background(), "s", "b"))
	require.NoError(t, l.Allow(context.Background(), "s", "c"))
	assert.InDelta(t, 2, rec.buckets(), 0)
}

func TestMetrics_NilProviderDoesNotPanic(t *testing.T) {
	l := New(Config{Rate: 0.001, Burst: 1})
	require.NoError(t, l.Allow(context.Background(), "s", "w"))
	require.Error(t, l.Allow(context.Background(), "s", "w"))
	l.Stop()

	assert.NotNil(t, NewMetrics(nil))
}

func TestMetrics_ConcurrentAllow(t *testing.T) {
	rec := newRecorder()
	l := New(Config{Rate: 0.001, Burst: 5, Metrics: rec})

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 10 {
				_ = l.Allow(context.Background(), "s", "w")
			}
		})
	}
	wg.Wait()

	assert.InDelta(t, 195, rec.deniedFor("s"), 0)
	assert.InDelta(t, 1, rec.buckets(), 0)
}
