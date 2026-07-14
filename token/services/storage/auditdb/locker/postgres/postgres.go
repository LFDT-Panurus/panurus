/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/dedup"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/errs"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/id"
	pgcond "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/postgres"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	qcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var logger = logging.MustGetLogger()

// Locker implements locker.Locker using a SQL lease table.
// Acquire and renew queries use Postgres-specific features (TIMESTAMPTZ,
// ON CONFLICT DO UPDATE … RETURNING, ::interval casts).
type Locker struct {
	db    *sql.DB
	table string
	cfg   Config
	ci    qcommon.CondInterpreter

	mu       sync.Mutex
	sessions map[string]*lockSession
}

type lockSession struct {
	eIDs   []string
	cancel context.CancelFunc
}

// New creates a Postgres-backed distributed Locker.
// The table is created if it does not exist. db must be a *sql.DB connected to Postgres.
func New(db *sql.DB, table string, cfg Config, replicaID id.ReplicaIDProvider) (*Locker, error) {
	if db == nil {
		return nil, errors.New("postgres locker requires a non-nil *sql.DB")
	}
	owner := ""
	if replicaID != nil {
		owner = replicaID.ID()
	}
	cfg = cfg.withDefaults(owner)
	l := &Locker{
		db:       db,
		table:    table,
		cfg:      cfg,
		ci:       pgcond.NewConditionInterpreter(),
		sessions: make(map[string]*lockSession),
	}
	if err := l.createSchema(); err != nil {
		return nil, err
	}

	return l, nil
}

