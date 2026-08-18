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
	"github.com/LFDT-Panurus/panurus/token/services/utils"
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
//
// cfg.Owner defaults to the identifier reported by replicaID (the FSC node ID in
// production). It returns an error wrapping errs.ErrLockerOwnerRequired when the
// resolved owner is empty or blank — including when replicaID is nil or reports
// an empty ID — because an owner shared by several replicas disables mutual
// exclusion across all of them. That check runs before the table is created, so
// a misconfigured node fails without touching the database.
func New(db *sql.DB, table string, cfg Config, replicaID id.ReplicaIDProvider) (*Locker, error) {
	if db == nil {
		return nil, errors.New("postgres locker requires a non-nil *sql.DB")
	}
	owner := ""
	if replicaID != nil {
		owner = replicaID.ID()
	}
	cfg = cfg.withDefaults(owner)
	if err := cfg.validate(); err != nil {
		return nil, err
	}
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
	logger.Infof("postgres auditor locker for table [%s] locking as owner [%s]", table, cfg.Owner)

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
// every ID — until AcquireDeadline passes (or ctx is cancelled), backing off
// between attempts with exponential growth and jitter. On success it records the
// held IDs under anchor and starts a background heartbeat that renews the leases
// before they expire, so a long-running audit keeps its locks while a crashed
// replica's leases expire and become claimable by others. Any partial state is
// released on the give-up/cancel paths, except over an anchor that already holds
// a live session, whose leases are left alone.
//
// A failure caused by another holder joins ErrLockContention, and additionally
// ErrLockAcquireTimeout once the waiting budget is spent, which tells callers
// this locker already waited and the attempt should not simply be repeated (see
// auditor.Service.acquireLocksWithRetry). AcquireDeadline is that budget.
//
// An empty eIDs set is a successful acquisition of nothing: there is no lease to
// take, so no session is opened and AssertLocksHeld has nothing to verify.
//
// Re-acquiring under a live anchor may keep or shrink its set, never grow it: see
// the Locker contract for why widening is refused with ErrLockSetWidened, and
// releaseDropped for what shrinking has to clean up.
func (p *Locker) AcquireLocks(ctx context.Context, anchor string, eIDs ...string) error {
	deduped := dedup.AndSort(eIDs)
	if len(deduped) == 0 {
		return nil
	}

	held := p.sessionEIDs(anchor)
	if added := dedup.Added(deduped, held); len(held) > 0 && len(added) > 0 {
		return errors.Wrapf(errs.ErrLockSetWidened,
			"anchor [%s] holds %v and cannot also take %v", anchor, held, added)
	}

	// The deadline is enforced through a derived context so it also bounds the
	// backoff sleeps and the queries themselves, not just the attempt loop.
	acquireCtx, cancelAcquire := context.WithTimeout(ctx, p.cfg.AcquireDeadline)
	defer cancelAcquire()

	// contended records whether any attempt actually lost a race for one of the
	// IDs. The outcome is classified from this rather than from which context
	// expired first: acquireCtx carries AcquireDeadline, a minute by default, so a
	// request-scoped caller context is nearly always the shorter of the two.
	// Keying off the caller's context meant that in production this backend
	// reported genuine contention as a bare context error — no sentinel at all —
	// while the in-memory locker reported it as contention, which is exactly the
	// kind of divergence the Locker contract exists to prevent.
	contended := false
	err := p.acquireRunner().RunWithErrorsContext(acquireCtx, func() (bool, error) {
		ok, err := p.tryAcquireAll(acquireCtx, anchor, deduped)
		if err != nil {
			return true, err
		}
		if !ok {
			contended = true
		}

		return ok, nil
	})
	if err == nil {
		// The upsert refreshed the leases named in this call, but a narrowing
		// re-acquisition also has to give up the ones it dropped.
		if err := p.releaseDropped(ctx, anchor, dedup.Dropped(held, deduped)); err != nil {
			return err
		}
		p.startSession(anchor, deduped)

		return nil
	}

	// A failed acquisition must leave a session the anchor already had intact.
	// tryAcquireAll rolls back unless it claims every ID, so a failure normally
	// leaves no rows behind at all and this cleanup only matters for a commit that
	// was applied but reported as failed. Running it over a live session would
	// delete that session's lease rows while its heartbeat kept going: the next
	// renewal would match nothing, report the leases lost and exit, and the
	// caller's next legitimate Append would fail its pre-write assertion even
	// though no lock had been stolen.
	if !p.hasSession(anchor) {
		_ = p.releaseAnchor(ctx, anchor)
	}

	// Whether the waiting budget is spent. acquireCtx reaching its deadline means
	// either AcquireDeadline elapsed or the caller's own deadline did, and in both
	// cases an identical retry adds delay rather than a fresh chance. A loop ended
	// by a database error leaves it un-expired, and an explicitly cancelled caller
	// is a cancellation rather than a timeout — matching how the in-memory locker
	// classifies the same two situations.
	deadlineElapsed := errors.Is(acquireCtx.Err(), context.DeadlineExceeded)

	switch {
	case contended && deadlineElapsed:
		// err is joined in rather than discarded: when the loop was ended by a query
		// the deadline killed, it is the only record of what the database was doing.
		return errors.Wrapf(
			errors.Join(errs.ErrLockContention, errs.ErrLockAcquireTimeout, err),
			"gave up acquiring eid leases for anchor [%s] after %v", anchor, p.cfg.AcquireDeadline)
	case contended:
		// The contention was real, but what ended the loop was a database failure or
		// a cancelled caller, not the waiting budget. Reporting ErrLockAcquireTimeout
		// here would tell the caller the budget was spent and stop it retrying a
		// transient failure that a later attempt could get past.
		return errors.Wrapf(errors.Join(errs.ErrLockContention, err),
			"failed to acquire contended eid leases for anchor [%s]", anchor)
	case ctx.Err() != nil:
		return ctx.Err()
	default:
		return err
	}
}

// acquireRunner builds the backoff policy for the acquisition loop: exponential
// growth from AcquireBackoff, capped at AcquireMaxBackoff, with jitter.
//
// A fixed poll interval is the wrong shape here on two counts. It costs one
// round trip per interval for the whole deadline — at the defaults, hundreds per
// acquisition — and, being identical on every replica, it keeps contenders
// phase-locked so they retry in lockstep and collide again. Jittered exponential
// backoff spreads them out and cuts the round trips to a few dozen. The attempt
// count is unbounded because the derived context, not a counter, is what ends
// the loop.
func (p *Locker) acquireRunner() utils.RetryRunner {
	return utils.NewRetryRunnerWithJitter(
		logger,
		utils.Infinitely,
		p.cfg.AcquireBackoff,
		p.cfg.AcquireMaxBackoff,
		acquireBackoffMultiplier,
		acquireJitterFactor,
	)
}

// startSession records the leases held under anchor and starts the heartbeat that
// keeps them alive. Any session previously tracked for the anchor is cancelled
// first so its heartbeat cannot outlive it.
func (p *Locker) startSession(anchor string, eIDs []string) {
	hbCtx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	if prev, exists := p.sessions[anchor]; exists {
		prev.cancel()
	}
	p.sessions[anchor] = &lockSession{eIDs: eIDs, cancel: cancel}
	p.mu.Unlock()

	go p.heartbeatLoop(hbCtx, anchor, len(eIDs))
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
// expired (InPast), or the row is this replica's own lease for this very anchor
// (a re-acquisition, whose lease is simply refreshed). The WHERE clause on the
// upsert enforces that condition, and RETURNING eid yields exactly the IDs that
// were claimed, which tryAcquireAll counts. expires_at is set to now()+TTL via
// an interval-bound parameter.
//
// The anchor must be compared as well as the owner: owner is a per-replica
// constant, so matching on it alone let one node's concurrent audits of two
// different anchors overwrite each other's live lease for a shared enrollment
// ID, with both acquisitions reporting success. Requiring anchor equality turns
// that case back into ordinary contention, which the caller retries.
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
			cond.And(
				cond.Cmp(tbl.Field("owner"), "=", q.ExcludedValue("owner")),
				cond.Cmp(tbl.Field("anchor"), "=", q.ExcludedValue("anchor")),
			),
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
//
// The delete runs on a context detached from the caller's but bounded by
// releaseTimeout. Detaching matters because the common case is a deferred release
// on a context that is already done, and a skipped delete leaves the enrollment
// IDs locked against every replica until their TTL expires. The bound matters
// just as much: a context that can neither be cancelled nor time out lets
// database/sql block indefinitely waiting for a free pooled connection or a
// conflicting row lock, which would leave AcquireLocks hanging past the very
// deadline it promises to honour, and would leak the goroutine on shutdown.
func (p *Locker) releaseAnchor(ctx context.Context, anchor string) error {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()

	query, args := q.DeleteFrom(p.table).
		Where(cond.And(cond.Eq("anchor", anchor), cond.Eq("owner", p.cfg.Owner))).
		Format(p.ci)
	_, err := p.db.ExecContext(releaseCtx, query, args...)

	return errors.Wrap(err, "release eid leases")
}

// releaseDropped deletes this replica's lease rows for the enrollment IDs an
// anchor no longer needs, so that a narrowing re-acquisition gives up the leases
// it dropped instead of leaving them behind.
//
// Leaving them behind was not merely a leak. renewLeases and AssertLocksHeld both
// count this replica's un-expired rows for the anchor and require exactly as many
// as the session recorded, so every extra row made both fail: the heartbeat
// reported the leases lost on its first tick and exited, StoreService.Append
// rejected the next legitimate write with "locks lost before write", and once the
// TTL passed the abandoned leases expired and became claimable by another replica
// while this one still believed it held them. The in-memory backend released them,
// so this was a divergence between two deployments of the same code as well.
//
// A failed delete leaves the previous session untouched and reports the error.
// That state is consistent: the rows in the table are exactly the ones the old
// session recorded (a narrowing set is a subset, and the upsert only refreshed
// their expiry), so its heartbeat and assertions keep matching. Releasing the
// anchor here instead would delete the live session's rows and break precisely
// what the failure path below is careful to preserve.
func (p *Locker) releaseDropped(ctx context.Context, anchor string, eIDs []string) error {
	if len(eIDs) == 0 {
		return nil
	}

	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()

	query, args := q.DeleteFrom(p.table).
		Where(cond.And(
			cond.Eq("anchor", anchor),
			cond.Eq("owner", p.cfg.Owner),
			cond.In[string]("eid", eIDs...),
		)).
		Format(p.ci)
	if _, err := p.db.ExecContext(releaseCtx, query, args...); err != nil {
		return errors.Wrapf(err, "failed to release eid leases dropped from anchor [%s]", anchor)
	}

	return nil
}

// sessionEIDs returns the enrollment IDs recorded for anchor, or nil when no live
// session is tracked for it.
func (p *Locker) sessionEIDs(anchor string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sessions[anchor]; ok {
		return s.eIDs
	}

	return nil
}

// hasSession reports whether a live lock session is tracked for anchor, i.e.
// whether an earlier AcquireLocks succeeded for it and has not been released.
func (p *Locker) hasSession(anchor string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.sessions[anchor]

	return ok
}

// AssertLocksHeld verifies this replica still holds every lease it acquired for
// anchor. It compares the number of IDs recorded locally at acquisition time
// against the count of matching, non-expired, owner-scoped rows in the table.
// A mismatch means a lease expired and may have been taken over by another
// replica, so it returns ErrLockNotHeld. Callers use this after long-running
// work to confirm their locks were not silently lost.
//
// An anchor with no recorded session holds no leases, so there is nothing that
// could have been lost and the assertion succeeds. Reporting ErrLockNotHeld
// instead conflated "lost the locks I took" with "never took any", which failed
// two legitimate flows under this backend while both succeeded under the
// in-memory locker: a request whose inputs and outputs yield no enrollment IDs
// at all, and an auditor that validates and appends without calling Audit (so
// never acquires locks) — see the dvp and nft auditor views.
//
// A session that failed renewal is not affected: heartbeatLoop leaves the
// session in place when it gives up, so its recorded IDs are still counted here
// and the mismatch is still reported.
func (p *Locker) AssertLocksHeld(ctx context.Context, anchor string) error {
	p.mu.Lock()
	s, ok := p.sessions[anchor]
	expected := 0
	if ok {
		expected = len(s.eIDs)
	}
	p.mu.Unlock()

	if expected == 0 {
		return nil
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
