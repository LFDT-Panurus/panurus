/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
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

	config := d.recoveryConfig(tmsID, network.config.Finality.Timeout)
	started := make([]*recovery.Manager, 0, 2)
	for _, store := range []recoveryStore{ttxStore, auditStore} {
		handler := ttxfinality.NewTTXRecoveryHandler(
			logger,
			settledNetwork{network},
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
//
// The TTL is raised to the finality timeout if it is shorter, which is what makes settledNetwork's
// verdict safe. See its comment for why the two belong together.
func (d *Driver) recoveryConfig(tmsID token2.TMSID, finalityTimeout time.Duration) recovery.Config {
	config := recovery.DefaultConfig()

	cfg, err := d.resolver.ConfigurationFor(tmsID)
	switch {
	case err != nil:
		logger.Debugf("no configuration for [%s]; recovering with the default settings: %v", tmsID, err)
	default:
		loaded, err := recovery.LoadConfig(cfg)
		if err != nil {
			logger.Warnf("failed to load the recovery configuration for [%s]; using the defaults: %v", tmsID, err)
		} else {
			config = loaded
		}
	}

	if finalityTimeout > config.TTL {
		config.TTL = finalityTimeout
	}

	return config
}

// settledNetwork answers "still unknown" as "invalid", for the recovery path only.
//
// A failed applyStateDelta reverts, so it emits no StateCommitted event and stores no token request
// hash. Nothing about a rejected transaction is ever written to the chain, which means an anchor that
// is absent is indistinguishable from one that was never submitted, and StatusByAnchor can only
// answer Unknown for both. Time is the sole remaining signal, exactly as the design says (§7.1,
// "Unknown, then Invalid after the configured timeout"; §7.4, the recipient's only failure signal).
//
// The finality listener already implements that escalation, because it knows when it started
// waiting. The status path did not, and the two disagreeing is the whole defect: the shared recovery
// handler treats Unknown as "transient, look again next sweep", so a rejected transaction was swept
// every few seconds forever while the record stayed Pending and the holding it reserved was never
// released.
//
// What makes the verdict safe here is *when* it is given. The recovery manager only claims rows whose
// stored timestamp is older than the configured TTL, and recoveryConfig raises that TTL to the
// finality timeout. So by the time this is asked, the database itself has established that the
// transaction has been outstanding for longer than a transaction is ever awaited. That clock is a
// column, not a field: it survives the restart that recovery exists to clean up after, which an
// in-memory timer would not.
type settledNetwork struct{ *Network }

// GetTransactionStatus resolves an anchor that is still absent to Invalid rather than Unknown.
func (n settledNetwork) GetTransactionStatus(
	ctx context.Context,
	namespace, txID string,
) (int, []byte, string, error) {
	status, hash, message, err := n.Network.GetTransactionStatus(ctx, namespace, txID)
	if err != nil || status != driver.Unknown {
		return status, hash, message, err
	}

	logger.Debugf(
		"transaction [%s] is still absent from the chain after the finality timeout; recording it as invalid", txID)

	return driver.Invalid, hash, "transaction was not applied within the finality timeout", nil
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
