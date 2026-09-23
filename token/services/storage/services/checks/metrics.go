/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package checks

import (
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
)

// Outcome values for the SweepsTotal counter.
const (
	// outcomeCompleted is a sweep that ran the checks and recorded their findings.
	outcomeCompleted = "completed"
	// outcomeNotLeader is a sweep another replica was already running.
	outcomeNotLeader = "not_leader"
	// outcomeFailed is a sweep that could not record what it found.
	outcomeFailed = "failed"
)

// Metrics holds the instrumentation of the ledger drift checks sweep.
//
// Every metric carries a "role" label naming the store being swept, "owner" or
// "auditor", because a node runs one sweep per store and the two look for
// different things.
type Metrics struct {
	// SweepDuration is a histogram of how long a sweep takes, in seconds. A sweep
	// approaching the configured timeout is the signal to narrow
	// TransactionWindow or lengthen ScanInterval.
	SweepDuration metrics.Histogram

	// SweepsTotal counts sweeps by outcome: completed, not_leader, or failed. A
	// node that only ever reports not_leader is not the one doing the checking,
	// which is the intended behaviour with several replicas on one database.
	SweepsTotal metrics.Counter

	// FindingsObserved counts findings as they are reported, labeled by the check
	// that produced them, their code and their severity. It counts sightings, not
	// distinct problems: one problem seen by ten sweeps adds ten.
	FindingsObserved metrics.Counter

	// FindingsOpen is the number of findings the latest sweep reported, by
	// severity. Unlike FindingsObserved this is a level, so it drops back to zero
	// once a problem stops being reported. This is the one to alert on.
	FindingsOpen metrics.Gauge

	// FindingsResolved counts stored findings closed because a sweep stopped
	// reporting them.
	FindingsResolved metrics.Counter
}

// newMetrics creates the checks instrumentation. A nil provider discards
// everything, so a caller with no metrics configured needs no special case.
func newMetrics(p metrics.Provider) *Metrics {
	if p == nil {
		p = &disabled.Provider{}
	}

	return &Metrics{
		SweepDuration: p.NewHistogram(metrics.HistogramOpts{
			Name:                           "storage_checks_sweep_duration_seconds",
			Help:                           "Histogram of the wall-clock time of one ledger drift checks sweep, in seconds",
			LabelNames:                     []string{"network", "channel", "namespace", "role"},
			Buckets:                        []float64{.1, .5, 1, 5, 15, 30, 60, 300, 600, 1800},
			NativeHistogramBucketFactor:    1.1,
			NativeHistogramMaxBucketNumber: 100,
		}),
		SweepsTotal: p.NewCounter(metrics.CounterOpts{
			Name:       "storage_checks_sweeps_total",
			Help:       "Total number of ledger drift checks sweeps by outcome (completed, not_leader, failed)",
			LabelNames: []string{"network", "channel", "namespace", "role", "outcome"},
		}),
		FindingsObserved: p.NewCounter(metrics.CounterOpts{
			Name:       "storage_checks_findings_observed_total",
			Help:       "Total number of ledger drift findings reported, by check, code and severity",
			LabelNames: []string{"network", "channel", "namespace", "role", "checker", "code", "severity"},
		}),
		FindingsOpen: p.NewGauge(metrics.GaugeOpts{
			Name:       "storage_checks_findings_open",
			Help:       "Number of ledger drift findings reported by the latest sweep, by severity",
			LabelNames: []string{"network", "channel", "namespace", "role", "severity"},
		}),
		FindingsResolved: p.NewCounter(metrics.CounterOpts{
			Name:       "storage_checks_findings_resolved_total",
			Help:       "Total number of stored ledger drift findings closed because a sweep stopped reporting them",
			LabelNames: []string{"network", "channel", "namespace", "role"},
		}),
	}
}

// NewMetrics creates the checks instrumentation with the given provider.
func NewMetrics(p metrics.Provider) *Metrics {
	return newMetrics(p)
}
