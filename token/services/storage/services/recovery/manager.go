/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package recovery

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	defaultBatchSize          = 16
	defaultWorkers            = 8
	defaultLeaseDuration      = 5 * time.Minute
	defaultTransactionTimeout = 60 * time.Second

	// minTransactionTimeout is the floor LoadConfig enforces on an operator's
	// explicit (non-zero) transactionTimeout setting. Below this, a transient
	// slow ledger round-trip under load reads as a stuck transaction and gets
	// abandoned before it had a real chance to finish (round 8 review,
	// finding on the 10s default). Enforced in LoadConfig rather than
	// validateConfig: LoadConfig is the only place that still knows whether a
	// value was the operator's explicit choice or an internal default/clamp,
	// which matters here (see LoadConfig's TransactionTimeout handling) the
	// same way it mattered for the timeout-vs-lease relationship round 6
	// hard-failed on and round 7 had to walk back to a Warn.
	minTransactionTimeout = 10 * time.Second

	// timeoutCountTTL bounds how long a txID lingers in timeoutCounts after its
	// last recorded timeout. A transaction that times out here and is then
	// confirmed by an independent finality listener leaves the Pending scan
	// range and is never reattempted by this manager, so nothing would
	// otherwise ever remove its entry.
	timeoutCountTTL = time.Hour
)

// errRecoveryInFlight is returned by callHandler when a previous attempt for
// the same txID is still running in an abandoned goroutine, see callHandler.
var errRecoveryInFlight = errors.New("recovery attempt already in flight")

// errSkippedInFlight is what recoverTransaction returns to recoverTransactions
// for the errRecoveryInFlight case: no recovery was attempted at all, so it
// must not be counted, or logged, as a success or a failure of this sweep.
var errSkippedInFlight = errors.New("recovery: transaction skipped, previous attempt still in flight")

// errSkippedShutdown is what recoverTransaction returns when callHandler's
// recoverCtx.Done() fired for a reason other than TransactionTimeout elapsing
// (in practice, the parent context being cancelled by Stop()). Like
// errSkippedInFlight, no timing-related conclusion about the transaction
// itself can be drawn, so it must not be counted, logged, or alerted on as a
// timeout or a failure.
var errSkippedShutdown = errors.New("recovery: transaction skipped, attempt interrupted by shutdown")

//go:generate counterfeiter -o mock/storage.go -fake-name Storage . Storage

// Storage defines the interface for querying pending transactions and transaction details
type Storage interface {
	AcquireRecoveryLeadership(ctx context.Context) (Leadership, bool, error)
	ClaimPendingTransactions(ctx context.Context, olderThan time.Duration, leaseDuration time.Duration, limit int, owner string) ([]*ttxdb.RecoveryClaim, error)
	ReleaseRecoveryClaim(ctx context.Context, txID string, owner string, message string) error
	// SetStatus updates a transaction's status row. Used by the recovery loop to
	// permanently mark orphan transactions (NotFound past grace period) as Orphan
	// so they exit the eligible scan range and stop blocking the queue head,
	// while remaining distinguishable from txs the ledger actively rejected.
	SetStatus(ctx context.Context, txID string, status storage.TxStatus, message string) error
}

//go:generate counterfeiter -o mock/handler.go -fake-name Handler . Handler

// Handler handles the recovery of a single transaction
type Handler interface {
	// Recover attempts to recover a transaction by re-registering its finality listener
	// Returns an error if recovery fails
	Recover(ctx context.Context, txID string) error
}

//go:generate counterfeiter -o mock/leadership.go -fake-name Leadership . Leadership

// Leadership represents an acquired advisory lock leadership session.
type Leadership = dbdriver.RecoveryLeadership

