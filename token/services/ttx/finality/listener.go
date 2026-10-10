/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.opentelemetry.io/otel/trace"
)

const MaxRetry = 3

// ErrUnrecognizedStatus reports a finality notification carrying a status this listener
// knows nothing about: neither terminal (network.Valid/network.Invalid) nor one of the
// in-flight states (network.Busy/network.Unknown). It is a should-never-happen path, and
// retrying it is pointless: the same arguments can never be reclassified, so this error
// terminates the retry loop at once instead of spending MaxRetry attempts and their backoff
// sleeps on it.
//
// Such a notification establishes nothing about where the transaction stands, so its
// selection locks must not be released - doing so could hand its tokens to a concurrent
// Select while it is still in flight. That is not this sentinel's doing: OnStatus releases
// only for network.Invalid, which an unrecognized status is not, so the locks are left to
// the recovery and lease-expiry sweeps by the same rule that covers every other non-rejected
// outcome.
var ErrUnrecognizedStatus = errors.New("unrecognized transaction status")

//go:generate counterfeiter -o mock/transaction_db.go -fake-name TransactionDB . transactionDB
type transactionDB interface {
	NewTransaction() (dbdriver.TransactionStoreTransaction, error)
	GetTokenRequest(ctx context.Context, txID string) ([]byte, error)
	SetStatus(ctx context.Context, txID string, status storage.TxStatus, message string) error
	NotifyStatus(ctx context.Context, txID string, status storage.TxStatus, message string)
}

// tokenRequestHasher defines the interface for processing token requests from raw bytes
//
//go:generate counterfeiter -o mock/token_request_hasher.go -fake-name TokenRequestHasher . tokenRequestHasher
type tokenRequestHasher interface {
	ProcessTokenRequest(ctx context.Context, tokenRequestRaw []byte) (tr *token.Request, msgToSign []byte, err error)
}

// tokensService defines the interface for token service operations needed by the listener
//
//go:generate counterfeiter -o mock/tokens_service.go -fake-name TokensService . tokensService
type tokensService interface {
	GetCachedTokenRequest(txID string) (*token.Request, []byte)
	// AppendValid applies the token request to the passed transaction, which it does not
	// commit. The returned function publishes the token events produced by the request and
	// must be invoked by the owner of the transaction, and only once the commit succeeded.
	AppendValid(ctx context.Context, tx dbdriver.Transaction, anchor token.RequestAnchor, tr *token.Request) (func(ctx context.Context), error)
}

// selectorManagerProvider resolves the token.SelectorManager for the TMS a
// transaction belongs to, so its locks can be released once its status is
// terminal (#2395 mechanism 4): without this, a settled transaction's locks
// linger until the lease-expiry sweep, during which its tokens stay invisible
// to the anti-join and keep colliding. Satisfied by dep.TokenManagementService.
//
//go:generate counterfeiter -o mock/selector_manager_provider.go -fake-name SelectorManagerProvider . selectorManagerProvider
type selectorManagerProvider interface {
	SelectorManager() (token.SelectorManager, error)
}

type Listener struct {
	logger                  logging.Logger
	net                     dep.Network
	namespace               string
	hasher                  tokenRequestHasher
	ttxDB                   transactionDB
	tokens                  tokensService
	selectorManagerProvider selectorManagerProvider
	tracer                  trace.Tracer
	metrics                 *Metrics
	retryRunner             utils.RetryRunner
}

func NewListener(
	logger logging.Logger,
	net dep.Network,
	namespace string,
	hasher tokenRequestHasher,
	ttxDB transactionDB,
	tokens tokensService,
	selectorManagerProvider selectorManagerProvider,
	tracer trace.Tracer,
	metricsProvider metrics.Provider,
) *Listener {
	return &Listener{
		logger:                  logger,
		net:                     net,
		namespace:               namespace,
		hasher:                  hasher,
		ttxDB:                   ttxDB,
		tokens:                  tokens,
		selectorManagerProvider: selectorManagerProvider,
		tracer:                  tracer,
		metrics:                 newMetrics(metricsProvider),
		retryRunner:             utils.NewRetryRunner(logger, MaxRetry, time.Second, true),
	}
}

