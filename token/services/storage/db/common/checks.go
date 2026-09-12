/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/pagination"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
)

var (
	logger = logging.MustGetLogger()
)

// transactionsCheckPageSize is the fallback page size used to iterate all
// transactions during integrity checks when the storage layer's max page size
// is disabled (0). When a cap is configured, that cap is used as the page size
// instead (see DefaultCheckers.maxPageSize), so the checks always stay within
// the bound the guard layer enforces.
const transactionsCheckPageSize = 100

// Names of the default checkers. A finding carries the name of the checker that
// produced it, and the name is part of the finding's key, so these are stable.
const (
	// CheckerTransactions is the name of the check that compares local transaction
	// status against the ledger.
	CheckerTransactions = "Transaction Check"
	// CheckerUnspentTokens is the name of the check that compares the content of
	// locally held unspent tokens against the ledger.
	CheckerUnspentTokens = "Unspent Tokens Check" //nolint:gosec // a display name, not a credential
	// CheckerTokenSpendability is the name of the check that verifies that locally
	// held unspent tokens can still be spent by the current TMS.
	CheckerTokenSpendability = "Token Spendability Check"
	// CheckerLocalCompleteness is the name of the check that looks for tokens the
	// ledger produced for this node that never landed in the local store.
	CheckerLocalCompleteness = "Local Completeness Check"
)

// DefaultBatchSize is how many tokens a checker resolves against the ledger in a
// single round trip. It matches the batch size the token pruning sweep uses.
const DefaultBatchSize = 50

type TokenTransactionDB interface {
	GetTokenRequest(ctx context.Context, txID string) ([]byte, error)
	Transactions(ctx context.Context, params driver.QueryTransactionsParams, pagination driver2.Pagination) (*driver2.PageIterator[*driver.TransactionRecord], error)
}

//go:generate counterfeiter -o mock/token_management_service_provider.go --fake-name TokenManagementServiceProvider . TokenManagementServiceProvider
type TokenManagementServiceProvider interface {
	GetManagementService(opts ...token.ServiceOption) (*token.ManagementService, error)
}

//go:generate counterfeiter -o mock/network_provider.go --fake-name NetworkProvider . NetworkProvider
type NetworkProvider interface {
	GetNetwork(network string, channel string) (*network.Network, error)
}

// Checker reports divergences between the local databases and the ledger as
// plain messages.
//
// Deprecated: implement FindingChecker instead. A Checker's messages cannot be
// aged across sweeps, because there is nothing stable to key them on. This type
// is kept because it is the dependency injection contract third parties register
// custom checks with.
type Checker = func(ctx context.Context) ([]string, error)

// NamedChecker is a Checker together with the name it reports under.
type NamedChecker struct {
	// Name identifies the check in reports and logs.
	Name string
	// Checker is the check itself.
	Checker Checker
}

// FindingChecker reports divergences between the local databases and the ledger
// as structured findings.
type FindingChecker = func(ctx context.Context) ([]Finding, error)

// NamedFindingChecker is a FindingChecker together with the name it reports under.
type NamedFindingChecker struct {
	// Name identifies the check in reports and logs, and is part of the key of
	// every finding it produces.
	Name string
	// Checker is the check itself.
	Checker FindingChecker
	// Complete reports whether one run of this check looks at everything the check
	// could ever report on.
	//
	// It is what decides whether a stored finding of this check may be closed
	// because the latest run did not report it again. A check that only looks at
	// part of the history says nothing about the part it skipped, so closing its
	// older findings would quietly discard real problems. The zero value is the
	// safe answer, which is why a check lifted from the plain message contract
	// never has its findings closed automatically.
	Complete bool
}

// AsNamedChecker downgrades a structured check to the plain message contract, so
// that it can be served through the legacy Checker API.
//
// SeverityInfo findings are dropped rather than downgraded. The plain message
// contract has no way to carry severity, so every message it returns reads as a
// problem to callers of the legacy on-demand Check API - both production callers
// and the message-count assertions integration tests make. Info is documented as
// "expected to resolve on its own" (a transaction the ledger has not caught up
// with yet, for example), which is exactly the case a node restart or a slow
// index produces routinely; surfacing it as an "error" here would turn a benign,
// self-resolving condition into a false alarm every legacy caller has to learn to
// ignore. The structured findings table this feeds in parallel keeps Info
// findings, since severity is representable there and losing them would hide
// real degraded-observability signal from anyone looking at it directly.
func AsNamedChecker(checker NamedFindingChecker) NamedChecker {
	return NamedChecker{
		Name: checker.Name,
		Checker: func(ctx context.Context) ([]string, error) {
			findings, err := checker.Checker(ctx)
			if err != nil {
				return nil, err
			}
			var messages []string
			for _, finding := range findings {
				if finding.Severity == SeverityInfo {
					continue
				}
				messages = append(messages, finding.String())
			}

			return messages, nil
		},
	}
}

