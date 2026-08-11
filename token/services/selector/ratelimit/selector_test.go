/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// test doubles
// ---------------------------------------------------------------------------

type ownerFilter string

func (o ownerFilter) ID() string { return string(o) }

// fakeSelector records the Select calls it receives and, for each one, simulates the
// candidate-token walk that both real selectors perform by invoking onLock locksPerSelect
// times. That is what lets us assert how the limiter is charged.
type fakeSelector struct {
	selects  atomic.Int64
	closes   atomic.Int64
	perCall  int
	onLock   func() error
	selectFn func() ([]*token2.ID, token2.Quantity, error)
}

func (s *fakeSelector) Select(_ context.Context, _ token.OwnerFilter, _ string, _ token2.Type) ([]*token2.ID, token2.Quantity, error) {
	s.selects.Add(1)
	for range s.perCall {
		if s.onLock != nil {
			if err := s.onLock(); err != nil {
				return nil, nil, err
			}
		}
	}
	if s.selectFn != nil {
		return s.selectFn()
	}

	return []*token2.ID{{TxId: "tx", Index: 0}}, nil, nil
}

func (s *fakeSelector) Close() error {
	s.closes.Add(1)

	return nil
}

type fakeManager struct {
	selector  *fakeSelector
	newErr    error
	newCalls  atomic.Int64
	unlockIDs []string
	closeIDs  []string
	mu        sync.Mutex
}

func (m *fakeManager) NewSelector(string) (token.Selector, error) {
	m.newCalls.Add(1)
	if m.newErr != nil {
		return nil, m.newErr
	}

	return m.selector, nil
}

func (m *fakeManager) Unlock(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unlockIDs = append(m.unlockIDs, id)

	return nil
}

func (m *fakeManager) Close(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeIDs = append(m.closeIDs, id)

	return nil
}

// countingLimiter records every Allow call and denies once denyAfter calls have been
// permitted (denyAfter < 0 never denies).
type countingLimiter struct {
	allows    atomic.Int64
	stops     atomic.Int64
	denyAfter int64
}

func (l *countingLimiter) Allow(identity string) error {
	if n := l.allows.Add(1); l.denyAfter >= 0 && n > l.denyAfter {
		return errors.Wrapf(token.SelectorRateLimited, "wallet %s throttled", identity)
	}

	return nil
}

func (l *countingLimiter) Stop() { l.stops.Add(1) }

func newLimiter(denyAfter int64) *countingLimiter {
	return &countingLimiter{denyAfter: denyAfter}
}

