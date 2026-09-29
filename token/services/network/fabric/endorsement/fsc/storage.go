/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fsc

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
)

// Storage defines the interface for storing token transaction records
//
//go:generate counterfeiter -o mock/storage.go -fake-name Storage . Storage
type Storage interface {
	// AlreadyProcessed reports whether the request identified by txID has already
	// been approved by this endorser (i.e. a validation record already exists for it).
	AlreadyProcessed(ctx context.Context, txID string) (bool, error)
	AppendValidationRecord(ctx context.Context, txID string, tokenRequest []byte, meta map[string][]byte, ppHash tdriver.PPHash) error
	// DeleteValidationRecord removes the validation record for txID, undoing an
	// AppendValidationRecord whose endorsement subsequently failed so the request stays
	// retryable. Deleting a txID that has no record is not an error.
	DeleteValidationRecord(ctx context.Context, txID string) error
	// RecordValidationFailure records, for auditing, that validation of the request filed under
	// txID failed, together with the raw request bytes, the public parameters hash the request
	// was evaluated against (empty if validation failed before it was resolved), and a free-text
	// reason. The record is marked rejected and never makes AlreadyProcessed return true, so
	// auditing a failed request does not block re-evaluating it later; the pp_hash is stored so
	// the rejection can be replayed as it happened.
	RecordValidationFailure(ctx context.Context, txID string, tokenRequest []byte, ppHash tdriver.PPHash, reason string) error
}

// StorageProvider defines the interface for obtaining token transaction storage instances
//
//go:generate counterfeiter -o mock/storage_provider.go -fake-name StorageProvider . StorageProvider
type StorageProvider interface {
	// GetStorage returns the Storage instance for the given TMS ID
	GetStorage(id token.TMSID) (Storage, error)
}
