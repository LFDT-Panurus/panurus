/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"context"
	"database/sql"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	tokensdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	common5 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils"
)

var advisoryLockLogger = logging.MustGetLogger()

// AdvisoryLock implements RecoveryLeadership using PostgreSQL advisory locks.
// Advisory locks are session-scoped and automatically released when the connection closes.
type AdvisoryLock struct {
	db     *sql.DB
	lockID int64
	conn   *sql.Conn
	logger logging.Logger
}

// NewAdvisoryLock attempts to acquire a PostgreSQL advisory lock for the given lockID.
// Returns (lock, true, nil) if the lock was acquired successfully.
// Returns (nil, false, nil) if the lock is held by another session.
// Returns (nil, false, error) if an error occurred during acquisition.
//
// The lock is session-scoped and will be automatically released when:
// - Close() is called explicitly
// - The connection is closed
// - The process terminates
func NewAdvisoryLock(ctx context.Context, db *sql.DB, lockID int64) (*AdvisoryLock, bool, error) {
	logger := advisoryLockLogger

	// Get a dedicated connection for this lock
	// This connection must remain open for the lifetime of the lock
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed to acquire connection for advisory lock")
	}

	// Try to acquire the lock (non-blocking)
	// pg_try_advisory_lock returns true if the lock was acquired, false otherwise
	var acquired bool
	query := "SELECT pg_try_advisory_lock($1)"
	err = conn.QueryRowContext(ctx, query, lockID).Scan(&acquired)
	if err != nil {
		utils.IgnoreErrorFunc(conn.Close)

		return nil, false, errors.Wrapf(err, "failed to execute pg_try_advisory_lock for lock %d", lockID)
	}

	if !acquired {
		// Lock is held by another session
		utils.IgnoreErrorFunc(conn.Close)
		logger.Debugf("Advisory lock %d is held by another instance", lockID)

		return nil, false, nil
	}

	logger.Debugf("Acquired advisory lock %d", lockID)

	return &AdvisoryLock{
		db:     db,
		lockID: lockID,
		conn:   conn,
		logger: logger,
	}, true, nil
}

// Close releases the advisory lock and closes the connection.
// It is safe to call Close multiple times.
func (l *AdvisoryLock) Close() error {
	if l.conn == nil {
		return nil
	}

	// Release the lock explicitly before closing the connection
	// This is not strictly necessary as the lock auto-releases on connection close,
	// but it's good practice for clarity and immediate release
	_, err := l.conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", l.lockID)
	if err != nil {
		l.logger.Warnf("Failed to explicitly release advisory lock %d: %v (will auto-release on connection close)", l.lockID, err)
	} else {
		l.logger.Debugf("Released advisory lock %d", l.lockID)
	}

	// Close the connection (this also releases the lock if unlock failed)
	closeErr := l.conn.Close()
	l.conn = nil // Prevent double-close

	if closeErr != nil {
		return errors.Wrapf(closeErr, "failed to close connection for advisory lock %d", l.lockID)
	}

	return nil
}

// recoveryLockID derives the advisory lock id guarding a TMS's recovery sweep from its fully
// qualified requests table name, which already carries the network, channel and namespace. The
// table prefix alone is not enough: TMSes normally share one persistence configuration and are
// distinguished only by those params, so a prefix-derived id would collide across them.
func recoveryLockID(tables common5.TableNames) int64 {
	return createTableLockID(tables.Requests + "_recovery")
}

// NewAdvisoryLockFactoryForID returns a recovery leader factory bound to lockID. Binding the id
// at construction keeps it out of the call path, so no caller has to know how a lock id is made
// unique and none can accidentally pass one that is shared with another TMS.
func NewAdvisoryLockFactoryForID(lockID int64) func(context.Context, *sql.DB) (tokensdriver.RecoveryLeadership, bool, error) {
	return func(ctx context.Context, db *sql.DB) (tokensdriver.RecoveryLeadership, bool, error) {
		lock, acquired, err := NewAdvisoryLock(ctx, db, lockID)
		if err != nil || !acquired {
			return nil, acquired, err
		}

		return lock, true, nil
	}
}

// tokenLockCleanupLockID derives the advisory lock id guarding a TMS's token lock cleanup sweep
// from its fully qualified token locks table name. Same reasoning as recoveryLockID.
func tokenLockCleanupLockID(tables common5.TableNames) int64 {
	return createTableLockID(tables.TokenLocks + "_cleanup")
}

// keystoreCleanupLockID derives the advisory lock id guarding a TMS's keystore cleanup sweep from
// its fully qualified token SKI cleanup table name, for the same reason as recoveryLockID: the
// table prefix alone is shared by every TMS on one persistence configuration.
func keystoreCleanupLockID(tables common5.TableNames) int64 {
	return createTableLockID(tables.TokenSKICleanups + "_cleanup")
}

// NewCleanupLeaderFactoryForID returns a cleanup leader factory bound to lockID, so the id is
// fixed when the store is built rather than supplied per call.
func NewCleanupLeaderFactoryForID(lockID int64) func(context.Context, *sql.DB) (tokensdriver.CleanupLeadership, bool, error) {
	return func(ctx context.Context, db *sql.DB) (tokensdriver.CleanupLeadership, bool, error) {
		lock, acquired, err := NewAdvisoryLock(ctx, db, lockID)
		if err != nil || !acquired {
			return nil, acquired, err
		}

		return lock, true, nil
	}
}
