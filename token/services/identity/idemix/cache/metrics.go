/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cache

import (
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
)

var (
	// LevelOpts defines gauge options for tracking cache level.
	LevelOpts = metrics.GaugeOpts{
		Name:       "cache_level",
		Help:       "Level of the idemix cache",
		LabelNames: []string{"network", "channel", "namespace"},
	}

	// ProvisionFailuresOpts counts failed attempts to pre-provision identities. A
	// rising rate means the key manager is failing: identities are generated on the
	// request path instead and the background loop is retrying. This is the signal to
	// alert on, since the corresponding log line alone is easy to miss.
	//
	// LabelNames must repeat network/channel/namespace: the provider is TMS-wrapped
	// and binds those three on every metric, so omitting them here panics with
	// "inconsistent label cardinality" on first use.
	ProvisionFailuresOpts = metrics.CounterOpts{
		Name:       "cache_provision_failures_total",
		Help:       "Failed attempts to pre-provision idemix identities",
		LabelNames: []string{"network", "channel", "namespace"},
	}
)

// Metrics contains metrics for monitoring identity cache performance.
type Metrics struct {
	// Current number of cached identities
	CacheLevelGauge metrics.Gauge
	// Failed background provisioning attempts
	ProvisionFailuresCount metrics.Counter
}

// NewMetrics creates a new Metrics instance.
func NewMetrics(p metrics.Provider) *Metrics {
	return &Metrics{
		CacheLevelGauge:        p.NewGauge(LevelOpts),
		ProvisionFailuresCount: p.NewCounter(ProvisionFailuresOpts),
	}
}