// Manager handles the recovery of transactions that may have lost their finality listeners
type Manager struct {
	logger  logging.Logger
	storage Storage
	handler Handler
	config  Config
	//nolint:containedctx // long-running service lifecycle, not a per-request context
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
	mu      sync.Mutex

	// timeoutCounts tracks, per txID, how many consecutive attempts have hit
	// the TransactionTimeout deadline, and when that was last recorded. It
	// only exists for the log escalation in recoverTransaction; it is
	// best-effort in-memory state (reset on restart, or if leadership moves to
	// another replica) and never changes a transaction's stored status.
	// pruneTimeoutCounts drops entries older than timeoutCountTTL.
	timeoutCounts map[string]timeoutEntry
	timeoutMu     sync.Mutex

	// inFlight tracks txIDs with a callHandler goroutine still running past
	// its TransactionTimeout deadline. A stuck transaction stays Pending and
	// is re-claimed on every sweep, so without this guard each sweep would
	// start a fresh goroutine for it: unbounded goroutine growth, and
	// duplicate concurrent Recover calls for the same txID, which defeats the
	// mutual exclusion the claim lease exists to provide (Recover reaches
	// Commit). It is also what runSweep checks to decide whether recovery
	// leadership must be held across sweeps rather than released at the end
	// of this one: see the leadership field below.
	inFlight   map[string]struct{}
	inFlightMu sync.Mutex

	// leadership is the recovery leadership lease currently held by this
	// instance, or nil if not held. It is touched only by runSweep, which
	// always runs on the single recoveryLoop goroutine, and by Stop, which
	// only reads/clears it after wg.Wait() guarantees runSweep is no longer
	// running; no mutex is needed for that reason.
	//
	// Round 7 released leadership at the end of every sweep unconditionally,
	// relying on this instance winning the advisory lock again on its next
	// tick to reclaim (and thereby renew the lease on) any row still
	// abandoned in inFlight. That assumption does not hold: AcquireRecoveryLeadership
	// is a non-blocking try-lock (runSweep gives up for this tick if it is
	// not acquired), so a different replica can legitimately win it on the
	// very next sweep and this instance would then never call
	// ClaimPendingTransactions again for that row. Its lease then lapses on
	// schedule and a live peer can claim and run Recover -> Commit against it
	// while this instance's abandoned goroutine is still silently running:
	// exactly the hazard round 7 was meant to close (round 8 review, finding 1).
	//
	// So leadership is now held across sweeps for as long as inFlight is
	// non-empty: as long as this instance keeps leadership, no other replica
	// can acquire it, so nobody else can claim an abandoned row out from
	// under this instance, and this instance's own next sweep keeps renewing
	// that row's lease via ClaimPendingTransactions exactly as before. Once
	// inFlight drains back to empty, leadership reverts to being released at
	// the end of each sweep, same as pre-round-8 behaviour, so a healthy,
	// unstuck instance still rotates leadership fairly with its peers. Stop
	// always releases leadership if held, regardless of inFlight: an
	// abandoned goroutine that outlives Stop() is already documented as
	// running unsupervised past shutdown (see finishAbandonedRecovery), and
	// this instance can no longer productively use leadership once its sweep
	// loop has exited, so holding onto it would only block live peers.
	leadership Leadership
}

// timeoutEntry is the value type of Manager.timeoutCounts.
type timeoutEntry struct {
	count    int
	lastSeen time.Time
}

// NewManager creates a new recovery manager
func NewManager(
	logger logging.Logger,
	storage Storage,
	handler Handler,
	config Config,
) *Manager {
	return &Manager{
		logger:        logger,
		storage:       storage,
		handler:       handler,
		config:        config,
		timeoutCounts: make(map[string]timeoutEntry),
		inFlight:      make(map[string]struct{}),
	}
}

// Start begins the recovery process
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.config.Enabled {
		m.logger.Debugf("transaction recovery is disabled")

		return nil
	}

	if m.started {
		return errors.Errorf("recovery manager already started")
	}

	if err := m.validateConfig(); err != nil {
		return err
	}

	m.warnIfLeaseMayExpireMidSweep()
	m.warnIfLeaseMayExpireBetweenSweeps()

	if m.config.TransactionTimeout <= 0 {
		// A ctx-blind Recover call (see callHandler) can only be bounded by a
		// deadline, not by cancellation, so disabling transactionTimeout
		// removes the one thing that can interrupt a hung call: Stop() itself
		// then blocks on it, wedging every later Start/Stop too.
		m.logger.Warnf("recovery: transactionTimeout is disabled (0); a Recover call that ignores its context can block Stop() indefinitely")
	}

	if m.config.InstanceID == "" {
		m.config.InstanceID = fmt.Sprintf("recovery-%p", m)
	}

	// The context is deliberately held on the manager: it scopes the background recovery loop
	// started below and is cancelled by Stop, so its lifetime is the manager's, not a caller's.
	//nolint:fatcontext // long-running service lifecycle, not a per-request context
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.started = true

	m.wg.Add(1)
	go m.recoveryLoop()

	m.logger.Infof("transaction recovery manager started (TTL: %s, Scan Interval: %s, Batch Size: %d, Workers: %d, Lease Duration: %s, Transaction Timeout: %s, Instance ID: %s)",
		m.config.TTL, m.config.ScanInterval, m.config.BatchSize, m.config.WorkerCount, m.config.LeaseDuration, m.config.TransactionTimeout, m.config.InstanceID)

	return nil
}

// Stop gracefully stops the recovery process
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return nil
	}

	m.logger.Infof("stopping transaction recovery manager")
	m.cancel()
	m.wg.Wait()
	m.releaseLeadership()
	m.started = false
	m.logger.Infof("transaction recovery manager stopped")

	return nil
}

// recoveryLoop is the main loop that periodically scans for transactions needing recovery
func (m *Manager) recoveryLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.ScanInterval)
	defer ticker.Stop()

	// Add random jitter (0-1 second) before initial sweep to prevent thundering herd
	// when multiple replicas restart simultaneously
	jitter := time.Duration(rand.Int63n(int64(time.Second)))
	m.logger.Debugf("delaying initial recovery sweep by %s to avoid thundering herd", jitter)

	select {
	case <-m.ctx.Done():
		m.logger.Debugf("recovery loop stopped before initial sweep")

		return
	case <-time.After(jitter):
		// Continue with initial sweep
	}

	if err := m.runSweep(m.ctx); err != nil {
		m.logSweepError("initial transaction recovery sweep", err)
	}

	for {
		select {
		case <-m.ctx.Done():
			m.logger.Debugf("recovery loop stopped")

			return
		case <-ticker.C:
			if err := m.runSweep(m.ctx); err != nil {
				m.logSweepError("transaction recovery sweep", err)
			}
		}
	}
}

