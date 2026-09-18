/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"sync"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/recovery"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/wrapper"
	ttxfinality "github.com/LFDT-Panurus/panurus/token/services/ttx/finality"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
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
	// Transactions is how settledNetwork reads a row's age. Both stores alias the same record and
	// pagination types, so one signature serves both.
	Transactions(
		ctx context.Context,
		params dbdriver.QueryTransactionsParams,
		pagination cdriver.Pagination,
	) (*cdriver.PageIterator[*dbdriver.TransactionRecord], error)
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

	binding, err := network.binding(tmsID.Namespace)
	if err != nil {
		return err
	}

	config := d.recoveryConfig(tmsID)
	parser := ttxfinality.NewTokenRequestHasher(wrapper.NewTokenManagementServiceProvider(d.tmsProvider), tmsID)
	started := make([]*recovery.Manager, 0, 2)
	for _, store := range []recoveryStore{ttxStore, auditStore} {
		handler := ttxfinality.NewTTXRecoveryHandler(
			logger,
			settledNetwork{
				Network: network,
				store:   store,
				timeout: binding.config.Finality.Timeout,
				grace:   binding.config.Finality.ConflictGrace,
				parser:  parser,
				spent:   network,
				conflicts: &conflictWatch{
					entries: map[string]conflictEntry{},
				},
			},
			tmsID.Namespace,
			parser,
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
// The sweep delay is deliberately left alone. It answers "how soon is it worth asking again", which
// is a different question from "how long before absence means rejection" - see settledNetwork.
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

// tokenRequestParser turns a store's raw token request bytes back into a *token2.Request. The one
// method settledNetwork needs is already implemented, verbatim, by *ttxfinality.TokenRequestHasher,
// which startRecovery builds once per TMS and hands to both the recovery handler and settledNetwork -
// declared here, on the consumer side, because that is the only method either of them uses of it.
type tokenRequestParser interface {
	ProcessTokenRequest(ctx context.Context, tokenRequestRaw []byte) (*token2.Request, []byte, error)
}

// spentReader answers whether tokens have already been spent on chain. *Network satisfies it; it is
// declared here, on the consumer side, so settledNetwork depends on the one method it calls rather
// than the whole network surface.
type spentReader interface {
	AreTokensSpent(ctx context.Context, namespace string, tokenIDs []*token.ID, meta []string) ([]bool, error)
}

// settledNetwork answers "still unknown" as "invalid" for the recovery path only, along two
// independent paths: elapsed time, and evidence that the transaction can never apply.
//
// A failed applyStateDelta reverts, so it emits no StateCommitted event and stores no token request
// hash. Nothing about a rejected transaction is ever written to the chain, which means an absent
// anchor is indistinguishable from one that was never submitted, and StatusByAnchor can only answer
// Unknown for both.
//
// Time is one signal: unknown, then invalid after the configured timeout, the same asymmetry a
// recipient resolving finality by anchor already lives with. The finality listener already
// implements that escalation, because it knows when it started waiting. The status path did not, and
// the two disagreeing is the defect that fixed first: the shared recovery handler reads Unknown as
// "transient, look again next sweep", so a rejected transaction was swept every few seconds forever
// while its record stayed Pending and the holding it reserved was never released.
//
// Evidence is the other, faster signal, and the reason this type exists rather than a bare timeout.
// An anchor absent from the chain plus an input already spent by some other, already-applied
// transaction proves this transaction can never apply: snSpent only ever moves from false to true
// (contracts/src/TokenState.sol), so the input cannot become unspent, and _applyTransfer reverts on
// any spent input every time it is tried. The verdict does not need to wait for the timeout because
// nothing about waiting longer could change it. This is also what makes the auditor recover on its
// own: startRecovery sweeps the audit store through the same settledNetwork, so an auditor that never
// saw the broadcaster's rejection reaches Invalid by reading the chain, not by being told.
//
// Evidence needs a grace window rather than firing the moment it appears - see
// FinalityConfig.ConflictGrace - and it needs the anchor re-read after a positive spent check, because
// the two eth_calls read a moving block tag and the transaction under suspicion could have applied in
// between them.
//
// Neither signal touches the finality listener (finality/manager.go), which has no store and so
// cannot enumerate a transaction's inputs; it keeps its own, slower, time-only escalation. A
// transaction's owner therefore still notices a permanent rejection through the listener no sooner
// than the timeout, while its own or its auditor's recovery sweep notices it in roughly ConflictGrace.
//
// The age comes from the store rather than from the sweep schedule. Those are two different
// questions and answering both with the recovery TTL gets one of them wrong: raising the TTL to the
// finality timeout does license the verdict, but it also stops recovery from rescuing a *committed*
// transaction until that timeout has passed, and the committed case is the one where the answer was
// available immediately. Reading the row's own timestamp keeps the sweep frequent and the verdict
// patient. It is also a column rather than a field, so it survives the restart recovery exists for,
// which an in-memory timer would not - unlike the conflict grace clock, which is exactly that in-memory
// timer, deliberately, because a restart losing a few seconds of accumulated grace only ever delays a
// permanent verdict, never reverses one.
type settledNetwork struct {
	*Network
	store   recoveryStore
	timeout time.Duration
	// grace, parser, spent, and conflicts implement the evidence path. parser and spent are nil-safe:
	// a settledNetwork built without them (as the pre-existing tests do) simply never finds evidence
	// and falls straight through to the timeout, unchanged.
	grace     time.Duration
	parser    tokenRequestParser
	spent     spentReader
	conflicts *conflictWatch
}

// conflictWatch remembers, per transaction, the input token ids worth re-checking and when a spent
// input was first observed among them. It is the in-memory clock behind ConflictGrace.
//
// A pointer lives inside settledNetwork rather than the map itself because the recovery handler
// (token/services/ttx/finality) takes settledNetwork by value; a plain map field would still share
// the same underlying map across copies, but a pointer makes that sharing the obvious, intended thing
// rather than an accident of map semantics. The mutex is required: the recovery manager runs its
// worker pool concurrently (WorkerCount, storage/services/recovery), and this map is looked up and
// written from every one of those workers.
type conflictWatch struct {
	mu      sync.Mutex
	entries map[string]conflictEntry
	// nextPrune is when the next stale-entry sweep is allowed to run. A prune walks the whole map, so
	// it is throttled to at most once per pruneInterval rather than paying that cost on every lookup.
	nextPrune time.Time
}

// conflictEntry is what conflictWatch remembers about one transaction.
type conflictEntry struct {
	// inputs are the transaction's spendable input ids, parsed once and reused: token IDs are
	// immutable for the life of a transaction, so re-parsing the stored request on every sweep would
	// buy nothing. A nil, non-empty-length-zero slice here is not a valid state; see noCandidates.
	inputs []*token.ID
	// noCandidates records that parsing found nothing worth checking - no inputs, or none with a
	// usable id - so the conflict check is skipped on every later sweep instead of re-parsing to
	// rediscover the same negative result.
	noCandidates bool
	// firstSeen is when a spent input was first observed for this transaction. Zero means none has
	// been observed yet, or a previously-observed conflict went away on a later check.
	firstSeen time.Time
	// touched is the last time this entry was read or written by any sweep. It drives eviction: a
	// transaction resolves (Valid, Invalid, or deleted from the store entirely) and its own entry has
	// no other trigger to go away, so without this every txID a sweep ever looked at would accumulate
	// here for the life of the process.
	touched time.Time
}

// pruneLocked drops entries not touched within maxAge, at most once per maxAge. The caller must hold
// w.mu. maxAge <= 0 disables pruning rather than treating it as "prune everything, every time" -
// settledNetwork uses it only when it has a meaningful timeout to derive a window from.
func (w *conflictWatch) pruneLocked(maxAge time.Duration) {
	if maxAge <= 0 {
		return
	}
	now := time.Now()
	if now.Before(w.nextPrune) {
		return
	}
	w.nextPrune = now.Add(maxAge)
	cutoff := now.Add(-maxAge)
	for id, entry := range w.entries {
		if entry.touched.Before(cutoff) {
			delete(w.entries, id)
		}
	}
}

// GetTransactionStatus resolves an Unknown status to Invalid either once the transaction is older
// than the finality timeout, or sooner, once its inputs are proven already spent - see
// settledNetwork's own doc for why both exist and how they interact.
func (n settledNetwork) GetTransactionStatus(
	ctx context.Context,
	namespace, txID string,
) (int, []byte, string, error) {
	status, hash, message, err := n.Network.GetTransactionStatus(ctx, namespace, txID)
	if err != nil || status != driver.Unknown {
		return status, hash, message, err
	}

	doomed, pending, condemnMessage := n.condemnedByConflict(ctx, namespace, txID)
	if doomed {
		// The anchor and spent reads are two eth_calls against a moving block tag; between them, the
		// very transaction under suspicion may have applied. Re-reading closes that gap: the re-read
		// runs at a block at least as recent as the spent read that just condemned it, so if the
		// transaction landed in between, this read sees it and the conflict is stood down instead of
		// deleting a transfer that is now final.
		recheckStatus, recheckHash, recheckMessage, recheckErr := n.Network.GetTransactionStatus(ctx, namespace, txID)
		if recheckErr != nil {
			logger.Debugf("could not re-read the anchor for [%s] before condemning it on conflicting "+
				"evidence; leaving it for the next sweep: %v", txID, recheckErr)

			return status, hash, message, nil
		}
		if recheckStatus == driver.Unknown {
			logger.Debugf("transaction [%s] has an input already spent by another transaction; "+
				"recording it as invalid", txID)

			return driver.Invalid, recheckHash, condemnMessage, nil
		}

		return recheckStatus, recheckHash, recheckMessage, nil
	}
	if pending {
		// A conflict has been observed but has not yet held for the full grace window. This must not
		// fall through to the age gate below: the store row backing a transaction that is merely
		// prepared-but-not-yet-broadcast is already older than the finality timeout by the time its
		// competing transaction lands (see FinalityConfig.ConflictGrace), so the age gate would condemn
		// it immediately on a timeout that has nothing to do with this transaction's own history,
		// defeating the entire point of waiting out the grace window first.
		return status, hash, message, nil
	}

	age, known, err := n.age(ctx, txID)
	if err != nil {
		// The chain says nothing and the store could not say when we started asking. Leaving it
		// Unknown keeps it in the sweep, which is the safe direction: the alternative is condemning a
		// transaction on the strength of a failed database read.
		logger.Debugf("could not read the age of [%s]; leaving it for the next sweep: %v", txID, err)

		return status, hash, message, nil
	}
	if !known || age <= n.timeout {
		return status, hash, message, nil
	}

	logger.Debugf("transaction [%s] has been absent from the chain for %s; recording it as invalid", txID, age)

	return driver.Invalid, hash, "transaction was not applied within the finality timeout", nil
}

// condemnedByConflict reports whether txID has an input token already spent by some other
// transaction. It returns two independent booleans, not one: doomed means the conflict has held for
// at least the configured grace window and the caller should condemn (subject to the anchor
// re-read); pending means a conflict was observed but has not yet held that long, and the caller
// must treat the transaction as still Unknown rather than falling through to the age gate - see the
// call site in GetTransactionStatus for why that fallthrough would defeat the grace window.
//
// An error reading the store or the chain is never treated as evidence: both results come back
// false, so a transient failure can only ever leave the timeout in charge, never manufacture a
// verdict.
func (n settledNetwork) condemnedByConflict(ctx context.Context, namespace, txID string) (doomed, pending bool, message string) {
	if n.conflicts == nil || n.parser == nil || n.spent == nil || n.grace <= 0 {
		return false, false, ""
	}

	// Pruned unconditionally, before candidateInputs, rather than only on the anySpent path further
	// down: a transaction with no usable inputs (an issue-only ledger, or - per spentTokenIDsOf's own
	// doc - every transfer as seen from the recipient's side) returns before ever reaching that later
	// code, and a conflictWatch swept only by such transactions would otherwise never prune at all.
	n.conflicts.mu.Lock()
	n.conflicts.pruneLocked(n.pruneWindow())
	n.conflicts.mu.Unlock()

	inputs, ok := n.candidateInputs(ctx, txID)
	if !ok {
		return false, false, ""
	}

	spent, err := n.spent.AreTokensSpent(ctx, namespace, inputs, nil)
	if err != nil {
		// A revert here is expected under graph hiding, where areTokensSpent is unsupported by design
		// (contracts/src/TokenState.sol); this degrades that case to the timeout rather than failing
		// the sweep.
		logger.Debugf("could not check whether the inputs of [%s] are already spent; leaving it for the "+
			"next sweep: %v", txID, err)

		return false, false, ""
	}

	anySpent := false
	for _, s := range spent {
		if s {
			anySpent = true

			break
		}
	}

	n.conflicts.mu.Lock()
	defer n.conflicts.mu.Unlock()
	entry := n.conflicts.entries[txID]
	entry.touched = time.Now()
	if !anySpent {
		// Whatever was observed before no longer holds. Clearing firstSeen rather than leaving it set
		// means a transient bad read followed by a good one cannot be stitched together into an
		// apparent grace window that was never actually continuous.
		entry.firstSeen = time.Time{}
		n.conflicts.entries[txID] = entry

		return false, false, ""
	}
	if entry.firstSeen.IsZero() {
		entry.firstSeen = entry.touched
		n.conflicts.entries[txID] = entry

		return false, true, ""
	}

	// Clamped rather than validated at load time: FinalityConfig.ConflictGrace documents why a grace
	// above the timeout is effectively dead code rather than a configuration error, since Timeout can
	// legitimately be edited after Config is loaded without ConflictGrace being re-derived from it.
	grace := n.grace
	if n.timeout > 0 && n.timeout < grace {
		grace = n.timeout
	}
	if time.Since(entry.firstSeen) < grace {
		return false, true, ""
	}

	return true, false, "transaction inputs were already spent by another transaction; it can never be applied"
}

// candidateInputs returns the input token ids of txID worth checking for a conflict, and whether there
// are any. The result is memoised in conflicts: parsing the stored request costs a store read and a
// full request decode, and a transaction's inputs cannot change once written.
func (n settledNetwork) candidateInputs(ctx context.Context, txID string) ([]*token.ID, bool) {
	n.conflicts.mu.Lock()
	entry, cached := n.conflicts.entries[txID]
	if cached {
		// Touched here too, not only in condemnedByConflict's own write: a transaction with no usable
		// inputs (noCandidates) or with an unspent input never reaches that later write in the same
		// call, and would otherwise look untouched to pruneLocked despite being checked every sweep.
		entry.touched = time.Now()
		n.conflicts.entries[txID] = entry
	}
	n.conflicts.mu.Unlock()
	if cached {
		if entry.noCandidates {
			return nil, false
		}

		return entry.inputs, true
	}

	inputs, err := n.spentTokenIDsOf(ctx, txID)
	n.conflicts.mu.Lock()
	defer n.conflicts.mu.Unlock()
	if err != nil {
		// Not remembered as noCandidates: a store or codec failure may be transient, and the next
		// sweep deserves another attempt at parsing rather than a permanent "nothing to check" verdict.
		logger.Debugf("could not read the inputs of [%s]; leaving it for the next sweep: %v", txID, err)

		return nil, false
	}
	if len(inputs) == 0 {
		n.conflicts.entries[txID] = conflictEntry{noCandidates: true, touched: time.Now()}

		return nil, false
	}

	n.conflicts.entries[txID] = conflictEntry{inputs: inputs, touched: time.Now()}

	return inputs, true
}

// pruneWindow is how long a conflictWatch entry may go untouched before it is eligible for eviction.
// It is derived from Timeout rather than a separate constant so that, like ConflictGrace itself, it
// scales with the same deployment's own notion of how long a transaction may reasonably take: ten
// timeouts is comfortably longer than any legitimate sweep gap, while still bounding memory rather
// than growing for the life of the process. Grace is the fallback only for the pathological
// configuration where Timeout is unset; pruning is disabled outright, rather than guessing a window,
// only if neither is positive.
func (n settledNetwork) pruneWindow() time.Duration {
	if n.timeout > 0 {
		return 10 * n.timeout
	}
	if n.grace > 0 {
		return 10 * n.grace
	}

	return 0
}

// spentTokenIDsOf reads and parses txID's stored token request and returns the usable input ids worth
// checking on chain.
//
// Two shapes of id are dropped rather than sent to AreTokensSpent, which would otherwise fail the
// whole batch over one bad id: a recipient's own store keeps only the metadata it filtered for itself,
// which drops TokenID from every input it did not also send (token/metadata.go, filterTransfer), so a
// recipient's copy of someone else's transfer yields nils here; and an issue action's inputs, present
// only for driver-specific uses such as token upgrades, may name ids from a ledger this TMS's
// TokenState never held, which keys.AnchorFromTxID rejects as an invalid anchor rather than a valid
// but foreign one.
func (n settledNetwork) spentTokenIDsOf(ctx context.Context, txID string) ([]*token.ID, error) {
	raw, err := n.store.GetTokenRequest(ctx, txID)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}

	request, _, err := n.parser.ProcessTokenRequest(ctx, raw)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Metadata == nil {
		return nil, nil
	}
	// Built directly over Metadata rather than through request.GetMetadata(), which additionally
	// dereferences TokenService.tms to fill in fields SpentTokenID never reads. Going around it means a
	// request that did not come from a fully wired TMS - which is all a parsed-from-bytes recovery read
	// ever needs to be - cannot nil-panic the sweep over a field this call has no use for.
	metadata := &token2.Metadata{TokenRequestMetadata: request.Metadata}
	all := metadata.SpentTokenID()
	usable := make([]*token.ID, 0, len(all))
	for _, id := range all {
		if id == nil || id.TxId == "" {
			continue
		}
		if _, err := keys.AnchorFromTxID(id.TxId); err != nil {
			continue
		}
		usable = append(usable, id)
	}

	return usable, nil
}

// age returns how long ago the transaction was written to the store.
func (n settledNetwork) age(ctx context.Context, txID string) (time.Duration, bool, error) {
	it, err := n.store.Transactions(ctx, dbdriver.QueryTransactionsParams{IDs: []string{txID}}, nil)
	if err != nil {
		return 0, false, err
	}
	if it == nil || it.Items == nil {
		// A store bug that returns success with nothing to read from is treated the same as a failed
		// read: not known, left for the next sweep, rather than dereferenced.
		return 0, false, nil
	}
	defer it.Items.Close()

	record, err := it.Items.Next()
	if err != nil {
		return 0, false, err
	}
	if record == nil {
		return 0, false, nil
	}
	// An unset timestamp would read as an age of two thousand years and condemn the transaction on the
	// very first sweep. "Not known" is the honest answer and the safe one: the caller leaves the
	// transaction Unknown and looks again, which is what it already does when the store cannot be read
	// at all.
	if record.Timestamp.IsZero() {
		return 0, false, nil
	}

	return time.Since(record.Timestamp), true, nil
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
