/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditor

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/finality"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/tracing"
	"go.opentelemetry.io/otel/trace"
)

var logger = logging.MustGetLogger()

//go:generate counterfeiter -o mock/transaction.go -fake-name Transaction . Transaction
//go:generate counterfeiter -o mock/network_provider.go -fake-name NetworkProvider . NetworkProvider
//go:generate counterfeiter -o mock/check_service.go -fake-name CheckService . CheckService
//go:generate counterfeiter -o mock/network_driver.go -fake-name Network github.com/LFDT-Panurus/panurus/token/services/network/driver.Network
//go:generate counterfeiter -o mock/audit_transaction_store.go -fake-name AuditTransactionStore github.com/LFDT-Panurus/panurus/token/services/storage/db/driver.AuditTransactionStore
//go:generate counterfeiter -o mock/tst.go -fake-name TransactionStoreTransaction github.com/LFDT-Panurus/panurus/token/services/storage/db/driver.TransactionStoreTransaction

// TxStatus is the status of a transaction
type TxStatus = auditdb.TxStatus

const (
	// Pending is the status of a transaction that has been submitted to the ledger
	Pending = auditdb.Pending
	// Confirmed is the status of a transaction that has been confirmed by the ledger
	Confirmed = auditdb.Confirmed
	// Deleted is the status of a transaction that has been deleted due to a failure to commit
	Deleted = auditdb.Deleted
	// Orphan is the status of a transaction that never reached the ledger
	Orphan = auditdb.Orphan
)

const txIdLabel tracing.LabelName = "tx_id"

var TxStatusMessage = auditdb.TxStatusMessage

// Transaction models a generic token transaction
type Transaction interface {
	ID() string
	Network() string
	Channel() string
	Namespace() string
	Request() *token.Request
}

type NetworkProvider interface {
	GetNetwork(network string, channel string) (*network.Network, error)
}

type CheckService interface {
	Check(ctx context.Context) ([]string, error)
}

// Service is the interface for the auditor service
type Service struct {
	tmsID           token.TMSID
	networkProvider NetworkProvider
	auditDB         *auditdb.StoreService
	tokenDB         *tokens.Service
	tmsProvider     dep.TokenManagementServiceProvider
	finalityTracer  trace.Tracer
	metricsProvider metrics.Provider
	metrics         *Metrics
	checkService    CheckService
	lockConfig      *LockConfig

	// auditRecords caches, per request anchor, a snapshot of the audit record
	// computed by Audit for reuse by Append; entries are dropped by Release.
	recordsMu    sync.Mutex
	auditRecords map[token.RequestAnchor]auditRecordEntry
}

// auditRecordEntry pairs a cached audit record with the request it was
// computed from.
type auditRecordEntry struct {
	request *token.Request
	record  *token.AuditRecord
}

// NewService creates a new auditor Service with the provided dependencies.
// If lockConfig is nil, default lock configuration will be used.
func NewService(
	tmsID token.TMSID,
	networkProvider NetworkProvider,
	auditDB *auditdb.StoreService,
	tokenDB *tokens.Service,
	tmsProvider dep.TokenManagementServiceProvider,
	finalityTracer trace.Tracer,
	metricsProvider metrics.Provider,
	checkService CheckService,
	lockConfig *LockConfig,
) *Service {
	if lockConfig == nil {
		lockConfig = DefaultLockConfig()
	}

	return &Service{
		tmsID:           tmsID,
		networkProvider: networkProvider,
		auditDB:         auditDB,
		tokenDB:         tokenDB,
		tmsProvider:     tmsProvider,
		finalityTracer:  finalityTracer,
		metricsProvider: metricsProvider,
		metrics:         newMetrics(metricsProvider),
		checkService:    checkService,
		lockConfig:      lockConfig,
	}
}

// Validate validates the passed token request
func (a *Service) Validate(ctx context.Context, request *token.Request) error {
	return request.AuditCheck(ctx)
}

