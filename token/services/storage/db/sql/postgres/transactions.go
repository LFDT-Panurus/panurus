/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	scommon "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"

	tokensdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
)

// AuditTransactionStore wraps common.TransactionStore to add advisory lock to schema creation
type AuditTransactionStore struct {
	*sqlcommon.TransactionStore
	writeDB *sql.DB
	lockID  int64
	claims  *recoveryClaimStore
}

// WriteDB returns the underlying write *sql.DB.
// Used by the auditor distributed locker to share the connection pool.
func (s *AuditTransactionStore) WriteDB() *sql.DB { return s.writeDB }

// GetSchema overrides the base GetSchema to prefix with advisory lock
func (s *AuditTransactionStore) GetSchema() string {
	baseSchema := s.TransactionStore.GetSchema()

	return prefixSchemaWithLock(baseSchema, s.lockID)
}

// CreateSchema overrides the base CreateSchema to ensure GetSchema is called on the correct receiver
func (s *AuditTransactionStore) CreateSchema() error {
	return common.InitSchema(s.writeDB, s.GetSchema())
}

// TransactionStore extends the common TransactionStore with PostgreSQL-specific atomic claim operations.
type TransactionStore struct {
	*sqlcommon.TransactionStore
	writeDB *sql.DB
	lockID  int64
	claims  *recoveryClaimStore
}

// recoveryClaimStore holds the PostgreSQL-specific recovery claim operations shared by the
// owner and audit transaction stores. Both stores keep their claim state in the same
// columns of their own requests table, so the SQL is identical and only the table differs.
//
// It is a named field rather than an embedded one on purpose: sqlcommon.TransactionStore
// already provides ClaimPendingTransactions and ReleaseRecoveryClaim, and embedding both at
// the same depth would make those selectors ambiguous. Go then drops them from the method
// set and the store silently stops satisfying driver.TransactionStore. Explicit forwarding
// keeps the override visible at the call site.
type recoveryClaimStore struct {
	readDB  *sql.DB
	writeDB *sql.DB
	tables  sqlcommon.TableNames
}

func newRecoveryClaimStore(dbs *scommon.RWDB, tableNames sqlcommon.TableNames) *recoveryClaimStore {
	return &recoveryClaimStore{
		readDB:  dbs.ReadDB,
		writeDB: dbs.WriteDB,
		tables:  tableNames,
	}
}

// ClaimPendingTransactions atomically claims a batch of pending transactions.
func (db *TransactionStore) ClaimPendingTransactions(ctx context.Context, params tokensdriver.RecoveryClaimParams) ([]*tokensdriver.RecoveryClaim, error) {
	return db.claims.claimPending(ctx, params)
}

// ReleaseRecoveryClaim releases the recovery claim on a transaction.
func (db *TransactionStore) ReleaseRecoveryClaim(ctx context.Context, txID string, owner string, message string) error {
	return db.claims.releaseClaim(ctx, txID, owner, message)
}

// CleanupExpiredClaims removes expired recovery claims. Returns the number of claims cleaned up.
func (db *TransactionStore) CleanupExpiredClaims(ctx context.Context) (int, error) {
	return db.claims.cleanupExpired(ctx)
}

// ClaimPendingTransactions atomically claims a batch of pending audit transactions.
//
// Without this override the store inherits the permissive SELECT from
// sqlcommon.TransactionStore, which persists no claim at all: every replica selects the
// same pending rows on every sweep and processes all of them. The owner path has had the
// atomic claim since it was introduced; audit was simply never given it.
func (s *AuditTransactionStore) ClaimPendingTransactions(ctx context.Context, params tokensdriver.RecoveryClaimParams) ([]*tokensdriver.RecoveryClaim, error) {
	return s.claims.claimPending(ctx, params)
}

// ReleaseRecoveryClaim releases the recovery claim on an audit transaction.
// The inherited implementation is a no-op, which would leave the claim set until its lease
// expired even after the sweep finished with the row.
//
// Deliberately drops the caller's message instead of forwarding it to status_message.
// recoverTransaction (recovery/manager.go) always releases with a claim-bookkeeping message
// ("recovered successfully", "recovery failed: %v"), not the tx's actual audit-trail reason:
// applyFinalityLogic (ttx/finality/recovery.go) already writes that reason via SetStatus
// *before* this release runs whenever a row's status genuinely changes, and returns nil
// without touching status for a still-Pending tx (Busy/Unknown), a case recoverTransaction
// cannot tell apart from success. Forwarding message here would overwrite a real ledger
// rejection reason with "recovered successfully", and would do so on every still-pending row
// on every sweep, exactly what an audit trail must not do. The owner path keeps forwarding
// the bookkeeping message; that behavior predates this PR and is unchanged here.
func (s *AuditTransactionStore) ReleaseRecoveryClaim(ctx context.Context, txID string, owner string, _ string) error {
	return s.claims.releaseClaim(ctx, txID, owner, "")
}

