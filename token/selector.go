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
	// SelectorRateLimited is the contract error a Locker implementation returns (directly
	// or wrapped) to deny a lock for policy reasons such as rate limiting or quota.
	// Both the simple and sherdlock selectors detect it via errors.Is and abort the
	// selection immediately, returning the error to the caller instead of retrying.
	// The token-sdk ships no built-in limiter: applications integrate their own
	// (e.g. a Redis-backed limiter) by providing a Locker implementation that returns
	// this error when a request must be throttled.
	SelectorRateLimited = errors.New("selection rate limit exceeded")
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
