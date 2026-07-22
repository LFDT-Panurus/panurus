/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/services/selector/ratelimit"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
)

// rateLimitedLocker decorates a sherdlock.Locker (the TokenLockStore) with the
// shared per-wallet rate limiter. Before delegating a Lock to the wrapped store
// it consults the limiter for the selecting wallet; when the limiter denies the
// request it returns the limiter's error (which wraps token.SelectorRateLimited),
// so the sherdlock selector aborts the selection immediately (see selector.go).
//
// This is the built-in default limiter wiring for the sherdlock selector. It uses
// the same ratelimit.Limiter type as the simple selector, so both drivers share
// one rate-limiting mechanism.
type rateLimitedLocker struct {
	Locker
	limiter ratelimit.Limiter
}

// NewRateLimitedLocker wraps inner with the given limiter. Passing a nil limiter
// returns inner unchanged (no throttling).
func NewRateLimitedLocker(inner Locker, limiter ratelimit.Limiter) Locker {
	if limiter == nil {
		return inner
	}

	return &rateLimitedLocker{Locker: inner, limiter: limiter}
}

// Lock consults the rate limiter for walletID, then delegates to the wrapped store.
func (l *rateLimitedLocker) Lock(ctx context.Context, tokenID *token2.ID, consumerTxID transaction.ID, walletID string) error {
	if err := l.limiter.Allow(walletID); err != nil {
		return err
	}

	return l.Locker.Lock(ctx, tokenID, consumerTxID, walletID)
}

// Stop shuts down the limiter. It is called by the SelectorService on shutdown.
func (l *rateLimitedLocker) Stop() {
	l.limiter.Stop()
}
