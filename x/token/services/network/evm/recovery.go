/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/recovery"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/wrapper"
	ttxfinality "github.com/LFDT-Panurus/panurus/token/services/ttx/finality"
)

// recoveryStore is what recovering a transaction needs of a store: the sweep that finds the ones
// stuck at Pending, and the writes that record what the chain answered. Both the transaction store
// and the audit store satisfy it, which is why one wiring serves both. It is declared here because
// the recovery handler's own view of a store is unexported.
type recoveryStore interface {
	recovery.Storage
	NewTransaction() (dbdriver.TransactionStoreTransaction, error)
	GetTokenRequest(ctx context.Context, txID string) ([]byte, error)
	NotifyStatus(ctx context.Context, txID string, status storage.TxStatus, message string)
}

// startRecovery starts the transaction-recovery sweep for a TMS, over both the transaction store and
// the audit store.
//
// A node learns that a transaction it holds became final through a finality listener the ttx layer
// registers when it stores that transaction (`token/services/ttx/db.go`, and the auditor's
// equivalent). That registration lives in memory. A node that restarts between storing a transaction
// and its finality is therefore left with a row stuck at Pending and nothing that will ever move it:
// the chain has the answer and nobody is asking. Every later wait on that transaction runs to its
// timeout, which reads as a finality bug and is not one.
//
// The recovery manager is what asks. It periodically claims transactions that have been Pending for
// longer than its TTL and resolves each through GetTransactionStatus, which for this driver is the
// same anchor lookup a fresh listener would have done. The Fabric driver starts exactly these two
// managers for the same reason, and fabricx inherits them by building on it. This driver started
// none, which stays invisible for as long as no node restarts.
func (d *Driver) startRecovery(tmsID token2.TMSID, network *Network) error {
	if d.ttxStores == nil || d.auditStores == nil || d.tokensManager == nil {
		logger.Debugf("no transaction stores available; pending transactions will not be recovered on [%s]", tmsID)

		return nil
	}

	key := tmsID.String()
	d.recoveryMu.Lock()
	defer d.recoveryMu.Unlock()
	// Connect is called once per namespace, but a network can be built more than once over a node's
	// life. Two managers sweeping one store would claim each other's transactions.
	if _, running := d.recoveries[key]; running {
		return nil
	}

	tokensService, err := d.tokensManager.ServiceByTMSId(tmsID)
	if err != nil {
		return errors.Wrapf(err, "evm: failed to get the tokens service for [%s]", tmsID)
	}
	ttxStore, err := d.ttxStores.StoreServiceByTMSId(tmsID)
	if err != nil {
		return errors.Wrapf(err, "evm: failed to get the transaction store for [%s]", tmsID)
	}
	auditStore, err := d.auditStores.StoreServiceByTMSId(tmsID)
	if err != nil {
		return errors.Wrapf(err, "evm: failed to get the audit store for [%s]", tmsID)
	}

	config := d.recoveryConfig(tmsID)
	started := make([]*recovery.Manager, 0, 2)
	for _, store := range []recoveryStore{ttxStore, auditStore} {
		handler := ttxfinality.NewTTXRecoveryHandler(
			logger,
			network,
			tmsID.Namespace,
			ttxfinality.NewTokenRequestHasher(wrapper.NewTokenManagementServiceProvider(d.tmsProvider), tmsID),
			tmsID,
			store,
			tokensService,
			d.recoveryTracer,
			d.metricsProvider,
		)

		manager := recovery.NewManager(logger, store, handler, config)
		if err := manager.Start(); err != nil {
			// Stop what already started, so a half-wired TMS does not leave a sweeper polling on
			// behalf of a network the caller is about to discard.
			stopAll(started)

			return errors.Wrapf(err, "evm: failed to start transaction recovery for [%s]", tmsID)
		}
		started = append(started, manager)
	}

	d.recoveries[key] = started
	logger.Debugf("transaction recovery started for [%s]", tmsID)

	return nil
}

// recoveryConfig returns the TMS's recovery settings, falling back to the defaults. Recovery is a
// safety net, so a configuration that cannot be read downgrades to the defaults rather than
// preventing the network from coming up without it.
func (d *Driver) recoveryConfig(tmsID token2.TMSID) recovery.Config {
	cfg, err := d.resolver.ConfigurationFor(tmsID)
	if err != nil {
		logger.Debugf("no configuration for [%s]; recovering with the default settings: %v", tmsID, err)

		return recovery.DefaultConfig()
	}
	loaded, err := recovery.LoadConfig(cfg)
	if err != nil {
		logger.Warnf("failed to load the recovery configuration for [%s]; using the defaults: %v", tmsID, err)

		return recovery.DefaultConfig()
	}

	return loaded
}

// stopAll stops a set of managers, used to unwind a partially started TMS.
//
// There is deliberately no driver-wide stop: driver.Driver has no shutdown seam to call one from, and
// the sweeps live as long as the process, exactly as the Fabric driver's do. If a shutdown hook is
// ever added to the interface, this is what it would call, per TMS, from the recoveries map.
func stopAll(managers []*recovery.Manager) {
	for _, manager := range managers {
		if err := manager.Stop(); err != nil {
			logger.Debugf("failed to stop a recovery manager: %v", err)
		}
	}
}