// logSweepError reports a sweep that returned an error.
//
// A sweep aborted because Stop cancelled the manager context is an ordinary
// shutdown rather than a failure, so it is logged at debug level: the fan-out
// surfaces the cancellation as an error to keep the undispatched claims visible
// to the caller, and warning about it would flag a healthy node shutdown.
func (m *Manager) logSweepError(what string, err error) {
	if errors.Is(err, context.Canceled) {
		m.logger.Debugf("%s stopped: %v", what, err)

		return
	}

	m.logger.Warnf("%s failed: %v", what, err)
}

func (m *Manager) validateConfig() error {
	switch {
	case m.config.TTL <= 0:
		return errors.Errorf("invalid recovery TTL [%s]", m.config.TTL)
	case m.config.ScanInterval <= 0:
		return errors.Errorf("invalid recovery scan interval [%s]", m.config.ScanInterval)
	case m.config.BatchSize <= 0:
		return errors.Errorf("invalid recovery batch size [%d]", m.config.BatchSize)
	case m.config.WorkerCount <= 0:
		return errors.Errorf("invalid recovery worker count [%d]", m.config.WorkerCount)
	case m.config.LeaseDuration <= 0:
		return errors.Errorf("invalid recovery lease duration [%s]", m.config.LeaseDuration)
	case m.config.TransactionTimeout < 0:
		return errors.Errorf("invalid recovery transaction timeout [%s]", m.config.TransactionTimeout)
	default:
		return nil
	}
}

// warnIfLeaseMayExpireMidSweep logs an advisory Warn when the worst-case time
// a single worker can spend on its share of one sweep's batch — up to
// TransactionTimeout per claim, times ceil(BatchSize/WorkerCount) claims per
// worker — could reach or exceed LeaseDuration. This is the invariant the docs
// describe (leaseDuration > (batchSize/workerCount) x transactionTimeout), not
// the simpler transactionTimeout < leaseDuration this package enforced through
// round 6: that comparison was both the wrong relationship (it ignores
// BatchSize/WorkerCount entirely) and unnecessarily fatal. Since round 7 a
// claim whose local attempt is still running is held, not released, until
// finishAbandonedRecovery's goroutine actually completes (see callHandler), so
// this is no longer a correctness requirement for that specific hazard, only a
// throughput one: a sweep that runs long enough can have its own claim lease
// on a not-yet-reached record expire and get picked up by another replica.
// Hence Warn, not a Start() failure.
func (m *Manager) warnIfLeaseMayExpireMidSweep() {
	if m.config.TransactionTimeout <= 0 || m.config.WorkerCount <= 0 {
		return
	}
	perWorker := (m.config.BatchSize + m.config.WorkerCount - 1) / m.config.WorkerCount
	worstCase := time.Duration(perWorker) * m.config.TransactionTimeout
	if worstCase >= m.config.LeaseDuration {
		m.logger.Warnf("recovery: worst-case sweep duration (%d claim(s)/worker x transactionTimeout %s = %s) may reach or exceed leaseDuration %s; some claims could be reclaimed by another replica before this sweep reaches them",
			perWorker, m.config.TransactionTimeout, worstCase, m.config.LeaseDuration)
	}
}

// warnIfLeaseMayExpireBetweenSweeps logs an advisory Warn when ScanInterval is
// not comfortably shorter than LeaseDuration.
//
// A transaction whose Recover call is still running past its
// TransactionTimeout keeps its claim: the claim is not released, and it is
// re-affirmed (recovery_claim_expires_at pushed out again) as a side effect of
// this instance's own next ClaimPendingTransactions call reclaiming the same
// still-Pending row, exactly as it already does every sweep for a stuck
// transaction (see the inFlight doc comment). Since round 8, runSweep holds
// recovery leadership across sweeps for as long as inFlight is non-empty
// specifically so this instance keeps winning that reclaim rather than
// leaving it to chance (see the leadership field doc comment). That
// reclaim still only arrives on time if this instance's sweeps run more often
// than the lease can expire. If ScanInterval is close to or exceeds
// LeaseDuration, a stuck transaction's claim can lapse in the gap between
// this instance's own sweeps, and be picked up by another replica while the
// original attempt is still abandoned in the background.
func (m *Manager) warnIfLeaseMayExpireBetweenSweeps() {
	if m.config.TransactionTimeout <= 0 {
		// No abandoned-goroutine hazard without a bounded attempt: nothing here
		// depends on renewal-by-reclaim.
		return
	}
	if m.config.ScanInterval >= m.config.LeaseDuration {
		m.logger.Warnf("recovery: scanInterval %s is not shorter than leaseDuration %s; a stuck transaction's claim could expire between sweeps and be reclaimed by another replica while this instance's attempt is still running",
			m.config.ScanInterval, m.config.LeaseDuration)
	}
}

