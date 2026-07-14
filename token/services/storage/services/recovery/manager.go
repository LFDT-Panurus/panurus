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
	defaultLockID        int64 = 0x74746b7265636f76
	defaultBatchSize           = 100
	defaultWorkers             = 4
	defaultLeaseDuration       = 30 * time.Second
)

//go:generate counterfeiter -o mock/storage.go -fake-name Storage . Storage

// Storage defines the interface for querying pending transactions and transaction details
type Storage interface {
	AcquireRecoveryLeadership(ctx context.Context, lockID int64) (Leadership, bool, error)
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
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
	mu      sync.Mutex
}

// NewManager creates a new recovery manager
func NewManager(
	logger logging.Logger,
	storage Storage,
	handler Handler,
	config Config,
) *Manager {
	return &Manager{
		logger:  logger,
		storage: storage,
		handler: handler,
		config:  config,
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

	if m.config.InstanceID == "" {
		m.config.InstanceID = fmt.Sprintf("recovery-%p", m)
	}

	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.started = true

	m.wg.Add(1)
	go m.recoveryLoop()

	m.logger.Infof("transaction recovery manager started (TTL: %s, Scan Interval: %s, Batch Size: %d, Workers: %d, Lease Duration: %s, Lock ID: %d, Instance ID: %s)",
		m.config.TTL, m.config.ScanInterval, m.config.BatchSize, m.config.WorkerCount, m.config.LeaseDuration, m.config.AdvisoryLockID, m.config.InstanceID)

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
		m.logger.Warnf("initial transaction recovery sweep failed: %v", err)
	}

	for {
		select {
		case <-m.ctx.Done():
			m.logger.Debugf("recovery loop stopped")

			return
		case <-ticker.C:
			if err := m.runSweep(m.ctx); err != nil {
				m.logger.Warnf("transaction recovery sweep failed: %v", err)
			}
		}
	}
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
	default:
		return nil
	}
}

func (m *Manager) runSweep(ctx context.Context) error {
	leadership, acquired, err := m.storage.AcquireRecoveryLeadership(ctx, m.config.AdvisoryLockID)
	if err != nil {
		return errors.Wrapf(err, "failed to acquire recovery leadership")
	}
	if !acquired {
		m.logger.Debugf("recovery leadership not acquired")

		return nil
	}
	defer func() {
		if err := leadership.Close(); err != nil {
			m.logger.Warnf("failed to release recovery leadership: %v", err)
		}
	}()

	return m.recoverTransactions(ctx)
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

	// ClaimPendingTransactions reads directly from the requests table where
	// tx_id is the primary key, so each claim is already unique. Fan out
	// straight to the workers; nil entries are defensive but should never
	// occur in practice.
	for _, claim := range records {
		if claim == nil {
			continue
		}
		work <- claim
	}
	close(work)

	workerWG.Wait()
	close(errCh)

	var firstErr error
	failures := 0
	for err := range errCh {
		if err == nil {
			continue
		}
		failures++
		// Log each individual failure for better debugging
		m.logger.Warnf("recovery failure: %v", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	if failures > 0 {
		m.logger.Warnf("completed recovery sweep: claimed=%d, succeeded=%d, failed=%d", len(records), len(records)-failures, failures)
	} else {
		m.logger.Debugf("completed recovery sweep: claimed=%d, all succeeded", len(records))
	}

	return firstErr
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

// recoverTransaction attempts to recover a transaction using the injected handler
// and always releases the claim with an appropriate message.
//
// If the handler reports the transaction is not on the ledger and the row was
// stored more than NotFoundGracePeriod ago, the row is force-marked Orphan to
// prevent the queue head from being permanently blocked by transactions that
// never reached the ledger (e.g. broadcast failures whose audit log was
// persisted but whose tx never reached the orderer). Without this,
// ORDER BY stored_at ASC + LIMIT BatchSize would replay the same oldest-100
// rows on every sweep forever. The status is deliberately distinct from
// Deleted so operators and downstream tooling can tell broadcast losses apart
// from txs the ledger actively rejected.
func (m *Manager) recoverTransaction(ctx context.Context, txID string, storedAt time.Time) error {
	m.logger.Debugf("recovering transaction [%s]", txID)

	// Attempt recovery using the injected handler
	err := m.handler.Recover(ctx, txID)

	// Residual race: SetStatus is unconditional, so an independent finality
	// listener that confirms this tx between our claim and the write below
	// could be overwritten by Orphan. In practice the NotFoundGracePeriod
	// (default 30 min) makes this window vanishingly small — we only get
	// here when the tx has been Pending for grace+ AND Recover just returned
	// NotFound. Future hardening: replace SetStatus with an atomic
	// "status=Pending → Orphan" CAS at the SQL layer.
	markedOrphan := false
	if err != nil && m.config.NotFoundGracePeriod > 0 && !storedAt.IsZero() && isNotFoundError(err) {
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
//	  - "code = NotFound"     — raw gRPC status text
//	  - "not found in index"  — committer's gRPC status desc field
//	  - "tx not found"        — FSC finality.TxNotFound sentinel appended
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
	}

	return false
}

func (m *Manager) releaseClaim(ctx context.Context, txID string, message string) error {
	return m.storage.ReleaseRecoveryClaim(ctx, txID, m.config.InstanceID, message)
}
