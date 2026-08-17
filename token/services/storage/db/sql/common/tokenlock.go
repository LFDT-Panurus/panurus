/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	fscdriver "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"
)

type tokenLockTables struct {
	TokenLocks string
	Tokens     string
	Requests   string
}

type TokenLockStore struct {
	ReadDB       *sql.DB
	WriteDB      *sql.DB
	Table        tokenLockTables
	Logger       logging.Logger
	ci           common3.CondInterpreter
	errorWrapper fscdriver.SQLErrorWrapper
}

func newTokenLockStore(readDB, writeDB *sql.DB, tables tokenLockTables, ci common3.CondInterpreter, errorWrapper fscdriver.SQLErrorWrapper) *TokenLockStore {
	return &TokenLockStore{
		ReadDB:       readDB,
		WriteDB:      writeDB,
		Table:        tables,
		Logger:       logger,
		ci:           ci,
		errorWrapper: errorWrapper,
	}
}

func NewTokenLockStore(readDB, writeDB *sql.DB, tables TableNames, ci common3.CondInterpreter, errorWrapper fscdriver.SQLErrorWrapper) (*TokenLockStore, error) {
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
func (db *TokenLockStore) LockAt(ctx context.Context, tokenID *token.ID, consumerTxID transaction.ID, _ string, createdAt time.Time) error {
	query, args := q.InsertInto(db.Table.TokenLocks).
		Fields("consumer_tx_id", "tx_id", "idx", "created_at").
		Row(consumerTxID, tokenID.TxId, tokenID.Index, createdAt.UTC()).
		Format()
	logging.Debug(logger, query, tokenID, consumerTxID)
	_, err := db.WriteDB.ExecContext(ctx, query, args...)
	if err != nil && errors.Is(db.errorWrapper.WrapError(err), fscdriver.UniqueKeyViolation) {
		return errors.Wrapf(driver.ErrTokenAlreadyLocked, "token %s is already locked", tokenID)
	}

	return err
}

func (db *TokenLockStore) UnlockByTxID(ctx context.Context, consumerTxID transaction.ID) error {
	query, args := q.DeleteFrom(db.Table.TokenLocks).
		Where(cond.Eq("consumer_tx_id", consumerTxID)).
		Format(db.ci)
	logging.Debug(logger, query, consumerTxID)

	_, err := db.WriteDB.ExecContext(ctx, query, args...)

	return err
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
	return common2.Close(db.ReadDB, db.WriteDB)
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