// Audit extracts the list of inputs and outputs from the passed transaction.
// In addition, the Audit locks the enrollment named ids with retry logic and exponential backoff
// to prevent livelock conditions.
// A snapshot of the computed audit record is cached so that Append can reuse
// it; the returned streams stay independent of the cached snapshot.
// The caller MUST call Release() to unlock these enrollment IDs after processing.
//
// IMPORTANT: The defer Release() statement MUST be placed immediately after checking
// the error returned by Audit(). This ensures locks are released even if subsequent
// operations fail. Example:
//
//	inputs, outputs, err := auditor.Audit(ctx, tx)
//	if err != nil {
//	    return errors.Wrap(err, "audit failed")
//	}
//	defer auditor.Release(ctx, tx)
//
// Note: The semaphore-based locking mechanism handles context cancellation during
// lock acquisition (see PR #1616), ensuring proper cleanup in case of timeouts or
// cancellations.
func (a *Service) Audit(ctx context.Context, tx Transaction) (*token.InputStream, *token.OutputStream, error) {
	start := time.Now()
	logger.DebugfContext(ctx, "audit transaction [%s]....", tx.ID())
	request := tx.Request()
	tms, err := a.bindProviderTMS(request)
	if err != nil {
		return nil, nil, err
	}
	// the record is completed before the enrollment IDs are collected, so that
	// the locks cover the enrollment ID every input is finally booked under
	record, err := newRequestWrapper(request, tms).AuditRecord(ctx)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "failed getting transaction audit record")
	}

	var eids []string
	eids = append(eids, record.Inputs.EnrollmentIDs()...)
	eids = append(eids, record.Outputs.EnrollmentIDs()...)

	// Acquire locks with retry and exponential backoff to prevent livelock
	logger.DebugfContext(ctx, "audit transaction [%s], acquire locks with retry", tx.ID())
	if err := a.acquireLocksWithRetry(ctx, string(request.Anchor), eids); err != nil {
		// Only a genuine conflict counts towards the conflict metric. Counting every
		// failure meant a graceful-shutdown cancellation or a database outage — neither
		// of which involves a second holder — inflated the one signal operators are
		// told to alert on for contention.
		if errors.Is(err, auditdb.ErrLockContention) {
			a.metrics.AuditLockConflicts.Add(1)
		}

		return nil, nil, err
	}

	logger.DebugfContext(ctx, "audit transaction [%s], acquire locks done", tx.ID())
	a.stashAuditRecord(request, snapshotAuditRecord(request, record))
	a.metrics.AuditDuration.Observe(time.Since(start).Seconds())

	return record.Inputs, record.Outputs, nil
}

// acquireLocksWithRetry attempts to acquire locks with exponential backoff and randomized jitter
// to prevent livelock conditions when multiple auditors compete for the same enrollment IDs.
// This implements the mitigation strategy for deadlock/livelock prevention.
//
// The locker owns the waiting policy and bounds it itself, so this loop must not
// re-run an attempt that already spent that budget: an error carrying
// ErrLockAcquireTimeout is final here. Retrying it anyway multiplied the locker's
// deadline by MaxRetries — worst case, ten minutes of blocking for a single audit
// against the Postgres backend, on top of the round trips each of those attempts
// spent polling. Context errors are final for the same reason: the caller is gone.
func (a *Service) acquireLocksWithRetry(ctx context.Context, anchor string, eids []string) error {
	// Create a retry runner with jitter support
	retryRunner := utils.NewRetryRunnerWithJitter(
		logger,
		a.lockConfig.MaxRetries,
		a.lockConfig.InitialBackoff,
		a.lockConfig.MaxBackoff,
		a.lockConfig.BackoffMultiplier,
		a.lockConfig.JitterFactor,
	)

	// Use the retry runner to acquire locks, stopping early on errors that another
	// attempt cannot improve on.
	err := retryRunner.RunWithErrorsContext(ctx, func() (bool, error) {
		err := a.auditDB.AcquireLocks(ctx, anchor, eids...)
		if err == nil {
			return true, nil
		}

		return !isRetriableLockError(ctx, err), err
	})
	if err != nil {
		return errors.WithMessagef(err, "failed to acquire locks for anchor [%s]", anchor)
	}

	return nil
}

