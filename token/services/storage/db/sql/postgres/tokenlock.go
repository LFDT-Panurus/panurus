/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"
	fscPostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	common5 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	"github.com/LFDT-Panurus/panurus/token/token"
	"go.uber.org/zap/zapcore"
)

// TokenLockStore implements the token lock storage for Postgres.
type TokenLockStore struct {
	*common5.TokenLockStore

	writeDB  *sql.DB
	ci       common3.CondInterpreter
	lockID   int64
	strategy string

	// roundTrips counts every Lock and LockBatch call - each now issues exactly one query
	// under every strategy, so this is also the exact DB round-trip count, and is expected to
	// come out equal across strategies for a given workload: batching, not strategy choice, is
	// what saves round trips. uniqueViolations counts only Lock calls whose ErrTokenAlreadyLocked
	// came from a real server-side unique-constraint violation - possible solely via Lock under
	// LockStrategyInsert (LockBatch never issues a plain INSERT, and LockStrategyOnConflict/
	// LockStrategySkipLocked translate a lost race into a clean zero-row result instead), so it
	// is the real, hard count of the server-side errors that caused the CERT log storm. Exists so
	// a benchmark can report both with real numbers rather than inferring them from conflict rate,
	// which does not move across strategies: see RoundTrips and UniqueViolations.
	roundTrips       atomic.Int64
	uniqueViolations atomic.Int64

	// cleanupLeaderFactory is bound at construction to an id derived from the fully-qualified
	// table name (not the prefix alone, which is not unique per TMS - see review discussion on
	// #1982), so it is unique per TMS - distinct from lockID (schema-creation
	// lock) and from other TMSes' cleanup locks on the same node. A single global constant here
	// was a real bug: it caused every TMS on a node to compete for the
	// exact same advisory lock, so only one TMS across the whole fleet
	// ever won cleanup on any tick. See #1798.
	cleanupLeaderFactory func(context.Context, *sql.DB) (driver.CleanupLeadership, bool, error)
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

// NewTokenLockStore returns a new TokenLockStore for the given RWDB and table names,
// using the default (insert) lock strategy.
func NewTokenLockStore(dbs *common2.RWDB, tableNames common5.TableNames) (*TokenLockStore, error) {
	return newTokenLockStoreWithStrategy(dbs, tableNames, common5.LockStrategyInsert)
}

// newTokenLockStoreWithStrategy is like NewTokenLockStore, but lets the caller select the
// lock-acquisition strategy (see common5.ConfigKeyLockStrategy). strategy is validated by
// common5.LoadStorageConfig before it reaches here; an empty string is treated as the
// default insert strategy.
func newTokenLockStoreWithStrategy(dbs *common2.RWDB, tableNames common5.TableNames, strategy string) (*TokenLockStore, error) {
	if strategy == "" {
		strategy = common5.LockStrategyInsert
	}
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
		cleanupLeaderFactory: NewCleanupLeaderFactoryForID(tokenLockCleanupLockID(tableNames)),
		strategy:             strategy,
	}, nil
}

// Lock locks the token for consumerTxID, using the configured strategy. The default
// (LockStrategyInsert) delegates unchanged to the embedded store: an INSERT that surfaces
// a lost race as a unique-constraint violation. LockStrategyOnConflict and
// LockStrategySkipLocked both use INSERT ... ON CONFLICT DO NOTHING RETURNING instead: a
// lost race is a normal zero-row result, not a server-side error. Note this overrides Lock,
// not LockAt: the embedded TokenLockStore.Lock calls LockAt on itself, not on this type (Go
// has no virtual dispatch), so overriding LockAt here would never be reached from callers
// that go through Lock.
func (db *TokenLockStore) Lock(ctx context.Context, tokenID *token.ID, consumerTxID transaction.ID, walletID string) error {
	db.roundTrips.Add(1)
	if db.strategy == common5.LockStrategyInsert {
		err := db.TokenLockStore.Lock(ctx, tokenID, consumerTxID, walletID)
		if errors.Is(err, driver.ErrTokenAlreadyLocked) {
			db.uniqueViolations.Add(1)
		}

		return err
	}

	won, err := db.tryInsertOnConflict(ctx, []*token.ID{tokenID}, consumerTxID, time.Now().UTC())
	if err != nil {
		return err
	}
	if len(won) == 0 {
		return errors.Wrapf(driver.ErrTokenAlreadyLocked, "token %s is already locked", tokenID)
	}

	return nil
}

// RoundTrips returns the number of Lock and LockBatch calls issued against this store
// instance since construction. Instrumentation only, for benchmarking Phase 6's lock
// strategies; not part of the driver.TokenLockStore contract.
func (db *TokenLockStore) RoundTrips() int64 {
	return db.roundTrips.Load()
}

// UniqueViolations returns the number of Lock calls that observed a real server-side
// unique-constraint violation, as opposed to a clean zero-row result - only possible under
// LockStrategyInsert. Instrumentation only, for benchmarking Phase 6's lock strategies; not
// part of the driver.TokenLockStore contract.
func (db *TokenLockStore) UniqueViolations() int64 {
	return db.uniqueViolations.Load()
}

