/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package db

import (
	"sync"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/auditor"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/checks"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var logger = logging.MustGetLogger()

// sweeper owns the background drift sweeps a check service provider starts.
//
// The provider is asked for a check service once per TMS, when that TMS's
// service is first built, which is also the moment the node knows it will be
// working with that store. That makes it the natural place to start the sweep
// that keeps checking the store afterwards, so the checks stop being something
// only an operator running a command ever triggers.
type sweeper struct {
	configuration   checks.Configuration
	metricsProvider metrics.Provider
	mu              sync.Mutex
	managers        []*checks.Manager
}

// start builds a drift checks manager for one store and starts it.
func (s *sweeper) start(
	tmsProvider common.TokenManagementServiceProvider,
	networkProvider common.NetworkProvider,
	db common.TokenTransactionDB,
	storage checks.Storage,
	tokenDB *tokens.Service,
	tmsID token.TMSID,
	custom []common.NamedChecker,
	role string,
	maxPageSize int,
) error {
	if s.configuration == nil {
		// nothing to configure the sweep from, which is the case in setups that wire
		// the check service by hand. The on-demand checks still work.
		logger.Debugf("no configuration available, not starting drift checks for [%s][%s]", tmsID, role)

		return nil
	}

	config := checks.ConfigFor(s.configuration, tmsID)
	checkers := common.NewSweepFindingCheckers(
		tmsProvider,
		networkProvider,
		db,
		tokenDB,
		tmsID,
		common.WithBatchSize(config.BatchSize),
		common.WithTransactionWindow(config.TransactionWindow),
		common.WithMaxPageSize(maxPageSize),
	)
	// custom checkers registered through dependency injection still return plain
	// messages, so they are lifted rather than reimplemented
	checkers = append(checkers, common.AsNamedFindingCheckers(custom)...)

	manager := checks.NewManager(
		logger,
		storage,
		common.NewFindingsService(checkers),
		checks.NewMetrics(metrics.NewTMSProvider(tmsID, s.metricsProvider)),
		config,
		role,
		tmsID,
	)

	// Start and the append are one critical section, not two: Stop() also takes s.mu, so if it
	// ran between Start succeeding and this manager being appended, it would never see this
	// manager and never stop it - leaking its goroutine and, on postgres, the advisory-lock
	// connection it holds past shutdown. Appending after Start while holding the lock across
	// both (rather than releasing it between them) closes that window: Stop() now either runs
	// entirely before this manager exists to sweeper.Stop's eyes, or entirely after it is both
	// started and tracked, never in between. Start itself only validates config and spawns a
	// goroutine, so holding the lock across it is not a meaningful bottleneck even when several
	// TMSes start concurrently through one sweeper.
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := manager.Start(); err != nil {
		return errors.Wrapf(err, "failed to start drift checks for [%s][%s]", tmsID, role)
	}
	s.managers = append(s.managers, manager)

	return nil
}

// Stop stops every sweep this provider started.
func (s *sweeper) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var errs []error
	for _, manager := range s.managers {
		if err := manager.Stop(); err != nil {
			errs = append(errs, err)
		}
	}
	s.managers = nil

	return errors.Join(errs...)
}

// AuditorCheckServiceProvider creates check services for auditors.
// It combines default checkers with custom checkers for database validation.
type AuditorCheckServiceProvider struct {
	tmsProvider     common.TokenManagementServiceProvider
	networkProvider common.NetworkProvider
	checkers        []common.NamedChecker
	sweeper         *sweeper
	// MaxPageSize is the storage layer's configured max read page size, passed to
	// the default checkers so they page within the guard layer's cap. 0 means the
	// built-in default is used.
	MaxPageSize int
}

// NewAuditorCheckServiceProvider creates a new auditor check service provider.
//
// A nil configuration means no background sweep is started and only the
// on-demand checks are available.
func NewAuditorCheckServiceProvider(
	tmsProvider common.TokenManagementServiceProvider,
	networkProvider common.NetworkProvider,
	checkers []common.NamedChecker,
	configuration checks.Configuration,
	metricsProvider metrics.Provider,
) *AuditorCheckServiceProvider {
	return &AuditorCheckServiceProvider{
		tmsProvider:     tmsProvider,
		networkProvider: networkProvider,
		checkers:        checkers,
		sweeper:         &sweeper{configuration: configuration, metricsProvider: metricsProvider},
	}
}

