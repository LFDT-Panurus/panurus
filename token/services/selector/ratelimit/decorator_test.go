/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ratelimit

import (
	"context"
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wallet is a token.OwnerFilter identified by a wallet id.
type wallet string

func (w wallet) ID() string { return string(w) }

// fakeSelector records what it was asked to do.
type fakeSelector struct {
	mu          sync.Mutex
	selectCalls int
	closeCalls  int
	err         error
}

func (s *fakeSelector) Select(_ context.Context, _ token.OwnerFilter, _ string, _ token2.Type) ([]*token2.ID, token2.Quantity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.selectCalls++
	if s.err != nil {
		return nil, nil, s.err
	}

	return []*token2.ID{{TxId: "tx", Index: 0}}, token2.NewZeroQuantity(64), nil
}

func (s *fakeSelector) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closeCalls++

	return nil
}

func (s *fakeSelector) calls() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.selectCalls, s.closeCalls
}

// fakeManager hands out one fakeSelector and records the unlock and close calls it receives.
type fakeManager struct {
	selector     *fakeSelector
	newSelectErr error
	unlockCalls  int
	closeCalls   int
}

func newFakeManager() *fakeManager {
	return &fakeManager{selector: &fakeSelector{}}
}

func (m *fakeManager) NewSelector(string) (token.Selector, error) {
	if m.newSelectErr != nil {
		return nil, m.newSelectErr
	}

	return m.selector, nil
}

func (m *fakeManager) Unlock(context.Context, string) error {
	m.unlockCalls++

	return nil
}

func (m *fakeManager) Close(string) error {
	m.closeCalls++

	return nil
}

// countingLimiter counts every metering decision, so tests can tell what was metered and what was
// not.
type countingLimiter struct {
	calls   int
	wallets []string
	err     error
}

func (l *countingLimiter) Allow(_ context.Context, _ string, walletID string) error {
	l.calls++
	l.wallets = append(l.wallets, walletID)

	return l.err
}

// TestDecorate_NilLimiterIsPassthrough verifies the default, disabled configuration adds no
// wrapper at all.
func TestDecorate_NilLimiterIsPassthrough(t *testing.T) {
	delegate := newFakeManager()

	assert.Same(t, delegate, Decorate(delegate, nil, testScope))
}

// TestDecorate_AllowedSelectionReachesDelegate verifies an allowed request is forwarded untouched,
// and that exactly one request is charged per Select call.
func TestDecorate_AllowedSelectionReachesDelegate(t *testing.T) {
	delegate := newFakeManager()
	limiter := &countingLimiter{}
	mgr := Decorate(delegate, limiter, testScope)

	selector, err := mgr.NewSelector("tx1")
	require.NoError(t, err)

	// Building the selector must not consume any allowance.
	assert.Equal(t, 0, limiter.calls)

	ids, sum, err := selector.Select(context.Background(), wallet("alice"), "10", "USD")
	require.NoError(t, err)
	assert.Len(t, ids, 1)
	assert.NotNil(t, sum)

	selectCalls, _ := delegate.selector.calls()
	assert.Equal(t, 1, selectCalls)
	assert.Equal(t, 1, limiter.calls)
	assert.Equal(t, []string{"alice"}, limiter.wallets)
}

// TestDecorate_DeniedSelectionNeverReachesDelegate verifies a denied request fails fast with the
// token.SelectorRateLimited contract error and does not touch the underlying selector, so no token
// is locked and there is nothing to leak.
func TestDecorate_DeniedSelectionNeverReachesDelegate(t *testing.T) {
	delegate := newFakeManager()
	limiter := &countingLimiter{err: errors.Wrapf(token.SelectorRateLimited, "wallet [alice] throttled")}
	mgr := Decorate(delegate, limiter, testScope)

	selector, err := mgr.NewSelector("tx1")
	require.NoError(t, err)

	ids, sum, err := selector.Select(context.Background(), wallet("alice"), "10", "USD")
	require.ErrorIs(t, err, token.SelectorRateLimited)
	assert.Nil(t, ids)
	assert.Nil(t, sum)

	selectCalls, _ := delegate.selector.calls()
	assert.Equal(t, 0, selectCalls, "the delegate must not run, so nothing gets locked")
}