func (m *Manager) runSweep(ctx context.Context) error {
	m.pruneTimeoutCounts()

	if m.leadership == nil {
		leadership, acquired, err := m.storage.AcquireRecoveryLeadership(ctx)
		if err != nil {
			return errors.Wrapf(err, "failed to acquire recovery leadership")
		}
		if !acquired {
			m.logger.Debugf("recovery leadership not acquired")

			return nil
		}
		m.leadership = leadership
	}

	sweepErr := m.recoverTransactions(ctx)

	// Only release leadership once nothing is left abandoned in inFlight: see
	// the leadership field's doc comment for why holding it across sweeps
	// while inFlight is non-empty is what keeps this instance's own reclaim
	// renewing an abandoned row's lease, instead of leaving that to chance.
	if !m.hasInFlight() {
		m.releaseLeadership()
	}

	return sweepErr
}

// hasInFlight reports whether any txID currently has a callHandler goroutine
// running past its TransactionTimeout deadline.
func (m *Manager) hasInFlight() bool {
	m.inFlightMu.Lock()
	defer m.inFlightMu.Unlock()

	return len(m.inFlight) > 0
}

// releaseLeadership closes the currently-held recovery leadership lease, if
// any, and clears it. Safe to call when no leadership is held.
func (m *Manager) releaseLeadership() {
	if m.leadership == nil {
		return
	}
	if err := m.leadership.Close(); err != nil {
		m.logger.Warnf("failed to release recovery leadership: %v", err)
	}
	m.leadership = nil
}

// recoverTransactions claims pending transactions and re-registers finality listeners using local workers.
func (m *Manager) recoverTransactions(ctx context.Context) error {
	m.logger.Debugf("claiming pending transactions older than %s (batch size: %d, lease duration: %s, owner: %s)",
		m.config.TTL, m.config.BatchSize, m.config.LeaseDuration, m.config.InstanceID)

	records, err := m.storage.ClaimPendingTransactions(
		ctx,
		m.config.TTL,
		m.config.LeaseDuration,
		m.config.BatchSize,
		m.config.InstanceID,
	)
	if err != nil {
		return errors.Wrapf(err, "failed to claim pending transactions")
	}

	if len(records) == 0 {
		m.logger.Debugf("no pending transactions found needing recovery")

		return nil
	}

	m.logger.Debugf("claimed %d pending transaction(s) needing recovery", len(records))

	work := make(chan *ttxdb.RecoveryClaim)
	errCh := make(chan error, len(records))
	var workerWG sync.WaitGroup

	for range m.config.WorkerCount {
		workerWG.Add(1)
		go m.worker(ctx, &workerWG, work, errCh)
	}

	dispatched, skippedNil, fanOutErr := m.fanOut(ctx, records, work)
	close(work)

	workerWG.Wait()
	close(errCh)

	var firstErr error
	failures := 0
	skipped := 0
	for err := range errCh {
		if err == nil {
			continue
		}
		if errors.Is(err, errSkippedInFlight) || errors.Is(err, errSkippedShutdown) {
			// Not a failure: recoverTransaction made no attempt at all (still
			// in flight), or its attempt was interrupted by shutdown rather
			// than actually failing, so neither must be logged as one or
			// count toward firstErr.
			skipped++

			continue
		}
		failures++
		// Log each individual failure for better debugging
		m.logger.Warnf("recovery failure: %v", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	// Successes are counted against what the fan-out actually dispatched, not
	// against len(records): a cancelled fan-out leaves the tail undispatched,
	// and those claims never reach errCh. Attributing them to len(records)-failures
	// would report never-attempted work as succeeded.
	succeeded := dispatched - failures - skipped
	// considered is what fanOut actually finished looking at: real dispatches
	// plus nil entries it stepped over without sending anywhere. dispatched
	// alone undercounts len(records) whenever a nil entry is present (fanOut
	// never sends those to a worker, so they never reach errCh either), which
	// would otherwise make dispatched < len(records) permanently true and
	// silently suppress the "all skipped" warning below on exactly the sweep
	// where it matters most.
	considered := dispatched + skippedNil
	switch {
	case skipped > 0 && skipped == dispatched && considered == len(records):
		// Every claimed transaction was either dispatched or a defensive nil
		// skip, and every dispatched transaction hit the in-flight guard: the
		// sweep made no progress at all, even though nothing "failed".
		// Debug-level "all succeeded" would read as healthy and hide that.
		// The considered == len(records) half of this condition matters too:
		// without it, a fan-out cancelled mid-batch after dispatching only
		// skips would also print "all skipped", overstating what the sweep
		// even attempted.
		m.logger.Warnf("completed recovery sweep: claimed=%d, all skipped (previous attempts still in flight), no progress made", len(records))
	case failures > 0 || skipped > 0 || considered < len(records):
		// skipped > 0 here means a mix of skips and real outcomes, or a
		// cancelled fan-out (the all-skipped case above already returned
		// otherwise), so this is not "fully wedged" the way the case above
		// is, just worth surfacing the breakdown rather than reporting a
		// clean "all succeeded".
		m.logger.Warnf("completed recovery sweep: claimed=%d, dispatched=%d, succeeded=%d, failed=%d, skipped=%d",
			len(records), dispatched, succeeded, failures, skipped)
	default:
		m.logger.Debugf("completed recovery sweep: claimed=%d, all succeeded", len(records))
	}

	if fanOutErr != nil {
		return errors.Join(fanOutErr, firstErr)
	}

	return firstErr
}

// fanOut dispatches the claimed records to the worker pool.
//
// ClaimPendingTransactions reads directly from the requests table where tx_id
// is the primary key, so each claim is already unique. Fan out straight to the
// workers; nil entries are defensive but should never occur in practice.
//
// The send is guarded on ctx.Done() because the workers return from their own
// select as soon as the context is cancelled, without draining what is left in
// work. An unguarded send would then block forever with no receiver, so
// close(work) and the deferred wg.Done() of the recovery loop would never run
// and Stop's wg.Wait() would hang while holding m.mu, wedging every later
// Start/Stop call.
//
// It returns the number of claims actually handed to a worker, and the number
// of nil entries stepped over along the way. Callers need both to report the
// sweep outcome: claims left undispatched by a cancellation never reach errCh,
// so they cannot be inferred from the failure count, and a nil entry is
// neither a dispatch nor a cancellation but still must count as "considered"
// or the completeness check in recoverTransactions never reaches len(records).
func (m *Manager) fanOut(ctx context.Context, records []*ttxdb.RecoveryClaim, work chan<- *ttxdb.RecoveryClaim) (dispatched int, skippedNil int, err error) {
	for i, claim := range records {
		if claim == nil {
			skippedNil++

			continue
		}
		select {
		case work <- claim:
			dispatched++
		case <-ctx.Done():
			m.logger.Debugf("recovery fan-out cancelled: %d of %d claim(s) not dispatched", len(records)-i, len(records))

			return dispatched, skippedNil, errors.Wrapf(ctx.Err(), "recovery fan-out cancelled")
		}
	}

	return dispatched, skippedNil, nil
}

func (m *Manager) worker(ctx context.Context, wg *sync.WaitGroup, work <-chan *ttxdb.RecoveryClaim, errCh chan<- error) {
	defer wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case claim, ok := <-work:
			if !ok {
				return
			}
			if err := m.recoverTransaction(ctx, claim.TxID, claim.StoredAt); err != nil {
				errCh <- err
			}
		}
	}
}

