/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package observability

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
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
	// RejectionsTotal tracks the number of requests rejected by the circuit breaker.
	RejectionsTotal metrics.Counter
	// State tracks the current circuit breaker state (0=Closed, 1=Open, 2=HalfOpen).
	State metrics.Gauge
}

// NewWalletMetrics constructs a new WalletMetrics instance using the provided metrics.Provider.
// If provider is nil, the FSC no-op disabled.Provider is used so callers never get a nil panic.
func NewWalletMetrics(p metrics.Provider) *WalletMetrics {
	if p == nil {
		p = &disabled.Provider{}
	}

	labels := []string{"network", "channel", "namespace", "method", "wallet_id"}

	return &WalletMetrics{
		RequestCount: p.NewCounter(metrics.CounterOpts{
			Name:       "wallet_requests_total",
			Help:       "Total number of wallet service requests",
			LabelNames: labels,
		}),
		ErrorCount: p.NewCounter(metrics.CounterOpts{
			Name:       "wallet_errors_total",
			Help:       "Total number of wallet service errors",
			LabelNames: labels,
		}),
		Latency: p.NewHistogram(metrics.HistogramOpts{
			Name:       "wallet_request_duration_seconds",
			Help:       "Execution duration of wallet service operations in seconds",
			LabelNames: labels,
			Buckets:    []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}),
		InFlight: p.NewGauge(metrics.GaugeOpts{
			Name:       "wallet_inflight_requests",
			Help:       "Current number of in-flight wallet service requests",
			LabelNames: labels,
		}),
		RejectionsTotal: p.NewCounter(metrics.CounterOpts{
			Name:       "wallet_circuit_breaker_rejections_total",
			Help:       "Total number of requests rejected by the circuit breaker",
			LabelNames: labels,
		}),
		State: p.NewGauge(metrics.GaugeOpts{
			Name:       "wallet_circuit_breaker_state",
			Help:       "Current circuit breaker state (0=Closed, 1=Open, 2=HalfOpen)",
			LabelNames: labels,
		}),
	}
}

// Observe records execution latency, increments request count, updates error count,
// and records circuit breaker rejections if err is ErrCircuitOpen.
// Circuit-breaker rejections are recorded in RejectionsTotal but excluded from ErrorCount
// and Latency so they are not double-counted and do not pollute the latency histogram.
func (m *WalletMetrics) Observe(method, walletID string, start time.Time, err error) {
	if m == nil {
		return
	}

	labels := []string{"method", method, "wallet_id", walletID}
	m.RequestCount.With(labels...).Add(1)

	if errors.Is(err, ErrCircuitOpen) {
		m.RejectionsTotal.With(labels...).Add(1)

		return
	}

	duration := timeSince(start).Seconds()
	m.Latency.With(labels...).Observe(duration)

	if err != nil {
		m.ErrorCount.With(labels...).Add(1)
	}
}

// RecordState records the current circuit breaker state for a method and wallet.
func (m *WalletMetrics) RecordState(method, walletID string, s State) {
	if m == nil {
		return
	}

	m.State.With("method", method, "wallet_id", walletID).Set(float64(s))
}