// TestDecorate_UnlockAndCloseAreNotMetered verifies releasing tokens is never throttled: a wallet
// that has exhausted its allowance must still be able to clean up.
func TestDecorate_UnlockAndCloseAreNotMetered(t *testing.T) {
	delegate := newFakeManager()
	limiter := &countingLimiter{err: errors.Wrapf(token.SelectorRateLimited, "throttled")}
	mgr := Decorate(delegate, limiter, testScope)

	selector, err := mgr.NewSelector("tx1")
	require.NoError(t, err)

	require.NoError(t, mgr.Unlock(context.Background(), "tx1"))
	require.NoError(t, mgr.Close("tx1"))
	require.NoError(t, selector.Close())

	assert.Equal(t, 0, limiter.calls)
	assert.Equal(t, 1, delegate.unlockCalls)
	assert.Equal(t, 1, delegate.closeCalls)
	_, closeCalls := delegate.selector.calls()
	assert.Equal(t, 1, closeCalls)
}

// TestDecorate_NilOwnerFilterIsNotThrottled verifies a request without an owner filter is metered
// as an empty wallet, which the built-in limiter lets through, and reaches the delegate that
// rejects it on its own terms.
func TestDecorate_NilOwnerFilterIsNotThrottled(t *testing.T) {
	delegate := newFakeManager()
	delegate.selector.err = errors.New("no owner filter specified")
	mgr := Decorate(delegate, New(Config{Rate: 1, Burst: 1}), testScope)

	selector, err := mgr.NewSelector("tx1")
	require.NoError(t, err)

	for range 10 {
		_, _, err = selector.Select(context.Background(), nil, "10", "USD")
		require.ErrorContains(t, err, "no owner filter specified")
	}

	selectCalls, _ := delegate.selector.calls()
	assert.Equal(t, 10, selectCalls)
}

// TestDecorate_NewSelectorError verifies a failure to build the underlying selector is propagated.
func TestDecorate_NewSelectorError(t *testing.T) {
	delegate := newFakeManager()
	delegate.newSelectErr = errors.New("no selector for you")
	mgr := Decorate(delegate, &countingLimiter{}, testScope)

	selector, err := mgr.NewSelector("tx1")
	require.ErrorContains(t, err, "no selector for you")
	assert.Nil(t, selector)
}

// TestDecorate_MetersPerSelectCallNotPerLock verifies the whole selection counts as one request:
// a Select call that locks many tokens, or retries internally, is charged exactly once.
func TestDecorate_MetersPerSelectCallNotPerLock(t *testing.T) {
	delegate := newFakeManager()
	limiter := New(Config{Rate: 0.001, Burst: 3})
	mgr := Decorate(delegate, limiter, testScope)

	selector, err := mgr.NewSelector("tx1")
	require.NoError(t, err)

	// The fake selector stands in for a selection that locks several tokens and retries: three
	// Select calls fit in a burst of three, whatever happens inside them.
	for range 3 {
		_, _, err = selector.Select(context.Background(), wallet("alice"), "10", "USD")
		require.NoError(t, err)
	}
	_, _, err = selector.Select(context.Background(), wallet("alice"), "10", "USD")
	require.ErrorIs(t, err, token.SelectorRateLimited)
}

// TestDecorate_WalletsAreIndependent verifies two wallets selecting through the same manager do not
// share an allowance.
func TestDecorate_WalletsAreIndependent(t *testing.T) {
	delegate := newFakeManager()
	mgr := Decorate(delegate, New(Config{Rate: 0.001, Burst: 1}), testScope)

	selector, err := mgr.NewSelector("tx1")
	require.NoError(t, err)
	ctx := context.Background()

	_, _, err = selector.Select(ctx, wallet("alice"), "10", "USD")
	require.NoError(t, err)
	_, _, err = selector.Select(ctx, wallet("alice"), "10", "USD")
	require.ErrorIs(t, err, token.SelectorRateLimited)

	_, _, err = selector.Select(ctx, wallet("bob"), "10", "USD")
	require.NoError(t, err)
}