// callHandler invokes the handler's Recover, bounded by TransactionTimeout.
//
// context.WithTimeout alone only signals cancellation, it cannot interrupt a
// callee that never reads the context, and the ledger status query behind
// Recover is exactly that on some backends (e.g. FSC's fabric
// Ledger.GetTransactionByID takes no context at all). So Recover is run in
// its own goroutine and callHandler gives up as soon as recoverCtx is done,
// instead of waiting for the call to return. On a real hang, the goroutine is
// abandoned: it leaks until the underlying blocking call eventually returns
// (or the process exits). That is the accepted cost of bounding a call this
// package does not control the cancellation semantics of; the alternative is
// leaving the worker (and the leadership it holds for the whole sweep)
// blocked indefinitely, which is the bug this exists to fix.
//
// finished reports whether callHandler has a final result to act on now. It
// is false in two cases, both meaning a goroutine is still running
// unsupervised by this call: a previous attempt for txID is still in flight
// (errRecoveryInFlight), or this attempt's own recoverCtx just timed out with
// nothing in resultCh yet. Round 7 changed what happens next in the
// finished == false case: earlier rounds returned immediately and let the
// caller release the claim regardless, which is exactly what let a second
// replica claim and run a concurrent Recover -> Commit against the same tx
// while the first replica's abandoned goroutine was still running. Now the
// claim is deliberately left alone (see recoverTransaction) and
// finishAbandonedRecovery takes over responsibility for clearing inFlight and
// releasing it, once the goroutine actually returns, however long that takes.
//
// A stuck transaction stays Pending and is re-claimed on every sweep, so
// without the tryMarkInFlight guard below each sweep would abandon another
// goroutine for the same txID: unbounded goroutine growth, and duplicate
// concurrent Recover calls racing each other to Commit for the same tx. That
// same re-claim is also what keeps the DB claim itself alive while
// finishAbandonedRecovery waits: see the inFlight field's doc comment.
//
// TransactionTimeout == 0 means "unbounded" (see LoadConfig / validateConfig):
// Recover is then called directly on ctx, with no derived deadline, no
// in-flight guard, and no extra goroutine, so a ctx-blind call can block
// Stop() indefinitely (Start logs a Warn about this when the timeout is
// disabled).
func (m *Manager) callHandler(ctx context.Context, txID string, storedAt time.Time) (finished bool, err error) {
	if m.config.TransactionTimeout <= 0 {
		return true, m.handler.Recover(ctx, txID)
	}

	if !m.tryMarkInFlight(txID) {
		return false, errRecoveryInFlight
	}

	recoverCtx, cancel := context.WithTimeout(ctx, m.config.TransactionTimeout)

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- m.handler.Recover(recoverCtx, txID)
	}()

	select {
	case result := <-resultCh:
		cancel()
		m.clearInFlight(txID)

		return true, result
	case <-recoverCtx.Done():
		if ok, result := preferResult(resultCh); ok {
			cancel()
			m.clearInFlight(txID)

			return true, result
		}

		//nolint:gosec // G118: deliberate, not an oversight, finishAbandonedRecovery's doc comment explains why it cannot use ctx or m.ctx for the release call itself; ctx is passed only as a stable snapshot to check against later.
		go m.finishAbandonedRecovery(ctx, txID, storedAt, resultCh, cancel)

		return false, recoverCtx.Err()
	}
}

