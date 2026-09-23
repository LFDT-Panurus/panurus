/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"context"
	"database/sql"
	"path"
	"reflect"
	"testing"
	"time"

	fscdriver "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	fscsqlite "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// busyFixture is two pools over one SQLite file, with SQLite's own busy handler
// disabled so that a contended write is rejected immediately. That makes
// SQLITE_BUSY reproducible without waiting out busy_timeout, and leaves the wait
// entirely to the policy under test.
type busyFixture struct {
	holder    *sql.DB
	contender *sql.DB
}

func newBusyFixture(t *testing.T) *busyFixture {
	t.Helper()

	dsn := "file:" + path.Join(t.TempDir(), "busy.sqlite") + "?_pragma=busy_timeout(0)"
	open := func() *sql.DB {
		db, err := sql.Open("sqlite", dsn)
		require.NoError(t, err)
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, db.Close()) })

		return db
	}

	f := &busyFixture{holder: open(), contender: open()}
	_, err := f.holder.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY)`)
	require.NoError(t, err)

	return f
}

// lockWrites takes the file's write lock and returns a function releasing it.
func (f *busyFixture) lockWrites(t *testing.T) func() {
	t.Helper()

	tx, err := f.holder.Begin()
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO t (id) VALUES (1)`)
	require.NoError(t, err)

	return func() { require.NoError(t, tx.Rollback()) }
}

// retryDB builds the wrapper with a policy tuned for the test: enough retries to
// outlast the fixture's lock, with a delay short enough to keep the test quick.
func retryDB(db *sql.DB, retries int) *BusyRetryWriteDB {
	return retryDBWithDelay(db, retries, 5*time.Millisecond)
}

func retryDBWithDelay(db *sql.DB, retries int, baseDelay time.Duration) *BusyRetryWriteDB {
	return &BusyRetryWriteDB{
		DB:           db,
		errorWrapper: &fscsqlite.ErrorMapper{},
		maxRetries:   retries,
		baseDelay:    baseDelay,
	}
}

// TestBusyRetryWriteDBIsNeeded pins the premise of #2043 item 4: with no retry,
// a write contended beyond busy_timeout surfaces SQLITE_BUSY to the caller.
func TestBusyRetryWriteDBIsNeeded(t *testing.T) {
	t.Parallel()

	f := newBusyFixture(t)
	release := f.lockWrites(t)
	defer release()

	_, err := f.contender.ExecContext(t.Context(), `INSERT INTO t (id) VALUES (2)`)
	require.Error(t, err)
	require.ErrorIs(t, (&fscsqlite.ErrorMapper{}).WrapError(err), fscdriver.SqlBusy)
}

// TestBusyRetryWriteDBRetriesUntilLockReleased verifies the statement succeeds
// once the contending writer lets go, rather than failing on the first refusal.
func TestBusyRetryWriteDBRetriesUntilLockReleased(t *testing.T) {
	t.Parallel()

	f := newBusyFixture(t)
	release := f.lockWrites(t)
	released := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		release()
		close(released)
	}()

	_, err := retryDB(f.contender, 10).ExecContext(t.Context(), `INSERT INTO t (id) VALUES (2)`)
	require.NoError(t, err)
	<-released
}

// TestBusyRetryWriteDBGivesUp verifies the retry is bounded: a lock that is never
// released returns the busy error instead of retrying forever, which is how this
// differs from FSC's unbounded retryWriteDB.
func TestBusyRetryWriteDBGivesUp(t *testing.T) {
	t.Parallel()

	f := newBusyFixture(t)
	release := f.lockWrites(t)
	defer release()

	_, err := retryDB(f.contender, 2).ExecContext(t.Context(), `INSERT INTO t (id) VALUES (2)`)
	require.Error(t, err)
	require.ErrorIs(t, (&fscsqlite.ErrorMapper{}).WrapError(err), fscdriver.SqlBusy,
		"the busy error itself must reach the caller, not a retry-exhausted substitute")
}

// TestBusyRetryWriteDBStopsOnContextExpiry verifies that a context expiring
// during a backoff ends the retries there and then, instead of sleeping out the
// remaining attempts, and that the caller is told what actually failed the
// statement: the database was busy, not that the context went away.
//
// The deadline is set well below the first backoff and well above the attempt
// that precedes it, so the expiry always lands inside the wait.
func TestBusyRetryWriteDBStopsOnContextExpiry(t *testing.T) {
	t.Parallel()

	f := newBusyFixture(t)
	release := f.lockWrites(t)
	defer release()

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := retryDBWithDelay(f.contender, 1000, 5*time.Second).
		ExecContext(ctx, `INSERT INTO t (id) VALUES (2)`)
	require.Error(t, err)
	require.Less(t, time.Since(start), 5*time.Second, "the backoff must be abandoned on expiry")
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorIs(t, (&fscsqlite.ErrorMapper{}).WrapError(err), fscdriver.SqlBusy)
}

