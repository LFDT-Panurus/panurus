/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package replay provides a driver-agnostic guard against processing the same request
// more than once. It is meant to be checked by an endorser-style responder as early as
// possible, before any expensive validation of the request content is performed.
package replay

import (
	"context"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ErrAlreadyProcessed signals that a request equivalent to the one being checked has
// already been seen.
var ErrAlreadyProcessed = errors.New("request already processed")

// ErrOutOfWindow signals that a request's claimed timestamp falls outside the freshness
// window accepted by the guard (too old or too far in the future relative to the node's clock).
var ErrOutOfWindow = errors.New("request timestamp outside freshness window")

// Key identifies a single request for replay-detection purposes. All fields are sourced
// from the content of the request itself (e.g., a Fabric proposal), not derived from one
// another, so the guard remains correct regardless of how a given network driver computes
// its transaction ID.
type Key struct {
	// TxID is the network-level transaction identifier.
	TxID string
	// Creator is the serialized identity of the request's creator.
	Creator []byte
	// Nonce is the nonce carried by the request.
	Nonce []byte
	// Timestamp is the time at which the request was created, as claimed by the request itself.
	Timestamp time.Time
}

// Guard detects whether a request has already been seen.
//
//go:generate counterfeiter -o mock/guard.go -fake-name Guard . Guard
type Guard interface {
	// Check atomically records key as seen and returns ErrAlreadyProcessed if an equivalent
	// key was already recorded. Implementations must guarantee that, of two concurrent Check
	// calls carrying the same key, at most one returns nil.
	Check(ctx context.Context, key Key) error
}
