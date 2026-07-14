/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
)

// Metrics holds the instrumentation for the finality Listener.
type Metrics struct {
	// ConfirmedTransactions counts transactions whose ledger status resolved to
	// Valid and whose token-request hash matched, i.e. fully committed.
	ConfirmedTransactions metrics.Counter

	// DeletedTransactions counts transactions whose ledger status resolved to
	// Invalid, or whose token-request hash did not match (integrity failure).
	DeletedTransactions metrics.Counter

	// HashMismatches counts the subset of deleted transactions that were
	// invalidated specifically because the committed token-request hash did not
	// match the locally stored one. Non-zero values indicate a data-integrity
	// violation worth alerting on.
	HashMismatches metrics.Counter

	// RetryExhausted counts calls to OnError, i.e. how many transactions had
	// their finality processing abandoned after all retries were exhausted.
	RetryExhausted metrics.Counter

	// OnStatusDuration is a histogram of the total wall-clock time for each
	// OnStatus invocation (including any retries), measured in seconds.
	OnStatusDuration metrics.Histogram
}

func newMetrics(p metrics.Provider) *Metrics {
	if p == nil {
		p = &noopProvider{}
	}

	return &Metrics{
		ConfirmedTransactions: p.NewCounter(metrics.CounterOpts{
			Name: "finality_listener_confirmed_total",
			Help: "Total number of transactions confirmed on the ledger and successfully committed to local storage",
		}),
		DeletedTransactions: p.NewCounter(metrics.CounterOpts{
			Name: "finality_listener_deleted_total",
			Help: "Total number of transactions marked as deleted due to an invalid ledger status or token-request hash mismatch",
		}),
		HashMismatches: p.NewCounter(metrics.CounterOpts{
			Name: "finality_listener_hash_mismatch_total",
			Help: "Total number of transactions rejected because the committed token-request hash did not match the locally stored one",
		}),
		RetryExhausted: p.NewCounter(metrics.CounterOpts{
			Name: "finality_listener_retry_exhausted_total",
			Help: "Total number of transactions whose finality processing was abandoned after all retries were exhausted",
		}),
		OnStatusDuration: p.NewHistogram(metrics.HistogramOpts{
			Name:                           "finality_listener_on_status_duration_seconds",
			Help:                           "Histogram of total OnStatus processing time per transaction (including retries), in seconds",
			Buckets:                        []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
			NativeHistogramBucketFactor:    1.1,
			NativeHistogramMaxBucketNumber: 100,
		}),
	}
}

// NewMetrics creates a new Metrics instance with the given provider.
// This is exported for use by other packages that need finality metrics.
func NewMetrics(p metrics.Provider) *Metrics {
	return newMetrics(p)
}

// noopProvider discards all observations. Used when no provider is configured.
type noopProvider struct{}

func (p *noopProvider) NewCounter(_ metrics.CounterOpts) metrics.Counter { return &noopCounter{} }
func (p *noopProvider) NewGauge(_ metrics.GaugeOpts) metrics.Gauge       { return &noopGauge{} }
func (p *noopProvider) NewHistogram(_ metrics.HistogramOpts) metrics.Histogram {
	return &noopHistogram{}
}

type noopCounter struct{}

func (c *noopCounter) With(_ ...string) metrics.Counter { return c }
func (c *noopCounter) Add(_ float64)                    {}

type noopGauge struct{}

func (g *noopGauge) With(_ ...string) metrics.Gauge { return g }
func (g *noopGauge) Add(_ float64)                  {}
func (g *noopGauge) Set(_ float64)                  {}

type noopHistogram struct{}

func (h *noopHistogram) With(_ ...string) metrics.Histogram { return h }
func (h *noopHistogram) Observe(_ float64)                  {}
