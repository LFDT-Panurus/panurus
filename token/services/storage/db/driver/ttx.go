/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
)

var ErrTokenRequestDoesNotExist = errors.New("token request does not exist")

// TokenTransactionStore defines the interface for a token transaction database.
// This database is used to store records related to the processed token transactions.
type TokenTransactionStore interface {
	TransactionStore
	TransactionEndorsementAckStore
}

//go:generate counterfeiter -o mock/tst.go -fake-name TransactionStoreTransaction . TransactionStoreTransaction
type TransactionStoreTransaction interface {
	Transaction

	// AddTokenRequest binds the passed transaction id to the passed token request
	//
	// Verification: an implementation must refuse an empty txID, an empty token
	// request, or an empty ppHash — see
	// integrity.CheckTokenRequestForStorage. A stored request must remain
	// retrievable as evidence about txID under the public parameters ppHash
	// identifies, and none of the three is meaningful when empty.
	AddTokenRequest(ctx context.Context, txID string, tr []byte, applicationMetadata, publicMetadata map[string][]byte, ppHash driver.PPHash) error

	// AddMovement adds a movement record to the database transaction.
	// Each token transaction can be seen as a list of movements.
	// This operation _requires_ a TokenRequest with the same tx_id to exist
	AddMovement(ctx context.Context, records ...MovementRecord) error

	// AddTransaction adds a transaction record to the database transaction.
	// This operation _requires_ a TokenRequest with the same tx_id to exist
	AddTransaction(ctx context.Context, records ...TransactionRecord) error

	// SetStatus sets the status of a TokenRequest
	// (and with that, the associated Movement and Transaction)
	SetStatus(ctx context.Context, txID string, status driver.TxStatus, message string) error
}

type TransactionStore interface {
	// Close closes the databases
	Close() error

	// NewTransactionStoreTransaction opens an atomic database transaction. It must be committed or discarded.
	NewTransactionStoreTransaction() (TransactionStoreTransaction, error)

	// SetStatus sets the status of a TokenRequest
	// (and with that, the associated Movement and Transaction)
	SetStatus(ctx context.Context, txID string, status TxStatus, message string) error

	// GetStatus returns the status of a given transaction.
	// It returns an error if the transaction is not found
	GetStatus(ctx context.Context, txID string) (TxStatus, string, error)

	// GetStatuses returns the status of the given transaction ids, in a
	// single query. The returned map contains an entry only for tx ids that
	// were present in storage — callers should treat a missing key
	// identically to GetStatus returning Unknown. An empty or nil txIDs
	// slice returns an empty map without touching the database.
	GetStatuses(ctx context.Context, txIDs []string) (map[string]TxStatus, error)

	// QueryTransactions returns a list of transactions that match the given criteria
	QueryTransactions(ctx context.Context, params QueryTransactionsParams, pagination driver2.Pagination) (*driver2.PageIterator[*TransactionRecord], error)

	// QueryMovements returns a list of movement records
	QueryMovements(ctx context.Context, params QueryMovementsParams) ([]*MovementRecord, error)

	// QueryTokenRequests returns an iterator over the token requests matching the passed params
	QueryTokenRequests(ctx context.Context, params QueryTokenRequestsParams) (TokenRequestIterator, error)

	// GetTokenRequest returns the token request bound to the passed transaction id, if available.
	// It returns nil without error if the key is not found.
	//
	// Verification: a returned payload is a TokenRequestWithMetadata whose
	// anchor is txID — see integrity.CheckStoredTokenRequest. Callers treat it
	// as authentic evidence about txID (an auditor replays it, a recovery sweep
	// resubmits it), so a payload that does not parse, that carries an
	// unsupported version, or that is anchored to another transaction must be
	// reported as an error rather than returned. Not-found stays nil, nil.
	GetTokenRequest(ctx context.Context, txID string) ([]byte, error)

	// GetTokenRequests returns the token requests bound to the given
	// transaction ids, in a single query. The returned map contains an
	// entry only for tx ids that were present in storage — callers should
	// treat a missing key identically to GetTokenRequest returning nil
	// (no error, no record). An empty or nil txIDs slice returns an empty
	// map without touching the database.
	//
	// Empty map values: a driver that does not omit the key for a zero-length
	// request column scans it as a nil []byte on some drivers and as an empty
	// slice on others. Either representation is treated identically to a
	// missing key — the service layer skips it rather than treating it as a
	// corrupt record.
	//
	// Verification: as for GetTokenRequest, applied to every non-empty entry.
	// One failing entry fails the whole call: the caller cannot tell which
	// entries were checked from a partially filled map.
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

	// Notifier returns a TransactionNotifier for this store to subscribe to transaction status changes.
	Notifier() (TransactionNotifier, error)
}

type TransactionEndorsementAckStore interface {
	// AddTransactionEndorsementAck records the signature of a given endorser for a given transaction
	//
	// Verification: an implementation must refuse an empty txID, an empty
	// endorser, or an empty sigma — see integrity.CheckEndorsementAck. The
	// signature itself is verified by the caller against the payload it sent to
	// that endorser; the store never sees that payload and cannot repeat the
	// check. See token/services/ttx.Service.AppendTransactionEndorseAck.
	AddTransactionEndorsementAck(ctx context.Context, txID string, endorser token.Identity, sigma []byte) error

	// GetTransactionEndorsementAcks returns the endorsement signatures for the given transaction id
	//
	// Verification: the returned signatures are returned as stored. The message
	// each one signs is the per-party filtered payload that was sent to that
	// endorser, and it is not persisted, so neither the store nor the caller can
	// re-verify a signature after the fact. The service layer above drops — with
	// an ERROR log — any row that fails the non-vacuity check
	// AddTransactionEndorsementAck applies, so that one bad row does not make a
	// whole transaction unreadable.
	GetTransactionEndorsementAcks(ctx context.Context, txID string) (map[string][]byte, error)
}

// RecoveryLeadership represents an acquired leadership session for recovery sweeps.
//
//nolint:iface // See the note on CleanupLeadership: deliberately parallel, not shared.
type RecoveryLeadership interface {
	Close() error
}

type RecoveryClaimParams struct {
	OlderThan     time.Time
	LeaseDuration time.Duration
	Limit         int
	Owner         string
}

// RecoveryClaim is the minimal projection of a pending transaction row
// returned by ClaimPendingTransactions. The recovery loop only needs the
// TxID to act on and the StoredAt timestamp to decide grace-period
// promotions; the rest of TransactionRecord (action type, amounts,
// metadata, ...) was always discarded by the caller, so the SQL layer
// stops projecting it.
type RecoveryClaim struct {
	// TxID is the transaction ID claimed for recovery.
	TxID string
	// StoredAt is the storage timestamp of the underlying row (UTC), used
	// by the recovery loop to compute row age for grace-period decisions.
	StoredAt time.Time
}

// TransactionRecordReference contains the primary key fields of a transaction request record.
type TransactionRecordReference struct {
	// TxID is the unique identifier of the transaction request.
	TxID string
}

// TransactionNotifier is used to subscribe to transaction status changes in the storage.
type TransactionNotifier interface {
	// Subscribe registers a callback function to be called when a transaction request status is updated.
	Subscribe(callback func(Operation, TransactionRecordReference)) error
	// UnsubscribeAll unregisters all callbacks.
	UnsubscribeAll() error
}
