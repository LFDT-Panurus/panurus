/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity

import (
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
)

// Metrics holds the instrumentation for the identity Provider and its SignerRouter.
type Metrics struct {
	// SignerResolutions counts GetSigner calls by how the signer was ultimately obtained:
	// "cache" (already cached), "routed" (conf_id-pinned SignerRouter hit), or "fallback"
	// (linear-scan probing deserializer).
	SignerResolutions metrics.Counter

	// GetSignerDuration is a histogram of GetSigner wall-clock time, in seconds, labeled by
	// the same "path" values as SignerResolutions. Comparing the "routed" and "fallback"
	// buckets shows the latency saved by skipping the cryptographic probe.
	GetSignerDuration metrics.Histogram

	// SignerRouterRegistrations counts conf_id->KeyManager bindings registered with the
	// SignerRouter. A near-zero count in a running deployment indicates routing is not being
	// populated and every GetSigner call is falling back to the probing deserializer.
	SignerRouterRegistrations metrics.Counter

	// NoProbeErrors counts failures of the SignerRouter's probe-free deserialization path
	// (ProbeFreeSignerDeserializer.DeserializeSignerNoProbe). Since that path skips the
	// cryptographic check that would otherwise catch a mismatched KeyManager, a non-zero
	// count is worth investigating as a potential conf_id routing bug.
	NoProbeErrors metrics.Counter
}

func newMetrics(p metrics.Provider) *Metrics {
	if p == nil {
		p = &disabled.Provider{}
	}

	return &Metrics{
		SignerResolutions: p.NewCounter(metrics.CounterOpts{
			Name:       "identity_signer_resolutions_total",
			Help:       "Total number of GetSigner calls by outcome (cache, routed, fallback)",
			LabelNames: []string{"network", "channel", "namespace", "outcome"},
		}),
		GetSignerDuration: p.NewHistogram(metrics.HistogramOpts{
			Name:                           "identity_get_signer_duration_seconds",
			Help:                           "Histogram of GetSigner wall-clock time in seconds, labeled by resolution path",
			LabelNames:                     []string{"network", "channel", "namespace", "path"},
			Buckets:                        []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
			NativeHistogramBucketFactor:    1.1,
			NativeHistogramMaxBucketNumber: 100,
		}),
		SignerRouterRegistrations: p.NewCounter(metrics.CounterOpts{
			Name:       "identity_signer_router_registrations_total",
			Help:       "Total number of conf_id-to-KeyManager bindings registered with the SignerRouter",
			LabelNames: []string{"network", "channel", "namespace"},
		}),
		NoProbeErrors: p.NewCounter(metrics.CounterOpts{
			Name:       "identity_signer_router_no_probe_errors_total",
			Help:       "Total number of errors from the SignerRouter's probe-free signer deserialization path",
			LabelNames: []string{"network", "channel", "namespace"},
		}),
	}
}

// NewMetrics creates a new Metrics instance with the given provider.
func NewMetrics(p metrics.Provider) *Metrics {
	return newMetrics(p)
}
