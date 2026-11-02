/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple

import (
	"context"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	metricsa "github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recHist struct{ obs []float64 }

func (h *recHist) With(...string) metricsa.Histogram { return h }
func (h *recHist) Observe(v float64)                 { h.obs = append(h.obs, v) }

type recProvider struct{ hist *recHist }

func (p *recProvider) NewCounter(metricsa.CounterOpts) metricsa.Counter { return nil }
func (p *recProvider) NewGauge(metricsa.GaugeOpts) metricsa.Gauge       { return nil }
func (p *recProvider) NewHistogram(o metricsa.HistogramOpts) metricsa.Histogram {
	return p.hist
}

func backoffSelector(timeout time.Duration, numRetry int) (*selector, *recHist) {
	h := &recHist{}
	// insufficient funds: 1 token of 1 unit, ask for 2 -> retries until numRetry
	sel := newSelector(&recordingLocker{lockFailAfter: 100}, &mockQueryService{tokens: makeTokens(1, "USD", -1)}, numRetry)
	sel.timeout = timeout
	sel.metrics = NewMetrics(&recProvider{hist: h})

	return sel, h
}

func TestBackoffDurationObservedBetweenAttemptsOnly(t *testing.T) {
	sel, h := backoffSelector(2*time.Millisecond, 3)
	_, _, err := sel.Select(context.Background(), &ownerFilter{id: "w"}, "0x2", "USD")
	require.ErrorIs(t, err, token.SelectorInsufficientFunds)
	assert.Len(t, h.obs, 2, "one wait between each pair of attempts, none after the last")
}

func TestBackoffDurationZeroIntervalNotObserved(t *testing.T) {
	sel, h := backoffSelector(0, 3)
	_, _, err := sel.Select(context.Background(), &ownerFilter{id: "w"}, "0x2", "USD")
	require.ErrorIs(t, err, token.SelectorInsufficientFunds)
	assert.Empty(t, h.obs)
}

func TestBackoffContextCancel(t *testing.T) {
	sel, h := backoffSelector(time.Hour, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// jitter is in [0,1h): practically always long; cancellation must cut it short
	_, _, err := sel.Select(ctx, &ownerFilter{id: "w"}, "0x2", "USD")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Len(t, h.obs, 1)
	assert.Less(t, h.obs[0], 5.0)
}

func TestBackoffNilMetricsDoesNotPanic(t *testing.T) {
	sel, _ := backoffSelector(time.Millisecond, 2)
	sel.metrics = nil
	_, _, err := sel.Select(context.Background(), &ownerFilter{id: "w"}, "0x2", "USD")
	require.ErrorIs(t, err, token.SelectorInsufficientFunds)
}

func TestNewMetricsNilProvider(t *testing.T) {
	assert.NotPanics(t, func() { NewMetrics(nil).BackoffDuration.Observe(1) })
}
