/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
)

// EndorserStore defines the interface for an endorser database.
// This database is used to store validation records related to token transaction endorsements.
type EndorserStore interface {
	// Close closes the database
	Close() error

	// NewEndorserStoreTransaction opens an atomic database transaction. It must be committed or discarded.
	NewEndorserStoreTransaction() (EndorserStoreTransaction, error)

	// QueryValidations returns a list of validation records
	QueryValidations(ctx context.Context, params QueryValidationRecordsParams) (ValidationRecordsIterator, error)

	// GetStatus returns the status of a given transaction from the validation records.
	// It returns an error if the transaction is not found
	GetStatus(ctx context.Context, txID string) (TxStatus, string, error)
}

//go:generate counterfeiter -o mock/endorser_store_transaction.go -fake-name EndorserStoreTransaction . EndorserStoreTransaction

// EndorserStoreTransaction represents an atomic database transaction for endorser operations
type EndorserStoreTransaction interface {
	Transaction

	// AddValidationRecord adds a new validation record for the given params.
	// The token request is stored directly in the validation table.
	AddValidationRecord(ctx context.Context, txID string, tokenRequest []byte, meta map[string][]byte, ppHash driver.PPHash) error

	// SetStatus sets the status of a validation record
	SetStatus(ctx context.Context, txID string, status TxStatus, message string) error
}

// ValidationRecord is a record that contains information about the validation of a given token request
type ValidationRecord struct {
	// TxID is the transaction ID
	TxID string
	// TokenRequest is the token request marshalled
	TokenRequest []byte
	// Metadata is the metadata produced by the validator when evaluating the token request
	Metadata map[string][]byte
	// Timestamp is the time the transaction was submitted to the db
	Timestamp time.Time
}

// ValidationRecordsIterator is an iterator for validation records
type ValidationRecordsIterator = iterators.Iterator[*ValidationRecord]

// QueryValidationRecordsParams defines the parameters for querying validation records.
type QueryValidationRecordsParams struct {
	// From is the start time of the query
	// If nil, the query starts from the first transaction
	From *time.Time
	// To is the end time of the query
	// If nil, the query ends at the last transaction
	To *time.Time
	// Statuses is the list of transaction status to accept
	// If empty, any status is accepted
	Statuses []TxStatus
	// Filter defines a custom filter function.
	// If specified, this filter will be applied.
	// the filter returns true if the record must be selected, false otherwise.
	Filter func(record *ValidationRecord) bool
}
