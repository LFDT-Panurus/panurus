/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
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

	// SignatureOps counts Signer/Verifier service operations by operation, role and outcome.
	// It is the counter an alert is written against: a rising "invalid" outcome on verify, or
	// a rising "throttled" outcome, is the signal that a principal is misbehaving.
	//
	// Its cardinality is bounded by the closed sets of operations, roles and outcomes declared
	// in the sigobserve package; no per-identity label is ever attached, since the number of
	// identities a deployment sees is unbounded.
	SignatureOps metrics.Counter

	// SignatureOpDuration is a histogram of Signer/Verifier operation wall-clock time in
	// seconds, labeled by operation only: the outcome is already carried by SignatureOps, and
	// crossing it with duration buckets would multiply the series for little insight.
	SignatureOpDuration metrics.Histogram

	// SignerCacheLookups counts signer-cache consultations by result ("hit" or "miss"). A
	// collapsing hit ratio means signer material is being re-derived on every call, which is
	// both a latency and a CPU-exhaustion concern.
	SignerCacheLookups metrics.Counter

	// ThrottleEscalations counts throttle level changes by the level entered and the reason.
	// De-escalations appear here too, with the level they returned to.
	ThrottleEscalations metrics.Counter

	// ThrottledPrincipals reports how many principals are currently held at each throttle
	// level above normal.
	ThrottledPrincipals metrics.Gauge
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
		SignatureOps: p.NewCounter(metrics.CounterOpts{
			Name:       "identity_signature_operations_total",
			Help:       "Total number of signer/verifier service operations by operation, role and outcome",
			LabelNames: []string{"network", "channel", "namespace", "op", "role", "outcome"},
		}),
		SignatureOpDuration: p.NewHistogram(metrics.HistogramOpts{
			Name:                           "identity_signature_operation_duration_seconds",
			Help:                           "Histogram of signer/verifier service operation wall-clock time in seconds, labeled by operation",
			LabelNames:                     []string{"network", "channel", "namespace", "op"},
			Buckets:                        []float64{.0005, .001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
			NativeHistogramBucketFactor:    1.1,
			NativeHistogramMaxBucketNumber: 100,
		}),
		SignerCacheLookups: p.NewCounter(metrics.CounterOpts{
			Name:       "identity_signer_cache_lookups_total",
			Help:       "Total number of signer cache lookups by result (hit, miss)",
			LabelNames: []string{"network", "channel", "namespace", "result"},
		}),
		ThrottleEscalations: p.NewCounter(metrics.CounterOpts{
			Name:       "identity_throttle_escalations_total",
			Help:       "Total number of throttle level changes by the level entered and the reason",
			LabelNames: []string{"network", "channel", "namespace", "level", "reason"},
		}),
		ThrottledPrincipals: p.NewGauge(metrics.GaugeOpts{
			Name:       "identity_throttled_principals",
			Help:       "Number of principals currently held at each throttle level above normal",
			LabelNames: []string{"network", "channel", "namespace", "level"},
		}),
	}
}

// NewMetrics creates a new Metrics instance with the given provider.
func NewMetrics(p metrics.Provider) *Metrics {
	return newMetrics(p)
}

// Observe implements sigobserve.Observer: it records e in the signature instruments. Escalation
// events, which report policy state rather than a service call, are counted separately and
// contribute no duration.
func (m *Metrics) Observe(_ context.Context, e sigobserve.Event) {
	if m == nil {
		return
	}

	if e.Op == sigobserve.OpEscalation {
		m.ThrottleEscalations.With("level", e.Level, "reason", e.Reason).Add(1)

		return
	}

	m.SignatureOps.With("op", string(e.Op), "role", roleLabel(e.Role), "outcome", string(e.Outcome)).Add(1)
	m.SignatureOpDuration.With("op", string(e.Op)).Observe(e.Duration.Seconds())
	if e.CacheChecked {
		m.SignerCacheLookups.With("result", cacheLabel(e.CacheHit)).Add(1)
	}
}

// SetThrottledPrincipals implements throttle.LevelGauge.
func (m *Metrics) SetThrottledPrincipals(level string, n int) {
	if m == nil {
		return
	}

	m.ThrottledPrincipals.With("level", level).Set(float64(n))
}

// roleLabel keeps the role label from ever being empty, so the label set of every series is the
// same and Prometheus does not see two variants of the same metric.
func roleLabel(role sigobserve.Role) string {
	if role == "" {
		return string(sigobserve.RoleUnknown)
	}

	return string(role)
}

// cacheLabel renders a cache lookup result.
func cacheLabel(hit bool) string {
	if hit {
		return "hit"
	}

	return "miss"
}