// CheckService creates a check service for the given TMS ID and databases, and
// starts the background drift sweep over the audit store.
// It combines default checkers with custom checkers provided during initialization.
//
// A failure to start the background sweep is logged rather than returned: the
// sweep is a safety net on top of the on-demand checks below, not a
// precondition for them, so a bad checks config or a store that briefly can't
// take leadership must not stop the auditor service itself from being built.
func (a *AuditorCheckServiceProvider) CheckService(id token.TMSID, adb *auditdb.StoreService, tdb *tokens.Service) (auditor.CheckService, error) {
	if err := a.sweeper.start(a.tmsProvider, a.networkProvider, adb, adb, tdb, id, a.checkers, checks.RoleAuditor, a.MaxPageSize); err != nil {
		logger.Warnf("not running background drift checks for [%s][auditor]: %v", id, err)
	}

	defaultCheckers := common.AsNamedCheckers(common.NewDefaultFindingCheckers(a.tmsProvider, a.networkProvider, adb, tdb, id, common.WithMaxPageSize(a.MaxPageSize)))

	return common.NewChecksService(append(defaultCheckers, a.checkers...)), nil
}

// Stop stops the background drift sweeps this provider started.
func (a *AuditorCheckServiceProvider) Stop() error {
	return a.sweeper.Stop()
}

// OwnerCheckServiceProvider creates check services for token owners.
// It combines default checkers with custom checkers for database validation.
type OwnerCheckServiceProvider struct {
	tmsProvider     common.TokenManagementServiceProvider
	networkProvider common.NetworkProvider
	checkers        []common.NamedChecker
	sweeper         *sweeper
	// MaxPageSize is the storage layer's configured max read page size, passed to
	// the default checkers so they page within the guard layer's cap. 0 means the
	// built-in default is used.
	MaxPageSize int
}

// NewOwnerCheckServiceProvider creates a new owner check service provider.
//
// A nil configuration means no background sweep is started and only the
// on-demand checks are available.
func NewOwnerCheckServiceProvider(
	tmsProvider common.TokenManagementServiceProvider,
	networkProvider common.NetworkProvider,
	checkers []common.NamedChecker,
	configuration checks.Configuration,
	metricsProvider metrics.Provider,
) *OwnerCheckServiceProvider {
	return &OwnerCheckServiceProvider{
		tmsProvider:     tmsProvider,
		networkProvider: networkProvider,
		checkers:        checkers,
		sweeper:         &sweeper{configuration: configuration, metricsProvider: metricsProvider},
	}
}

// CheckService creates a check service for the given TMS ID and databases, and
// starts the background drift sweep over the owner's transaction store.
// It combines default checkers with custom checkers provided during initialization.
//
// A failure to start the background sweep is logged rather than returned: the
// sweep is a safety net on top of the on-demand checks below, not a
// precondition for them, so a bad checks config or a store that briefly can't
// take leadership must not stop the owner service itself from being built.
func (a *OwnerCheckServiceProvider) CheckService(id token.TMSID, txdb *ttxdb.StoreService, tdb *tokens.Service) (ttx.CheckService, error) {
	if err := a.sweeper.start(a.tmsProvider, a.networkProvider, txdb, txdb, tdb, id, a.checkers, checks.RoleOwner, a.MaxPageSize); err != nil {
		logger.Warnf("not running background drift checks for [%s][owner]: %v", id, err)
	}

	defaultCheckers := common.AsNamedCheckers(common.NewDefaultFindingCheckers(a.tmsProvider, a.networkProvider, txdb, tdb, id, common.WithMaxPageSize(a.MaxPageSize)))

	return common.NewChecksService(append(defaultCheckers, a.checkers...)), nil
}

// Stop stops the background drift sweeps this provider started.
func (a *OwnerCheckServiceProvider) Stop() error {
	return a.sweeper.Stop()
}