// AsNamedCheckers downgrades a list of structured checks. See AsNamedChecker.
func AsNamedCheckers(checkers []NamedFindingChecker) []NamedChecker {
	res := make([]NamedChecker, len(checkers))
	for i, checker := range checkers {
		res[i] = AsNamedChecker(checker)
	}

	return res
}

// AsNamedFindingChecker lifts a plain message check into the structured contract.
// The findings it produces are unclassified, see FindingFromMessage for what that
// costs.
func AsNamedFindingChecker(checker NamedChecker) NamedFindingChecker {
	return NamedFindingChecker{
		Name: checker.Name,
		Checker: func(ctx context.Context) ([]Finding, error) {
			messages, err := checker.Checker(ctx)
			if err != nil {
				return nil, err
			}
			findings := make([]Finding, len(messages))
			for i, message := range messages {
				findings[i] = FindingFromMessage(checker.Name, message)
			}

			return findings, nil
		},
	}
}

// AsNamedFindingCheckers lifts a list of plain message checks. See AsNamedFindingChecker.
func AsNamedFindingCheckers(checkers []NamedChecker) []NamedFindingChecker {
	res := make([]NamedFindingChecker, len(checkers))
	for i, checker := range checkers {
		res[i] = AsNamedFindingChecker(checker)
	}

	return res
}

// ChecksService runs a list of checks and collects their messages.
type ChecksService struct {
	checkers []NamedChecker
}

// NewChecksService returns a service that runs the passed checks in order.
func NewChecksService(checkers []NamedChecker) *ChecksService {
	return &ChecksService{checkers: checkers}
}

// Check runs every checker and returns the concatenation of their messages.
// It stops at the first checker that fails, so a partial result is never
// mistaken for a clean run.
func (a *ChecksService) Check(ctx context.Context) ([]string, error) {
	var errorMessages []string
	for _, checker := range a.checkers {
		errs, err := checker.Checker(ctx)
		if err != nil {
			return nil, errors.Wrapf(err, "failed checking with checker [%s]", checker.Name)
		}
		errorMessages = append(errorMessages, errs...)
	}

	return errorMessages, nil
}

// FindingsService runs a list of checks and collects their findings.
type FindingsService struct {
	checkers []NamedFindingChecker
}

// NewFindingsService returns a service that runs the passed checks in order.
func NewFindingsService(checkers []NamedFindingChecker) *FindingsService {
	return &FindingsService{checkers: checkers}
}

// Names returns the names of the checks this service runs, in order.
func (a *FindingsService) Names() []string {
	names := make([]string, len(a.checkers))
	for i, checker := range a.checkers {
		names[i] = checker.Name
	}

	return names
}

// ResolvableCheckers returns the names of the checks whose stored findings may be
// closed when a sweep stops reporting them. See NamedFindingChecker.Complete.
func (a *FindingsService) ResolvableCheckers() []string {
	var names []string
	for _, checker := range a.checkers {
		if checker.Complete {
			names = append(names, checker.Name)
		}
	}

	return names
}

// Check runs every checker and returns the concatenation of their findings.
//
// Unlike ChecksService.Check it does not abort when a checker fails. A failing
// checker is turned into a CodeCheckFailed finding and the sweep continues, so
// one unreachable ledger does not hide what the other checks did manage to
// establish. The returned error is always nil; it is part of the signature so
// that the caller does not have to special case this service.
func (a *FindingsService) Check(ctx context.Context) ([]Finding, error) {
	var findings []Finding
	for _, checker := range a.checkers {
		res, err := checker.Checker(ctx)
		if err != nil {
			logger.ErrorfContext(ctx, "checker [%s] failed: %s", checker.Name, err)
			findings = append(findings, Finding{
				Checker:  checker.Name,
				Code:     CodeCheckFailed,
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("check [%s] could not complete: [%s]", checker.Name, err),
			})

			continue
		}
		findings = append(findings, res...)
	}

	return findings, nil
}

// CheckerOption customises how the default checkers query the ledger.
type CheckerOption func(*DefaultCheckers)

// WithBatchSize sets how many tokens are resolved against the ledger per round
// trip. Values below one are ignored.
func WithBatchSize(size int) CheckerOption {
	return func(c *DefaultCheckers) {
		if size > 0 {
			c.batchSize = size
		}
	}
}