// TestBusyRetryWriteDBDoesNotRetryACancelledContext verifies that a context
// already done when the call starts costs a single attempt: database/sql rejects
// it before touching the driver, and that is not a busy error to retry.
func TestBusyRetryWriteDBDoesNotRetryACancelledContext(t *testing.T) {
	t.Parallel()

	f := newBusyFixture(t)
	release := f.lockWrites(t)
	defer release()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	start := time.Now()
	_, err := retryDBWithDelay(f.contender, 1000, time.Second).
		ExecContext(ctx, `INSERT INTO t (id) VALUES (2)`)
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, time.Since(start), time.Second)
}

// TestBusyRetryWriteDBPassesThroughOtherErrors verifies only busy is retried: any
// other failure is returned as it is, on the first attempt.
func TestBusyRetryWriteDBPassesThroughOtherErrors(t *testing.T) {
	t.Parallel()

	f := newBusyFixture(t)

	_, err := retryDB(f.contender, 10).ExecContext(t.Context(), `INSERT INTO nonexistent (id) VALUES (1)`)
	require.Error(t, err)
	require.NotErrorIs(t, (&fscsqlite.ErrorMapper{}).WrapError(err), fscdriver.SqlBusy)
}

// TestBusyRetryWriteDBBackoffIsBoundedAndJittered guards the backoff: it grows
// per attempt and stays within the jitter window, so a retry storm cannot turn
// into an unbounded wait.
func TestBusyRetryWriteDBBackoffIsBoundedAndJittered(t *testing.T) {
	t.Parallel()

	db := retryDB(nil, maxBusyRetries)
	for attempt := range maxBusyRetries {
		base := db.baseDelay << attempt
		for range 50 {
			d := db.backoff(attempt)
			require.GreaterOrEqual(t, d, base)
			require.LessOrEqual(t, d, base+base/2)
		}
	}
}

// TestNewBusyRetryWriteDBSatisfiesWriteDB pins that the wrapper is usable
// wherever a store expects a write handle, Conn included - Postgres advisory
// locks need it, and a decorated handle must not lose it.
func TestNewBusyRetryWriteDBSatisfiesWriteDB(t *testing.T) {
	t.Parallel()

	f := newBusyFixture(t)
	wrapped := NewBusyRetryWriteDB(f.contender)

	conn, err := wrapped.Conn(t.Context())
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	tx, err := wrapped.Begin()
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())

	_, err = wrapped.Exec(`INSERT INTO t (id) VALUES (3)`)
	require.NoError(t, errors.WithMessage(err, "Exec through the wrapper"))
}

// TestEverySQLiteStoreWrapsItsWriteDB is the regression guard for #2043 item 4:
// none of the SQLite stores wrapped their write pool, so write contention beyond
// busy_timeout reached callers as a raw SQLITE_BUSY. It walks every store the
// driver can build and asserts the write handle is the retry wrapper, so a store
// added later that passes dbs.WriteDB straight through fails here.
//
// The handle is reached by reflection because most stores keep it in an
// unexported field; only its type is read, never its value.
func TestEverySQLiteStoreWrapsItsWriteDB(t *testing.T) {
	t.Parallel()

	d := NewDriver(sqliteCfg(t.TempDir(), "retry_wiring"))

	for _, tc := range []struct {
		name  string
		field string
		build func() (any, error)
	}{
		{"token lock", "WriteDB", func() (any, error) { return d.NewTokenLock("") }},
		{"wallet", "writeDB", func() (any, error) { return d.NewWallet("") }},
		{"identity", "writeDB", func() (any, error) { return d.NewIdentity("") }},
		{"key store", "writeDB", func() (any, error) { return d.NewKeyStore("") }},
		{"token", "writeDB", func() (any, error) { return d.NewToken("") }},
		{"audit transaction", "writeDB", func() (any, error) { return d.NewAuditTransaction("") }},
		{"owner transaction", "writeDB", func() (any, error) { return d.NewOwnerTransaction("") }},
		{"endorser", "writeDB", func() (any, error) { return d.NewEndorser("") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := tc.build()
			require.NoError(t, err)
			require.Equal(t, reflect.TypeFor[*BusyRetryWriteDB](), writeHandleType(t, store, tc.field),
				"the %s store must execute its writes through the SQLITE_BUSY retry", tc.name)
		})
	}
}

// writeHandleType returns the dynamic type behind a store's write handle field,
// following the embedded common store when the field is not on the outer type.
func writeHandleType(t *testing.T, store any, field string) reflect.Type {
	t.Helper()

	v := reflect.ValueOf(store)
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}

	handle := v.FieldByName(field)
	if !handle.IsValid() {
		// The outer type only embeds the shared store; descend into it.
		for _, embedded := range v.Fields() {
			for embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() != reflect.Struct {
				continue
			}
			if handle = embedded.FieldByName(field); handle.IsValid() {
				break
			}
		}
	}
	require.True(t, handle.IsValid(), "no %q field found on %T", field, store)
	require.False(t, handle.IsNil(), "the %q field of %T is nil", field, store)

	return handle.Elem().Type()
}