// isRetriableLockError reports whether re-running AcquireLocks stands a chance of
// a different outcome. Most failures do — a contended lock may be free by now, a
// database blip may have passed — but three do not: ErrLockAcquireTimeout means
// the locker already spent its whole waiting budget, so an identical attempt would
// just spend it again; ErrLockSetWidened is a caller error that every attempt will
// reproduce; and a caller whose own context is done is no longer waiting for an
// answer.
//
// Whether the caller is gone is read from ctx, not from the error. The lockers
// bound their own waiting, and a budget of their own that elapses with nothing
// contending surfaces as a bare context.DeadlineExceeded — indistinguishable, by
// the error alone, from the caller's deadline elapsing. Classifying it from the
// error stopped the auditor after a single attempt at exactly the transient
// database failures this retry exists to survive, while ctx was still perfectly
// live.
func isRetriableLockError(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}

	return !errors.Is(err, auditdb.ErrLockAcquireTimeout) &&
		!errors.Is(err, auditdb.ErrLockSetWidened)
}

// Append adds the passed transaction to the auditor database, reusing the
// audit record computed by Audit, when available.
// It also releases the locks acquired by Audit.
func (a *Service) Append(ctx context.Context, tx Transaction) error {
	start := time.Now()
	defer func() { a.metrics.AppendDuration.Observe(time.Since(start).Seconds()) }()
	defer a.Release(ctx, tx)

	tms, err := a.bindProviderTMS(tx.Request())
	if err != nil {
		return err
	}
	// append request to audit db
	wrapper := newRequestWrapper(tx.Request(), tms)
	wrapper.cached = a.cachedAuditRecord(tx.Request())
	if err := a.auditDB.Append(ctx, wrapper); err != nil {
		a.metrics.AppendErrors.Add(1)

		return errors.WithMessagef(err, "failed appending request %s", tx.ID())
	}

	// lister to events
	net, err := a.networkProvider.GetNetwork(tx.Network(), tx.Channel())
	if err != nil {
		return errors.WithMessagef(err, "failed getting network instance for [%s:%s]", tx.Network(), tx.Channel())
	}
	logger.DebugfContext(ctx, "register tx status listener for tx [%s] at network [%s]", tx.ID(), tx.Network())
	var r driver.FinalityListener = finality.NewListener(
		logger,
		net,
		tx.Namespace(),
		finality.NewTokenRequestHasher(a.tmsProvider, a.tmsID),
		a.auditDB,
		a.tokenDB,
		a.finalityTracer,
		a.metricsProvider,
	)
	if err := net.AddFinalityListener(tx.Namespace(), tx.ID(), r); err != nil {
		return errors.WithMessagef(err, "failed listening to network [%s:%s]", tx.Network(), tx.Channel())
	}
	logger.DebugfContext(ctx, "append done for request [%s]", tx.ID())

	return nil
}

// bindProviderTMS resolves the TMS for the service's TMS ID through the
// provider and rebinds the request to it. The record computation runs through
// request.TokenService, so this is what keeps the request from influencing
// which TMS computes and attributes the record.
func (a *Service) bindProviderTMS(request *token.Request) (dep.TokenManagementServiceWithExtensions, error) {
	tms, err := a.tmsProvider.TokenManagementService(token.WithTMSID(a.tmsID))
	if err != nil {
		return nil, err
	}
	if err := tms.SetTokenManagementService(request); err != nil {
		return nil, err
	}

	return tms, nil
}

// Release releases the lock acquired of the passed transaction and drops the
// audit record cached for it.
func (a *Service) Release(ctx context.Context, tx Transaction) {
	a.metrics.ReleasesTotal.Add(1)
	anchor := tx.Request().Anchor
	a.dropAuditRecord(anchor)
	a.auditDB.ReleaseLocks(ctx, string(anchor))
}