// preferResult performs a non-blocking check of resultCh, for the moment
// recoverCtx's deadline fires: Go's select picks pseudo-randomly among
// simultaneously-ready cases, so a handler result that arrived at the same
// instant as the deadline can otherwise be discarded as a timeout even though
// the recovery genuinely succeeded. ok reports whether a result was actually
// there; callers use it to tell "genuinely finished, race lost the select"
// from "still running", which need different handling (see callHandler).
func preferResult(resultCh <-chan error) (ok bool, err error) {
	select {
	case err := <-resultCh:
		return true, err
	default:
		return false, nil
	}
}

// finishAbandonedRecovery waits for a Recover call that already outlived its
// TransactionTimeout to actually return, then performs the cleanup
// recoverTransaction deliberately skipped for it: releasing the claim with
// the real outcome via finishRecovery, and only then clearing inFlight.
//
// That order matters (round 8 review, finding 2): clearing inFlight first
// would let a sweep that reclaims txID between clearInFlight and finishRecovery's
// release start a brand new Recover call, and then have this goroutine's
// finishRecovery release the claim and overwrite status_message out from
// under that fresh attempt. Keeping inFlight set until the release actually
// completes means any such reclaim in that window is turned away by
// tryMarkInFlight and simply retried on a later sweep instead.
//
// This runs detached from both the sweep that started it and from Stop(): a
// call that already outlived one deadline may run for an unbounded time (see
// callHandler's doc comment above), so by the time it finally returns, the
// sweep's own ctx has long since completed and m.ctx may already be cancelled
// by Stop(). Tying this goroutine's cleanup to either would mean the release
// either never happens or is attempted with an already-cancelled context;
// context.Background() below is deliberate for the same reason. A release
// call that itself hangs (e.g. the store is unreachable) blocks only this one
// detached goroutine, the same accepted leak shape as the Recover call it is
// finishing up after.
//
// sweepCtx is the context callHandler was itself called with, i.e. the
// specific sweep's ctx at the moment this attempt was abandoned, not
// m.ctx read live, which is a Manager field a later Start() can reassign
// (see Start) while this detached goroutine is still running, making a live
// read of m.ctx a data race. A context.Context is safe for concurrent use by
// design, and this specific one does not change once captured: it only ever
// transitions from not-done to done, so reading its Err() here needs no
// further synchronization. sweepCtx.Err() being non-nil records whether Stop()
// had already run, for this lifecycle, before this call finally returned,
// i.e. whether this instance no longer necessarily held recovery leadership
// continuously on txID's behalf (Stop always releases leadership regardless
// of inFlight, see the leadership field). finishRecovery uses that to decide
// whether the Orphan-promotion heuristic is still safe to apply (round 8
// review, finding 3).
func (m *Manager) finishAbandonedRecovery(sweepCtx context.Context, txID string, storedAt time.Time, resultCh <-chan error, cancel context.CancelFunc) {
	err := <-resultCh
	cancel()
	stoppedBeforeReturn := sweepCtx.Err() != nil
	m.logger.Infof("recovery: tx [%s] finished after previously timing out; finalizing now", txID)

	if releaseErr := m.finishRecovery(context.Background(), txID, storedAt, err, !stoppedBeforeReturn); releaseErr != nil {
		m.logger.Warnf("recovery: abandoned attempt for transaction [%s] finished with an error: %v", txID, releaseErr)
	}

	m.clearInFlight(txID)
}

// tryMarkInFlight reports whether txID was not already in flight, marking it
// so as a side effect. The caller must pair a true result with a later
// clearInFlight once the underlying attempt actually finishes.
func (m *Manager) tryMarkInFlight(txID string) bool {
	m.inFlightMu.Lock()
	defer m.inFlightMu.Unlock()
	if _, ok := m.inFlight[txID]; ok {
		return false
	}
	m.inFlight[txID] = struct{}{}

	return true
}

func (m *Manager) clearInFlight(txID string) {
	m.inFlightMu.Lock()
	defer m.inFlightMu.Unlock()
	delete(m.inFlight, txID)
}

// recordTimeout increments and returns the consecutive-timeout count for txID.
func (m *Manager) recordTimeout(txID string) int {
	m.timeoutMu.Lock()
	defer m.timeoutMu.Unlock()
	entry := m.timeoutCounts[txID]
	entry.count++
	entry.lastSeen = time.Now()
	m.timeoutCounts[txID] = entry

	return entry.count
}

