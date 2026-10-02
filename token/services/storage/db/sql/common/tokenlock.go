/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	fscdriver "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"
)

type tokenLockTables struct {
	TokenLocks string
	Tokens     string
	Requests   string
}

type TokenLockStore struct {
	ReadDB       *sql.DB
	WriteDB      WriteDB
	Table        tokenLockTables
	Logger       logging.Logger
	ci           common3.CondInterpreter
	errorWrapper fscdriver.SQLErrorWrapper
}

func newTokenLockStore(readDB *sql.DB, writeDB WriteDB, tables tokenLockTables, ci common3.CondInterpreter, errorWrapper fscdriver.SQLErrorWrapper) *TokenLockStore {
	return &TokenLockStore{
		ReadDB:       readDB,
		WriteDB:      writeDB,
		Table:        tables,
		Logger:       logger,
		ci:           ci,
		errorWrapper: errorWrapper,
	}
}

func NewTokenLockStore(readDB *sql.DB, writeDB WriteDB, tables TableNames, ci common3.CondInterpreter, errorWrapper fscdriver.SQLErrorWrapper) (*TokenLockStore, error) {
	return newTokenLockStore(
		readDB,
		writeDB,
		tokenLockTables{
			TokenLocks: tables.TokenLocks,
			Tokens:     tables.Tokens,
			Requests:   tables.Requests,
		},
		ci,
		errorWrapper,
	), nil
}

func (db *TokenLockStore) CreateSchema() error {
	return common.InitSchema(db.WriteDB, []string{db.GetSchema()}...)
}

// Lock locks the token for consumerTxID. walletID identifies the wallet the tokens are
// selected for; this SQL-backed store does not apply per-wallet rate limiting and so
// ignores it. A custom TokenLockStore may use walletID to throttle per wallet.
func (db *TokenLockStore) Lock(ctx context.Context, tokenID *token.ID, consumerTxID transaction.ID, walletID string) error {
	return db.LockAt(ctx, tokenID, consumerTxID, walletID, time.Now().UTC())
}

// LockAt is like Lock but records the supplied timestamp as the lock creation
// time instead of the current time. It is intended for testing (backdating locks
// to exercise the lease-age expiry path without sleeping).
//
// The lock row is inserted only if the token is still spendable at that instant, in the
// same statement, so the two outcomes a caller has to tell apart cannot interleave: a
// primary-key conflict means somebody else holds the lock (ErrTokenAlreadyLocked), and a
// zero-row insert means the token is no longer a valid candidate (ErrTokenNotSpendable).
// The foreign key alone is not enough for the second: it only requires the token row to
// exist, and spending a token soft-deletes it rather than removing the row, so without
// the predicate a lock can be acquired on a token that was spent since the caller read
// it - after which the caller returns it to a consumer that cannot load it. See #2395.
func (db *TokenLockStore) LockAt(ctx context.Context, tokenID *token.ID, consumerTxID transaction.ID, _ string, createdAt time.Time) error {
	query, args := db.lockQuery(tokenID, consumerTxID, createdAt)
	logging.Debug(logger, query, tokenID, consumerTxID)
	res, err := db.WriteDB.ExecContext(ctx, query, args...)
	if err != nil {
		if errors.Is(db.errorWrapper.WrapError(err), fscdriver.UniqueKeyViolation) {
			return errors.Wrapf(driver.ErrTokenAlreadyLocked, "token %s is already locked", tokenID)
		}

		// Any other failure means the lock was NOT taken. It has to be reported:
		// returning nil here would tell the caller it holds a lock it does not.
		return errors.Wrapf(err, "failed locking token [%s] for consumer [%s]", tokenID, consumerTxID)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		// The driver does not report affected rows. The INSERT itself succeeded, so
		// treat the lock as acquired rather than failing a selection on a driver
		// capability: this is the pre-#2395 behaviour, and the spendability predicate
		// is a narrowing of a race, never the only thing keeping a lock correct.
		logger.Debugf("driver does not report affected rows for the lock on [%s]: [%s]", tokenID, err)

		return nil
	}
	if affected == 0 {
		return errors.Wrapf(driver.ErrTokenNotSpendable, "token %s is no longer spendable", tokenID)
	}

	return nil
}

