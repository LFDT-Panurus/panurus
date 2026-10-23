/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbpool

import (
	"database/sql"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	driver3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

var logger = logging.MustGetLogger()

// pool is one registered *sql.DB together with the previous cumulative
// sql.DBStats values the sampler has already turned into counter deltas.
//
// statsFn reads the current stats and defaults to db.Stats; tests override it
// to drive Sample() against a scripted sql.DBStats sequence without needing a
// real pool to reach a particular state (a real pool is exercised separately,
// see TestSamplerAgainstRealSQLitePool).
type pool struct {
	label            string
	db               *sql.DB
	statsFn          func() sql.DBStats
	lastWaitCount    int64
	lastWaitDuration time.Duration
}

// Sampler periodically reads (*sql.DB).Stats() for every registered pool and
// reports it through Metrics.
//
// It exists because Panurus never checks out a connection itself - the pool
// lives inside fabric-smart-client's postgres.Open and reaches Panurus only
// as a *sql.DB - so (*sql.DB).Stats() is the only available signal, and
// metrics.Provider has no way to register a custom collector that would be
// evaluated at scrape time. Something has to poll.
//
// The InUse and MaxOpen gauges are deliberately instantaneous, not a
// max-since-last-sample: sampling at DefaultPoolMetricsInterval (1s) makes a
// sustained exhaustion episode - the kind that actually hangs Select calls,
// as in #2474 - visible on most samples regardless, and a transient burst too
// short to land on a sample still shows up in WaitCountTotal, which
// database/sql increments once per blocked checkout independent of how often
// anything scrapes it. Peak occupancy is a PromQL concern
// (max_over_time(db_pool_in_use[...])), not something the sampler itself
// needs to track. Do not raise the interval back toward something like 10s
// without re-reading this: at a slower poll the gauge can sit well below
// MaxOpen throughout an episode that was in fact permanently exhausted
// between samples.
type Sampler struct {
	metrics  *Metrics
	interval time.Duration

	mu    sync.Mutex
	pools map[string]*pool // keyed by the raw data source, not by label

	stop   chan struct{}
	done   chan struct{}
	ticker *time.Ticker
}

// NewSampler builds a Sampler from cfg's token.storage.db.poolMetricsInterval
// (DefaultPoolMetricsInterval when unset) and p. A nil p (or a cfg that fails
// to parse) does not prevent construction - see Start, which no-ops when the
// interval is 0, and NewMetrics's disabled-provider fallback.
func NewSampler(cfg driver3.Config, p metrics.Provider) *Sampler {
	storageConfig, err := common2.LoadStorageConfig(cfg)
	if err != nil {
		logger.Warnf("failed to load storage config for the connection-pool sampler: %v — using default interval", err)
		storageConfig.PoolMetricsInterval = common2.DefaultPoolMetricsInterval
	}

	return &Sampler{
		metrics:  NewMetrics(p),
		interval: storageConfig.PoolMetricsInterval,
		pools:    map[string]*pool{},
	}
}

// Register adds db to the set of pools this sampler reports on, keyed by key
// (the pool's identity - callers pass the raw data source, since every store
// sharing a data source shares one *sql.DB and must be registered under one
// key) with label as the exported "pool" label value. Registering the same
// key again is a no-op: N stores on one data source must produce one series,
// not N.
func (s *Sampler) Register(key, label string, db *sql.DB) {
	if db == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pools[key]; ok {
		return
	}
	s.pools[key] = &pool{label: label, db: db, statsFn: db.Stats}
}

// Sample reads Stats() for every registered pool once and updates Metrics.
// Exported so tests can drive a deterministic pass without waiting on the
// ticker; Start calls it on the same interval it reports.
func (s *Sampler) Sample() {
	s.mu.Lock()
	pools := make([]*pool, 0, len(s.pools))
	for _, p := range s.pools {
		pools = append(pools, p)
	}
	s.mu.Unlock()

	for _, p := range pools {
		stats := p.statsFn()

		s.metrics.InUse.With(poolLabel, p.label).Set(float64(stats.InUse))
		s.metrics.MaxOpen.With(poolLabel, p.label).Set(float64(stats.MaxOpenConnections))

		// WaitCount/WaitDuration are cumulative totals from database/sql, and
		// metrics.Counter only exposes Add(delta), so the delta since the last
		// sample is what gets added. A negative delta means the *sql.DB was
		// replaced or reset under this key between samples (not expected in
		// practice, since Register never overwrites an existing entry) rather
		// than time running backwards; it is dropped instead of making the
		// exported counter decrease, which Prometheus counters must never do.
		s.mu.Lock()
		waitCountDelta := stats.WaitCount - p.lastWaitCount
		waitDurationDelta := stats.WaitDuration - p.lastWaitDuration
		if waitCountDelta >= 0 {
			p.lastWaitCount = stats.WaitCount
		}
		if waitDurationDelta >= 0 {
			p.lastWaitDuration = stats.WaitDuration
		}
		s.mu.Unlock()

		if waitCountDelta > 0 {
			s.metrics.WaitCountTotal.With(poolLabel, p.label).Add(float64(waitCountDelta))
		}
		if waitDurationDelta > 0 {
			s.metrics.WaitSecondsTotal.With(poolLabel, p.label).Add(waitDurationDelta.Seconds())
		}
	}
}

// Start begins the sampling loop. A zero interval disables the sampler: Start
// returns nil without starting a goroutine, so an operator who does not
// scrape Prometheus pays nothing for it. Calling Start twice is a no-op.
func (s *Sampler) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.interval <= 0 || s.ticker != nil {
		return nil
	}

	s.ticker = time.NewTicker(s.interval)
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	ticker := s.ticker
	stop := s.stop
	done := s.done
	go func() {
		defer close(done)
		for {
			select {
			case <-ticker.C:
				s.Sample()
			case <-stop:
				return
			}
		}
	}()

	return nil
}

// Stop ends the sampling loop and waits for its goroutine to exit, so no
// sampler outlives an in-process SDK restart - the same reason SDK.Stop
// already stops the ledger drift checks sweeps (token/sdk/dig/sdk.go).
// Calling Stop before Start, or twice, is a no-op.
func (s *Sampler) Stop() error {
	s.mu.Lock()
	if s.ticker == nil {
		s.mu.Unlock()

		return nil
	}
	ticker := s.ticker
	stop := s.stop
	done := s.done
	s.ticker = nil
	s.mu.Unlock()

	close(stop)
	ticker.Stop()
	<-done

	return nil
}
