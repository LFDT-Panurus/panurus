/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package observability

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
)

// WalletMetrics holds metrics collectors for Token-API / WalletService operations.
type WalletMetrics struct {
	// RequestCount tracks the total number of service invocations.
	RequestCount metrics.Counter
	// ErrorCount tracks the total number of errors returned by service invocations.
	ErrorCount metrics.Counter
	// Latency tracks the execution duration of service operations.
	Latency metrics.Histogram
	// InFlight tracks the number of currently active in-flight service requests.
	InFlight metrics.Gauge
}

// NewWalletMetrics constructs a new WalletMetrics instance using the provided metrics.Provider.
// If provider is nil, the FSC no-op disabled.Provider is used so callers never get a nil panic.
func NewWalletMetrics(p metrics.Provider) *WalletMetrics {
	if p == nil {
		p = &disabled.Provider{}
	}

	return &WalletMetrics{
		RequestCount: p.NewCounter(metrics.CounterOpts{
			Namespace:  "token",
			Subsystem:  "wallet",
			Name:       "requests_total",
			Help:       "Total number of wallet service requests",
			LabelNames: []string{"method"},
		}),
		ErrorCount: p.NewCounter(metrics.CounterOpts{
			Namespace:  "token",
			Subsystem:  "wallet",
			Name:       "errors_total",
			Help:       "Total number of wallet service errors",
			LabelNames: []string{"method"},
		}),
		Latency: p.NewHistogram(metrics.HistogramOpts{
			Namespace:  "token",
			Subsystem:  "wallet",
			Name:       "request_duration_seconds",
			Help:       "Execution duration of wallet service operations in seconds",
			LabelNames: []string{"method"},
			Buckets:    []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}),
		InFlight: p.NewGauge(metrics.GaugeOpts{
			Namespace:  "token",
			Subsystem:  "wallet",
			Name:       "inflight_requests",
			Help:       "Current number of in-flight wallet service requests",
			LabelNames: []string{"method"},
		}),
	}
}

// Observe records execution latency, increments request count, and updates error count.
func (m *WalletMetrics) Observe(method string, start time.Time, err error) {
	if m == nil {
		return
	}

	duration := time.Since(start).Seconds()
	m.RequestCount.With("method", method).Add(1)
	m.Latency.With("method", method).Observe(duration)

	if err != nil {
		m.ErrorCount.With("method", method).Add(1)
	}
}
