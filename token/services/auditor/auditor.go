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
	record, err := request.AuditRecord(ctx)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "failed getting transaction audit record")
	}

	var eids []string
	eids = append(eids, record.Inputs.EnrollmentIDs()...)
	eids = append(eids, record.Outputs.EnrollmentIDs()...)

	// Acquire locks with retry and exponential backoff to prevent livelock
	logger.DebugfContext(ctx, "audit transaction [%s], acquire locks with retry", tx.ID())
	if err := a.acquireLocksWithRetry(ctx, string(request.Anchor), eids); err != nil {
		a.metrics.AuditLockConflicts.Add(1)

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

	// Use the retry runner to acquire locks
	err := retryRunner.RunWithContext(ctx, func() error {
		return a.auditDB.AcquireLocks(ctx, anchor, eids...)
	})

	if err != nil {
		return errors.WithMessagef(err, "failed to acquire locks for anchor [%s]", anchor)
	}

	return nil
}

// Append adds the passed transaction to the auditor database, reusing the
// audit record computed by Audit, when available.
// It also releases the locks acquired by Audit.
func (a *Service) Append(ctx context.Context, tx Transaction) error {
	start := time.Now()
	defer func() { a.metrics.AppendDuration.Observe(time.Since(start).Seconds()) }()
	defer a.Release(ctx, tx)

	tms, err := a.tmsProvider.TokenManagementService(token.WithTMSID(a.tmsID))
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
// The gap filling always runs, also on a cached record, because it depends on
// the current vault state.
func (r *requestWrapper) AuditRecord(ctx context.Context) (*token.AuditRecord, error) {
	record := r.cached
	if record == nil {
		var err error
		record, err = r.r.AuditRecord(ctx)
		if err != nil {
			return nil, err
		}
	}
	if err := r.completeInputsWithEmptyEID(ctx, record); err != nil {
		return nil, errors.WithMessagef(err, "failed filling gaps for request [%s]", r.r.Anchor)
	}

	return record, nil
}

// completeInputsWithEmptyEID fills in missing enrollment ID information for inputs in the audit record
// by querying the token vault. This is necessary when inputs don't have enrollment IDs explicitly set.
// It uses the first output's enrollment ID as the target and retrieves token details from the vault.
func (r *requestWrapper) completeInputsWithEmptyEID(ctx context.Context, record *token.AuditRecord) error {
	filter := record.Inputs.ByEnrollmentID("")
	if filter.Count() == 0 {
		return nil
	}
	// TODO: extract from the audit tokens
	targetEID := record.Outputs.EnrollmentIDs()[0]

	// fetch all the tokens
	tokens, err := r.tms.Vault().NewQueryEngine().ListAuditTokens(ctx, filter.IDs()...)
	if err != nil {
		return errors.WithMessagef(err, "failed listing tokens for [%s]", filter.IDs())
	}
	precision := r.tms.PublicParametersManager().PublicParameters().Precision()
	for i := range filter.Count() {
		item := filter.At(i)
		item.EnrollmentID = targetEID
		item.Owner = tokens[i].Owner
		item.Type = tokens[i].Type
		q, err := token2.ToQuantity(tokens[i].Quantity, precision)
		if err != nil {
			return errors.WithMessagef(err, "failed converting token quantity [%s]", tokens[i].Quantity)
		}
		item.Quantity = q
	}

	return nil
}

// String returns a string representation of the wrapped token request.
func (r *requestWrapper) String() string {
	return r.r.String()
}
