/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"context"
	"database/sql"
	"math/rand/v2"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	fscdriver "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	fscsqlite "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"

	common4 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

const (
	// maxBusyRetries bounds how many times a statement rejected with
	// SQLITE_BUSY is retried. The write pool is opened with maxOpenConns=1 and
	// busy_timeout=5000, so SQLITE_BUSY means the lock was held by a *different*
	// connection - another store on the same file, or the shared-cache memory
	// database - for longer than five seconds. Retrying is worth it, retrying
	// forever is not: a caller waiting on a write it cannot get needs the error
	// back so it can fail its own operation rather than hang.
	maxBusyRetries = 3

	// busyRetryBaseDelay is the first backoff, doubled on each further attempt.
	// It is short relative to busy_timeout (which each attempt spends inside
	// SQLite waiting for the lock) and only exists to stagger contending writers.
	busyRetryBaseDelay = 10 * time.Millisecond
)

// BusyRetryWriteDB decorates a SQLite write pool so that a statement rejected
// with SQLITE_BUSY is retried a bounded number of times instead of surfacing to
// the caller.
//
// Without it, write contention beyond busy_timeout reaches Panurus as a raw
// SQLITE_BUSY: FSC wraps its own stores this way but nothing wrapped Panurus's,
// so the token, token-lock, wallet, identity, keystore, transaction and endorser
// stores all propagated it. That bites single-node deployments under concurrent
// writes, and the in-memory driver most of all - it is this driver, on
// file::memory:?cache=shared, where every store's write pool contends for one
// shared cache.
//
// It differs from FSC's retryWriteDB in being bounded: that one recurses on
// every SQLITE_BUSY with no limit and no backoff, so a lock held for good turns
// into an unbounded retry loop growing the stack.
//
// Only Exec and ExecContext are decorated. Begin and BeginTx are promoted from
// the embedded pool and hand out a raw *sql.Tx, so statements inside a
// transaction still see SQLITE_BUSY: replaying those means replaying the whole
// transaction, which only the caller can decide to do.
type BusyRetryWriteDB struct {
	*sql.DB

	errorWrapper fscdriver.SQLErrorWrapper
	maxRetries   int
	baseDelay    time.Duration
}

// The wrapper must remain usable wherever a store expects its write handle.
var _ common4.WriteDB = (*BusyRetryWriteDB)(nil)

// NewBusyRetryWriteDB wraps a SQLite write pool with a bounded SQLITE_BUSY retry.
func NewBusyRetryWriteDB(db *sql.DB) *BusyRetryWriteDB {
	return &BusyRetryWriteDB{
		DB:           db,
		errorWrapper: &fscsqlite.ErrorMapper{},
		maxRetries:   maxBusyRetries,
		baseDelay:    busyRetryBaseDelay,
	}
}

// Exec runs the statement with the retry policy, outside any context.
func (db *BusyRetryWriteDB) Exec(query string, args ...any) (sql.Result, error) {
	return db.ExecContext(context.Background(), query, args...)
}

// ExecContext runs the statement, retrying while SQLite reports the database as
// busy. It gives up on the first non-busy outcome, once maxRetries retries are
// spent, or as soon as ctx is done - in which case the busy error is returned
// rather than the context's, since that is what actually failed the statement.
func (db *BusyRetryWriteDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	for attempt := 0; ; attempt++ {
		res, err := db.DB.ExecContext(ctx, query, args...)
		if err == nil || !errors.Is(db.errorWrapper.WrapError(err), fscdriver.SqlBusy) {
			return res, err
		}
		if attempt >= db.maxRetries {
			logger.WarnfContext(ctx, "sqlite busy after %d retries, giving up on query [%s]", db.maxRetries, query)

			return res, err
		}

		logger.DebugfContext(ctx, "sqlite busy, retrying query [%s] (attempt %d/%d)", query, attempt+1, db.maxRetries)
		if waitErr := sleepCtx(ctx, db.backoff(attempt)); waitErr != nil {
			return res, err
		}
	}
}

// backoff returns the delay before the retry following attempt: the base delay
// doubled per attempt, with up to 50% jitter so that writers contending for the
// same lock do not all wake together.
func (db *BusyRetryWriteDB) backoff(attempt int) time.Duration {
	d := db.baseDelay << attempt

	return d + rand.N(d/2+1)
}

// sleepCtx waits for d, or returns ctx's error if it is done first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
