/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity_test

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tmsLabels are the labels every TMS-scoped metric must declare first, in this order: the
// provider prepends the network/channel/namespace values, so a metric that omits them makes
// Prometheus reject the series with an inconsistent label cardinality.
var tmsLabels = []string{"network", "channel", "namespace"}

// TestMetricsDeclareTMSLabelsFirst guards the whole metric set at once, since a missing leading
// label is not a compile error and only shows up as a panic in a deployment with a real registry.
func TestMetricsDeclareTMSLabelsFirst(t *testing.T) {
	provider := newFakeMetricsProvider()
	identity.NewMetrics(provider)

	expected := []string{
		"identity_signer_resolutions_total",
		"identity_get_signer_duration_seconds",
		"identity_signer_router_registrations_total",
		"identity_signer_router_no_probe_errors_total",
		"identity_signature_operations_total",
		"identity_signature_operation_duration_seconds",
		"identity_signer_cache_lookups_total",
		"identity_throttle_escalations_total",
		"identity_throttled_principals",
	}
	for _, name := range expected {
		labels, ok := provider.declaredLabels[name]
		require.True(t, ok, "metric [%s] is not registered", name)
		require.GreaterOrEqual(t, len(labels), len(tmsLabels), "metric [%s] declares too few labels", name)
		assert.Equal(t, tmsLabels, labels[:len(tmsLabels)], "metric [%s] must declare the TMS labels first", name)
	}
}

func TestMetricsObserveSignatureOperation(t *testing.T) {
	provider := newFakeMetricsProvider()
	m := identity.NewMetrics(provider)

	m.Observe(t.Context(), sigobserve.Event{
		Op:       sigobserve.OpVerify,
		Role:     sigobserve.RoleOwner,
		Outcome:  sigobserve.OutcomeInvalid,
		Duration: 5 * time.Millisecond,
	})

	assert.Equal(t, 1, provider.counterAddCount("identity_signature_operations_total",
		"op", "verify", "role", "owner", "outcome", "invalid"))
	assert.Equal(t, 1, provider.histogramObserveCount("identity_signature_operation_duration_seconds", "op", "verify"))
	assert.Equal(t, 0, provider.counterAddCount("identity_signer_cache_lookups_total", "result", "hit"),
		"an operation that consults no cache must not report a lookup")
}

// TestMetricsObserveRoleIsNeverEmpty pins the label-cardinality invariant: an empty role would
// register a second variant of the same series.
func TestMetricsObserveRoleIsNeverEmpty(t *testing.T) {
	provider := newFakeMetricsProvider()
	m := identity.NewMetrics(provider)

	m.Observe(t.Context(), sigobserve.Event{Op: sigobserve.OpBind, Outcome: sigobserve.OutcomeOK})

	assert.Equal(t, 1, provider.counterAddCount("identity_signature_operations_total",
		"op", "bind", "role", "unknown", "outcome", "ok"))
}

func TestMetricsObserveCacheLookups(t *testing.T) {
	tests := []struct {
		name   string
		event  sigobserve.Event
		result string
	}{
		{
			name:   "hit",
			event:  sigobserve.Event{Op: sigobserve.OpGetSigner, Outcome: sigobserve.OutcomeOK, Path: sigobserve.PathCache, CacheChecked: true, CacheHit: true},
			result: "hit",
		},
		{
			name:   "miss",
			event:  sigobserve.Event{Op: sigobserve.OpGetSigner, Outcome: sigobserve.OutcomeOK, Path: sigobserve.PathFallback, CacheChecked: true},
			result: "miss",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := newFakeMetricsProvider()
			identity.NewMetrics(provider).Observe(t.Context(), test.event)

			assert.Equal(t, 1, provider.counterAddCount("identity_signer_cache_lookups_total", "result", test.result))
		})
	}
}

func TestMetricsObserveEscalation(t *testing.T) {
	provider := newFakeMetricsProvider()
	m := identity.NewMetrics(provider)

	m.Observe(t.Context(), sigobserve.Event{
		Op:      sigobserve.OpEscalation,
		Outcome: sigobserve.OutcomeOK,
		Level:   "blocked",
		Reason:  "invalid_signature_rate",
	})

	assert.Equal(t, 1, provider.counterAddCount("identity_throttle_escalations_total",
		"level", "blocked", "reason", "invalid_signature_rate"))
	assert.Equal(t, 0, provider.counterAddCount("identity_signature_operations_total",
		"op", "escalation", "role", "unknown", "outcome", "ok"),
		"policy state must not be counted as a service call")
	assert.Equal(t, 0, provider.histogramObserveCount("identity_signature_operation_duration_seconds", "op", "escalation"))
}

func TestMetricsSetThrottledPrincipals(t *testing.T) {
	provider := newFakeMetricsProvider()
	m := identity.NewMetrics(provider)

	m.SetThrottledPrincipals("soft", 3)
	m.SetThrottledPrincipals("blocked", 1)

	soft, ok := provider.gaugeSetValue("identity_throttled_principals", "level", "soft")
	require.True(t, ok)
	assert.InDelta(t, 3.0, soft, 0)
	blocked, ok := provider.gaugeSetValue("identity_throttled_principals", "level", "blocked")
	require.True(t, ok)
	assert.InDelta(t, 1.0, blocked, 0)
}

// TestMetricsToleratesNilReceiverAndProvider covers the two ways a caller can end up without
// instrumentation: no metrics at all, or a provider that discards everything.
func TestMetricsToleratesNilReceiverAndProvider(t *testing.T) {
	var m *identity.Metrics
	m.Observe(t.Context(), sigobserve.Event{Op: sigobserve.OpSign})
	m.SetThrottledPrincipals("soft", 1)

	m = identity.NewMetrics(nil)
	m.Observe(t.Context(), sigobserve.Event{Op: sigobserve.OpSign, Outcome: sigobserve.OutcomeOK})
	m.SetThrottledPrincipals("soft", 1)
}
