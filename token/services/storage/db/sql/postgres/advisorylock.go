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

// NewAdvisoryLockFactory returns a recovery leader factory function that uses PostgreSQL advisory locks.
func NewAdvisoryLockFactory() func(context.Context, *sql.DB, int64) (tokensdriver.RecoveryLeadership, bool, error) {
	return func(ctx context.Context, db *sql.DB, lockID int64) (tokensdriver.RecoveryLeadership, bool, error) {
		lock, acquired, err := NewAdvisoryLock(ctx, db, lockID)
		if err != nil || !acquired {
			return nil, acquired, err
		}

		return lock, true, nil
	}
}

// NewCleanupLeaderFactory returns a cleanup leader factory function that uses PostgreSQL advisory locks.
func NewCleanupLeaderFactory() func(context.Context, *sql.DB, int64) (tokensdriver.CleanupLeadership, bool, error) {
	return func(ctx context.Context, db *sql.DB, lockID int64) (tokensdriver.CleanupLeadership, bool, error) {
		lock, acquired, err := NewAdvisoryLock(ctx, db, lockID)
		if err != nil || !acquired {
			return nil, acquired, err
		}

		return lock, true, nil
	}
}