// snapshotAuditRecord deep-copies record so that the cached copy and the
// streams returned by Audit share no mutable memory.
func snapshotAuditRecord(request *token.Request, record *token.AuditRecord) *token.AuditRecord {
	inputs := record.Inputs.Inputs()
	inputsCopy := make([]*token.Input, len(inputs))
	for i, in := range inputs {
		cp := *in
		if in.Id != nil {
			id := *in.Id
			cp.Id = &id
		}
		cp.Owner = slices.Clone(in.Owner)
		cp.OwnerAuditInfo = slices.Clone(in.OwnerAuditInfo)
		if in.Quantity != nil {
			cp.Quantity = in.Quantity.Clone()
		}
		inputsCopy[i] = &cp
	}
	outputs := record.Outputs.Outputs()
	outputsCopy := make([]*token.Output, len(outputs))
	for i, out := range outputs {
		cp := *out
		cp.Token.Owner = slices.Clone(out.Token.Owner)
		cp.Owner = slices.Clone(out.Owner)
		cp.OwnerAuditInfo = slices.Clone(out.OwnerAuditInfo)
		if out.Quantity != nil {
			cp.Quantity = out.Quantity.Clone()
		}
		cp.LedgerOutput = slices.Clone(out.LedgerOutput)
		cp.LedgerOutputMetadata = slices.Clone(out.LedgerOutputMetadata)
		cp.Issuer = slices.Clone(out.Issuer)
		outputsCopy[i] = &cp
	}
	var attributes map[string][]byte
	if record.Attributes != nil {
		attributes = make(map[string][]byte, len(record.Attributes))
		for k, v := range record.Attributes {
			attributes[k] = slices.Clone(v)
		}
	}
	precision := record.Outputs.Precision

	return &token.AuditRecord{
		Anchor:     record.Anchor,
		Inputs:     token.NewInputStream(request.TokenService.Vault().NewQueryEngine(), inputsCopy, precision),
		Outputs:    token.NewOutputStream(outputsCopy, precision),
		Attributes: attributes,
	}
}

func (a *Service) stashAuditRecord(request *token.Request, record *token.AuditRecord) {
	a.recordsMu.Lock()
	defer a.recordsMu.Unlock()
	if a.auditRecords == nil {
		a.auditRecords = map[token.RequestAnchor]auditRecordEntry{}
	}
	a.auditRecords[request.Anchor] = auditRecordEntry{request: request, record: record}
}

// cachedAuditRecord returns the audit record cached for the passed request,
// or nil if none is cached or the cached one was computed from a different
// request with the same anchor.
func (a *Service) cachedAuditRecord(request *token.Request) *token.AuditRecord {
	a.recordsMu.Lock()
	defer a.recordsMu.Unlock()
	entry, ok := a.auditRecords[request.Anchor]
	if !ok || entry.request != request {
		return nil
	}

	return entry.record
}

func (a *Service) dropAuditRecord(anchor token.RequestAnchor) {
	a.recordsMu.Lock()
	defer a.recordsMu.Unlock()
	delete(a.auditRecords, anchor)
}

// SetStatus sets the status of the audit records with the passed transaction id to the passed status
func (a *Service) SetStatus(ctx context.Context, txID string, status storage.TxStatus, message string) error {
	return a.auditDB.SetStatus(ctx, txID, status, message)
}

// GetStatus return the status of the given transaction id.
// It returns an error if no transaction with that id is found
func (a *Service) GetStatus(ctx context.Context, txID string) (TxStatus, string, error) {
	return a.auditDB.GetStatus(ctx, txID)
}

// GetTokenRequest returns the token request bound to the passed transaction id, if available.
func (a *Service) GetTokenRequest(ctx context.Context, txID string) ([]byte, error) {
	return a.auditDB.GetTokenRequest(ctx, txID)
}

// Check performs a health check on the auditor service and returns any issues found.
func (a *Service) Check(ctx context.Context) ([]string, error) {
	return a.checkService.Check(ctx)
}

type requestWrapper struct {
	r   *token.Request
	tms dep.TokenManagementService
	// cached, when set, is a snapshot of the audit record computed by Audit
	// for this request; AuditRecord reuses it instead of recomputing it.
	cached *token.AuditRecord
}

// newRequestWrapper creates a new requestWrapper that wraps a token request with its associated
// token management service for enhanced audit record processing.
func newRequestWrapper(r *token.Request, tms dep.TokenManagementService) *requestWrapper {
	return &requestWrapper{r: r, tms: tms}
}

// ID returns the unique identifier (anchor) of the wrapped token request.
func (r *requestWrapper) ID() token.RequestAnchor {
	return r.r.ID()
}

// Bytes returns the serialized byte representation of the wrapped token request.
func (r *requestWrapper) Bytes() ([]byte, error) { return r.r.Bytes() }

// AllApplicationMetadata returns all application-specific metadata associated with the token request.
func (r *requestWrapper) AllApplicationMetadata() map[string][]byte {
	return r.r.AllApplicationMetadata()
}

