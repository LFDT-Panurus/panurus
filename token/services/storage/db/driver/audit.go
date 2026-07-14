/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
)

// AuditTransactionStore defines the interface for a database to store the audit records of token transactions.
type AuditTransactionStore interface {
	// Close closes the database
	Close() error

	// NewTransactionStoreTransaction opens an atomic database transaction. It must be committed or discarded.
	NewTransactionStoreTransaction() (TransactionStoreTransaction, error)

	// SetStatus sets the status of a TokenRequest
	// (and with that, the associated Movement and Transaction)
	SetStatus(ctx context.Context, txID string, status TxStatus, message string) error

	// GetStatus returns the status of a given transaction.
	// It returns an error if the transaction is not found
	GetStatus(ctx context.Context, txID string) (TxStatus, string, error)

	// QueryTransactions returns a list of transactions that match the passed params
	QueryTransactions(ctx context.Context, params QueryTransactionsParams, pagination driver.Pagination) (*driver.PageIterator[*TransactionRecord], error)

	// QueryMovements returns a list of movement records
	QueryMovements(ctx context.Context, params QueryMovementsParams) ([]*MovementRecord, error)

	// QueryTokenRequests returns an iterator over the token requests matching the passed params
	QueryTokenRequests(ctx context.Context, params QueryTokenRequestsParams) (TokenRequestIterator, error)

	// GetTokenRequest returns the token request bound to the passed transaction id, if available.
	// It returns nil without error if the key is not found.
	GetTokenRequest(ctx context.Context, txID string) ([]byte, error)

	// GetTokenRequests returns the token requests bound to the given tx ids
	// in a single query. Missing tx ids are absent from the returned map.
	// Empty input returns an empty map without touching the database.
	GetTokenRequests(ctx context.Context, txIDs []string) (map[string][]byte, error)

	// AcquireRecoveryLeadership tries to acquire the PostgreSQL advisory lock backing the sweeper leader election.
	// If acquired is false, leadership was not obtained and the returned lease must be nil.
	AcquireRecoveryLeadership(ctx context.Context, lockID int64) (RecoveryLeadership, bool, error)

	// ClaimPendingTransactions atomically claims a batch of Pending transactions for recovery processing.
	// Transactions whose recovery lease expired are eligible again.
	// Returns the minimal projection (TxID + StoredAt) needed by the recovery loop;
	// callers do not need the full TransactionRecord.
	ClaimPendingTransactions(ctx context.Context, params RecoveryClaimParams) ([]*RecoveryClaim, error)

	// ReleaseRecoveryClaim clears the recovery claim metadata for the given transaction if owned by owner.
	// The message parameter is stored for audit/debugging purposes.
	ReleaseRecoveryClaim(ctx context.Context, txID string, owner string, message string) error

	// PrefixedTableName returns the formatted table name for the given logical table name,
	// following the persistence naming rules of this store.
	PrefixedTableName(name string) string
}