// WithTransactionWindow restricts the checks that walk the transaction history to
// transactions stored within the passed duration of now. A zero or negative
// duration means the whole history, which is the default.
func WithTransactionWindow(window time.Duration) CheckerOption {
	return func(c *DefaultCheckers) {
		c.window = window
	}
}

// WithMaxPageSize sets the page size used to iterate transactions, matching the
// storage layer's configured max read page size so the checks read within the
// bound the guard layer enforces. A value <= 0 leaves the built-in fallback
// (transactionsCheckPageSize) in place.
func WithMaxPageSize(maxPageSize int) CheckerOption {
	return func(c *DefaultCheckers) {
		if maxPageSize > 0 {
			c.maxPageSize = maxPageSize
		}
	}
}

// DefaultCheckers holds the checks that ship with the SDK.
type DefaultCheckers struct {
	tmsProvider     TokenManagementServiceProvider
	networkProvider NetworkProvider
	db              TokenTransactionDB
	tokenDB         *tokens.Service
	tmsID           token.TMSID
	// maxPageSize is the storage layer's configured max read page size. It is used
	// as the transaction-iteration page size so integrity checks read within the
	// bound the guard layer enforces. A value <= 0 means the cap is disabled, in
	// which case the built-in fallback (transactionsCheckPageSize) is used.
	maxPageSize int
	batchSize   int
	window      time.Duration
}

// NewDefaultFindingCheckers returns the checks that are cheap enough to run while
// a caller waits: they walk what the node already holds and resolve it against
// the ledger.
func NewDefaultFindingCheckers(tmsProvider TokenManagementServiceProvider, networkProvider NetworkProvider, db TokenTransactionDB, tokenDB *tokens.Service, tmsID token.TMSID, opts ...CheckerOption) []NamedFindingChecker {
	checkers := newDefaultCheckers(tmsProvider, networkProvider, db, tokenDB, tmsID, opts...)

	return []NamedFindingChecker{
		// the token checks walk every token the node currently holds, so they always
		// see everything they could report on. The transaction check walks history,
		// which WithTransactionWindow can cut short.
		{Name: CheckerTransactions, Checker: checkers.CheckTransactions, Complete: checkers.window <= 0},
		{Name: CheckerUnspentTokens, Checker: checkers.CheckUnspentTokens, Complete: true},
		{Name: CheckerTokenSpendability, Checker: checkers.CheckTokenSpendability, Complete: true},
	}
}

// NewSweepFindingCheckers returns every check, including the ones too expensive to
// run in a request path. This is what the background checks service runs.
//
// On top of the default checks it adds CheckLocalCompleteness, which rebuilds
// every confirmed transaction to find tokens that should have landed locally and
// did not. That is the direction the other checks cannot see, and the one that
// costs the owner money.
func NewSweepFindingCheckers(tmsProvider TokenManagementServiceProvider, networkProvider NetworkProvider, db TokenTransactionDB, tokenDB *tokens.Service, tmsID token.TMSID, opts ...CheckerOption) []NamedFindingChecker {
	checkers := newDefaultCheckers(tmsProvider, networkProvider, db, tokenDB, tmsID, opts...)

	return []NamedFindingChecker{
		{Name: CheckerTransactions, Checker: checkers.CheckTransactions, Complete: checkers.window <= 0},
		{Name: CheckerUnspentTokens, Checker: checkers.CheckUnspentTokens, Complete: true},
		{Name: CheckerTokenSpendability, Checker: checkers.CheckTokenSpendability, Complete: true},
		{Name: CheckerLocalCompleteness, Checker: checkers.CheckLocalCompleteness, Complete: checkers.window <= 0},
	}
}

func newDefaultCheckers(tmsProvider TokenManagementServiceProvider, networkProvider NetworkProvider, db TokenTransactionDB, tokenDB *tokens.Service, tmsID token.TMSID, opts ...CheckerOption) *DefaultCheckers {
	checkers := &DefaultCheckers{
		tmsProvider:     tmsProvider,
		networkProvider: networkProvider,
		db:              db,
		tokenDB:         tokenDB,
		tmsID:           tmsID,
		batchSize:       DefaultBatchSize,
	}
	for _, opt := range opts {
		opt(checkers)
	}

	return checkers
}