// lockQuery builds the conditional lock insert described on LockAt. The query builder
// only emits INSERT ... VALUES, so the INSERT ... SELECT ... WHERE EXISTS form is written
// out here; both supported dialects accept a SELECT with no FROM clause, and both take the
// $N placeholders the builder uses elsewhere, so the text needs no per-dialect variant.
// Every per-request value is bound, including the three booleans, so no value is formatted
// into the text.
func (db *TokenLockStore) lockQuery(tokenID *token.ID, consumerTxID transaction.ID, createdAt time.Time) (string, []any) {
	// #nosec G201 -- db.Table.TokenLocks/Tokens are trusted table names derived from this
	// process's own configuration at construction time, never from request input.
	query := fmt.Sprintf(
		"INSERT INTO %s (consumer_tx_id, tx_id, idx, created_at) "+
			"SELECT $1, $2, $3, $4 WHERE EXISTS ("+
			"SELECT 1 FROM %s WHERE tx_id = $5 AND idx = $6 "+
			"AND is_deleted = $7 AND spendable = $8 AND owner = $9)",
		db.Table.TokenLocks, db.Table.Tokens,
	)

	return query, []any{
		consumerTxID, tokenID.TxId, tokenID.Index, createdAt.UTC(),
		tokenID.TxId, tokenID.Index, false, true, true,
	}
}

func (db *TokenLockStore) UnlockByTxID(ctx context.Context, consumerTxID transaction.ID) error {
	query, args := q.DeleteFrom(db.Table.TokenLocks).
		Where(cond.Eq("consumer_tx_id", consumerTxID)).
		Format(db.ci)
	logging.Debug(logger, query, consumerTxID)

	if _, err := db.WriteDB.ExecContext(ctx, query, args...); err != nil {
		return errors.Wrapf(err, "failed unlocking tokens for consumer [%s]", consumerTxID)
	}

	return nil
}

// ListLocks returns every currently held lock, joined with the status of its consuming
// transaction. It reuses the same TokenLocks/Requests join as IsStaleLock/Cleanup, so
// the notion of "consuming transaction" stays consistent across the diagnostic reader
// and the actual expiry logic. See driver.TokenLockStore.ListLocks and #2395.
func (db *TokenLockStore) ListLocks(ctx context.Context) ([]driver.LockRecord, error) {
	tokenLocks, tokenRequests := q.Table(db.Table.TokenLocks), q.Table(db.Table.Requests)

	query, args := q.Select().
		Fields(
			tokenLocks.Field("consumer_tx_id"), tokenLocks.Field("tx_id"), tokenLocks.Field("idx"),
			tokenRequests.Field("status"), tokenLocks.Field("created_at"),
		).
		From(tokenLocks.Join(tokenRequests, cond.Cmp(tokenLocks.Field("consumer_tx_id"), "=", tokenRequests.Field("tx_id")))).
		Format(db.ci)
	logging.Debug(logger, query, args)

	rows, err := db.ReadDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	it := common.NewIterator(rows, func(entry *driver.LockRecord) error {
		var createdAt scannableTime
		if err := rows.Scan(&entry.ConsumerTxID, &entry.TokenID.TxId, &entry.TokenID.Index, &entry.Status, &createdAt); err != nil {
			return err
		}
		entry.CreatedAt = createdAt.Time

		return nil
	})

	records, err := iterators.ReadAllValues(it)
	if err != nil {
		return nil, err
	}
	// ReadAllValues stops as soon as rows.Next() reports false, and the shared
	// rowIterator never consults rows.Err(), so a mid-scan failure (connection reset,
	// statement timeout on this unbounded scan) would otherwise return a truncated
	// slice with a nil error - and the caller would report fewer locks than are
	// actually held, with no indication anything went wrong.
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "failed to read lock records")
	}

	return records, nil
}

// scannableTime scans a created_at value regardless of dialect. Postgres hands the
// database/sql driver a native time.Time; sqlite (modernc.org/sqlite) only performs
// that conversion for columns declared DATE/DATETIME/TIMESTAMP, and our shared schema
// declares created_at as TIMESTAMPTZ (deliberately, for timezone-consistent comparison
// against Postgres's NOW() - see Cleanup), so on sqlite the driver hands back the raw
// text it wrote the value as instead. Rather than weaken the shared schema, accept
// either shape here.
//
// The text shape is decided by the writer and by the driver's _time_format, neither of
// which this reader controls, so it tries several layouts rather than the single one
// modernc.org/sqlite happens to default to: pinning it to one makes ListLocks fail
// outright on any value that deviates. See sqliteTimeLayouts and #2395.
type scannableTime struct {
	time.Time
}

// sqliteTimeLayouts are the layouts a created_at value can arrive in as text, most likely
// first. The first is what modernc.org/sqlite writes a bound time.Time as by default
// (time.Time.String), absent a _time_format DSN option we don't set; the second is the same
// shape for a zone that has no abbreviation; the rest cover the _time_format settings and
// hand-written values that produce an RFC 3339 or a zone-less timestamp. Each carries
// .999999999, which makes the fractional seconds optional, so a whole-second value parses
// against the same layout.
var sqliteTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999 -0700",
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
}