func selectOnce(t *testing.T, m token.SelectorManager, wallet string) error {
	t.Helper()
	s, err := m.NewSelector("txID")
	require.NoError(t, err)
	_, _, err = s.Select(context.Background(), ownerFilter(wallet), "1", "EUR")

	return err
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

func TestNewSelectorManager_NilLimiterReturnsInnerUnchanged(t *testing.T) {
	inner := &fakeManager{selector: &fakeSelector{}}

	assert.Same(t, inner, NewSelectorManager(inner, nil, "tms1"))
}

func TestSelectorManager_AllowsWhenUnderLimit(t *testing.T) {
	inner := &fakeManager{selector: &fakeSelector{}}
	limiter := newLimiter(-1)

	require.NoError(t, selectOnce(t, NewSelectorManager(inner, limiter, "tms1"), "alice"))
	assert.Equal(t, int64(1), limiter.allows.Load())
	assert.Equal(t, int64(1), inner.selector.selects.Load())
}

func TestSelectorManager_DeniesWithSentinel(t *testing.T) {
	inner := &fakeManager{selector: &fakeSelector{}}
	limiter := newLimiter(0) // deny everything

	err := selectOnce(t, NewSelectorManager(inner, limiter, "tms1"), "alice")

	require.ErrorIs(t, err, token.SelectorRateLimited)
	assert.Contains(t, err.Error(), "throttled")
	assert.Equal(t, int64(0), inner.selector.selects.Load(), "denied request must not reach the inner selector")
}

// TestSelectorManager_ChargedOncePerSelectionNotPerLock is the regression test for the
// CI failure this decorator fixes. The limiter used to be a Locker decorator, so a
// single selection that walked many candidate tokens was charged once per lock attempt
// and could exhaust its own per-wallet budget. Metering happens per selection request,
// so a selection performing many locks must cost exactly one bucket token.
func TestSelectorManager_ChargedOncePerSelectionNotPerLock(t *testing.T) {
	const locksPerSelect = 50

	locks := atomic.Int64{}
	inner := &fakeManager{selector: &fakeSelector{
		perCall: locksPerSelect,
		onLock: func() error {
			locks.Add(1)

			return nil
		},
	}}
	limiter := newLimiter(-1)
	mgr := NewSelectorManager(inner, limiter, "tms1")

	require.NoError(t, selectOnce(t, mgr, "alice"))

	assert.Equal(t, int64(locksPerSelect), locks.Load(), "the inner selector must still walk every candidate")
	assert.Equal(t, int64(1), limiter.allows.Load(), "a selection must cost exactly one bucket token")
}

// The real limiter, at the shipped default, must not throttle a wallet performing a
// realistic burst of sequential selections. Nine back-to-back transfers are what the
// fabtoken t1 integration test does, and what used to fail.
func TestSelectorManager_DefaultBudgetSurvivesSequentialTransfers(t *testing.T) {
	limiter := NewTokenBucketRateLimiter(10, 20, 0)
	defer limiter.Stop()

	inner := &fakeManager{selector: &fakeSelector{perCall: 50}}
	mgr := NewSelectorManager(inner, limiter, "tms1")

	for i := range 9 {
		require.NoErrorf(t, selectOnce(t, mgr, "alice"), "transfer %d was throttled", i+1)
	}
}

func TestSelectorManager_ThrottlesRunawayCaller(t *testing.T) {
	limiter := NewTokenBucketRateLimiter(10, 20, 0)
	defer limiter.Stop()

	inner := &fakeManager{selector: &fakeSelector{}}
	mgr := NewSelectorManager(inner, limiter, "tms1")

	// The bucket holds 20 tokens and refills at 10/s, so a tight loop of 200 selections
	// cannot all be served: the limiter must start denying.
	var denied int
	for range 200 {
		if err := selectOnce(t, mgr, "alice"); err != nil {
			require.ErrorIs(t, err, token.SelectorRateLimited)
			denied++
		}
	}
	assert.Positive(t, denied, "a runaway caller must be throttled")
}

func TestSelectorManager_LimitIsPerWallet(t *testing.T) {
	// Burst is coerced up to the rate, so 1/s is what yields a single-token bucket.
	limiter := NewTokenBucketRateLimiter(1, 1, 0)
	defer limiter.Stop()

	inner := &fakeManager{selector: &fakeSelector{}}
	mgr := NewSelectorManager(inner, limiter, "tms1")

	require.NoError(t, selectOnce(t, mgr, "alice"))
	// alice's single-token bucket is now empty, but bob has his own.
	require.ErrorIs(t, selectOnce(t, mgr, "alice"), token.SelectorRateLimited)
	require.NoError(t, selectOnce(t, mgr, "bob"))
}

func TestSelectorManager_LimitIsPerTMS(t *testing.T) {
	// A single-token bucket per wallet. alice's budget on tms1 must be independent from
	// alice's budget on tms2: one network's runaway caller must not throttle the other.
	limiter := NewTokenBucketRateLimiter(1, 1, 0)
	defer limiter.Stop()

	inner := &fakeManager{selector: &fakeSelector{}}
	mgr1 := NewSelectorManager(inner, limiter, "net,ch,ns1")
	mgr2 := NewSelectorManager(inner, limiter, "net,ch,ns2")

	// Exhaust alice's bucket on tms1.
	require.NoError(t, selectOnce(t, mgr1, "alice"))
	require.ErrorIs(t, selectOnce(t, mgr1, "alice"), token.SelectorRateLimited)

	// alice on tms2 has her own full bucket — tms1 exhaustion must not affect it.
	require.NoError(t, selectOnce(t, mgr2, "alice"))
}

func TestSelectorManager_EmptyWalletIDNotThrottled(t *testing.T) {
	limiter := NewTokenBucketRateLimiter(1, 1, 0)
	defer limiter.Stop()

	inner := &fakeManager{selector: &fakeSelector{}}
	mgr := NewSelectorManager(inner, limiter, "tms1")

	// An empty wallet id means "no policy": it is never throttled.
	for range 10 {
		require.NoError(t, selectOnce(t, mgr, ""))
	}
	assert.Equal(t, int64(10), inner.selector.selects.Load())
}

func TestSelectorManager_NilOwnerFilterDelegates(t *testing.T) {
	inner := &fakeManager{selector: &fakeSelector{
		selectFn: func() ([]*token2.ID, token2.Quantity, error) {
			return nil, nil, errors.New("no owner filter specified")
		},
	}}
	limiter := newLimiter(-1)

	s, err := NewSelectorManager(inner, limiter, "tms1").NewSelector("txID")
	require.NoError(t, err)

	// A nil owner filter carries no wallet id, so the limiter is not consulted and the
	// wrapped selector reports the error it already reports for this case.
	_, _, err = s.Select(context.Background(), nil, "1", "EUR")
	require.EqualError(t, err, "no owner filter specified")
	assert.Equal(t, int64(0), limiter.allows.Load())
}

func TestSelectorManager_PropagatesNewSelectorError(t *testing.T) {
	inner := &fakeManager{newErr: errors.New("boom")}
	limiter := newLimiter(-1)

	s, err := NewSelectorManager(inner, limiter, "tms1").NewSelector("txID")

	require.EqualError(t, err, "boom")
	assert.Nil(t, s)
}

func TestSelectorManager_UnlockAndCloseAreNotThrottled(t *testing.T) {
	inner := &fakeManager{selector: &fakeSelector{}}
	limiter := newLimiter(0) // deny everything
	mgr := NewSelectorManager(inner, limiter, "tms1")

	// Releasing tokens must never be blocked by the limiter, otherwise a throttled
	// wallet could never free the tokens it holds.
	require.NoError(t, mgr.Unlock(context.Background(), "txID"))
	require.NoError(t, mgr.Close("txID"))
	assert.Equal(t, []string{"txID"}, inner.unlockIDs)
	assert.Equal(t, []string{"txID"}, inner.closeIDs)
	assert.Equal(t, int64(0), limiter.allows.Load())
}

func TestSelectorManager_SelectorCloseDelegates(t *testing.T) {
	inner := &fakeManager{selector: &fakeSelector{}}
	mgr := NewSelectorManager(inner, newLimiter(-1), "tms1")

	s, err := mgr.NewSelector("txID")
	require.NoError(t, err)
	require.NoError(t, s.Close())
	assert.Equal(t, int64(1), inner.selector.closes.Load())
}

func TestSelectorManager_RefillAllowsLaterSelection(t *testing.T) {
	// Burst is coerced up to the rate, so drain the whole 100-token bucket first. At
	// 100 tokens/s the next token is then available in ~10ms.
	const burst = 100
	limiter := NewTokenBucketRateLimiter(burst, burst, 0)
	defer limiter.Stop()

	inner := &fakeManager{selector: &fakeSelector{}}
	mgr := NewSelectorManager(inner, limiter, "tms1")

	for range burst {
		require.NoError(t, selectOnce(t, mgr, "alice"))
	}
	require.ErrorIs(t, selectOnce(t, mgr, "alice"), token.SelectorRateLimited)

	assert.Eventually(t, func() bool {
		return selectOnce(t, mgr, "alice") == nil
	}, time.Second, 5*time.Millisecond, "bucket must refill")
}

func TestSelectorManager_ConcurrentSelects(t *testing.T) {
	limiter := NewTokenBucketRateLimiter(1000, 1000, 0)
	defer limiter.Stop()

	inner := &fakeManager{selector: &fakeSelector{}}
	mgr := NewSelectorManager(inner, limiter, "tms1")

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			wallet := "alice"
			if i%2 == 0 {
				wallet = "bob"
			}
			_ = selectOnce(t, mgr, wallet)
		})
	}
	wg.Wait()

	assert.Equal(t, int64(50), inner.selector.selects.Load())
}