// OnError is called when a finality event for txID could not be delivered. It does not release
// txID's selection locks, because at this point the transaction's status is not known to be
// terminal — and on some backends is not known at all.
//
// The EVM driver calls OnError precisely when no verdict could be obtained: once when every
// poll in the finality window failed to reach the chain ("could not reach the chain before the
// timeout", x/token/services/network/evm/finality/manager.go, which states outright that there
// is no evidence the anchor is invalid, only that it could not be observed) and once when the
// watch ends without one. A transaction that is in fact in flight — or already mined, with only
// the local observation broken — would then have its tokens handed to a concurrent Select,
// which is the same hazard that keeps Busy/Unknown and ErrUnrecognizedStatus from releasing,
// and strictly worse than the #2395 mechanism-4 window a release here would close. The fabricx
// driver only reaches OnError after a Valid verdict, so releasing would be harmless there, but
// the listener cannot tell the two callers apart.
//
// The locks are therefore left to the two backstops that do not need to know the verdict: the
// recovery sweep, which re-derives the status from the ledger and releases through
// TTXRecoveryHandler, and the lease-expiry sweep behind it.
func (t *Listener) OnError(_ context.Context, txID string, err error) {
	t.metrics.RetryExhausted.Add(1)
	t.logger.Errorf("finality listener: all retries exhausted for tx [%s]: %v", txID, err)
}

func (t *Listener) OnStatus(ctx context.Context, txID string, status int, message string, tokenRequestHash []byte) {
	start := time.Now()
	newCtx, span := t.tracer.Start(ctx, "on_status")
	defer span.End()
	// Deferred so the give-up/cancellation early returns below still record the duration,
	// exactly as the single success path at the end of this method does.
	defer func() { t.metrics.OnStatusDuration.Observe(time.Since(start).Seconds()) }()
	if err := t.retryRunner.RunWithErrorsContext(newCtx, func() (bool, error) {
		err := t.runOnStatus(newCtx, txID, status, message, tokenRequestHash)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, ErrUnrecognizedStatus):
			// Not retriable: the same arguments can never be reclassified.
			return true, err
		default:
			t.logger.Errorf("finality listener on [%s] failed with error: [%+v], retrying...", txID, err)

			return false, err
		}
	}); err != nil {
		t.logger.Errorf("finality listener on [%s] failed with error: [%+v], stop.", txID, err)
		if status != network.Invalid {
			// Giving up on anything other than a rejection leaves the locks held. The release
			// below is justified by local state agreeing with the ledger, which only a
			// rejection gives for free - there is nothing to apply locally for a transaction
			// the ledger threw away, so its inputs are unspent whether or not the status
			// persisted.
			//
			// Every other status fails that test here. On network.Valid the transaction
			// committed and its inputs are spent on the ledger, but the retry loop gave up
			// precisely because applying it locally kept failing: the token store still shows
			// those inputs as unspent, so releasing their locks lets a concurrent Select
			// re-pick tokens that are already gone and build a transaction that cannot
			// validate. network.Busy/network.Unknown never reach this branch (runOnStatus
			// returns nil for them), an unrecognized status establishes nothing about the
			// transaction at all (see ErrUnrecognizedStatus), and a canceled context means the
			// loop was abandoned rather than exhausted - RunWithErrorsContext returns ctx.Err()
			// as soon as the context is done - so a shutdown is not a verdict either.
			//
			// The locks are left to the two backstops that do not need the verdict and do not
			// depend on this notification: the recovery sweep, which re-derives the status from
			// the ledger and releases through TTXRecoveryHandler once the request is applied,
			// and the lease-expiry sweep behind it. Same reasoning as OnError.
			t.logger.Debugf("finality listener gave up on [%s] with status [%d]: selection locks left for the recovery and lease-expiry sweeps", txID, status)

			return
		}
		if ctxErr := newCtx.Err(); ctxErr != nil {
			// A rejection whose retry loop was abandoned rather than exhausted. The Unlock
			// would in any case be issued on this already-canceled context, and the sweeps
			// above are the backstop.
			//
			// The context is inspected rather than the error: a store error returned at the
			// moment of cancellation may itself wrap context.Canceled or
			// context.DeadlineExceeded while this listener's context is still live, and that
			// case is an ordinary persistence failure on a rejected transaction, which must
			// still release.
			t.logger.Debugf("finality listener on [%s] interrupted [%v], selection locks left for the recovery and lease-expiry sweeps", txID, ctxErr)

			return
		}
		// A rejected transaction whose local persistence kept failing: the retry budget is
		// exhausted, so this notification is given up on for good. Its inputs were never spent
		// - the ledger threw the transaction away - so nothing local has to be applied first
		// for the release to be correct, and txID's selection locks would otherwise sit held
		// until the next lease-expiry sweep, reproducing #2395 mechanism 4 via this path.
		// Releasing here — once per notification rather than once per retry attempt — is safe
		// for the same reason documented on OnError: Unlock is an idempotent no-op on an
		// already unlocked tx, and a subsequent selection attempt simply re-acquires locks as
		// needed.
		releaseLocks(newCtx, t.logger, t.selectorManagerProvider, txID)
	}
}

