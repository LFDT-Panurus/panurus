/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package interactive

import (
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
)

var (
	certifiedTokens = metrics.CounterOpts{
		Name:       "certified_tokens",
		Help:       "The number of tokens certified.",
		LabelNames: []string{"network", "channel", "namespace"},
	}

	certificationRequestDuration = metrics.HistogramOpts{
		Name:                           "certification_request_duration_seconds",
		Help:                           "Histogram of certification batch request durations in seconds.",
		Buckets:                        []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		LabelNames:                     []string{"channel", "namespace"},
		NativeHistogramBucketFactor:    1.1,
		NativeHistogramMaxBucketNumber: 100,
	}

	certificationErrors = metrics.CounterOpts{
		Name:       "certification_errors_total",
		Help:       "Total number of failed certification request attempts.",
		LabelNames: []string{"channel", "namespace"},
	}

	pendingTokens = metrics.GaugeOpts{
		Name:       "certification_pending_tokens",
		Help:       "Current number of tokens waiting in the certification input buffer.",
		LabelNames: []string{"channel", "namespace"},
	}

	droppedTokens = metrics.CounterOpts{
		Name:       "certification_dropped_tokens_total",
		Help:       "Total number of tokens dropped because the certification buffer was full.",
		LabelNames: []string{"channel", "namespace"},
	}
)

// Metrics holds the instrumentation for the CertificationService (server side).
type Metrics struct {
	CertifiedTokens metrics.Counter
}

func NewMetrics(p metrics.Provider) *Metrics {
	return &Metrics{
		CertifiedTokens: p.NewCounter(certifiedTokens),
	}
}

// ClientMetrics holds the instrumentation for the CertificationClient (client side).
type ClientMetrics struct {
	// RequestDuration is a histogram of end-to-end certification batch durations.
	RequestDuration metrics.Histogram
	// Errors counts failed certification request attempts.
	Errors metrics.Counter
	// PendingTokens is a gauge tracking how many tokens are waiting in the input buffer.
	PendingTokens metrics.Gauge
	// DroppedTokens counts tokens dropped because the input buffer was full.
	DroppedTokens metrics.Counter
}

// NewClientMetrics creates a new ClientMetrics instance with the given provider.
// It is exported so that the metrics reference guard can instantiate the
// certification client instrumentation without building a full
// CertificationClient; see docs/development/metrics.md.
func NewClientMetrics(p metrics.Provider) *ClientMetrics {
	return newClientMetrics(p)
}

func newClientMetrics(p metrics.Provider) *ClientMetrics {
	return &ClientMetrics{
		RequestDuration: p.NewHistogram(certificationRequestDuration),
		Errors:          p.NewCounter(certificationErrors),
		PendingTokens:   p.NewGauge(pendingTokens),
		DroppedTokens:   p.NewCounter(droppedTokens),
	}
}