// createSchema creates the lease table and its supporting indexes if they do
// not already exist. Each row is one held enrollment-ID lease: eid is the
// primary key (so at most one owner holds a given ID at a time), anchor groups
// the leases of a request, owner identifies the holding replica, and expires_at
// is the lease deadline used for crash recovery. The anchor and expires_at
// indexes back the release and expiry-reclaim queries.
func (p *Locker) createSchema() error {
	// #nosec G201 -- table name is configuration-driven, not user input; DDL has no query-builder support.
	schema := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			eid         TEXT        PRIMARY KEY,
			anchor      TEXT        NOT NULL,
			owner       TEXT        NOT NULL,
			expires_at  TIMESTAMPTZ NOT NULL
		);
		CREATE INDEX IF NOT EXISTS %s_anchor_idx     ON %s (anchor);
		CREATE INDEX IF NOT EXISTS %s_expires_at_idx ON %s (expires_at);
	`, p.table, p.table, p.table, p.table, p.table)
	_, err := p.db.Exec(schema)

	return errors.Wrap(err, "failed to create auditor eid lease table")
}

// AcquireLocks claims a lease on every enrollment ID in eIDs for anchor, across
// all replicas sharing the table.
//
// Implementation: the IDs are deduplicated and sorted (dedup.AndSort) for the
// same deadlock-free ordering the in-memory locker relies on. It then retries
// tryAcquireAll — a single atomic upsert that succeeds only if it can claim
// every ID — sleeping AcquireBackoff between attempts and giving up with a
// contention error once AcquireDeadline passes (or ctx is cancelled). On
// success it records the held IDs under anchor and starts a background
// heartbeat that renews the leases before they expire, so a long-running audit
// keeps its locks while a crashed replica's leases expire and become claimable
// by others. Any partial state is released on the give-up/cancel paths.
func (p *Locker) AcquireLocks(ctx context.Context, anchor string, eIDs ...string) error {
	deduped := dedup.AndSort(eIDs)
	if len(deduped) == 0 {
		return nil
	}

	deadline := time.Now().Add(p.cfg.AcquireDeadline)
	for {
		ok, err := p.tryAcquireAll(ctx, anchor, deduped)
		if err != nil {
			return err
		}
		if ok {
			hbCtx, cancel := context.WithCancel(context.Background())
			p.mu.Lock()
			if prev, exists := p.sessions[anchor]; exists {
				prev.cancel()
			}
			p.sessions[anchor] = &lockSession{eIDs: deduped, cancel: cancel}
			p.mu.Unlock()
			go p.heartbeatLoop(hbCtx, anchor, len(deduped))

			return nil
		}
		if time.Now().After(deadline) {
			_ = p.releaseAnchor(ctx, anchor)

			return errors.Join(errs.ErrLockAcquireTimeout, errs.ErrLockContention)
		}
		select {
		case <-ctx.Done():
			_ = p.releaseAnchor(ctx, anchor)

			return ctx.Err()
		case <-time.After(p.cfg.AcquireBackoff):
		}
	}
}

// tryAcquireAll attempts to claim all eIDs in a single transaction. It runs the
// upsert built by buildAcquireQuery and counts the RETURNING rows: the upsert
// only returns a row for an ID it could actually claim (free, expired, or
// already owned by this replica), so claiming every ID means the count equals
// len(eIDs). If so it commits and reports success; otherwise the transaction is
// rolled back (releasing nothing) and it reports false so the caller can retry.
func (p *Locker) tryAcquireAll(ctx context.Context, anchor string, eIDs []string) (bool, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.Wrap(err, "begin eid lock tx")
	}
	defer func() { _ = tx.Rollback() }()

	query, args := p.buildAcquireQuery(anchor, eIDs)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return false, errors.Wrap(err, "acquire eid leases")
	}
	defer func() { _ = rows.Close() }()

	acquired := 0
	for rows.Next() {
		var eid string
		if err := rows.Scan(&eid); err != nil {
			return false, errors.Wrap(err, "scan acquired eid")
		}
		acquired++
	}
	if err := rows.Err(); err != nil {
		return false, errors.Wrap(err, "iterate acquired eids")
	}
	if acquired != len(eIDs) {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, errors.Wrap(err, "commit eid lock tx")
	}

	return true, nil
}

// buildAcquireQuery builds the atomic acquisition statement: an INSERT of one
// row per enrollment ID that, ON CONFLICT on the eid primary key, overwrites
// the existing row only when it is safe to steal — the current lease has
// expired (InPast) or is already owned by this replica. The WHERE clause on the
// upsert enforces that condition, and RETURNING eid yields exactly the IDs that
// were claimed, which tryAcquireAll counts. expires_at is set to now()+TTL via
// an interval-bound parameter.
func (p *Locker) buildAcquireQuery(anchor string, eIDs []string) (string, []any) {
	tbl := q.Table(p.table)
	ins := q.InsertInto(p.table).
		Fields("eid", "anchor", "owner", "expires_at").
		WithBoundParams(anchor, p.cfg.Owner, p.cfg.TTL.String())
	for _, eid := range eIDs {
		ins = ins.RowValues(
			qcommon.Bind(eid),
			qcommon.Ref(1),
			qcommon.Ref(2),
			qcommon.IntervalAfterNow(3),
		)
	}

	query, args := ins.
		OnConflict([]qcommon.FieldName{"eid"},
			q.OverwriteValue("anchor"),
			q.OverwriteValue("owner"),
			q.OverwriteValue("expires_at"),
		).
		Where(cond.Or(
			cond.InPast(tbl.Field("expires_at")),
			cond.Cmp(tbl.Field("owner"), "=", q.ExcludedValue("owner")),
		)).
		Returning("eid").
		Format()

	return query, args
}

// ReleaseLocks releases all leases held under anchor: it stops the background
// heartbeat for that anchor and deletes the corresponding rows from the table.
func (p *Locker) ReleaseLocks(ctx context.Context, anchor string) {
	p.stopHeartbeat(anchor)
	_ = p.releaseAnchor(ctx, anchor)
}

// releaseAnchor deletes this replica's lease rows for anchor. It is scoped by
// owner so a replica only ever removes leases it still holds (never one that
// expired and was since claimed by another replica), which makes it safe to
// call even on the timeout/cancel paths of AcquireLocks.
func (p *Locker) releaseAnchor(ctx context.Context, anchor string) error {
	query, args := q.DeleteFrom(p.table).
		Where(cond.And(cond.Eq("anchor", anchor), cond.Eq("owner", p.cfg.Owner))).
		Format(p.ci)
	_, err := p.db.ExecContext(ctx, query, args...)

	return errors.Wrap(err, "release eid leases")
}

// AssertLocksHeld verifies this replica still holds every lease it acquired for
// anchor. It compares the number of IDs recorded locally at acquisition time
// against the count of matching, non-expired, owner-scoped rows in the table.
// A mismatch (or no local record) means a lease expired and may have been
// taken over by another replica, so it returns ErrLockNotHeld. Callers use this
// after long-running work to confirm their locks were not silently lost.
func (p *Locker) AssertLocksHeld(ctx context.Context, anchor string) error {
	p.mu.Lock()
	s, ok := p.sessions[anchor]
	expected := 0
	if ok {
		expected = len(s.eIDs)
	}
	p.mu.Unlock()

	if !ok || expected == 0 {
		return errs.ErrLockNotHeld
	}

	var held int
	query, args := q.Select().
		FieldsByName("COUNT(*)").
		From(q.Table(p.table)).
		Where(cond.And(
			cond.Eq("anchor", anchor),
			cond.Eq("owner", p.cfg.Owner),
			cond.InFuture(qcommon.FieldName("expires_at")),
		)).
		Format(p.ci)
	if err := p.db.QueryRowContext(ctx, query, args...).Scan(&held); err != nil {
		return errors.Wrap(err, "assert eid leases held")
	}
	if held != expected {
		return errs.ErrLockNotHeld
	}

	return nil
}

// heartbeatLoop periodically renews the leases for anchor until ctx is
// cancelled (by ReleaseLocks/stopHeartbeat) or a renewal fails. On renewal
// failure it logs and returns, ending the loop: the leases will then expire and
// become claimable by other replicas, and a subsequent AssertLocksHeld will
// report the locks as lost.
func (p *Locker) heartbeatLoop(ctx context.Context, anchor string, expected int) {
	ticker := time.NewTicker(p.cfg.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.renewLeases(ctx, anchor, expected); err != nil {
				logger.Warnf("eid lease heartbeat failed for [%s]: %v", anchor, err)

				return
			}
		}
	}
}

// renewLeases pushes expires_at to now()+TTL for this replica's still-valid
// leases under anchor (owner-scoped, and only rows not already expired). It
// requires exactly expected rows to be updated; if fewer match, at least one
// lease has expired or been stolen, so it returns ErrLockLost to stop the
// heartbeat rather than silently re-extending a partial set.
func (p *Locker) renewLeases(ctx context.Context, anchor string, expected int) error {
	query, args := q.Update(p.table).
		SetIntervalFromNow("expires_at", p.cfg.TTL.String()).
		Where(cond.And(
			cond.Eq("anchor", anchor),
			cond.Eq("owner", p.cfg.Owner),
			cond.InFuture(qcommon.FieldName("expires_at")),
		)).
		Format(p.ci)
	res, err := p.db.ExecContext(ctx, query, args...)
	if err != nil {
		return errors.Wrap(err, "renew eid leases")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "rows affected on renew")
	}
	if int(n) != expected {
		return errs.ErrLockLost
	}

	return nil
}

// stopHeartbeat cancels the background heartbeat goroutine for anchor and drops
// its session entry. It is a no-op if no session is tracked for anchor, so it
// is safe to call on already-released anchors.
func (p *Locker) stopHeartbeat(anchor string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sessions[anchor]; ok {
		s.cancel()
		delete(p.sessions, anchor)
	}
}