// CheckTransactions checks that for each transaction stored in the local database,
// the status of this transaction matches the status of the transaction on the ledger.
//
// A transaction is reported once even when the local database holds several
// records for it, which it does whenever a transaction moved more than one
// amount.
func (a *DefaultCheckers) CheckTransactions(ctx context.Context) ([]Finding, error) {
	var findings []Finding

	tms, err := a.tmsProvider.GetManagementService(token.WithTMSID(a.tmsID))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting tms [%s]", a.tmsID)
	}
	net, err := a.networkProvider.GetNetwork(tms.Network(), tms.Channel())
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get network [%s]", tms.ID())
	}
	l, err := net.Ledger()
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get ledger [%s]", tms.ID())
	}

	err = a.walkTransactions(ctx, tms.ID(), a.transactionQuery(), func(transactionRecord *driver.TransactionRecord) error {
		tokenRequest, err := a.db.GetTokenRequest(ctx, transactionRecord.TxID)
		if err != nil {
			return errors.WithMessagef(err, "failed getting token request [%s]", transactionRecord.TxID)
		}
		if len(tokenRequest) == 0 {
			// a transaction record without its request is a local inconsistency in its
			// own right. It used to abort the whole sweep, which meant everything
			// after it went unchecked.
			findings = append(findings, Finding{
				Checker:  CheckerTransactions,
				Code:     CodeTxRequestMissing,
				Severity: SeverityCritical,
				TxID:     transactionRecord.TxID,
				Message:  fmt.Sprintf("token request [%s] is nil", transactionRecord.TxID),
			})

			return nil
		}

		findings = append(findings, a.compareTransactionStatus(l, transactionRecord)...)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return findings, nil
}

// walkTransactions pages through the store's transactions matching params,
// calling fn once per transaction (the local database holds one record per
// movement, so a transaction that moved more than one amount is deduplicated
// here rather than reported several times).
//
// A single unlimited query is rejected by the storage layer, so this pages
// through the whole set using offset pagination and stops once a page comes
// back short. It pages at the configured max page size so reads stay within
// the cap the guard layer enforces; when the cap is disabled (0) it falls
// back to a built-in default.
func (a *DefaultCheckers) walkTransactions(ctx context.Context, tmsID token.TMSID, params driver.QueryTransactionsParams, fn func(*driver.TransactionRecord) error) error {
	pageSize := a.maxPageSize
	if pageSize <= 0 {
		pageSize = transactionsCheckPageSize
	}
	var page driver2.Pagination
	page, err := pagination.Offset(0, pageSize)
	if err != nil {
		return errors.WithMessagef(err, "failed to create pagination [%s]", tmsID)
	}

	seen := collections.NewSet[string]()
	for {
		count, err := func() (int, error) {
			it, err := a.db.Transactions(ctx, params, page)
			if err != nil {
				return 0, errors.WithMessagef(err, "failed querying transactions [%s]", tmsID)
			}
			defer it.Items.Close()
			count := 0
			for {
				transactionRecord, err := it.Items.Next()
				if err != nil {
					return 0, errors.WithMessagef(err, "failed querying transactions [%s]", tmsID)
				}
				if transactionRecord == nil {
					break
				}
				count++

				if seen.Contains(transactionRecord.TxID) {
					continue
				}
				seen.Add(transactionRecord.TxID)

				if err := fn(transactionRecord); err != nil {
					return 0, err
				}
			}

			return count, nil
		}()
		if err != nil {
			return err
		}
		if count < pageSize {
			return nil
		}
		if page, err = page.Next(); err != nil {
			return errors.WithMessagef(err, "failed advancing pagination [%s]", tmsID)
		}
	}
}

// compareTransactionStatus resolves one local transaction record against the ledger.
func (a *DefaultCheckers) compareTransactionStatus(l *network.Ledger, transactionRecord *driver.TransactionRecord) []Finding {
	lVC, _, err := l.Status(transactionRecord.TxID)
	if err != nil {
		// nothing is known about this transaction's real ledger status. Falling back
		// to network.Unknown and comparing it as if it were a real answer would turn
		// a connectivity failure into a false claim that the ledger disagrees with
		// the local record, so this reports only that the check was inconclusive.
		return []Finding{{
			Checker:  CheckerTransactions,
			Code:     CodeTxStatusUnavailable,
			Severity: SeverityInfo,
			TxID:     transactionRecord.TxID,
			Message:  fmt.Sprintf("failed to get ledger transaction status for [%s]: [%s]", transactionRecord.TxID, err),
		}}
	}

	mismatch := func(severity Severity, format string) []Finding {
		return []Finding{{
			Checker:  CheckerTransactions,
			Code:     CodeTxStatusMismatch,
			Severity: severity,
			TxID:     transactionRecord.TxID,
			Message:  fmt.Sprintf(format, transactionRecord.TxID, lVC),
		}}
	}

	switch {
	case transactionRecord.Status == driver.Confirmed && lVC != network.Valid:
		// the node credited tokens for something the ledger did not accept
		return mismatch(SeverityCritical, "transaction record [%s] is valid for vault but not for the ledger [%d]")
	case transactionRecord.Status == driver.Deleted && lVC != network.Invalid:
		if lVC == network.Unknown {
			// the ledger has never heard of a transaction the node already discarded,
			// which is what a rejected transaction looks like from here
			return nil
		}

		return mismatch(SeverityCritical, "transaction record [%s] is invalid for vault but not for the ledger [%d]")
	case transactionRecord.Status == driver.Unknown && lVC != network.Unknown:
		return mismatch(SeverityWarning, "transaction record [%s] is unknown for vault but not for the ledger [%d]")
	case transactionRecord.Status == driver.Pending && lVC == network.Busy:
		// this is fine, let's continue
		return nil
	case transactionRecord.Status == driver.Pending && lVC != network.Unknown:
		return mismatch(SeverityWarning, "transaction record [%s] is busy for vault but not for the ledger [%d]")
	}

	return nil
}

// CheckUnspentTokens checks that for each unspent token, the content of the local database matches the ledger.
//
// Tokens are resolved in batches: one ledger round trip covers a whole batch and
// the answers are compared position by position. A batch call fails as soon as
// one of its tokens is not on the ledger, indistinguishably from the call
// itself failing to reach the ledger, so a failed batch is resolved one token
// at a time: a token the ledger genuinely does not have is reported as such,
// while a per-token query failure fails the whole check instead of being
// mistaken for one.
func (a *DefaultCheckers) CheckUnspentTokens(ctx context.Context) ([]Finding, error) {
	var findings []Finding

	tms, err := a.tmsProvider.GetManagementService(token.WithTMSID(a.tmsID))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting tms [%s]", a.tmsID)
	}
	net, err := a.networkProvider.GetNetwork(tms.Network(), tms.Channel())
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get network [%s]", tms.ID())
	}
	qe := tms.Vault().NewQueryEngine()
	uit, err := qe.UnspentTokensIterator(ctx)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed querying utxo engine")
	}
	defer uit.Close()

	batch := make([]*token2.ID, 0, a.batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		res, err := a.checkUnspentBatch(ctx, tms, net, qe, batch)
		if err != nil {
			return err
		}
		findings = append(findings, res...)
		batch = batch[:0]

		return nil
	}

	for {
		tok, err := uit.Next()
		if err != nil {
			return nil, errors.WithMessagef(err, "failed querying next unspent token")
		}
		if tok == nil {
			break
		}
		id := tok.Id
		batch = append(batch, &id)
		if len(batch) >= a.batchSize {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}

	return findings, nil
}

