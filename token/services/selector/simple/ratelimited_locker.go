/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/services/selector/ratelimit"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
)

// rateLimitedLocker decorates a simple.Locker with the shared per-wallet rate
// limiter. Before delegating a Lock to the wrapped locker it consults the
// limiter for the selecting wallet; when the limiter denies the request it
// returns the limiter's error (which wraps token.SelectorRateLimited), so the
// simple selector aborts the selection immediately (see selector.go).
//
// This is the built-in default limiter wiring for the simple selector. The same
// ratelimit.Limiter type is used by the sherdlock selector, so both drivers share
// one rate-limiting mechanism. Applications can still bypass this entirely by
// providing their own Locker.
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

// Lock consults the rate limiter for walletID, then delegates to the wrapped locker.
func (l *rateLimitedLocker) Lock(ctx context.Context, id *token2.ID, txID string, walletID string, reclaim bool) (string, error) {
	if err := l.limiter.Allow(walletID); err != nil {
		return "", err
	}

	return l.Locker.Lock(ctx, id, txID, walletID, reclaim)
}

// Stop shuts down the limiter and then the wrapped locker if it too has a
// lifecycle. It satisfies the SelectorService's stoppable shutdown path.
func (l *rateLimitedLocker) Stop() error {
	l.limiter.Stop()
	if s, ok := l.Locker.(stoppable); ok {
		return s.Stop()
	}

	return nil
}
