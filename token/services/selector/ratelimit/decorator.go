/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ratelimit

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
)

// Decorate returns a token.SelectorManager that meters every selection request performed by the
// selectors delegate hands out. scope identifies the token management service the manager belongs
// to (its TMS id), so wallets of different networks or namespaces get separate allowances.
//
// Only Selector.Select is metered. Unlock and Close pass straight through: releasing tokens must
// never be throttled, or a throttled wallet could not clean up after itself.
//
// When limiter is nil, delegate is returned unchanged. Rate limiting is disabled by default, and
// this keeps the disabled path free of any wrapper.
//
// The returned manager does not own limiter: closing or stopping the manager leaves the limiter
// and the allowances it tracks untouched, which is what makes it safe to share one limiter across
// the managers a selector service recreates on every public-parameter reload.
func Decorate(delegate token.SelectorManager, limiter Limiter, scope string) token.SelectorManager {
	if limiter == nil {
		return delegate
	}

	return &manager{delegate: delegate, limiter: limiter, scope: scope}
}

// manager decorates a token.SelectorManager with per-wallet metering of selection requests.
type manager struct {
	delegate token.SelectorManager
	limiter  Limiter
	scope    string
}

// NewSelector returns a selector bound to the passed transaction id whose Select calls are
// metered by the manager's limiter.
func (m *manager) NewSelector(id string) (token.Selector, error) {
	delegate, err := m.delegate.NewSelector(id)
	if err != nil {
		return nil, err
	}

	return &selector{delegate: delegate, limiter: m.limiter, scope: m.scope}, nil
}

// Unlock unlocks the tokens bound to the passed id. It is never metered.
func (m *manager) Unlock(ctx context.Context, id string) error {
	return m.delegate.Unlock(ctx, id)
}

// Close closes the selector bound to the passed id and releases its resources. It is never
// metered, and it does not stop the limiter.
func (m *manager) Close(id string) error {
	return m.delegate.Close(id)
}

// selector decorates a token.Selector, charging one request to the wallet's bucket per Select
// call. The whole call counts as one request no matter how many tokens it locks or how many times
// it retries internally.
type selector struct {
	delegate token.Selector
	limiter  Limiter
	scope    string
}

// Select meters the request against the owner's allowance and, if allowed, delegates the actual
// selection. When the allowance is exhausted it returns the limiter's error, which wraps
// token.SelectorRateLimited, without touching the underlying selector: nothing is locked, so
// there is nothing to release.
func (s *selector) Select(ctx context.Context, ownerFilter token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	var walletID string
	if ownerFilter != nil {
		walletID = ownerFilter.ID()
	}
	if err := s.limiter.Allow(ctx, s.scope, walletID); err != nil {
		return nil, nil, err
	}

	return s.delegate.Select(ctx, ownerFilter, q, tokenType)
}

// Close closes the underlying selector. It does not stop the limiter, which outlives the
// selectors it meters.
func (s *selector) Close() error {
	return s.delegate.Close()
}