// GetSchema overrides the base GetSchema to prefix with advisory lock
func (s *TransactionStore) GetSchema() string {
	baseSchema := s.TransactionStore.GetSchema()

	return prefixSchemaWithLock(baseSchema, s.lockID)
}

// CreateSchema overrides the base CreateSchema to ensure GetSchema is called on the correct receiver
func (s *TransactionStore) CreateSchema() error {
	return common.InitSchema(s.writeDB, s.GetSchema())
}

// NewTransactionStoreWithNotifier creates a new TransactionStore with the provided notifier and recovery support.
func NewTransactionStoreWithNotifier(dbs *scommon.RWDB, tableNames sqlcommon.TableNames, notifier *TransactionNotifier) (*TransactionStore, error) {
	// Derive the recovery lock id from the fully qualified requests table name, which already
	// carries the network, channel and namespace that tell one TMS from another. Deriving it from
	// the table prefix would not be enough: several TMSes normally share one persistence
	// configuration and are distinguished only by those params, so they would all land on the same
	// id and every TMS but one would skip its sweep. See #1798 for the same bug on the token locks.
	recoveryLeaderFactory := NewAdvisoryLockFactoryForID(recoveryLockID(tableNames))

	commonStore, err := sqlcommon.NewTransactionStoreWithNotifierAndRecovery(
		dbs.ReadDB,
		dbs.WriteDB,
		tableNames,
		NewConditionInterpreter(),
		NewPaginationInterpreter(),
		notifier,
		recoveryLeaderFactory,
	)
	if err != nil {
		return nil, err
	}

	return &TransactionStore{
		TransactionStore: commonStore,
		writeDB:          dbs.WriteDB,
		lockID:           createTableLockID("transactions"),
		claims:           newRecoveryClaimStore(dbs, tableNames),
	}, nil
}

// NewAuditTransactionStore creates a new AuditTransactionStore.
func NewAuditTransactionStore(dbs *scommon.RWDB, tableNames sqlcommon.TableNames) (*AuditTransactionStore, error) {
	baseStore, err := sqlcommon.NewAuditTransactionStore(
		dbs.ReadDB,
		dbs.WriteDB,
		tableNames,
		NewConditionInterpreter(),
		NewPaginationInterpreter(),
	)
	if err != nil {
		return nil, err
	}

	return &AuditTransactionStore{
		TransactionStore: baseStore,
		writeDB:          dbs.WriteDB,
		lockID:           createTableLockID("audittx"),
		claims:           newRecoveryClaimStore(dbs, tableNames),
	}, nil
}

// claimPending atomically claims a batch of pending transactions using PostgreSQL's UPDATE...RETURNING.
// This ensures only one recovery instance can claim a specific transaction.
// All state we need lives on the requests table (tx_id PK + stored_at + status
// + recovery_claim_* lease columns); the transactions table is no longer
// touched. RETURNING tx_id, stored_at directly from the UPDATE removes the
// outer join the previous CTE used to recover the timestamp.
func (db *recoveryClaimStore) claimPending(ctx context.Context, params tokensdriver.RecoveryClaimParams) ([]*tokensdriver.RecoveryClaim, error) {
	logger.Debugf("Claiming pending transactions: owner=%s, olderThan=%s, limit=%d, lease=%s",
		params.Owner, params.OlderThan, params.Limit, params.LeaseDuration)

	// Single-table atomic claim:
	// 1. Find pending rows older than olderThan whose claim slot is free or
	//    already owned by us (FOR UPDATE SKIP LOCKED to avoid blocking peers).
	// 2. UPDATE sets the claim and RETURNINGs the columns the recovery loop
	//    consumes. ORDER BY is applied in the inner SELECT; the outer ordering
	//    is dropped because RETURNING from UPDATE does not preserve order
	//    deterministically anyway, and the recovery loop does not depend on it.
	// #nosec G201
	query := fmt.Sprintf(`
		UPDATE %s
		SET
			recovery_claimed_by = $1,
			recovery_claim_expires_at = NOW() + $2::INTERVAL
		WHERE tx_id IN (
			SELECT tx_id
			FROM %s
			WHERE status = $3
			  AND stored_at < $4
			  AND (
				  recovery_claimed_by IS NULL
				  OR recovery_claim_expires_at < NOW()
				  OR recovery_claimed_by = $1
			  )
			ORDER BY stored_at ASC
			LIMIT $5
			FOR UPDATE SKIP LOCKED
		)
		RETURNING tx_id, stored_at`,
		db.tables.Requests,
		db.tables.Requests,
	)

	// Convert lease duration to PostgreSQL interval format. Milliseconds, not seconds:
	// truncating a sub-second lease to "0 seconds" claims a row that is already expired.
	leaseInterval := fmt.Sprintf("%d milliseconds", params.LeaseDuration.Milliseconds())

	args := []any{
		params.Owner,
		leaseInterval,
		tokensdriver.Pending,
		params.OlderThan,
		params.Limit,
	}

	logger.Debug(query, args)

	// Execute the query
	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to claim pending transactions")
	}

	// Parse results
	results := common.NewIterator(rows, func(r *tokensdriver.RecoveryClaim) error {
		return rows.Scan(&r.TxID, &r.StoredAt)
	})

	claimed, err := iterators.ReadAllPointers(results)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read claimed transactions")
	}

	logger.Debugf("Claimed %d pending transactions for owner %s", len(claimed), params.Owner)

	return claimed, nil
}

