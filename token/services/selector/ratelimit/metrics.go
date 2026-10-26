/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ratelimit

import (
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
)

// tmsLabel is the label carrying the scope (the TMS id) a denial belongs to.
const tmsLabel = "tms"

// Metrics are the metrics of the built-in rate limiter.
//
// The provider handed to NewMetrics must be the process-wide one, not a TMS-scoped provider
// (metrics.NewTMSProvider): the limiter is shared by every TMS of a selector service and carries
// the TMS in its own tms label. A TMS-scoped provider would require network, channel and namespace
// to lead LabelNames, and would panic on first use otherwise.
type Metrics struct {
	// Denied counts selection requests denied because the wallet exhausted its allowance. It is
	// labeled by tms only: a wallet id label would be unbounded cardinality. The denial error names
	// the wallet, which is where per-wallet attribution belongs. This is what tells a policy-driven
	// rejection apart from lock contention (lock_conflicts_total).
	Denied metrics.Counter
	// BucketsActive is the number of live wallet buckets, across every scope the limiter serves.
	// It approaches Config.MaxBuckets when the limiter starts evicting live wallets. The gauge is
	// unlabeled, so it assumes one metered limiter per provider: two limiters reporting to the
	// same provider overwrite each other's value.
	BucketsActive metrics.Gauge
}

// NewMetrics returns the rate limiter metrics. A nil provider yields metrics that discard
// everything.
func NewMetrics(p metrics.Provider) *Metrics {
	if p == nil {
		p = &disabled.Provider{}
	}

	return &Metrics{
		Denied: p.NewCounter(metrics.CounterOpts{
			Name:       "denied_total",
			Help:       "Total number of token selection requests denied by the per-wallet rate limiter",
			LabelNames: []string{tmsLabel},
		}),
		BucketsActive: p.NewGauge(metrics.GaugeOpts{
			Name: "buckets_active",
			Help: "Number of live per-wallet buckets held by the token selection rate limiter",
		}),
	}
}