// checkUnspentBatch compares one batch of locally held unspent tokens against the ledger.
func (a *DefaultCheckers) checkUnspentBatch(
	ctx context.Context,
	tms *token.ManagementService,
	net *network.Network,
	qe *token.QueryEngine,
	ids []*token2.ID,
) ([]Finding, error) {
	local := make(map[token2.ID][]byte, len(ids))
	if err := qe.GetTokenOutputs(ctx, ids, func(id *token2.ID, tokenRaw []byte) error {
		local[*id] = tokenRaw

		return nil
	}); err != nil {
		return nil, errors.WithMessagef(err, "failed reading local content of tokens [%v]", ids)
	}

	ledgerContent, err := net.QueryTokens(ctx, tms.Namespace(), ids)
	if err != nil {
		// a batch call fails as soon as one of its tokens is not on the ledger,
		// indistinguishably from the call itself failing to reach the ledger (the
		// Fabric translator folds both into the same generic error). Resolving the
		// whole batch as a single check_failed would lose every other token's
		// comparison over one absent or concurrently-spent token, so this falls
		// back to resolving the batch one token at a time instead.
		return a.checkUnspentOneByOne(ctx, tms, net, local, ids)
	}

	var findings []Finding
	for i, id := range ids {
		if i >= len(ledgerContent) || ledgerContent[i] == nil {
			// fabricx preserves the batch length and reports an absent token as a
			// nil entry at its position rather than an error or a shorter slice.
			// Comparing nil as content would misreport this as a content mismatch
			// instead of the missing token it actually is.
			findings = append(findings, Finding{
				Checker:  CheckerUnspentTokens,
				Code:     CodeTokenMissingOnLedger,
				Severity: SeverityCritical,
				TxID:     id.TxId,
				TokenID:  id,
				Message:  fmt.Sprintf("token [%s] is unspent locally but the ledger does not have it", id),
			})

			continue
		}
		if finding, ok := compareTokenContent(id, local[*id], ledgerContent[i]); ok {
			findings = append(findings, finding)
		}
	}

	return findings, nil
}

