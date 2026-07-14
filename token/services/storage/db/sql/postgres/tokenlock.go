/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	common5 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/LFDT-Panurus/panurus/token/token"
	"go.uber.org/zap/zapcore"
)

// TokenLockStore implements the token lock storage for Postgres.
type TokenLockStore struct {
	*common5.TokenLockStore

	writeDB *sql.DB
	ci      common3.CondInterpreter
	lockID  int64
}

// GetSchema overrides the base GetSchema to prefix with advisory lock
func (s *TokenLockStore) GetSchema() string {
	baseSchema := s.TokenLockStore.GetSchema()

	return prefixSchemaWithLock(baseSchema, s.lockID)
}

// CreateSchema overrides the base CreateSchema to ensure GetSchema is called on the correct receiver
func (s *TokenLockStore) CreateSchema() error {
	return common.InitSchema(s.writeDB, s.GetSchema())
}

// NewTokenLockStore returns a new TokenLockStore for the given RWDB and table names.
func NewTokenLockStore(dbs *common2.RWDB, tableNames common5.TableNames) (*TokenLockStore, error) {
	ci := NewConditionInterpreter()
	tldb, err := common5.NewTokenLockStore(dbs.ReadDB, dbs.WriteDB, tableNames, ci)
	if err != nil {
		return nil, err
	}

	return &TokenLockStore{
		TokenLockStore: tldb,
		writeDB:        dbs.WriteDB,
		ci:             ci,
		lockID:         createTableLockID("tokenlock"),
	}, nil
}

// Cleanup removes stale token locks that have expired.
func (db *TokenLockStore) Cleanup(ctx context.Context, leaseExpiry time.Duration) error {
	if err := db.logStaleLocks(ctx, leaseExpiry); err != nil {
		db.Logger.Warnf("Could not log stale locks: %v", err)
	}
	tokenLocks, tokenRequests := q.Table(db.Table.TokenLocks), q.Table(db.Table.Requests)

	existsDeletedOrOrphan := cond.Exists(
		q.Select().
			Fields(common3.FieldName("1")).
			From(tokenRequests).
			Where(cond.And(
				cond.Cmp(tokenRequests.Field("tx_id"), "=", tokenLocks.Field("consumer_tx_id")),
				cond.FieldIn(tokenRequests.Field("status"), driver.Deleted, driver.Orphan),
			)),
	)

	query, args := q.DeleteFrom(db.Table.TokenLocks).
		Where(cond.Or(
			cond.OlderThan(tokenLocks.Field("created_at"), leaseExpiry),
			existsDeletedOrOrphan,
		)).
		Format(db.ci)

	db.Logger.Debug(query)
	_, err := db.WriteDB.ExecContext(ctx, query, args...)
	if err != nil {
		db.Logger.Errorf("query failed: %s", query)
	}

	return err
}

// logStaleLocks logs the token locks that are about to be deleted.
func (db *TokenLockStore) logStaleLocks(ctx context.Context, leaseExpiry time.Duration) error {
	if !db.Logger.IsEnabledFor(zapcore.InfoLevel) {
		return nil
	}
	tokenLocks, tokenRequests := q.Table(db.Table.TokenLocks), q.Table(db.Table.Requests)

	query, args := q.Select().
		Fields(
			tokenLocks.Field("consumer_tx_id"), tokenLocks.Field("tx_id"), tokenLocks.Field("idx"),
			tokenRequests.Field("status"), tokenLocks.Field("created_at"), common3.FieldName("NOW() AS now"),
		).
		From(tokenLocks.Join(tokenRequests, cond.Cmp(tokenLocks.Field("consumer_tx_id"), "=", tokenRequests.Field("tx_id")))).
		Where(common5.IsExpiredToken(tokenRequests, tokenLocks, leaseExpiry)).Format(db.ci)
	db.Logger.Debug(query, args)

	rows, err := db.ReadDB.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}

	it := common.NewIterator(rows, func(entry *lockEntry) error {
		entry.LeaseExpiry = leaseExpiry

		return rows.Scan(&entry.ConsumerTxID, &entry.TokenID.TxId, &entry.TokenID.Index, &entry.Status, &entry.CreatedAt, &entry.Now)
	})
	lockEntries, err := iterators.ReadAllValues(it)
	if err != nil {
		return err
	}

	db.Logger.Debugf("Found following entries ready for deletion: [%v]", lockEntries)

	return nil
}

type lockEntry struct {
	ConsumerTxID string
	TokenID      token.ID
	Status       *driver.TxStatus
	CreatedAt    time.Time
	Now          time.Time
	LeaseExpiry  time.Duration
}

func (e lockEntry) Expired() bool {
	return e.CreatedAt.Add(e.LeaseExpiry).Before(e.Now)
}

func (e lockEntry) String() string {
	if expired := e.Expired(); e.Status == nil && expired {
		return fmt.Sprintf("Expired lock created at [%v] for token [%s] consumed by [%s]", e.CreatedAt, e.TokenID, e.ConsumerTxID)
	} else if e.Status != nil && *e.Status == driver.Deleted && !expired {
		return fmt.Sprintf("Lock created at [%v] of spent token [%s] consumed by [%s]", e.CreatedAt, e.TokenID, e.ConsumerTxID)
	} else {
		return fmt.Sprintf("Invalid token lock state: [%s] created at [%v], expired [%v], status: [%v]", e.TokenID, e.CreatedAt, expired, e.Status)
	}
}