// clearTimeoutCount resets the consecutive-timeout count for txID, e.g. once
// an attempt succeeds or fails for a reason other than a timeout.
func (m *Manager) clearTimeoutCount(txID string) {
	m.timeoutMu.Lock()
	defer m.timeoutMu.Unlock()
	delete(m.timeoutCounts, txID)
}

// pruneTimeoutCounts drops entries untouched for longer than timeoutCountTTL.
// clearTimeoutCount only fires when this manager reattempts the same txID; a
// transaction that times out and is then confirmed by an independent
// finality listener leaves the Pending scan range and is never reattempted,
// so its entry would otherwise never be removed. Called once per sweep
// rather than per timeout to keep the cost off the hot path.
func (m *Manager) pruneTimeoutCounts() {
	cutoff := time.Now().Add(-timeoutCountTTL)

	m.timeoutMu.Lock()
	defer m.timeoutMu.Unlock()
	for txID, entry := range m.timeoutCounts {
		if entry.lastSeen.Before(cutoff) {
			delete(m.timeoutCounts, txID)
		}
	}
}

// maybeAlertStuckTransaction logs at Error once count crosses
// StuckTransactionAlertThreshold, then backs off by doubling (threshold, 2x,
// 4x, 8x, ...) instead of logging on every sweep. A stuck transaction is
// re-claimed every scan interval, so an unthrottled log here would flood
// alerting at that rate for as long as it stays stuck, which tends to get the
// whole alert muted rather than actually surfacing it.
func (m *Manager) maybeAlertStuckTransaction(txID string, count int) {
	threshold := m.config.StuckTransactionAlertThreshold
	if threshold <= 0 || count < threshold {
		return
	}
	if count != threshold && (count%threshold != 0 || !isPowerOfTwo(count/threshold)) {
		return
	}
	m.logger.Errorf("recovery: tx [%s] has timed out %d consecutive times (transactionTimeout=%s), status still unknown, investigate peer/network health", txID, count, m.config.TransactionTimeout)
}

func isPowerOfTwo(n int) bool {
	return n > 0 && n&(n-1) == 0
}

// recoverTransaction attempts to recover a transaction using the injected
// handler. The claim is released exactly once, with an appropriate message,
// for whichever of two paths actually produces a final result first: here,
// synchronously, if the handler returns within TransactionTimeout, or later,
// asynchronously by finishAbandonedRecovery, if it does not (see callHandler).
func (m *Manager) recoverTransaction(ctx context.Context, txID string, storedAt time.Time) error {
	m.logger.Debugf("recovering transaction [%s]", txID)

	finished, err := m.callHandler(ctx, txID, storedAt)

	if !finished {
		// A goroutine is still running unsupervised by this call, for one of
		// three reasons, and the claim must NOT be released here regardless
		// of which: doing so, as pre-round-7 code did, is what let a second
		// replica claim and run a concurrent Recover -> Commit against the
		// same tx while the original attempt was still in flight. The claim
		// stays held; finishAbandonedRecovery releases it once that attempt
		// actually returns, whenever that is.
		if errors.Is(err, errRecoveryInFlight) {
			// A previous attempt for this txID is already running past its
			// own TransactionTimeout: this is genuinely stuck from this
			// sweep's point of view.
			count := m.recordTimeout(txID)
			m.maybeAlertStuckTransaction(txID, count)
			m.logger.Debugf("recovery: tx [%s] skipped, a previous attempt is still running past transactionTimeout (%d consecutive timeout(s))", txID, count)

			return errSkippedInFlight
		}

		if !errors.Is(err, context.DeadlineExceeded) {
			// recoverCtx.Done() fired for a reason other than
			// TransactionTimeout elapsing: in practice, ctx's parent (m.ctx)
			// was cancelled by Stop(). A clean shutdown catching an attempt
			// mid-flight is not evidence the transaction itself is stuck, so
			// unlike the two cases above it must not count toward, or alert
			// on, the consecutive-timeout tally (round 8 review, finding 4).
			m.logger.Debugf("recovery: tx [%s] recovery attempt interrupted (%v), claim held for finishAbandonedRecovery to release", txID, err)

			return errSkippedShutdown
		}

		// This attempt's own deadline just fired with nothing back yet: a
		// real, this-attempt timeout.
		count := m.recordTimeout(txID)
		m.maybeAlertStuckTransaction(txID, count)
		m.logger.Debugf("recovery: tx [%s] still running past transactionTimeout, claim held for finishAbandonedRecovery to release (%d consecutive timeout(s))", txID, count)

		return errors.Wrapf(err, "recovery timed out for transaction [%s]", txID)
	}

	if errors.Is(err, context.DeadlineExceeded) {
		count := m.recordTimeout(txID)
		m.maybeAlertStuckTransaction(txID, count)
	} else {
		m.clearTimeoutCount(txID)
	}

	return m.finishRecovery(ctx, txID, storedAt, err, true)
}

