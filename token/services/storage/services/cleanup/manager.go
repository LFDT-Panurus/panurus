/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cleanup

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

//go:generate counterfeiter -o mock/storage.go -fake-name Storage . Storage

// DeletedToken represents a token that has been deleted and needs key cleanup
type DeletedToken struct {
	TxID          string
	Index         uint64
	OwnerIdentity []byte
	OwnerType     string
	DeletedAt     time.Time
}

// Storage defines the interface for querying deleted tokens
type Storage interface {
	// AcquireCleanupLeadership acquires an advisory lock for cleanup leadership
	AcquireCleanupLeadership(ctx context.Context, lockID int64) (Leadership, bool, error)
	// GetDeletedTokensPendingSKICleanup returns deleted tokens older than the specified duration that haven't had their SKI keys cleaned.
	// Only tokens owned by this node are returned, since this node only holds the secret keys for tokens it owns.
	GetDeletedTokensPendingSKICleanup(ctx context.Context, olderThan time.Duration, limit int) ([]DeletedToken, error)
	// MarkTokenCleaned marks a token as having its SKI keys cleaned up
	MarkTokenCleaned(ctx context.Context, txID string, index uint64, cleanedBy string) error
}

//go:generate counterfeiter -o mock/leadership.go -fake-name Leadership . Leadership

// Leadership represents an acquired advisory lock leadership session
type Leadership interface {
	// Close releases the leadership lock
	Close() error
}

//go:generate counterfeiter -o mock/identity_provider.go -fake-name IdentityProvider . IdentityProvider

// SKIProvider provides methods to derive SKIs from identities
type SKIProvider interface {
	// GetSKIsFromIdentity derives one or more SKIs from an owner identity
	GetSKIsFromIdentity(ctx context.Context, identity []byte, identityType string) ([]string, error)
}

//go:generate counterfeiter -o mock/keystore.go -fake-name Keystore . Keystore

// Keystore provides key deletion operations
type Keystore interface {
	// Delete removes the key with the given identifier
	Delete(id string) error
	// Close closes the keystore
	Close() error
}

//go:generate counterfeiter -o mock/keystore_provider.go -fake-name KeystoreProvider . KeystoreProvider

// KeystoreProvider provides access to keystores
type KeystoreProvider interface {
	// Keystore returns the keystore for the given TMS
	Keystore(tmsID token.TMSID) (Keystore, error)
}

// Manager handles the cleanup of cryptographic keys for deleted tokens
type Manager struct {
	logger           logging.Logger
	storage          Storage
	skiProvider      SKIProvider
	keystoreProvider KeystoreProvider
	tmsID            token.TMSID
	config           Config
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	started          bool
	mu               sync.Mutex
}

// NewManager creates a new keystore cleanup manager
func NewManager(
	logger logging.Logger,
	storage Storage,
	skiProvider SKIProvider,
	keystoreProvider KeystoreProvider,
	tmsID token.TMSID,
	config Config,
) *Manager {
	return &Manager{
		logger:           logger,
		storage:          storage,
		skiProvider:      skiProvider,
		keystoreProvider: keystoreProvider,
		tmsID:            tmsID,
		config:           config,
	}
}

// Start begins the cleanup process
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.config.Enabled {
		m.logger.Debugf("keystore cleanup is disabled")

		return nil
	}

	if m.started {
		return errors.Errorf("cleanup manager already started")
	}

	if err := m.validateConfig(); err != nil {
		return err
	}

	if m.config.InstanceID == "" {
		m.config.InstanceID = fmt.Sprintf("cleanup-%p", m)
	}

	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.started = true

	m.wg.Add(1)
	go m.cleanupLoop()

	m.logger.Infof("keystore cleanup manager started (TTL: %s, Scan Interval: %s, Batch Size: %d, Workers: %d, Lock ID: %d, Instance ID: %s)",
		m.config.TTL, m.config.ScanInterval, m.config.BatchSize, m.config.WorkerCount, m.config.AdvisoryLockID, m.config.InstanceID)

	return nil
}

// Stop gracefully stops the cleanup process
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return nil
	}

	m.logger.Infof("stopping keystore cleanup manager")
	m.cancel()
	m.wg.Wait()
	m.started = false
	m.logger.Infof("keystore cleanup manager stopped")

	return nil
}

// cleanupLoop is the main loop that periodically scans for deleted tokens needing cleanup
func (m *Manager) cleanupLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.ScanInterval)
	defer ticker.Stop()

	// Run initial sweep immediately
	if err := m.runSweep(m.ctx); err != nil {
		m.logger.Warnf("initial keystore cleanup sweep failed: %v", err)
	}

	for {
		select {
		case <-m.ctx.Done():
			m.logger.Debugf("cleanup loop stopped")

			return
		case <-ticker.C:
			if err := m.runSweep(m.ctx); err != nil {
				m.logger.Warnf("keystore cleanup sweep failed: %v", err)
			}
		}
	}
}

func (m *Manager) validateConfig() error {
	switch {
	case m.config.TTL <= 0:
		return errors.Errorf("invalid cleanup TTL [%s]", m.config.TTL)
	case m.config.ScanInterval <= 0:
		return errors.Errorf("invalid cleanup scan interval [%s]", m.config.ScanInterval)
	case m.config.BatchSize <= 0:
		return errors.Errorf("invalid cleanup batch size [%d]", m.config.BatchSize)
	case m.config.WorkerCount <= 0:
		return errors.Errorf("invalid cleanup worker count [%d]", m.config.WorkerCount)
	default:
		return nil
	}
}

