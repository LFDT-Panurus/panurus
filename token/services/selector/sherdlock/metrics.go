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
	// LockConflicts counts every lost lock race (TryLock finding a token already
	// locked by another process). Deliberately unlabeled by token id or wallet id:
	// either would be unbounded cardinality. Per-token attribution belongs in the
	// debug-level log line in selector.go and in the `tokendiag locks` command.
	LockConflicts metrics.Counter
	// DistinctTokensAttempted tracks, per Select() call, how many distinct tokens
	// were tried: the lock was won, lost to another process, or denied by the rate
	// limiter. This is what distinguishes "one hot token retried many times" from
	// "many tokens each contended once".
	DistinctTokensAttempted metrics.Histogram
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
		LockConflicts: p.NewCounter(metrics.CounterOpts{
			Name: "lock_conflicts_total",
			Help: "Total number of lost lock races (a token was already locked by another process)",
		}),
		DistinctTokensAttempted: p.NewHistogram(metrics.HistogramOpts{
			Name:    "distinct_tokens_attempted",
			Help:    "Distribution of the number of distinct tokens a lock was attempted on (won, lost, or rate-limited) per token selection call",
			Buckets: []float64{1, 2, 5, 10, 25, 50, 100},
		}),
	}
}