// finishRecovery applies the Orphan-promotion policy and releases the claim
// with an outcome message, given a final result for txID. Called
// synchronously by recoverTransaction on the normal, within-deadline path,
// and asynchronously by finishAbandonedRecovery once an attempt that outlived
// its own deadline actually completes; either way this is called exactly once
// per attempt, whichever path resolves first, and is the only place that
// releases the claim.
//
// If the handler reports the transaction is not on the ledger and the row was
// stored more than NotFoundGracePeriod ago, the row is force-marked Orphan to
// prevent the queue head from being permanently blocked by transactions that
// never reached the ledger (e.g. broadcast failures whose audit log was
// persisted but whose tx never reached the orderer). Without this,
// ORDER BY stored_at ASC + LIMIT BatchSize would replay the same oldest rows
// on every sweep forever. The status is deliberately distinct from Deleted so
// operators and downstream tooling can tell broadcast losses apart from txs
// the ledger actively rejected.
//
// allowOrphanPromotion gates that write. The synchronous call site always
// passes true: it runs while this instance still holds recovery leadership
// for the sweep in progress, so nothing else can have raced ahead on txID.
// finishAbandonedRecovery passes false once it observes the sweep's own ctx
// already cancelled (Stop() ran before this attempt finally returned): Stop always
// releases leadership regardless of inFlight (see the leadership field), so a
// live peer could since have legitimately reclaimed and resolved txID, to
// say Confirmed, while this attempt was still finishing in the background.
// SetStatus is unconditional and owner-blind, unlike the owner-scoped release
// below it, so an unguarded write here could stomp that outcome with a stale
// Orphan (round 8 review, finding 3). Skipping the promotion in that case
// still releases the claim and logs the real outcome; a true fix needs an
// owner/status-scoped CAS at the SQL layer, filed as a follow-up rather than
// widening this PR into the storage driver.
func (m *Manager) finishRecovery(ctx context.Context, txID string, storedAt time.Time, err error, allowOrphanPromotion bool) error {
	markedOrphan := false
	if allowOrphanPromotion && err != nil && m.config.NotFoundGracePeriod > 0 && !storedAt.IsZero() && isNotFoundError(err) {
		age := time.Since(storedAt)
		if age > m.config.NotFoundGracePeriod {
			orphanMsg := fmt.Sprintf("tx never reached ledger (NotFound after %v, grace=%v)", age.Truncate(time.Second), m.config.NotFoundGracePeriod)
			m.logger.Warnf("recovery: marking tx [%s] as Orphan: %s", txID, orphanMsg)
			if setErr := m.storage.SetStatus(ctx, txID, storage.Orphan, orphanMsg); setErr != nil {
				m.logger.Errorf("recovery: failed to mark tx [%s] Orphan: %v", txID, setErr)
			} else {
				markedOrphan = true
			}
		}
	}

	// Always release the claim with appropriate message
	var message string
	switch {
	case markedOrphan:
		message = "tx marked Orphan after NotFound grace period"
	case err != nil:
		message = fmt.Sprintf("recovery failed: %v", err)
	default:
		message = "recovered successfully"
	}

	if releaseErr := m.releaseClaim(ctx, txID, message); releaseErr != nil {
		m.logger.Warnf("failed to release recovery claim for transaction [%s]: %v", txID, releaseErr)
	}

	if err != nil && !markedOrphan {
		return errors.Wrapf(err, "failed to recover transaction [%s]", txID)
	}

	if markedOrphan {
		// Treat as resolved — no need to noisily report a "failure" the next sweep
		// would otherwise re-encounter (the row is now Orphan and ineligible for
		// the Pending-only claim filter).
		return nil
	}

	m.logger.Infof("successfully recovered transaction [%s]", txID)

	return nil
}

// isNotFoundError reports whether err looks like a "transaction not found on
// ledger" failure surfaced from the recovery handler path. Matching by string
// is intentionally loose so the recovery loop does not pull grpc/codes or
// the network/fabric finality wrappers into its dependency surface; upstream
// wraps these statuses with errors.Wrapf so the substrings survive.
//
// Patterns covered (verified against the dev environment runtime error
//
//		"rpc error: code = NotFound desc = transaction ID [X]: not found in
//		  index: tx not found"):
//
//	  - "code = NotFound"         — raw gRPC status text
//	  - "not found in index"      — committer's gRPC status desc field
//	  - "tx not found"            — FSC finality.TxNotFound sentinel appended
//	  - "no such transaction ID"  — direct return from fabric in common/ledger/blkstorage/blockindex.go
//	    by fabric-x ledger.GetTransactionByID
//	    (fabric-smart-client/platform/fabricx/core/ledger/ledger.go:64).
//	    Stable across committer error format changes since the sentinel
//	    is wrapped at the FSC layer, above the committer's gRPC text.
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "code = NotFound"):
		return true
	case strings.Contains(msg, "not found in index"):
		return true
	case strings.Contains(msg, "tx not found"):
		return true
	case strings.Contains(msg, "no such transaction ID"):
		return true
	}

	return false
}

func (m *Manager) releaseClaim(ctx context.Context, txID string, message string) error {
	return m.storage.ReleaseRecoveryClaim(ctx, txID, m.config.InstanceID, message)
}
