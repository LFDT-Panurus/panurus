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
)

// Metrics contains metrics for monitoring identity cache performance.
type Metrics struct {
	// Current number of cached identities
	CacheLevelGauge metrics.Gauge
}

// NewMetrics creates a new Metrics instance.
func NewMetrics(p metrics.Provider) *Metrics {
	return &Metrics{
		CacheLevelGauge: p.NewGauge(LevelOpts),
	}
}