// handleValidStatus processes a network.Valid status change for txID: it loads (or reuses the
// cached) token request, checks its hash against tokenRequestHash, and commits it. It returns
// the resulting tx status (Confirmed, or Deleted on a hash mismatch) and status message.
func (t *Listener) handleValidStatus(ctx context.Context, txID, message string, tokenRequestHash []byte) (storage.TxStatus, string, error) {
	txStatus := storage.Confirmed
	t.logger.DebugfContext(ctx, "get token request for [%s]", txID)

	tr, msgToSign := t.tokens.GetCachedTokenRequest(txID)
	if tr == nil {
		// load it
		tokenRequestRaw, err := t.ttxDB.GetTokenRequest(ctx, txID)
		if err != nil {
			t.logger.ErrorfContext(ctx, "failed retrieving token request [%s]: [%s]", txID, err)

			return txStatus, message, errors.Errorf("failed retrieving token request [%s]: [%w]", txID, err)
		}
		t.logger.DebugfContext(ctx, "Read token request")

		// Process token request using the hasher
		tr, msgToSign, err = t.hasher.ProcessTokenRequest(ctx, tokenRequestRaw)
		if err != nil {
			return txStatus, message, errors.Errorf("failed to process token request [%s]: [%w]", txID, err)
		}
	}
	t.logger.DebugfContext(ctx, "Check token request")
	if err := t.checkTokenRequest(txID, msgToSign, tokenRequestHash); err != nil {
		t.logger.ErrorfContext(ctx, "tx [%s], %s", txID, err)
		t.metrics.HashMismatches.Add(1)

		return storage.Deleted, err.Error(), nil
	}

	if err := Commit(ctx, t.logger, t.tokens, t.ttxDB, txID, tr); err != nil {
		t.logger.ErrorfContext(ctx, "tx [%s], %s", txID, err)

		return txStatus, message, err
	}

	return txStatus, message, nil
}

func (t *Listener) runOnStatus(ctx context.Context, txID string, status int, message string, tokenRequestHash []byte) error {
	t.logger.DebugfContext(ctx, "tx status changed for tx [%s]: [%s]", txID, status)
	var txStatus storage.TxStatus
	switch status {
	case network.Valid:
		var err error
		txStatus, message, err = t.handleValidStatus(ctx, txID, message, tokenRequestHash)
		if err != nil {
			return err
		}
	case network.Invalid:
		txStatus = storage.Deleted
	case network.Busy, network.Unknown:
		// Not a terminal status: the transaction is still being committed and will be
		// notified again (or picked up by the recovery scan) once it settles. Releasing
		// its selection locks here would let a concurrent Select hand the very same tokens
		// to another transaction while this one is still in flight, which is strictly worse
		// than the #2395 mechanism-4 window the release exists to close. Mirrors
		// TTXRecoveryHandler.applyFinalityLogic's treatment of the same two statuses.
		t.logger.DebugfContext(ctx, "tx [%s] has status [%d], not yet finalized - nothing to do", txID, status)

		return nil
	default:
		// Genuinely unrecognized: retrying runOnStatus with the same arguments can never
		// reclassify it, and the transaction's true state is unknown, so neither retrying
		// nor releasing its selection locks is warranted. See ErrUnrecognizedStatus.
		return errors.Wrapf(ErrUnrecognizedStatus, "listener invoked on [%s] with status [%d], cannot proceed", txID, status)
	}

	// update the status, if here, either txStatus is not Confirmed
	if txStatus != storage.Confirmed {
		if err := t.ttxDB.SetStatus(ctx, txID, txStatus, message); err != nil {
			t.logger.ErrorfContext(ctx, "<message> [%s]: [%s]", txID, err)

			return errors.Errorf("<message> [%s]: [%w]", txID, err)
		}
	}

	if txStatus == storage.Confirmed {
		t.metrics.ConfirmedTransactions.Add(1)
	} else {
		t.metrics.DeletedTransactions.Add(1)
	}
	releaseLocks(ctx, t.logger, t.selectorManagerProvider, txID)
	t.logger.DebugfContext(ctx, "tx status changed for tx [%s]: [%s] done", txID, status)

	return nil
}

