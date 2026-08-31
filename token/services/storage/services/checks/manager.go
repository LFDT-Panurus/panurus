/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package checks

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math/rand"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	dbcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Roles a sweep can run under. A node runs one sweep per store it owns.
const (
	// RoleOwner sweeps the transaction store of a token owner.
	RoleOwner = "owner"
	// RoleAuditor sweeps the transaction store of an auditor.
	RoleAuditor = "auditor"
)

// writeTimeout bounds persisting one sweep's results (UpsertFindings and
// ResolveFindingsNotSeenSince), off the parent context rather than
// Config.Timeout. That budget is spent by checker.Check, which can legitimately
// consume nearly all of it on a node with a lot to check; sharing it with the
// writes would mean the sweeps with the most to report are the ones most
// likely to reach the deadline before persisting any of it.
const writeTimeout = 2 * time.Minute

//go:generate counterfeiter -o mock/storage.go -fake-name Storage . Storage

// Storage is the part of a transaction store the checks sweep needs: the lock it
// elects a leader with, and the table it records findings in.
type Storage interface {
	// AcquireLeadership tries to take the advisory lock that makes this
	// replica the one that sweeps. It uses its own lock id, independent of
	// the store's recovery lock, so the two sweeps never wait on each other.
	AcquireLeadership(ctx context.Context, lockID int64) (Leadership, bool, error)
	// UpsertFindings records what a sweep found.
	UpsertFindings(ctx context.Context, findings []dbdriver.FindingRecord, seenAt time.Time) error
	// ResolveFindingsNotSeenSince closes findings a sweep stopped reporting.
	ResolveFindingsNotSeenSince(ctx context.Context, checkers []string, seenAt time.Time) (int64, error)
}

//go:generate counterfeiter -o mock/leadership.go -fake-name Leadership . Leadership

// Leadership is an acquired advisory lock session.
//
// The checks sweep reuses the recovery lock machinery rather than adding a
// parallel one; it takes a different lock id, so the two sweeps do not wait on
// each other.
type Leadership = dbdriver.RecoveryLeadership

//go:generate counterfeiter -o mock/checker.go -fake-name Checker . Checker

// Checker runs the drift checks.
type Checker interface {
	// Check runs every check and returns everything they found.
	Check(ctx context.Context) ([]dbcommon.Finding, error)
	// ResolvableCheckers names the checks whose stored findings may be closed when
	// a sweep stops reporting them.
	ResolvableCheckers() []string
}

// Manager runs the ledger drift checks in the background.
//
// The checks themselves are not new: a node has always been able to run them on
// demand. What was missing is anyone running them on a node that is simply up,
// which is when drift actually appears. This sweeps them on an interval, records
// what they find so a repeated problem is aged rather than re-reported, and
// closes a finding once it stops being reported.
//
// Only one replica sweeps a given store at a time, decided by an advisory lock,
// so putting several replicas on one database multiplies neither the ledger
// traffic nor the findings.
type Manager struct {
	logger  logging.Logger
	storage Storage
	checker Checker
	metrics *Metrics
	config  Config
	role    string
	tmsID   token.TMSID
	// lockID is the advisory lock this sweep elects a leader with. It is derived
	// from tmsID and role rather than taken from config, so that TMSes sharing a
	// persistence configuration - which are told apart only by network, channel
	// and namespace - never collide on the same lock. A single lock id for every
	// TMS on a node was a real bug: it meant only one TMS across the whole fleet
	// ever won the checks sweep on any tick, and every other TMS's ledger drift
	// checks silently never ran.
	lockID int64
	//nolint:containedctx // long-running service lifecycle, not a per-request context
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
	mu      sync.Mutex
}

// NewManager creates a checks manager for one store of one TMS. The role names
// which store it is, and shows up in logs and metrics.
func NewManager(
	logger logging.Logger,
	storage Storage,
	checker Checker,
	metrics *Metrics,
	config Config,
	role string,
	tmsID token.TMSID,
) *Manager {
	return &Manager{
		logger:  logger,
		storage: storage,
		checker: checker,
		metrics: metrics,
		config:  config,
		role:    role,
		tmsID:   tmsID,
		lockID:  checksLockID(tmsID, role),
	}
}

// checksLockID derives a deterministic advisory lock id for one store's checks
// sweep from its TMS and role, the same fields that make the store itself
// unique. Using SHA256 rather than a smaller hash keeps collisions between
// unrelated TMSes astronomically unlikely.
func checksLockID(tmsID token.TMSID, role string) int64 {
	h := sha256.Sum256([]byte("checks:" + tmsID.Network + "/" + tmsID.Channel + "/" + tmsID.Namespace + "/" + role))

	return int64(binary.BigEndian.Uint64(h[:])) //nolint:gosec
}