func (m *Manager) runSweep(ctx context.Context) error {
	leadership, acquired, err := m.storage.AcquireCleanupLeadership(ctx, m.config.AdvisoryLockID)
	if err != nil {
		return errors.Wrapf(err, "failed to acquire cleanup leadership")
	}
	if !acquired {
		m.logger.Debugf("cleanup leadership not acquired")

		return nil
	}
	defer func() {
		if err := leadership.Close(); err != nil {
			m.logger.Warnf("failed to release cleanup leadership: %v", err)
		}
	}()

	m.logger.Debugf("scanning for deleted tokens older than %s (batch size: %d)",
		m.config.TTL, m.config.BatchSize)

	tokens, err := m.storage.GetDeletedTokensPendingSKICleanup(ctx, m.config.TTL, m.config.BatchSize)
	if err != nil {
		return errors.Wrapf(err, "failed to get deleted tokens")
	}

	if len(tokens) == 0 {
		m.logger.Debugf("no deleted tokens found needing cleanup")

		return nil
	}

	m.logger.Debugf("found %d deleted token(s) needing key cleanup", len(tokens))

	return m.cleanupTokens(ctx, tokens)
}

// cleanupTokens processes deleted tokens using local workers
func (m *Manager) cleanupTokens(ctx context.Context, tokens []DeletedToken) error {
	work := make(chan DeletedToken)
	errCh := make(chan error, len(tokens))
	var workerWG sync.WaitGroup

	// Start workers
	for range m.config.WorkerCount {
		workerWG.Add(1)
		go m.worker(ctx, &workerWG, work, errCh)
	}

	// Fan out work to workers
	for _, token := range tokens {
		select {
		case work <- token:
			// continue
		case <-m.ctx.Done():
			m.logger.Debugf("cleanup tokens cancelled")

			return errors.Errorf("cleanup tokens cancelled [%w]", m.ctx.Err())
		}
	}
	close(work)

	workerWG.Wait()
	close(errCh)

	// Collect errors
	failures := 0
	errs := make([]error, 0, len(tokens))
	for err := range errCh {
		if err == nil {
			continue
		}
		failures++
		m.logger.Warnf("cleanup failure: %v", err)
		errs = append(errs, err)
	}

	if failures > 0 {
		m.logger.Warnf("completed cleanup sweep: processed=%d, succeeded=%d, failed=%d",
			len(tokens), len(tokens)-failures, failures)
	} else {
		m.logger.Debugf("completed cleanup sweep: processed=%d, all succeeded", len(tokens))
	}

	return errors.Join(errs...)
}

func (m *Manager) worker(ctx context.Context, wg *sync.WaitGroup, work <-chan DeletedToken, errCh chan<- error) {
	defer wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case token, ok := <-work:
			if !ok {
				return
			}
			if err := m.cleanupToken(ctx, token); err != nil {
				errCh <- err
			}
		}
	}
}

// cleanupToken cleans up keys for a single deleted token
func (m *Manager) cleanupToken(ctx context.Context, token DeletedToken) error {
	m.logger.Debugf("cleaning up keys for deleted token [%s:%d]", token.TxID, token.Index)

	// Get keystore
	keystore, err := m.keystoreProvider.Keystore(m.tmsID)
	if err != nil {
		return errors.Wrapf(err, "failed to get keystore for token [%s:%d]", token.TxID, token.Index)
	}

	// Derive SKIs from owner identity
	skis, err := m.skiProvider.GetSKIsFromIdentity(ctx, token.OwnerIdentity, token.OwnerType)
	if err != nil {
		return errors.Wrapf(err, "failed to derive SKIs for token [%s:%d]", token.TxID, token.Index)
	}

	if len(skis) == 0 {
		m.logger.Warnf("no SKIs derived for token [%s:%d], skipping", token.TxID, token.Index)
		// Still mark as cleaned to avoid retrying
		if err := m.storage.MarkTokenCleaned(ctx, token.TxID, token.Index, m.config.InstanceID); err != nil {
			return errors.Wrapf(err, "failed to mark token [%s:%d] as cleaned", token.TxID, token.Index)
		}

		return nil
	}

	m.logger.Debugf("deleting %d key(s) for token [%s:%d]", len(skis), token.TxID, token.Index)

	// Delete each key
	var deleteErrors []error
	for _, ski := range skis {
		if err := keystore.Delete(ski); err != nil {
			m.logger.Warnf("failed to delete key [%s] for token [%s:%d]: %v", ski, token.TxID, token.Index, err)
			deleteErrors = append(deleteErrors, err)
		} else {
			m.logger.Debugf("deleted key [%s] for token [%s:%d]", ski, token.TxID, token.Index)
		}
	}

	// If all deletions failed, return error without marking as cleaned
	if len(deleteErrors) > 0 {
		return errors.Errorf("failed to delete keys for token [%s:%d]: %v", token.TxID, token.Index, deleteErrors)
	}

	// Mark token as cleaned (even if some keys failed to delete)
	if err := m.storage.MarkTokenCleaned(ctx, token.TxID, token.Index, m.config.InstanceID); err != nil {
		return errors.Wrapf(err, "failed to mark token [%s:%d] as cleaned", token.TxID, token.Index)
	}

	m.logger.Infof("successfully cleaned up keys for token [%s:%d]", token.TxID, token.Index)

	return nil
}