// releaseLocks unlocks any tokens txID locked during selection, now that its
// status is terminal (#2395 mechanism 4). It must never fail the settlement
// path, so it logs and continues on error, mirroring Transaction.Release.
func releaseLocks(ctx context.Context, logger logging.Logger, sp selectorManagerProvider, txID string) {
	sm, err := sp.SelectorManager()
	if err != nil {
		logger.WarnfContext(ctx, "failed to get selector manager to release locks for tx [%s]: [%s]", txID, err)

		return
	}
	if sm == nil {
		return
	}
	if err := sm.Unlock(ctx, txID); err != nil {
		logger.WarnfContext(ctx, "failed to release locks for tx [%s]: [%s]", txID, err)
	}
}

func (t *Listener) checkTokenRequest(txID string, trToSign []byte, reference []byte) error {
	if base64.StdEncoding.EncodeToString(reference) != utils.Hashable(trToSign).String() {
		t.logger.Errorf("tx [%s], tr hashes [%s][%s]", txID, base64.StdEncoding.EncodeToString(reference), utils.Hashable(trToSign))
		// no further processing of the tokens of these transactions
		return errors.Errorf(
			"token requests do not match, tr hashes [%s][%s]",
			base64.StdEncoding.EncodeToString(reference),
			utils.Hashable(trToSign),
		)
	}

	return nil
}

func Commit(
	ctx context.Context,
	logger logging.Logger,
	tokens tokensService,
	ttxDB transactionDB,
	txID string,
	tr *token.Request,
) error {
	logger.DebugfContext(ctx, "append valid token request for [%s]", txID)
	tx, err := ttxDB.NewTransaction()
	if err != nil {
		logger.ErrorfContext(ctx, "failed creating new transaction [%s]: [%s]", txID, err)

		return errors.Wrapf(err, "failed creating new transaction [%s]", txID)
	}
	defer func() {
		if tx != nil {
			tx.Rollback()
		}
	}()

	publishTokenEvents, err := tokens.AppendValid(ctx, tx, token.RequestAnchor(txID), tr)
	if err != nil {
		logger.ErrorfContext(ctx, "failed to append valid token request to token db [%s]: [%s]", txID, err)

		return errors.Wrapf(err, "failed to append valid token request to token db [%s]", txID)
	}
	logger.DebugfContext(ctx, "successfully appended valid token request for [%s]", txID)

	if err := tx.SetStatus(ctx, txID, driver.Confirmed, ""); err != nil {
		logger.ErrorfContext(ctx, "failed to set status [%s]: [%s]", txID, err)

		return errors.Wrapf(err, "failed to set status [%s]", txID)
	}

	if err := tx.Commit(); err != nil {
		return errors.Wrapf(err, "failed commit [%s]", txID)
	}

	tx = nil

	// The tokens are durably stored only now, so this is the first point at which
	// the add-token and delete-token events they produced may be observed. Publish
	// them before the status event, so that a woken finality waiter does not see a
	// confirmed transaction whose token events are still pending. The nil check
	// guards against an implementation that returns none: a missing publication is
	// preferable to panicking in the listener's goroutine.
	if publishTokenEvents != nil {
		publishTokenEvents(ctx)
	}

	// The transactional SetStatus above bypasses the store service, so push
	// the status event explicitly — otherwise finality waiters only wake on
	// the fallback polling.
	ttxDB.NotifyStatus(ctx, txID, driver.Confirmed, "")

	return nil
}
