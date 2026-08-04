/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role

import (
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
)

var (
	CacheLevelOpts = metrics.GaugeOpts{
		Name:       "recipient_data_cache_level",
		Help:       "Level of the wallet recipient data cache",
		LabelNames: []string{"network", "channel", "namespace"},
	}

	// ProvisionFailuresOpts counts failed attempts to pre-provision recipient data.
	// A rising rate means the identity backend is failing: the cache falls back to
	// generating recipient data on the request path, which is slower, and the
	// background loop is retrying. This is the signal to alert on, since the
	// corresponding log line alone is easy to miss.
	//
	// LabelNames must repeat network/channel/namespace: the provider is TMS-wrapped
	// and binds those three on every metric, so omitting them here panics with
	// "inconsistent label cardinality" on first use.
	ProvisionFailuresOpts = metrics.CounterOpts{
		Name:       "recipient_data_provision_failures_total",
		Help:       "Failed attempts to pre-provision wallet recipient data",
		LabelNames: []string{"network", "channel", "namespace"},
	}
)

// Metrics contains the metrics for this package
type Metrics struct {
	CacheLevelGauge        metrics.Gauge
	ProvisionFailuresCount metrics.Counter
}

// NewMetrics instantiate the metrics for this package
func NewMetrics(p metrics.Provider) *Metrics {
	return &Metrics{
		CacheLevelGauge:        p.NewGauge(CacheLevelOpts),
		ProvisionFailuresCount: p.NewCounter(ProvisionFailuresOpts),
	}
}