// Start begins sweeping. It returns without starting anything when the checks
// are disabled.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.config.Enabled {
		m.logger.Debugf("ledger drift checks are disabled for [%s][%s]", m.tmsID, m.role)

		return nil
	}

	if m.started {
		return errors.Errorf("checks manager already started for [%s][%s]", m.tmsID, m.role)
	}

	if err := m.validateConfig(); err != nil {
		return err
	}

	// The context is deliberately held on the manager: it scopes the background
	// sweep loop started below and is cancelled by Stop, so its lifetime is the
	// manager's, not a caller's.
	//nolint:fatcontext // long-running service lifecycle, not a per-request context
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.started = true

	m.wg.Add(1)
	go m.checksLoop()

	m.logger.Infof("ledger drift checks started for [%s][%s] (scan interval: %s, timeout: %s, batch size: %d, transaction window: %s, lock id: %d)",
		m.tmsID, m.role, m.config.ScanInterval, m.config.Timeout, m.config.BatchSize, m.config.TransactionWindow, m.lockID)

	return nil
}

// Stop gracefully stops sweeping, waiting for a sweep in flight to unwind.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return nil
	}

	m.logger.Infof("stopping ledger drift checks for [%s][%s]", m.tmsID, m.role)
	m.cancel()
	m.wg.Wait()
	m.started = false
	m.logger.Infof("ledger drift checks stopped for [%s][%s]", m.tmsID, m.role)

	return nil
}

func (m *Manager) validateConfig() error {
	switch {
	case m.config.ScanInterval <= 0:
		return errors.Errorf("invalid checks scan interval [%s]", m.config.ScanInterval)
	case m.config.Timeout <= 0:
		return errors.Errorf("invalid checks timeout [%s]", m.config.Timeout)
	case m.config.BatchSize <= 0:
		return errors.Errorf("invalid checks batch size [%d]", m.config.BatchSize)
	case m.config.TransactionWindow < 0:
		return errors.Errorf("invalid checks transaction window [%s]", m.config.TransactionWindow)
	case m.config.Timeout > m.config.ScanInterval:
		return errors.Errorf("invalid checks timeout [%s] exceeds scan interval [%s], sweeps would run back to back",
			m.config.Timeout, m.config.ScanInterval)
	default:
		return nil
	}
}

// checksLoop sweeps on the configured interval until the manager is stopped.
func (m *Manager) checksLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.ScanInterval)
	defer ticker.Stop()

	// Spread the initial sweep over a second so that replicas restarted together do
	// not all reach for the advisory lock at the same instant.
	jitter := time.Duration(rand.Int63n(int64(time.Second)))
	select {
	case <-m.ctx.Done():
		m.logger.Debugf("checks loop stopped before initial sweep")

		return
	case <-time.After(jitter):
	}

	if err := m.runSweep(m.ctx); err != nil {
		m.logSweepError(err)
	}

	for {
		select {
		case <-m.ctx.Done():
			m.logger.Debugf("checks loop stopped")

			return
		case <-ticker.C:
			if err := m.runSweep(m.ctx); err != nil {
				m.logSweepError(err)
			}
		}
	}
}

// logSweepError reports a sweep that failed.
//
// A sweep cut short by Stop is an ordinary shutdown rather than a failure, and a
// sweep that ran past its timeout is a configuration problem the duration
// histogram already shows, so neither is worth a warning.
func (m *Manager) logSweepError(err error) {
	switch {
	case errors.Is(err, context.Canceled):
		m.logger.Debugf("ledger drift checks sweep stopped for [%s][%s]: %v", m.tmsID, m.role, err)
	case errors.Is(err, context.DeadlineExceeded):
		m.logger.Warnf("ledger drift checks sweep for [%s][%s] ran past its %s timeout, consider a shorter transaction window or a longer interval",
			m.tmsID, m.role, m.config.Timeout)
	default:
		m.logger.Warnf("ledger drift checks sweep failed for [%s][%s]: %v", m.tmsID, m.role, err)
	}
}

// RunSweep runs one sweep now, if this replica can take the leadership. It is
// what the background loop calls, exported so that an operator-facing command or
// a test can force a sweep without waiting for the interval.
func (m *Manager) RunSweep(ctx context.Context) error {
	return m.runSweep(ctx)
}

func (m *Manager) runSweep(ctx context.Context) error {
	leadership, acquired, err := m.storage.AcquireLeadership(ctx, m.lockID)
	if err != nil {
		m.metrics.SweepsTotal.With("role", m.role, "outcome", outcomeFailed).Add(1)

		return errors.Wrapf(err, "failed to acquire checks leadership")
	}
	if !acquired {
		m.logger.Debugf("checks leadership not acquired for [%s][%s], another replica is sweeping", m.tmsID, m.role)
		m.metrics.SweepsTotal.With("role", m.role, "outcome", outcomeNotLeader).Add(1)

		return nil
	}
	defer func() {
		if err := leadership.Close(); err != nil {
			m.logger.Warnf("failed to release checks leadership: %v", err)
		}
	}()

	if err := m.sweep(ctx); err != nil {
		m.metrics.SweepsTotal.With("role", m.role, "outcome", outcomeFailed).Add(1)

		return err
	}
	m.metrics.SweepsTotal.With("role", m.role, "outcome", outcomeCompleted).Add(1)

	return nil
}

