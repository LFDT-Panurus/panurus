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
	fscPostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"

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

	// cleanupLockID is derived from the fully-qualified table name (not the
	// prefix alone, which is not unique per TMS - see review discussion on
	// #1982), so it is unique per TMS - distinct from lockID (schema-creation
	// lock) and from other TMSes' cleanup locks on the same node. A single global constant here
	// was a real bug: it caused every TMS on a node to compete for the
	// exact same advisory lock, so only one TMS across the whole fleet
	// ever won cleanup on any tick. See #1798.
	cleanupLockID        int64
	cleanupLeaderFactory func(context.Context, *sql.DB, int64) (driver.CleanupLeadership, bool, error)
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
	tldb, err := common5.NewTokenLockStore(dbs.ReadDB, dbs.WriteDB, tableNames, ci, &fscPostgres.ErrorMapper{})
	if err != nil {
		return nil, err
	}

	return &TokenLockStore{
		TokenLockStore:       tldb,
		writeDB:              dbs.WriteDB,
		ci:                   ci,
		lockID:               createTableLockID(tableNames.TokenLocks),
		cleanupLockID:        createTableLockID(tableNames.TokenLocks + "_cleanup"),
		cleanupLeaderFactory: NewCleanupLeaderFactory(),
	}, nil
}

// AcquireCleanupLeadership attempts to acquire a Postgres advisory lock so
// only one replica runs Cleanup per tick for this TMS; others skip the tick
// and release no held resources, so contention is limited to the acquire
// attempt itself. The lock id and factory are fixed at construction. Note
// the winner holds a dedicated connection off writeDB for the tick's
// duration (see NewAdvisoryLock), so writeDB's connection pool needs at
// least one spare connection beyond normal write traffic. See #1798.
func (db *TokenLockStore) AcquireCleanupLeadership(ctx context.Context) (driver.CleanupLeadership, bool, error) {
	return db.cleanupLeaderFactory(ctx, db.writeDB, db.cleanupLockID)
}

// Cleanup removes stale token locks that have expired. The deletion itself is the
// backend-independent one implemented by the embedded store; Postgres only adds the
// logging of the rows that are about to go.
func (db *TokenLockStore) Cleanup(ctx context.Context, leaseExpiry time.Duration) error {
	if err := db.logStaleLocks(ctx, leaseExpiry); err != nil {
		db.Logger.Warnf("Could not log stale locks: %v", err)
	}

	return db.TokenLockStore.Cleanup(ctx, leaseExpiry)
}

// logStaleLocks logs the token locks that are about to be deleted.
// NOW() returns timestamptz; created_at is also TIMESTAMPTZ, so both sides of
// the age comparison are timezone-consistent.
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