// monotonicSuffix introduces the monotonic clock reading that time.Time.String() appends when
// the value still carries one. It is not part of any layout, so it has to come off before
// parsing: writers strip the reading today (LockAt binds createdAt.UTC(), and Time.UTC() drops
// it), but a reader that fails on the one shape a forgotten .UTC() produces is needlessly
// brittle. The reading itself is process-local and meaningless once persisted, so discarding it
// loses nothing - the wall-clock part it is appended to is the value.
const monotonicSuffix = " m="

func parseSQLTime(v string) (time.Time, error) {
	text := strings.TrimSpace(v)
	if i := strings.LastIndex(text, monotonicSuffix); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}

	var firstErr error
	for _, layout := range sqliteTimeLayouts {
		t, err := time.Parse(layout, text)
		if err == nil {
			return t, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}

	return time.Time{}, errors.Wrapf(firstErr, "cannot parse created_at value [%s] with any known layout", v)
}

func (s *scannableTime) Scan(src any) error {
	switch v := src.(type) {
	case time.Time:
		s.Time = v

		return nil
	case string:
		t, err := parseSQLTime(v)
		if err != nil {
			return err
		}
		s.Time = t

		return nil
	case []byte:
		return s.Scan(string(v))
	default:
		return errors.Errorf("cannot scan value of type [%T] into time.Time", src)
	}
}

func (db *TokenLockStore) GetSchema() string {
	return fmt.Sprintf(`
		-- TokenLocks
		CREATE TABLE IF NOT EXISTS %s (
			tx_id TEXT NOT NULL,
			idx INT NOT NULL,
			consumer_tx_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY(tx_id, idx),
			FOREIGN KEY (tx_id, idx) REFERENCES %s
		);
		CREATE INDEX IF NOT EXISTS idx_consumer_tx_id_%s ON %s ( consumer_tx_id );`,
		db.Table.TokenLocks,
		db.Table.Tokens,
		db.Table.TokenLocks,
		db.Table.TokenLocks,
	)
}

func (db *TokenLockStore) Close() error {
	return CloseRWDB(db.ReadDB, db.WriteDB)
}

func IsExpiredToken(tokenRequests, tokenLocks common3.Table, leaseExpiry time.Duration) cond.Condition {
	return cond.Or(
		cond.FieldIn(tokenRequests.Field("status"), driver.Deleted, driver.Orphan),
		cond.OlderThan(tokenLocks.Field("created_at"), leaseExpiry),
	)
}

// IsStaleLock matches the lock rows whose lease has aged out, or whose consuming
// transaction is Deleted or Orphan. The correlation is on consumer_tx_id, the
// transaction that is trying to spend the token: (tx_id, idx) identifies the locked
// token, i.e. the transaction that created it, whose status says nothing about
// whether the lock is still live. The condition is correlated rather than a
// partial-key IN on tx_id, so cleanup removes only the matching (tx_id, idx) rows
// and leaves the other indices of the same transaction locked. See #2018.
//
// The argument order matches IsExpiredToken (tokenRequests first, tokenLocks second)
// so a swapped call is immediately visible and consistent across this file.
func IsStaleLock(tokenRequests, tokenLocks common3.Table, leaseExpiry time.Duration) cond.Condition {
	return cond.Or(
		cond.OlderThan(tokenLocks.Field("created_at"), leaseExpiry),
		cond.Exists(
			q.Select().
				Fields(common3.FieldName("1")).
				From(tokenRequests).
				Where(cond.And(
					cond.Cmp(tokenRequests.Field("tx_id"), "=", tokenLocks.Field("consumer_tx_id")),
					cond.FieldIn(tokenRequests.Field("status"), driver.Deleted, driver.Orphan),
				)),
		),
	)
}

// Cleanup releases the stale token locks: those whose consuming transaction is
// Deleted or Orphan, and those whose lease is older than leaseExpiry. Only the
// affected (tx_id, idx) rows are deleted. The same statement is used by every SQL
// backend. created_at is declared TIMESTAMPTZ so the comparison with the
// database-side NOW() expression is always timezone-consistent on Postgres.
func (db *TokenLockStore) Cleanup(ctx context.Context, leaseExpiry time.Duration) error {
	tokenLocks, tokenRequests := q.Table(db.Table.TokenLocks), q.Table(db.Table.Requests)

	query, args := q.DeleteFrom(db.Table.TokenLocks).
		Where(IsStaleLock(tokenRequests, tokenLocks, leaseExpiry)).
		Format(db.ci)

	db.Logger.Debug(query, args)
	_, err := db.WriteDB.ExecContext(ctx, query, args...)
	if err != nil {
		db.Logger.Errorf("query failed: %s", query)
	}

	return err
}
