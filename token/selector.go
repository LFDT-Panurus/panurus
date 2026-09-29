/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var (
	// SelectorInsufficientFunds is returned when funds are not sufficient to cover the request
	SelectorInsufficientFunds = errors.New("insufficient funds")
	// SelectorSufficientButLockedFunds is returned when funds are sufficient to cover the request, but some tokens are locked
	// by other transactions
	SelectorSufficientButLockedFunds = errors.New("sufficient but partially locked funds")
	// SelectorSufficientButNotCertifiedFunds is returned when funds are sufficient to cover the request, but some tokens
	// are not yet certified and therefore cannot be used.
	SelectorSufficientButNotCertifiedFunds = errors.New("sufficient but partially not certified")
	// SelectorSufficientFundsButConcurrencyIssue is returned when funds are sufficient to cover the request, but
	// concurrency issues does not make some of the selected tokens available.
	SelectorSufficientFundsButConcurrencyIssue = errors.New("sufficient funds but concurrency issue")
	// SelectorRateLimited is the contract error returned (directly or wrapped) to deny a
	// selection for policy reasons such as rate limiting or quota.
	// Both the simple and sherdlock selectors detect it via errors.Is and abort the
	// selection immediately, returning the error to the caller instead of retrying.
	// Panurus ships an opt-in per-wallet limiter that returns it, see
	// token/services/selector/ratelimit and the token.selector.rateLimit* configuration
	// keys; it is disabled by default. Applications that would rather reuse their own
	// infrastructure (e.g. a Redis-backed limiter) can either supply a Limiter to that
	// package or return this error from a custom Locker implementation.
	SelectorRateLimited = errors.New("selection rate limit exceeded")
	// SelectorTimedOut is returned when token selection is aborted because the
	// configured selection timeout was exceeded.  Unlike SelectorSufficientButLockedFunds
	// it does not imply that funds were present; callers should treat it as a load-shedding
	// signal and back off rather than immediately retrying.
	SelectorTimedOut = errors.New("token selection timed out")
	// SelectorResourceLimitExceeded is returned when a selection is aborted because
	// it hit one of the operator-configured resource limits (maxTokensPerSelection or
	// maxLockAttempts, see token/services/selector/config) before it could satisfy the
	// request. It is not a genuine "insufficient funds" answer and it is not a bug:
	// retrying re-reads the same bounded, deterministically ordered page, so callers
	// should treat it as a configuration/load signal rather than retrying blindly.
	SelectorResourceLimitExceeded = errors.New("token selection resource limit exceeded")
)

// OwnerFilter tells if a passed identity is recognized
type OwnerFilter interface {
	// ID is the wallet identifier of the owner
	ID() string
}

// Selector is the interface of token selectors
//
//go:generate counterfeiter -o mock/selector.go -fake-name Selector . Selector
type Selector interface {
	// Select returns the list of token identifiers where
	// 1. The owner match the passed owner filter.
	// 2. The type is equal to the passed token type.
	// 3. The sum of amount in each token is at least the passed quantity.
	// Quantity is a string in decimal format
	// Notice that, the quantity selected might exceed the quantity requested due to the amounts
	// stored in each token.
	Select(ctx context.Context, ownerFilter OwnerFilter, q string, tokenType token.Type) ([]*token.ID, token.Quantity, error)
	// Close closes the selector and releases its memory/cpu resources
	Close() error
}