// checkUnspentOneByOne resolves each token of a failed batch on its own, so that a
// single missing token is reported as such instead of hiding the rest of the batch.
func (a *DefaultCheckers) checkUnspentOneByOne(
	ctx context.Context,
	tms *token.ManagementService,
	net *network.Network,
	local map[token2.ID][]byte,
	ids []*token2.ID,
) ([]Finding, error) {
	var findings []Finding
	for _, id := range ids {
		ledgerContent, err := net.QueryTokens(ctx, tms.Namespace(), []*token2.ID{id})
		if err != nil {
			// still nothing learned about this token: a query failure is not evidence
			// that the ledger does not have it, so this must not be reported as a
			// missing-token finding. See the identical reasoning in checkUnspentBatch.
			return nil, errors.WithMessagef(err, "failed querying ledger for token [%s]", id)
		}
		if len(ledgerContent) != 1 {
			findings = append(findings, Finding{
				Checker:  CheckerUnspentTokens,
				Code:     CodeTokenMissingOnLedger,
				Severity: SeverityCritical,
				TxID:     id.TxId,
				TokenID:  id,
				Message:  fmt.Sprintf("token [%s] is unspent locally but the ledger does not have it", id),
			})

			continue
		}
		if finding, ok := compareTokenContent(id, local[*id], ledgerContent[0]); ok {
			findings = append(findings, finding)
		}
	}

	return findings, nil
}

// compareTokenContent reports whether the local and ledger copies of a token differ.
func compareTokenContent(id *token2.ID, localRaw, ledgerRaw []byte) (Finding, bool) {
	if bytes.Equal(localRaw, ledgerRaw) {
		return Finding{}, false
	}

	return Finding{
		Checker:  CheckerUnspentTokens,
		Code:     CodeTokenContentMismatch,
		Severity: SeverityCritical,
		TxID:     id.TxId,
		TokenID:  id,
		Message: fmt.Sprintf(
			"token content does not match at [%s], local [%s], ledger [%s]",
			id, utils.Hashable(localRaw), utils.Hashable(ledgerRaw),
		),
	}, true
}

// CheckTokenSpendability checks that for each unspent token, it is still spendable.
// Spendability is verified against the current TMS for the given TMS ID.
// A token is still spendable if:
// - The token type is among the supported;
// - The token is parsable;
// - The token's recipients are still valid.
func (a *DefaultCheckers) CheckTokenSpendability(ctx context.Context) ([]Finding, error) {
	var findings []Finding

	tms, err := a.tmsProvider.GetManagementService(token.WithTMSID(a.tmsID))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting tms [%s]", a.tmsID)
	}
	tv := tms.Vault()
	uit, err := tv.NewQueryEngine().UnspentLedgerTokensIteratorBy(ctx)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed querying utxo engine")
	}
	defer uit.Close()

	ts := tms.TokensService()
	sigService := tms.SigService()
	supportedTokenFormats := ts.SupportedTokenFormats()
	supportedTokenFormatsSet := collections.NewSet(supportedTokenFormats...)
	logger.DebugfContext(ctx, "checking token spendability for [%s], supported tokens [%s]", tms.ID(), supportedTokenFormatsSet.ToSlice())
	for {
		tok, err := uit.Next()
		if err != nil {
			return nil, errors.WithMessagef(err, "failed querying next unspent token")
		}
		if tok == nil {
			break
		}
		id := tok.ID
		// is the token's format supported?
		if !supportedTokenFormatsSet.Contains(tok.Format) {
			findings = append(findings, Finding{
				Checker:  CheckerTokenSpendability,
				Code:     CodeTokenFormatUnsupported,
				Severity: SeverityCritical,
				TxID:     id.TxId,
				TokenID:  &id,
				Message:  fmt.Sprintf("token format not supported [%s][%s]", tok.ID, tok.Format),
			})

			continue
		}

		logger.DebugfContext(ctx, "deobfuscating token [%s][%s]...", tok.ID, tok.Format)
		// extract the token's recipients and try to get a verifier for it
		_, _, recipients, _, err := ts.Deobfuscate(ctx, tok.Token, tok.TokenMetadata)
		if err != nil {
			findings = append(findings, Finding{
				Checker:  CheckerTokenSpendability,
				Code:     CodeTokenNotDeobfuscatable,
				Severity: SeverityCritical,
				TxID:     id.TxId,
				TokenID:  &id,
				Message:  fmt.Sprintf("failed to deobfuscate token [%s][%s], [%s]", tok.ID, tok.Format, err),
			})

			continue
		}
		logger.DebugfContext(ctx, "deobfuscated token [%s][%s][%v]...", tok.ID, tok.Format, recipients)
		if len(recipients) == 0 {
			findings = append(findings, Finding{
				Checker:  CheckerTokenSpendability,
				Code:     CodeTokenNoRecipients,
				Severity: SeverityCritical,
				TxID:     id.TxId,
				TokenID:  &id,
				Message:  fmt.Sprintf("token recipient list is empty for [%s][%s]", tok.ID, tok.Format),
			})

			continue
		}
		for _, recipient := range recipients {
			if _, err := sigService.OwnerVerifier(ctx, recipient); err != nil {
				findings = append(findings, Finding{
					Checker:  CheckerTokenSpendability,
					Code:     CodeTokenRecipientUnverifiable,
					Severity: SeverityCritical,
					TxID:     id.TxId,
					TokenID:  &id,
					Message:  fmt.Sprintf("failed to verify recipient [%s][%s][%s], [%s]", tok.ID, recipient, tok.Format, err),
				})
			}
		}
	}

	logger.DebugfContext(ctx, "finished checks with [%d] findings", len(findings))

	return findings, nil
}

