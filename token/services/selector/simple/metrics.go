/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple

import (
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
)

var backoffDurationBuckets = []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60}

// Metrics holds the metrics of the simple selector driver.
type Metrics struct {
	// BackoffDuration tracks, per backoff wait between selection rounds, the time actually spent
	// waiting in seconds. A wait whose random jitter is zero is observed as 0. When the manager's
	// timeout is non-positive the selector does not wait at all and nothing is observed. No wait
	// follows the last attempt.
	BackoffDuration metrics.Histogram
}

// NewMetrics returns the simple driver metrics. A nil provider yields metrics that discard everything.
func NewMetrics(p metrics.Provider) *Metrics {
	if p == nil {
		p = &disabled.Provider{}
	}

	return &Metrics{
		BackoffDuration: p.NewHistogram(metrics.HistogramOpts{
			Name:                           "selection_backoff_duration_seconds",
			Help:                           "Time spent in a backoff wait between token selection rounds in seconds",
			Buckets:                        backoffDurationBuckets,
			NativeHistogramBucketFactor:    1.1,
			NativeHistogramMaxBucketNumber: 100,
		}),
	}
}