// releaseClaim releases the recovery claim on a transaction.
// This clears the claim metadata and optionally updates the status message.
func (db *recoveryClaimStore) releaseClaim(ctx context.Context, txID string, owner string, message string) error {
	logger.Debugf("Releasing recovery claim: txID=%s, owner=%s, message=%s", txID, owner, message)

	// Build the release query using query builder
	// Only release if the transaction is owned by the specified owner (safety check)
	var query string
	var args []any

	if message != "" {
		query, args = q.Update(db.tables.Requests).
			Set("recovery_claimed_by", nil).
			Set("recovery_claim_expires_at", nil).
			Set("status_message", message).
			Where(cond.And(
				cond.Eq("tx_id", txID),
				cond.Eq("recovery_claimed_by", owner),
			)).
			Format(NewConditionInterpreter())
	} else {
		query, args = q.Update(db.tables.Requests).
			Set("recovery_claimed_by", nil).
			Set("recovery_claim_expires_at", nil).
			Where(cond.And(
				cond.Eq("tx_id", txID),
				cond.Eq("recovery_claimed_by", owner),
			)).
			Format(NewConditionInterpreter())
	}

	logger.Debug(query, args)

	result, err := db.writeDB.ExecContext(ctx, query, args...)
	if err != nil {
		return errors.Wrapf(err, "failed to release recovery claim for tx %s", txID)
	}

	// Check if any rows were affected
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return errors.Wrapf(err, "failed to get rows affected for tx %s", txID)
	}

	if rowsAffected == 0 {
		logger.Warnf("No recovery claim released for tx %s (not owned by %s or already released)", txID, owner)
	} else {
		logger.Debugf("Released recovery claim for tx %s", txID)
	}

	return nil
}

// cleanupExpired removes expired recovery claims.
// Returns the number of claims cleaned up.
func (db *recoveryClaimStore) cleanupExpired(ctx context.Context) (int, error) {
	logger.Debug("Cleaning up expired recovery claims")

	query, args := q.Update(db.tables.Requests).
		Set("recovery_claimed_by", nil).
		Set("recovery_claim_expires_at", nil).
		Where(cond.Lt("recovery_claim_expires_at", common3.FieldName("NOW()"))).
		Format(NewConditionInterpreter())

	logger.Debug(query, args)

	result, err := db.writeDB.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to cleanup expired recovery claims")
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, errors.Wrapf(err, "failed to get rows affected during cleanup")
	}

	logger.Debugf("Cleaned up %d expired recovery claims", rowsAffected)

	return int(rowsAffected), nil
}

// TransactionNotifier handles notifications for transaction status changes.
type TransactionNotifier struct {
	*Notifier
}

// NewTransactionNotifier returns a new TransactionNotifier for the given RWDB and table names.
func NewTransactionNotifier(dbs *scommon.RWDB, tableNames sqlcommon.TableNames, dataSource string) (*TransactionNotifier, error) {
	return &TransactionNotifier{
		Notifier: NewNotifier(
			dbs.WriteDB,
			tableNames.Requests,
			dataSource,
			[]tokensdriver.Operation{tokensdriver.Update}, // Only listen to UPDATE operations for status changes
			*NewSimplePrimaryKey("tx_id"),
		),
	}, nil
}

// Subscribe registers a callback function to be called when a transaction request status is updated.
func (n *TransactionNotifier) Subscribe(callback func(tokensdriver.Operation, tokensdriver.TransactionRecordReference)) error {
	return n.Notifier.Subscribe(func(operation tokensdriver.Operation, m map[tokensdriver.ColumnKey]string) {
		callback(operation, tokensdriver.TransactionRecordReference{
			TxID: m["tx_id"],
		})
	})
}