// PublicParamsHash returns the hash of the public parameters used in the token request.
func (r *requestWrapper) PublicParamsHash() token.PPHash { return r.r.PublicParamsHash() }

// AuditRecord retrieves the audit record for the wrapped token request and completes any
// inputs with missing enrollment IDs by querying the token vault.
// A record cached by Audit is returned as it stands: it was already attributed
// there, and the locks were taken for the enrollment IDs it carries.
func (r *requestWrapper) AuditRecord(ctx context.Context) (*token.AuditRecord, error) {
	// re-running the gap filling on a cached record could attribute an input
	// Audit deliberately left empty, booking it under an enrollment ID that
	// was never locked
	if r.cached != nil {
		return r.cached, nil
	}

	record, err := r.r.AuditRecord(ctx)
	if err != nil {
		return nil, err
	}
	if err := r.completeInputsWithEmptyEID(ctx, record); err != nil {
		return nil, errors.WithMessagef(err, "failed filling gaps for request [%s]", r.r.Anchor)
	}
	if err := rejectMultiOwnerActions(record); err != nil {
		return nil, err
	}

	return record, nil
}

// rejectMultiOwnerActions fails when one action spends tokens attributed to
// more than one enrollment ID. A transaction record keeps a single sender per
// action, so the store would reject such a record only at Append time, with
// an error that does not name the cause. It mirrors the store's grouping:
// unattributed inputs are skipped.
func rejectMultiOwnerActions(record *token.AuditRecord) error {
	firstEID := map[int]string{}
	for _, in := range record.Inputs.Inputs() {
		if in.EnrollmentID == "" {
			continue
		}
		eID, ok := firstEID[in.ActionIndex]
		if !ok {
			firstEID[in.ActionIndex] = in.EnrollmentID

			continue
		}
		if eID != in.EnrollmentID {
			return errors.Errorf("action [%d] of request [%s] spends tokens of multiple enrollment IDs ([%s] and [%s]): a transaction record keeps a single sender per action", in.ActionIndex, record.Anchor, eID, in.EnrollmentID)
		}
	}

	return nil
}

// completeInputsWithEmptyEID fills in missing enrollment ID information for inputs in the audit record
// by querying the token vault. This is necessary when inputs don't have enrollment IDs explicitly set.
// Each input is attributed to the enrollment ID resolved from its own token
// owner and the audit info the input carries — the locally stored audit info
// of the owner where present, the one carried by the request otherwise (see
// Request.AuditRecord). An owner the identity layer cannot decode counts as
// resolving to nothing; any other resolution failure — a storage error, a
// canceled context — fails the audit. An input the request describes no sender for is an
// upgrade input, and falls back to the enrollment ID the request issues to;
// an upgrade issued to a composite owner spanning enrollment IDs fails the
// audit (see issuedToEIDAndRH). An owner that maps to no single enrollment ID
// leaves its input unattributed rather than booked under a guessed enrollment
// ID, so a record keeping such an input is not fully attributed on return.
func (r *requestWrapper) completeInputsWithEmptyEID(ctx context.Context, record *token.AuditRecord) error {
	filter := record.Inputs.ByEnrollmentID("")
	if filter.Count() == 0 {
		return nil
	}

	// fetch all the tokens
	tokens, err := r.tms.Vault().NewQueryEngine().ListAuditTokens(ctx, filter.IDs()...)
	if err != nil {
		return errors.WithMessagef(err, "failed listing tokens for [%s]", filter.IDs())
	}
	if filter.Count() != len(tokens) {
		return errors.Errorf("expected %d audit tokens, got %d", filter.Count(), len(tokens))
	}
	precision := r.tms.PublicParametersManager().PublicParameters().Precision()
	wm := r.tms.WalletManager()
	for i := range filter.Count() {
		item := filter.At(i)
		if tokens[i] == nil {
			return errors.Errorf("failed to audit inputs: nil input at [%d]th input", i)
		}
		// an input the request describes no sender for: extractIssueInputs fills
		// only the token id, so across the built-in drivers this is an upgrade
		upgraded := len(item.Owner) == 0

		item.Owner = tokens[i].Owner
		item.Type = tokens[i].Type
		q, err := token2.ToQuantity(tokens[i].Quantity, precision)
		if err != nil {
			return errors.WithMessagef(err, "failed converting token quantity [%s]", tokens[i].Quantity)
		}
		item.Quantity = q

		eID, rID, err := wm.GetEIDAndRH(ctx, item.Owner, item.OwnerAuditInfo)
		if err != nil {
			// only a decoding failure counts as "this owner does not resolve";
			// anything else — a storage failure, a canceled context — fails the
			// audit rather than silently leaving the input unattributed
			if ctx.Err() != nil || !errors.Is(err, identity.ErrUnresolvableIdentity) {
				return errors.WithMessagef(err, "failed resolving enrollment id for input [%v]", item.Id)
			}
			logger.DebugfContext(ctx, "owner of input [%v] does not resolve, treating it as unresolved: %v", item.Id, err)
			eID, rID = "", ""
		}
		if eID == "" && upgraded {
			// an upgrade re-issues the spent tokens to their owner under a fresh
			// identity, so the outputs of the very same action carry the
			// enrollment ID the input belongs to. The pre-upgrade identity
			// itself often resolves to nothing here: it predates the current
			// driver and the request metadata carries no audit info for it.
			eID, rID, err = issuedToEIDAndRH(record.Outputs, item.ActionIndex)
			if err != nil {
				return err
			}
		}
		if eID == "" {
			// the owner maps to no single enrollment ID — a composite owner,
			// or one whose audit info is unavailable. Leave the input
			// unattributed: amount aggregations skip an empty enrollment ID,
			// whereas a guessed one would be charged to the wrong party.
			continue
		}
		item.EnrollmentID = eID
		item.RevocationHandler = rID
	}

	return nil
}