// CheckLocalCompleteness looks for tokens the ledger produced for this node that
// never landed in the local store.
//
// The other checks all start from what the node already holds, so a token that
// was never written locally is invisible to them. This one starts from the
// confirmed transactions instead: it rebuilds each transaction's request, asks
// the token service which tokens that transaction should have appended, and
// reports the ones the local store does not have. When a token is missing, the
// ledger decides how bad it is: still unspent means the node is holding money it
// cannot see, already spent means the books are wrong but nothing is lost.
//
// This walks the whole transaction history unless WithTransactionWindow narrows
// it, so it is meant for a background sweep rather than a request path.
func (a *DefaultCheckers) CheckLocalCompleteness(ctx context.Context) ([]Finding, error) {
	var findings []Finding

	tms, err := a.tmsProvider.GetManagementService(token.WithTMSID(a.tmsID))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting tms [%s]", a.tmsID)
	}
	net, err := a.networkProvider.GetNetwork(tms.Network(), tms.Channel())
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get network [%s]", tms.ID())
	}
	qe := tms.Vault().NewQueryEngine()

	params := a.transactionQuery()
	params.Statuses = []driver.TxStatus{driver.Confirmed}

	batch := make([]*token2.ID, 0, a.batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		res, err := a.checkPresenceBatch(ctx, tms, net, qe, batch)
		if err != nil {
			return err
		}
		findings = append(findings, res...)
		batch = batch[:0]

		return nil
	}

	err = a.walkTransactions(ctx, tms.ID(), params, func(transactionRecord *driver.TransactionRecord) error {
		expected, finding, err := a.expectedTokens(ctx, tms, transactionRecord.TxID)
		if err != nil {
			return err
		}
		if finding != nil {
			findings = append(findings, *finding)

			return nil
		}
		for _, id := range expected {
			batch = append(batch, id)
			if len(batch) >= a.batchSize {
				if err := flush(); err != nil {
					return err
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}

	return findings, nil
}

// expectedTokens returns the ids of the tokens the passed transaction should have
// appended to the local store. A non nil finding means the transaction could not
// be rebuilt and nothing can be said about it.
func (a *DefaultCheckers) expectedTokens(ctx context.Context, tms *token.ManagementService, txID string) ([]*token2.ID, *Finding, error) {
	requestRaw, err := a.db.GetTokenRequest(ctx, txID)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "failed getting token request [%s]", txID)
	}
	if len(requestRaw) == 0 {
		// CheckTransactions already reports this, no need to say it twice
		return nil, nil, nil
	}

	unparsable := func(err error) *Finding {
		return &Finding{
			Checker:  CheckerLocalCompleteness,
			Code:     CodeTxRequestUnparsable,
			Severity: SeverityWarning,
			TxID:     txID,
			Message:  fmt.Sprintf("cannot rebuild token request [%s], its tokens cannot be accounted for: [%s]", txID, err),
		}
	}

	request, err := tms.NewFullRequestFromBytes(requestRaw)
	if err != nil {
		return nil, unparsable(err), nil
	}
	md, err := request.GetMetadata()
	if err != nil {
		return nil, unparsable(err), nil
	}
	is, os, err := request.InputsAndOutputsNoRecipients(ctx)
	if err != nil {
		return nil, unparsable(err), nil
	}

	pp := tms.PublicParametersManager().PublicParameters()
	auth := tms.Authorization()
	_, toAppend, err := a.tokenDB.Parse(
		ctx,
		auth,
		token.RequestAnchor(txID),
		md,
		is,
		os,
		auth.AmIAnAuditor(),
		pp.Precision(),
		pp.GraphHiding(),
	)
	if err != nil {
		return nil, unparsable(err), nil
	}

	ids := make([]*token2.ID, 0, len(toAppend))
	for _, tta := range toAppend {
		ids = append(ids, &token2.ID{TxId: tta.TxID, Index: tta.Index})
	}

	return ids, nil, nil
}

// checkPresenceBatch reports which of the passed tokens are absent from the local store.
//
// The local store answers a batch either in full or not at all, so a batch that
// resolves cleanly proves every token in it landed. Only a batch that fails is
// walked token by token, and each token it then flags is confirmed against the
// ledger before being reported.
func (a *DefaultCheckers) checkPresenceBatch(
	ctx context.Context,
	tms *token.ManagementService,
	net *network.Network,
	qe *token.QueryEngine,
	ids []*token2.ID,
) ([]Finding, error) {
	if _, _, err := qe.WhoDeletedTokens(ctx, ids...); err == nil {
		// every id resolved, so all of them are in the local store, spent or not
		return nil, nil
	}

	var missing []*token2.ID
	for _, id := range ids {
		_, _, err := qe.WhoDeletedTokens(ctx, id)
		switch {
		case err == nil:
			// this one is present after all; only the rest of the batch was the problem
		case errors.Is(err, tdriver.ErrTokenNotFound):
			missing = append(missing, id)
		default:
			// a genuine query failure (connection reset, statement timeout) is not
			// evidence this token is absent. Reporting it through reportMissingLocally
			// would turn a transient local DB failure into a false critical claim that
			// the node owns money it cannot see.
			return nil, errors.Wrapf(err, "failed checking local presence of token [%s]", id)
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}

	return a.reportMissingLocally(ctx, tms, net, missing), nil
}

// reportMissingLocally turns tokens absent from the local store into findings,
// asking the ledger whether they are still unspent to decide how bad each one is.
func (a *DefaultCheckers) reportMissingLocally(
	ctx context.Context,
	tms *token.ManagementService,
	net *network.Network,
	missing []*token2.ID,
) []Finding {
	findings := make([]Finding, 0, len(missing))

	spent, err := a.areSpentOnLedger(ctx, tms, net, missing)
	if err != nil {
		// the local store says these tokens are not there and the ledger will not say
		// whether that matters. Report the check as inconclusive rather than raise an
		// alarm we cannot substantiate.
		return append(findings, Finding{
			Checker:  CheckerLocalCompleteness,
			Code:     CodeCheckFailed,
			Severity: SeverityWarning,
			Message:  fmt.Sprintf("[%d] tokens are missing locally but the ledger could not confirm their status: [%s]", len(missing), err),
		})
	}

	for i, id := range missing {
		severity := SeverityCritical
		message := fmt.Sprintf("token [%s] is unspent on the ledger but never landed in the local store", id)
		if spent[i] {
			severity = SeverityWarning
			message = fmt.Sprintf("token [%s] never landed in the local store and has already been spent on the ledger", id)
		}
		findings = append(findings, Finding{
			Checker:  CheckerLocalCompleteness,
			Code:     CodeTokenMissingLocally,
			Severity: severity,
			TxID:     id.TxId,
			TokenID:  id,
			Message:  message,
		})
	}

	return findings
}

// areSpentOnLedger asks the ledger whether the passed tokens have been spent.
func (a *DefaultCheckers) areSpentOnLedger(ctx context.Context, tms *token.ManagementService, net *network.Network, ids []*token2.ID) ([]bool, error) {
	meta, err := tms.WalletManager().SpentIDs(ids)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed computing spent ids for [%v]", ids)
	}
	spent, err := net.AreTokensSpent(ctx, tms.Namespace(), ids, meta)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed checking if tokens are spent [%v]", ids)
	}
	if len(spent) != len(ids) {
		return nil, errors.Errorf("ledger answered for [%d] tokens, asked for [%d]", len(spent), len(ids))
	}

	return spent, nil
}

// transactionQuery builds the transaction query the history walking checks use,
// narrowed to the configured window if there is one.
func (a *DefaultCheckers) transactionQuery() driver.QueryTransactionsParams {
	params := driver.QueryTransactionsParams{}
	if a.window > 0 {
		from := time.Now().Add(-a.window)
		params.From = &from
	}

	return params
}