// sweep runs the checks once and records what they found.
func (m *Manager) sweep(ctx context.Context) error {
	checkCtx, checkCancel := context.WithTimeout(ctx, m.config.Timeout)
	start := time.Now()
	findings, err := m.checker.Check(checkCtx)
	checkCancel()
	m.metrics.SweepDuration.With("role", m.role).Observe(time.Since(start).Seconds())
	if err != nil {
		return errors.Wrapf(err, "failed running the drift checks")
	}

	// Timestamp the sighting after the checks have run, so that a finding recorded
	// now is strictly newer than one from the previous sweep, which is what makes
	// "not seen since" mean what it says.
	seenAt := time.Now().UTC()

	// The writes get their own budget off the parent context: sharing Config.Timeout
	// with checker.Check would mean a sweep that used nearly all of it reaches these
	// calls with almost no time left, discarding everything it just found.
	writeCtx, writeCancel := context.WithTimeout(ctx, writeTimeout)
	defer writeCancel()

	if err := m.storage.UpsertFindings(writeCtx, findingRecords(findings), seenAt); err != nil {
		return errors.Wrapf(err, "failed recording [%d] findings", len(findings))
	}

	resolved, err := m.storage.ResolveFindingsNotSeenSince(writeCtx, resolvableCheckers(m.checker.ResolvableCheckers(), findings), seenAt)
	if err != nil {
		return errors.Wrapf(err, "failed closing findings that are no longer reported")
	}

	m.report(findings, resolved)

	return nil
}

// report logs and counts what a sweep found.
func (m *Manager) report(findings []dbcommon.Finding, resolved int64) {
	open := map[dbcommon.Severity]int{
		dbcommon.SeverityInfo:     0,
		dbcommon.SeverityWarning:  0,
		dbcommon.SeverityCritical: 0,
	}
	for _, finding := range findings {
		open[finding.Severity]++
		m.metrics.FindingsObserved.With(
			"role", m.role,
			"checker", finding.Checker,
			"code", finding.Code,
			"severity", finding.Severity.String(),
		).Add(1)
	}
	for severity, count := range open {
		m.metrics.FindingsOpen.With("role", m.role, "severity", severity.String()).Set(float64(count))
	}
	if resolved > 0 {
		m.metrics.FindingsResolved.With("role", m.role).Add(float64(resolved))
	}

	critical := open[dbcommon.SeverityCritical]
	warnings := open[dbcommon.SeverityWarning]
	switch {
	case critical > 0:
		// Log every critical one: these are the findings that mean the node's books
		// and the ledger disagree about money, and a count alone is not actionable.
		for _, finding := range findings {
			if finding.Severity == dbcommon.SeverityCritical {
				m.logger.Errorf("ledger drift [%s][%s] %s: %s", m.tmsID, m.role, finding.Checker, finding.Message)
			}
		}
		m.logger.Errorf("ledger drift checks for [%s][%s] found %d critical and %d other finding(s), resolved %d",
			m.tmsID, m.role, critical, len(findings)-critical, resolved)
	case len(findings) > 0:
		m.logger.Warnf("ledger drift checks for [%s][%s] found %d warning(s) and %d informational finding(s), resolved %d",
			m.tmsID, m.role, warnings, len(findings)-warnings, resolved)
	default:
		m.logger.Debugf("ledger drift checks for [%s][%s] found nothing, resolved %d", m.tmsID, m.role, resolved)
	}
}

// findingRecords converts findings into the rows the store persists.
func findingRecords(findings []dbcommon.Finding) []dbdriver.FindingRecord {
	records := make([]dbdriver.FindingRecord, 0, len(findings))
	for _, finding := range findings {
		tokenID := ""
		if finding.TokenID != nil {
			tokenID = finding.TokenID.String()
		}
		records = append(records, dbdriver.FindingRecord{
			Key:      finding.Key(),
			Checker:  finding.Checker,
			Code:     finding.Code,
			Severity: int(finding.Severity),
			TxID:     finding.TxID,
			TokenID:  tokenID,
			Message:  finding.Message,
		})
	}

	return records
}

// resolvableCheckers drops from candidates every check that reported an
// inconclusive finding in this sweep - not just an outright CodeCheckFailed,
// but any code meaning some part of the check could not reach a real verdict
// (dbcommon.IsInconclusive), such as CodeTxStatusUnavailable.
//
// A check that could not complete, even partially, proved nothing for the
// items it could not resolve, so its older findings must stay open. Closing
// them would turn an unreachable ledger into a clean bill of health, which is
// the failure mode this whole service exists to avoid.
func resolvableCheckers(candidates []string, findings []dbcommon.Finding) []string {
	if len(candidates) == 0 {
		return nil
	}

	inconclusive := make(map[string]struct{})
	for _, finding := range findings {
		if dbcommon.IsInconclusive(finding.Code) {
			inconclusive[finding.Checker] = struct{}{}
		}
	}
	if len(inconclusive) == 0 {
		return candidates
	}

	resolvable := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := inconclusive[candidate]; !ok {
			resolvable = append(resolvable, candidate)
		}
	}

	return resolvable
}