// issuedToEIDAndRH returns the enrollment ID and revocation handle the given issue
// action issues to, and empty values when it does not issue to exactly one party.
// An issued output carries both an issuer and an owner; a redeem output carries an
// issuer but no owner. Every issued output of the action must resolve to the same
// enrollment ID. The handle is kept only while it stays paired with that ID.
//
// A composite owner issues one output row per member, all under one output index.
// Members resolving to distinct enrollment IDs leave no single enrollment ID to
// book the input under, and an unattributed input would credit the members
// without debiting anyone: such an action fails the audit instead. Outputs that
// only partly resolve fail the audit for the same reason; when none resolve,
// nothing is credited and the input may stay unattributed.
func issuedToEIDAndRH(outputs *token.OutputStream, actionIndex int) (string, string, error) {
	issued := outputs.Filter(func(o *token.Output) bool {
		return o.ActionIndex == actionIndex && len(o.Issuer) != 0 && len(o.Owner) != 0
	}).Outputs()
	if len(issued) == 0 {
		return "", "", nil
	}

	unresolved := 0
	eIDByIndex := map[uint64]string{}
	for _, output := range issued {
		if output.EnrollmentID == "" {
			unresolved++

			continue
		}
		if eID, ok := eIDByIndex[output.Index]; ok && eID != output.EnrollmentID {
			return "", "", errors.Errorf(
				"output [%d] of action [%d] is issued to a composite owner whose members span enrollment IDs ([%s] and [%s]): no single enrollment ID to attribute its input to",
				output.Index, actionIndex, eID, output.EnrollmentID,
			)
		}
		eIDByIndex[output.Index] = output.EnrollmentID
	}
	if unresolved == len(issued) {
		// nothing is credited to anybody, so nothing needs debiting
		return "", "", nil
	}
	if unresolved > 0 {
		return "", "", errors.Errorf(
			"action [%d] issues [%d] of [%d] outputs to owners resolving to no enrollment ID: crediting the resolved ones would leave the input undebited",
			actionIndex, unresolved, len(issued),
		)
	}

	eID, rH := issued[0].EnrollmentID, issued[0].RevocationHandler
	for _, output := range issued[1:] {
		if output.EnrollmentID != eID {
			return "", "", nil
		}
		if output.RevocationHandler != rH {
			rH = ""
		}
	}

	return eID, rH, nil
}

// String returns a string representation of the wrapped token request.
func (r *requestWrapper) String() string {
	return r.r.String()
}
