/*

Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package queue

import (
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
)

// Metrics holds the instrumentation for the EventQueue.
type Metrics struct {
	// PendingEvents is a gauge tracking how many events are currently waiting
	// in the queue buffer. Updated on every successful enqueue.
	PendingEvents metrics.Gauge
	// EnqueueDrops counts the total number of events dropped because the queue
	// was full at the time of a non-blocking Enqueue call.
	EnqueueDrops metrics.Counter
	// ProcessingErrors counts the total number of errors returned by
	// event.Process inside worker goroutines.
	ProcessingErrors metrics.Counter
	// ProcessingDuration is a histogram of successful event processing times in
	// worker goroutines, measured in seconds. Only recorded on success; error
	// paths are already counted by ProcessingErrors.
	ProcessingDuration metrics.Histogram
}

func newMetrics(p metrics.Provider) *Metrics {
	if p == nil {
		p = &noopProvider{}
	}

	return &Metrics{
		PendingEvents: p.NewGauge(metrics.GaugeOpts{
			Name: "finality_queue_pending_events",
			Help: "Current number of finality events waiting in the queue buffer",
		}),
		EnqueueDrops: p.NewCounter(metrics.CounterOpts{
			Name: "finality_queue_enqueue_drops_total",
			Help: "Total number of finality events dropped because the queue was full",
		}),
		ProcessingErrors: p.NewCounter(metrics.CounterOpts{
			Name: "finality_queue_processing_errors_total",
			Help: "Total number of errors returned by event.Process in worker goroutines",
		}),
		ProcessingDuration: p.NewHistogram(metrics.HistogramOpts{
			Name:                           "finality_queue_processing_duration_seconds",
			Help:                           "Histogram of successful event processing time in worker goroutines (seconds)",
			Buckets:                        []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
			NativeHistogramBucketFactor:    1.1,
			NativeHistogramMaxBucketNumber: 100,
		}),
	}
}

// noopProvider is a metrics.Provider that discards all observations.
// It is used when no provider is configured (e.g. in tests).
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
