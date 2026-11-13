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

	// RecordValidationFailure records, for auditing purposes, that validation of the token
	// request filed under txID failed, together with the raw request bytes, the public
	// parameters hash the request was evaluated against, and a free-text reason describing the
	// failure.
	//
	// It shares the validations table with AddValidationRecord but marks the row as rejected,
	// so it is never treated as an approval: a rejected row reads back as Unknown from GetStatus
	// and so never participates in the already-processed / single-approval guard. Only the most
	// recent record is kept per txID (it is keyed by txID), so re-recording a failure for the
	// same anchor overwrites the previous one; it can never overwrite an existing approved row.
	//
	// Unlike AddValidationRecord it is not subject to the token-request well-formedness checks
	// (the request may have failed precisely because it was malformed) and ppHash may be empty
	// (validation can fail before the endorser has resolved its own public parameters hash). It
	// is stored so a rejection can be replayed exactly as it happened.
	RecordValidationFailure(ctx context.Context, txID string, tokenRequest []byte, ppHash driver.PPHash, reason string) error

	// QueryValidationFailures returns a list of validation failure records matching params.
	QueryValidationFailures(ctx context.Context, params QueryValidationFailureRecordsParams) (ValidationFailureRecordsIterator, error)
}

//go:generate counterfeiter -o mock/endorser_store_transaction.go -fake-name EndorserStoreTransaction . EndorserStoreTransaction

// EndorserStoreTransaction represents an atomic database transaction for endorser operations
type EndorserStoreTransaction interface {
	Transaction

	// AddValidationRecord adds a new validation record for the given params.
	// The token request is stored directly in the validation table.
	//
	// Verification: an implementation must refuse an empty txID, an empty
	// tokenRequest, or an empty ppHash. Unlike the ttx and audit stores, this one
	// holds the bare actions-and-signatures format rather than
	// TokenRequestWithMetadata, so there is no anchor to bind to txID; what is
	// checked instead is that the payload deserializes at a supported version and
	// carries at least one action — see integrity.CheckTokenRequestActions. A
	// validation record asserting that a request was validated is worthless if
	// the request it names carries nothing to validate.
	AddValidationRecord(ctx context.Context, txID string, tokenRequest []byte, meta map[string][]byte, ppHash driver.PPHash) error

	// DeleteValidationRecord removes the validation record for txID, if any. It is used to
	// undo a record whose endorsement subsequently failed, so the request is left retryable
	// rather than permanently marked as processed. Deleting a txID that has no record is not
	// an error.
	DeleteValidationRecord(ctx context.Context, txID string) error

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

// ValidationFailureRecord is an audit-only record of a token request whose validation failed
// on the endorser approval path. It shares the validations table with ValidationRecord but is
// marked rejected, so it never gates re-evaluation of the same transaction. Unlike
// ValidationRecord it carries no metadata, and its public parameters hash may be empty (there
// may be none to produce for a request that failed before the endorser resolved it).
type ValidationFailureRecord struct {
	// TxID is the transaction ID (request anchor) the failed request was filed under
	TxID string
	// TokenRequest is the raw token request bytes that failed validation, as received
	TokenRequest []byte
	// PPHash is the public parameters hash the request was evaluated against, so the
	// rejection can be replayed as it happened. It may be empty if validation failed before
	// the endorser resolved its public parameters hash.
	PPHash driver.PPHash
	// Reason is a free-text description of why validation failed
	Reason string
	// Timestamp is the time the failure was recorded
	Timestamp time.Time
}

// ValidationFailureRecordsIterator is an iterator for validation failure records
type ValidationFailureRecordsIterator = iterators.Iterator[*ValidationFailureRecord]

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

// QueryValidationFailureRecordsParams defines the parameters for querying validation failure
// records.
type QueryValidationFailureRecordsParams struct {
	// From is the start time of the query.
	// If nil, the query starts from the first record.
	From *time.Time
	// To is the end time of the query.
	// If nil, the query ends at the last record.
	To *time.Time
	// Filter defines a custom filter function.
	// If specified, this filter will be applied.
	// the filter returns true if the record must be selected, false otherwise.
	Filter func(record *ValidationFailureRecord) bool
}
