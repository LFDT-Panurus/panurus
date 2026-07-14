/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/tracing"
)

const (
	fetcherTypeLabel tracing.LabelName = "fetcher_type"
	outcomeLabel     tracing.LabelName = "outcome"
	lazy             string            = "lazy"
	eager            string            = "eager"
)

var selectionDurationBuckets = []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

type Metrics struct {
	UnspentTokensInvocations metrics.Counter
	// SelectionDuration tracks the end-to-end duration of a Select() call in seconds.
	SelectionDuration metrics.Histogram
	// SelectionOutcome counts selection outcomes by type: success, insufficient_funds, locked_funds, error.
	SelectionOutcome metrics.Counter
	// ImmediateRetries tracks the distribution of immediate retry counts per Select() call.
	ImmediateRetries metrics.Histogram
}

func NewMetrics(p metrics.Provider) *Metrics {
	return &Metrics{
		UnspentTokensInvocations: p.NewCounter(metrics.CounterOpts{
			Name:       "unspent_tokens_invocations",
			Help:       "The number of invocations",
			LabelNames: []string{fetcherTypeLabel},
		}),
		SelectionDuration: p.NewHistogram(metrics.HistogramOpts{
			Name:                           "selection_duration_seconds",
			Help:                           "Duration of a token selection call in seconds",
			Buckets:                        selectionDurationBuckets,
			NativeHistogramBucketFactor:    1.1,
			NativeHistogramMaxBucketNumber: 100,
		}),
		SelectionOutcome: p.NewCounter(metrics.CounterOpts{
			Name:       "selection_outcome_total",
			Help:       "Total number of token selection outcomes by result type",
			LabelNames: []string{outcomeLabel},
		}),
		ImmediateRetries: p.NewHistogram(metrics.HistogramOpts{
			Name:    "selection_immediate_retries",
			Help:    "Distribution of immediate retry counts per token selection call",
			Buckets: []float64{0, 1, 2, 3, 4, 5},
		}),
	}
}