// LockBatch attempts to lock, in a single round trip, every token in tokenIDs on behalf of
// consumerTxID, and returns those it actually won. It never claims a token outside
// tokenIDs, so callers remain responsible for supplying only candidates that are already
// known to be spendable: LockBatch itself applies no eligibility predicate beyond "not
// already locked". Under LockStrategySkipLocked the claim uses FOR UPDATE SKIP LOCKED on
// the underlying Tokens rows, so a caller walks past rows a concurrent claimant is already
// processing instead of colliding with them; under LockStrategyOnConflict and
// LockStrategyInsert it issues the same multi-row INSERT ... ON CONFLICT DO NOTHING as
// tryInsertOnConflict's single-token callers, for the whole window in one round trip -
// only the plain LockStrategyInsert single-token Lock path (which must surface a lost race
// as a unique-constraint violation, not a zero-row result) does not go through this.
func (db *TokenLockStore) LockBatch(ctx context.Context, tokenIDs []*token.ID, consumerTxID transaction.ID, _ string) ([]*token.ID, error) {
	if len(tokenIDs) == 0 {
		return nil, nil
	}
	db.roundTrips.Add(1)
	createdAt := time.Now().UTC()
	if db.strategy != common5.LockStrategySkipLocked {
		return db.tryInsertOnConflict(ctx, tokenIDs, consumerTxID, createdAt)
	}

	return db.tryLockSkipLocked(ctx, tokenIDs, consumerTxID, createdAt)
}

// tryInsertOnConflict claims a batch of specific (tx_id, idx) candidates using
// INSERT ... ON CONFLICT DO NOTHING RETURNING, and returns those it actually won.
func (db *TokenLockStore) tryInsertOnConflict(ctx context.Context, tokenIDs []*token.ID, consumerTxID transaction.ID, createdAt time.Time) ([]*token.ID, error) {
	iq := q.InsertInto(db.Table.TokenLocks).Fields("consumer_tx_id", "tx_id", "idx", "created_at")
	for _, id := range tokenIDs {
		iq = iq.Row(consumerTxID, id.TxId, id.Index, createdAt)
	}
	query, args := iq.OnConflictDoNothing().Returning("tx_id", "idx").Format()
	db.Logger.Debug(query, args)

	rows, err := db.writeDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var won []*token.ID
	for rows.Next() {
		var id token.ID
		if err := rows.Scan(&id.TxId, &id.Index); err != nil {
			return nil, err
		}
		won = append(won, &id)
	}

	return won, rows.Err()
}

// tryLockSkipLocked claims a covering window of candidate tokens in one statement: it joins
// the caller-supplied (tx_id, idx) pairs against the Tokens table under
// FOR UPDATE SKIP LOCKED, so a claimant skips past rows a concurrent claimant is already
// working on instead of blocking on or colliding with them, then inserts a lock row per
// surviving candidate with ON CONFLICT DO NOTHING as a correctness backstop (e.g. a
// mixed-strategy rolling deploy). It never touches a (tx_id, idx) pair outside tokenIDs.
func (db *TokenLockStore) tryLockSkipLocked(ctx context.Context, tokenIDs []*token.ID, consumerTxID transaction.ID, createdAt time.Time) ([]*token.ID, error) {
	args := make([]any, 0, len(tokenIDs)*2+2)
	values := make([]string, 0, len(tokenIDs))
	for _, id := range tokenIDs {
		values = append(values, fmt.Sprintf("($%d, $%d::bigint)", len(args)+1, len(args)+2))
		args = append(args, id.TxId, id.Index)
	}
	consumerTxIDPlaceholder := fmt.Sprintf("$%d", len(args)+1)
	args = append(args, consumerTxID)
	createdAtPlaceholder := fmt.Sprintf("$%d", len(args)+1)
	args = append(args, createdAt)

	// #nosec G202 -- db.Table.Tokens/TokenLocks are trusted table names derived from
	// this process's own config at construction time, never from request input; the
	// only per-request values (tokenIDs, consumerTxID, createdAt) are passed as
	// placeholders in args, never concatenated into the query text.
	query := "WITH candidates(tx_id, idx) AS (VALUES " + strings.Join(values, ", ") + "), " +
		"claimed AS (" +
		"SELECT t.tx_id, t.idx FROM " + db.Table.Tokens + " t " +
		"JOIN candidates c ON c.tx_id = t.tx_id AND c.idx = t.idx " +
		"FOR UPDATE OF t SKIP LOCKED" +
		") " +
		"INSERT INTO " + db.Table.TokenLocks + " (consumer_tx_id, tx_id, idx, created_at) " +
		"SELECT " + consumerTxIDPlaceholder + ", tx_id, idx, " + createdAtPlaceholder + " FROM claimed " +
		"ON CONFLICT (tx_id, idx) DO NOTHING " +
		"RETURNING tx_id, idx"
	db.Logger.Debug(query, args)

	rows, err := db.writeDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var won []*token.ID
	for rows.Next() {
		var id token.ID
		if err := rows.Scan(&id.TxId, &id.Index); err != nil {
			return nil, err
		}
		won = append(won, &id)
	}

	return won, rows.Err()
}

// AcquireCleanupLeadership attempts to acquire a Postgres advisory lock so
// only one replica runs Cleanup per tick for this TMS; others skip the tick
// and release no held resources, so contention is limited to the acquire
// attempt itself. The lock id and factory are fixed at construction. Note
// the winner holds a dedicated connection off writeDB for the tick's
// duration (see NewAdvisoryLock). This lock, the recovery lock and the
// keystore-cleanup lock are all now derived per TMS rather than shared
// node-wide, and FSC keys connection pools by data source alone, so TMSes
// sharing one database share one *sql.DB. A node running N such TMSes can
// therefore have up to 3N connections pinned by lock holders at once, before
// counting the further connections winners need for their own work (e.g.
// recovery's ClaimPendingTransactions). Size writeDB's maxOpenConns against
// that floor, not a single spare connection. See #1798.
func (db *TokenLockStore) AcquireCleanupLeadership(ctx context.Context) (driver.CleanupLeadership, bool, error) {
	return db.cleanupLeaderFactory(ctx, db.writeDB)
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
