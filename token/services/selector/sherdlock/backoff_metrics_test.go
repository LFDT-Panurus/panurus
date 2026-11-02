/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"context"
	"testing"
	"time"

	metricsa "github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backoffHistogram builds Metrics and returns the fake behind selection_backoff_duration_seconds.
func backoffHistogram() (*mocks.FakeHistogram, *sherdlock.Metrics) {
	counter := &mocks.FakeCounter{}
	counter.WithReturns(counter)
	var backoff *mocks.FakeHistogram
	p := &mocks.FakeProvider{}
	p.NewCounterReturns(counter)
	p.NewHistogramCalls(func(o metricsa.HistogramOpts) metricsa.Histogram {
		h := &mocks.FakeHistogram{}
		h.WithReturns(h)
		if o.Name == "selection_backoff_duration_seconds" {
			backoff = h
		}

		return h
	})
	m := sherdlock.NewMetrics(p)

	return backoff, m
}

func lostRaceFetcherAndLocker(winOnCall int) (*mocks.FakeTokenFetcher, *mocks.FakeTokenLocker) {
	tok := &token2.UnspentTokenInWallet{Id: token2.ID{TxId: "tx-hot"}, Type: "ABC", Quantity: "100"}
	f := &mocks.FakeTokenFetcher{}
	f.UnspentTokensIteratorByStub = singleTokenIteratorStub(tok)
	l := &mocks.FakeTokenLocker{}
	lost := errors.Wrapf(driver.ErrTokenAlreadyLocked, "already locked")
	for i := range winOnCall {
		l.TryLockReturnsOnCall(i, false, lost)
	}
	l.TryLockReturnsOnCall(winOnCall, true, nil)

	return f, l
}

func TestStubbornSelector_BackoffDurationObservedPerBackoff(t *testing.T) {
	h, m := backoffHistogram()
	// 6 losses exhaust the first outer attempt (see AggregatesRetryMetricsAcrossBackoff), win on the 5th call of the next.
	f, l := lostRaceFetcherAndLocker(4)
	s := sherdlock.NewStubbornSelector(sherdlock.Logger(), f, l, 64, 5*time.Millisecond, 1, m)
	_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
	require.NoError(t, err)
	assert.Equal(t, 1, h.ObserveCallCount(), "one backoff, one observation")
	assert.GreaterOrEqual(t, h.ObserveArgsForCall(0), 0.0)
	assert.Less(t, h.ObserveArgsForCall(0), 1.0)
}

func TestStubbornSelector_BackoffDurationNotObservedOnFirstTrySuccess(t *testing.T) {
	h, m := backoffHistogram()
	f, l := lostRaceFetcherAndLocker(0)
	s := sherdlock.NewStubbornSelector(sherdlock.Logger(), f, l, 64, time.Millisecond, 1, m)
	_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
	require.NoError(t, err)
	assert.Zero(t, h.ObserveCallCount())
}

// TestStubbornSelector_BackoffDurationOnContextCancel: cancelling mid-backoff observes the
// (short) elapsed wait and surfaces the context error.
func TestStubbornSelector_BackoffDurationOnContextCancel(t *testing.T) {
	h, m := backoffHistogram()
	f, l := lostRaceFetcherAndLocker(100) // never wins
	s := sherdlock.NewStubbornSelector(sherdlock.Logger(), f, l, 64, time.Hour, 3, m)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, _, err := s.Select(ctx, &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 1, h.ObserveCallCount())
	assert.Less(t, h.ObserveArgsForCall(0), 5.0, "elapsed wait, not the planned hour")
}

// TestStubbornSelector_BackoffAfterFinalAttempt pins the current behaviour: the loop sleeps
// after the last failed attempt too, so max retries yields max+1 backoff waits.
func TestStubbornSelector_BackoffAfterFinalAttempt(t *testing.T) {
	h, m := backoffHistogram()
	f, l := lostRaceFetcherAndLocker(1000) // never wins
	s := sherdlock.NewStubbornSelector(sherdlock.Logger(), f, l, 64, time.Millisecond, 2, m)
	_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
	require.Error(t, err)
	assert.Equal(t, 3, h.ObserveCallCount())
}
