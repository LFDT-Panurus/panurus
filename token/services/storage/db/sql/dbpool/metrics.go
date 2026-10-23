/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbpool

import (
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
)

// poolLabel is the variable label every metric in this package carries,
// identifying the underlying data source (host:port/database), not the
// Panurus store that happens to read or write through it: every store
// sharing a data source shares one *sql.DB pool - see Sampler.Register - and
// a per-store label would register several identical series for one pool.
const poolLabel = "pool"

// Metrics holds the instrumentation of a Sampler. Unlike most Panurus metrics
// families, these are exported under an explicitly set Namespace/Subsystem
// rather than letting the calling package derive the name - see "How a metric
// name is built" and "Metric hygiene" in docs/development/metrics.md. This is
// the first Panurus metric family to do so, a deliberate exception rather
// than an oversight: it is the fix the Metric hygiene section recommends for
// new metrics, and there is no existing dashboard or alert to break.
type Metrics struct {
	// InUse is the number of connections currently checked out of the pool,
	// sampled instantaneously (not a max-since-last-sample - see Sampler).
	// Compare against MaxOpen for saturation; a value pinned at MaxOpen
	// together with a rising WaitCount is the pool-exhaustion signature that
	// made #2474 need a goroutine dump to diagnose.
	InUse metrics.Gauge
	// MaxOpen is the pool's configured connection limit (sql.DBStats.MaxOpenConnections),
	// exported so dashboards can compute saturation (InUse/MaxOpen) rather than
	// an absolute connection count that means nothing without cross-referencing
	// the node's core.yaml.
	MaxOpen metrics.Gauge
	// WaitCountTotal is the cumulative number of connection checkouts that had
	// to wait because the pool was at its limit. Unlike InUse this is
	// loss-free between samples: database/sql increments it once per blocked
	// checkout regardless of how often it is scraped, so a burst too short
	// for the gauge to catch still shows up here.
	WaitCountTotal metrics.Counter
	// WaitSecondsTotal is the cumulative time callers spent waiting for a
	// connection. Divided by WaitCountTotal (as a rate ratio, not a raw
	// division, since both are counters) it gives the mean wait per checkout:
	// rate(wait_seconds_total[$__rate_interval]) / rate(wait_count_total[$__rate_interval]).
	WaitSecondsTotal metrics.Counter
}

// NewMetrics creates the connection-pool instrumentation. A nil provider
// discards everything, so a Sampler built with no metrics configured needs no
// special case.
func NewMetrics(p metrics.Provider) *Metrics {
	if p == nil {
		p = &disabled.Provider{}
	}

	return &Metrics{
		InUse: p.NewGauge(metrics.GaugeOpts{
			Namespace:  "panurus_storage",
			Subsystem:  "db_pool",
			Name:       "in_use",
			Help:       "Number of connections currently checked out of the pool",
			LabelNames: []string{poolLabel},
		}),
		MaxOpen: p.NewGauge(metrics.GaugeOpts{
			Namespace:  "panurus_storage",
			Subsystem:  "db_pool",
			Name:       "max_open",
			Help:       "Configured maximum number of open connections for the pool",
			LabelNames: []string{poolLabel},
		}),
		WaitCountTotal: p.NewCounter(metrics.CounterOpts{
			Namespace:  "panurus_storage",
			Subsystem:  "db_pool",
			Name:       "wait_count_total",
			Help:       "Total number of connection checkouts that had to wait because the pool was at its connection limit",
			LabelNames: []string{poolLabel},
		}),
		WaitSecondsTotal: p.NewCounter(metrics.CounterOpts{
			Namespace:  "panurus_storage",
			Subsystem:  "db_pool",
			Name:       "wait_seconds_total",
			Help:       "Total time, in seconds, callers spent waiting for a connection to become available",
			LabelNames: []string{poolLabel},
		}),
	}
}
