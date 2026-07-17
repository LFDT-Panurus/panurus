/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var (
	// ErrUnconsumedSignatures is returned when validation leaves signatures unused.
	ErrUnconsumedSignatures = errors.New("unconsumed signatures")
	// ErrNilSignatureVerifier is returned when signature verification has no verifier.
	ErrNilSignatureVerifier = errors.New("signature verifier is nil")
)

// Backend represents a backend for token operations, providing access to the ledger and transaction signatures.
type Backend struct {
	Logger logging.Logger
	// Ledger to access the ledger state
	Ledger driver.GetStateFnc
	// Message to be signed or verified
	Message []byte
	// Cursor is used to iterate over the signatures
	Cursor int
	// Sigs contains signatures on Message
	Sigs [][]byte
}

// NewBackend returns a new Backend instance with the provided logger, ledger, message, and signatures.
func NewBackend(logger logging.Logger, ledger driver.GetStateFnc, message []byte, sigs [][]byte) *Backend {
	return &Backend{Logger: logger, Ledger: ledger, Message: message, Sigs: sigs}
}

// HasBeenSignedBy checks if a given Message has been signed by the signing identity matching
// the passed verifier. It returns the signature and any error encountered during verification.
func (b *Backend) HasBeenSignedBy(ctx context.Context, id driver.Identity, verifier driver.Verifier) ([]byte, error) {
	if b == nil {
		return nil, errors.New("signature backend is nil")
	}
	if b.Cursor >= len(b.Sigs) {
		return nil, errors.New("invalid state, insufficient number of signatures")
	}
	if utils.IsNil(verifier) {
		return nil, ErrNilSignatureVerifier
	}
	sigma := b.Sigs[b.Cursor]
	b.Cursor++

	if !utils.IsNil(b.Logger) {
		b.Logger.DebugfContext(ctx, "verify signature [%s][%s][%s]", id, logging.Base64(sigma), utils.Hashable(b.Message))
	}

	return sigma, verifier.Verify(b.Message, sigma)
}

// GetState returns the state associated with the provided token ID from the ledger.
func (b *Backend) GetState(id token.ID) ([]byte, error) {
	if b == nil || b.Ledger == nil {
		return nil, errors.New("ledger not available")
	}

	return b.Ledger(id)
}

// Signatures returns the signatures associated with the backend.
func (b *Backend) Signatures() [][]byte {
	if b == nil {
		return nil
	}

	return b.Sigs
}

// EnsureExhausted returns an error when signatures remain unconsumed.
func (b *Backend) EnsureExhausted() error {
	if b == nil {
		return errors.New("signature backend is nil")
	}
	if b.Cursor != len(b.Sigs) {
		return errors.Wrapf(ErrUnconsumedSignatures, "consumed [%d] of [%d]", b.Cursor, len(b.Sigs))
	}

	return nil
}
