/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttxdb

import (
	"context"
	"math/big"
	"reflect"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/multiplexed"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
)

//go:generate counterfeiter -o mock/ttx_store_service_manager.go --fake-name TTXStoreServiceManager . StoreServiceManager

type StoreServiceManager db.StoreServiceManager[*StoreService]

var (
	managerType = reflect.TypeFor[*StoreServiceManager]()
	logger      = logging.MustGetLogger()
)

func NewStoreServiceManager(cp db.ConfigService, drivers multiplexed.Driver) StoreServiceManager {
	return db.NewStoreServiceManager(cp, "ttxdb.persistence", drivers.NewOwnerTransaction, newStoreService)
}

func GetByTMSId(sp token.ServiceProvider, tmsID token.TMSID) (*StoreService, error) {
	s, err := sp.GetService(managerType)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get manager service")
	}
	c, err := s.(StoreServiceManager).StoreServiceByTMSId(tmsID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get db for tms [%s]", tmsID)
	}

	return c, nil
}

// TxStatus is the status of a transaction
type TxStatus = dbdriver.TxStatus

const (
	// Unknown is the status of a transaction that is unknown
	Unknown = dbdriver.Unknown
	// Pending is the status of a transaction that has been submitted to the ledger
	Pending = dbdriver.Pending
	// Confirmed is the status of a transaction that has been confirmed by the ledger
	Confirmed = dbdriver.Confirmed
	// Deleted is the status of a transaction that has been deleted due to a failure to commit
	Deleted = dbdriver.Deleted
	// Orphan is the status of a transaction that never reached the ledger
	Orphan = dbdriver.Orphan
)

// TxStatusMessage maps TxStatus to string
var TxStatusMessage = dbdriver.TxStatusMessage

// ActionType is the type of action performed by a transaction.
type ActionType = dbdriver.ActionType

const (
	// Issue is the action type for issuing tokens.
	Issue = dbdriver.Issue
	// Transfer is the action type for transferring tokens.
	Transfer = dbdriver.Transfer
	// Redeem is the action type for redeeming tokens.
	Redeem = dbdriver.Redeem
)

// SearchDirection defines the direction of a search.
type SearchDirection = dbdriver.SearchDirection

const (
	// FromLast defines the direction of a search from the last key.
	FromLast = dbdriver.FromLast
	// FromBeginning defines the direction of a search from the first key.
	FromBeginning = dbdriver.FromBeginning
)

// TransactionRecord is a more finer-grained version of a movement record.
// Given a Token Transaction, for each token action in the Token Request,
// a transaction record is created for each unique enrollment ID found in the outputs.
// The transaction record contains the total amount of the token type that was transferred to/from that enrollment ID
// in that action.
type TransactionRecord = dbdriver.TransactionRecord

// RecoveryClaim is the minimal projection of a pending transaction row
// returned by ClaimPendingTransactions for recovery processing.
type RecoveryClaim = dbdriver.RecoveryClaim

// MovementRecord is a record of a movement of assets.
// Given a Token Transaction, a movement record is created for each enrollment ID that participated in the transaction
// and each token type that was transferred.
// The movement record contains the total amount of the token type that was transferred to/from the enrollment ID
// in a given token transaction.
type MovementRecord = dbdriver.MovementRecord

// TransactionIterator is an iterator over transaction records
type TransactionIterator struct {
	it dbdriver.TransactionIterator
}

// Close closes the iterator. It must be called when done with the iterator.
func (t *TransactionIterator) Close() {
	t.it.Close()
}

// Next returns the next transaction record, if any.
// It returns nil, nil if there are no more records.
func (t *TransactionIterator) Next() (*TransactionRecord, error) {
	next, err := t.it.Next()
	if err != nil {
		return nil, err
	}
	if next == nil {
		return nil, nil
	}

	return next, nil
}

// Wallet models a wallet
type Wallet interface {
	// ID returns the wallet ID
	ID() string
	// TMS returns the TMS of the wallet
	TMS() *token.ManagementService
}

type Cache interface {
	Get(key string) ([]byte, bool)
	Add(key string, value []byte)
	Delete(key string)
}

// StoreService is a database that stores token transactions related information
type StoreService struct {
	*common.StatusSupport
	db dbdriver.TokenTransactionStore
}

func newStoreService(p dbdriver.TokenTransactionStore) (*StoreService, error) {
	return &StoreService{
		StatusSupport: common.NewStatusSupport(),
		db:            p,
	}, nil
}

// QueryTransactionsParams defines the parameters for querying movements
type QueryTransactionsParams = dbdriver.QueryTransactionsParams

// QueryTokenRequestsParams defines the parameters for querying token requests
type QueryTokenRequestsParams = dbdriver.QueryTokenRequestsParams

// Pagination defines the pagination for querying movements
type Pagination = cdriver.Pagination

// PageTransactionsIterator iterator defines the pagination iterator for movements query results
type PageTransactionsIterator = cdriver.PageIterator[*TransactionRecord]

// Transactions returns an iterators of transaction records filtered by the given params.
func (d *StoreService) Transactions(ctx context.Context, params QueryTransactionsParams, pagination Pagination) (*PageTransactionsIterator, error) {
	return d.db.QueryTransactions(ctx, params, pagination)
}

// TokenRequests returns an iterator over the token requests matching the passed params
func (d *StoreService) TokenRequests(ctx context.Context, params QueryTokenRequestsParams) (dbdriver.TokenRequestIterator, error) {
	return d.db.QueryTokenRequests(ctx, params)
}

func (d *StoreService) NewTransaction() (dbdriver.TransactionStoreTransaction, error) {
	return d.db.NewTransactionStoreTransaction()
}

// AppendTransactionRecord appends the transaction records corresponding to the passed token request.
func (d *StoreService) AppendTransactionRecord(ctx context.Context, req *token.Request) error {
	logger.DebugfContext(ctx, "appending new transaction record... [%s]", req.Anchor)

	ins, outs, attrs, err := req.InputsAndOutputs(ctx)
	if err != nil {
		return errors.WithMessagef(err, "failed getting inputs and outputs for request [%s]", req.Anchor)
	}
	record := &token.AuditRecord{
		Anchor:     req.Anchor,
		Inputs:     ins,
		Outputs:    outs,
		Attributes: attrs,
	}

	raw, err := req.Bytes()
	if err != nil {
		return errors.Wrapf(err, "failed to marshal token request [%s]", req.Anchor)
	}
	txs, err := TransactionRecords(ctx, record, time.Now().UTC())
	if err != nil {
		return errors.WithMessagef(err, "failed parsing transactions from audit record")
	}

	logger.DebugfContext(ctx, "storing new records... [%d,%d]", len(raw), len(txs))
	w, err := d.db.NewTransactionStoreTransaction()
	if err != nil {
		return errors.WithMessagef(err, "begin update for txid [%s] failed", record.Anchor)
	}
	anchor := string(record.Anchor)
	if err := w.AddTokenRequest(
		ctx,
		anchor,
		raw,
		req.AllApplicationMetadata(),
		record.Attributes,
		req.PublicParamsHash(),
	); err != nil {
		w.Rollback()

		return errors.WithMessagef(err, "append token request for txid [%s] failed", record.Anchor)
	}
	if len(txs) > 0 {
		if err := w.AddTransaction(ctx, txs...); err != nil {
			w.Rollback()

			return errors.WithMessagef(err, "append transactions for txid [%s] failed", record.Anchor)
		}
	}
	if err := w.Commit(); err != nil {
		return errors.WithMessagef(err, "committing tx for txid [%s] failed", record.Anchor)
	}

	logger.DebugfContext(ctx, "appending transaction record new completed without errors")

	return nil
}

// SetStatus sets the status of the audit records with the passed transaction id to the passed status
func (d *StoreService) SetStatus(ctx context.Context, txID string, status dbdriver.TxStatus, message string) error {
	logger.DebugfContext(ctx, "set status [%s][%s]...", txID, status)
	if err := d.db.SetStatus(ctx, txID, status, message); err != nil {
		return errors.Wrapf(err, "failed setting status [%s][%s]", txID, dbdriver.TxStatusMessage[status])
	}

	// notify the listeners
	d.Notify(common.StatusEvent{
		Ctx:            ctx,
		TxID:           txID,
		ValidationCode: status,
	})
	logger.DebugfContext(ctx, "set status [%s][%s] done", txID, dbdriver.TxStatusMessage[status])

	return nil
}

// GetStatus return the status of the given transaction id.
// It returns an error if no transaction with that id is found
func (d *StoreService) GetStatus(ctx context.Context, txID string) (TxStatus, string, error) {
	logger.DebugfContext(ctx, "get status [%s]...", txID)
	status, message, err := d.db.GetStatus(ctx, txID)
	if err != nil {
		return Unknown, "", errors.Wrapf(err, "failed getting status [%s]", txID)
	}
	logger.DebugfContext(ctx, "got status [%s][%s]", txID, status)

	return status, message, nil
}

// GetTokenRequest returns the token request bound to the passed transaction id, if available.
func (d *StoreService) GetTokenRequest(ctx context.Context, txID string) ([]byte, error) {
	return d.db.GetTokenRequest(ctx, txID)
}

// GetTokenRequests returns the token requests bound to the given tx ids in
// a single query. See driver.TransactionStore.GetTokenRequests for details
// about missing-key semantics.
func (d *StoreService) GetTokenRequests(ctx context.Context, txIDs []string) (map[string][]byte, error) {
	return d.db.GetTokenRequests(ctx, txIDs)
}

// AddTransactionEndorsementAck records the signature of a given endorser for a given transaction
func (d *StoreService) AddTransactionEndorsementAck(ctx context.Context, txID string, id token.Identity, sigma []byte) error {
	return d.db.AddTransactionEndorsementAck(ctx, txID, id, sigma)
}

// GetTransactionEndorsementAcks returns the endorsement signatures for the given transaction id
func (d *StoreService) GetTransactionEndorsementAcks(ctx context.Context, txID string) (map[string][]byte, error) {
	return d.db.GetTransactionEndorsementAcks(ctx, txID)
}

// AcquireRecoveryLeadership tries to acquire the DB-backed recovery leadership lease.
func (d *StoreService) AcquireRecoveryLeadership(ctx context.Context, lockID int64) (dbdriver.RecoveryLeadership, bool, error) {
	leadership, acquired, err := d.db.AcquireRecoveryLeadership(ctx, lockID)
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed to acquire recovery leadership")
	}

	return leadership, acquired, nil
}

// ClaimPendingTransactions returns a claimed batch of Pending transactions older than the given duration.
// Each returned RecoveryClaim carries the TxID and StoredAt timestamp the recovery loop needs;
// the rest of the row is intentionally not projected from SQL.
func (d *StoreService) ClaimPendingTransactions(ctx context.Context, olderThan time.Duration, leaseDuration time.Duration, limit int, owner string) ([]*RecoveryClaim, error) {
	storedBefore := time.Now().UTC().Add(-olderThan)
	logger.DebugfContext(ctx, "claiming pending transactions stored before %s (older than %s), lease duration [%s], limit [%d], owner [%s]",
		storedBefore, olderThan, leaseDuration, limit, owner)

	records, err := d.db.ClaimPendingTransactions(ctx, dbdriver.RecoveryClaimParams{
		OlderThan:     storedBefore,
		LeaseDuration: leaseDuration,
		Limit:         limit,
		Owner:         owner,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to claim pending transactions")
	}

	logger.DebugfContext(ctx, "claimed %d pending transactions older than %s for owner [%s]", len(records), olderThan, owner)

	return records, nil
}

// ReleaseRecoveryClaim clears the recovery claim for the passed transaction if owned by owner.
// The message parameter is stored for audit/debugging purposes.
func (d *StoreService) ReleaseRecoveryClaim(ctx context.Context, txID string, owner string, message string) error {
	if err := d.db.ReleaseRecoveryClaim(ctx, txID, owner, message); err != nil {
		return errors.Wrapf(err, "failed to release recovery claim for tx [%s]", txID)
	}

	return nil
}

// TransactionRecords is a pure function that converts an AuditRecord for storage in the database.
func TransactionRecords(ctx context.Context, record *token.AuditRecord, timestamp time.Time) (txs []TransactionRecord, err error) {
	inputs := record.Inputs
	outputs := record.Outputs

	actionIndex := 0
	for {
		// collect inputs and outputs from the same action
		ins := inputs.Filter(func(t *token.Input) bool {
			return t.ActionIndex == actionIndex
		})
		ous := outputs.Filter(func(t *token.Output) bool {
			return t.ActionIndex == actionIndex
		})
		if ins.Count() == 0 && ous.Count() == 0 {
			logger.DebugfContext(ctx, "no actions left for tx [%s][%d]", record.Anchor, actionIndex)

			break
		}

		// create a transaction record from ins and ous

		// All ins should be for same EID, check this
		inEIDs := ins.EnrollmentIDs()
		if len(inEIDs) > 1 {
			return nil, errors.Errorf("expected at most 1 input enrollment id, got %d, [%v]", len(inEIDs), inEIDs)
		}
		inEID := ""
		if len(inEIDs) == 1 {
			inEID = inEIDs[0]
		}

		outEIDs := ous.EnrollmentIDs()
		outEIDs = append(outEIDs, "")
		outTT := ous.TokenTypes()
		for _, outEID := range outEIDs {
			for _, tokenType := range outTT {
				received := ous.ByEnrollmentID(outEID).ByType(tokenType).Sum()
				if received.Cmp(big.NewInt(0)) <= 0 {
					continue
				}

				tt := dbdriver.Issue
				if len(inEIDs) != 0 {
					if len(outEID) == 0 {
						tt = dbdriver.Redeem
					} else {
						tt = dbdriver.Transfer
					}
				}

				txs = append(txs, dbdriver.TransactionRecord{
					TxID:         string(record.Anchor),
					SenderEID:    inEID,
					RecipientEID: outEID,
					TokenType:    tokenType,
					Amount:       received,
					Status:       dbdriver.Pending,
					ActionType:   tt,
					Timestamp:    timestamp,
				})
			}
		}

		actionIndex++
	}
	logger.DebugfContext(ctx, "parsed transactions for tx [%s]", record.Anchor)

	return txs, err
}

// Movements converts an AuditRecord to MovementRecords for storage in the database.
// A positive movement Amount means incoming tokens, and negative means outgoing tokens from the enrollment ID.
func Movements(ctx context.Context, record *token.AuditRecord, created time.Time) (mv []MovementRecord, err error) {
	inputs := record.Inputs
	outputs := record.Outputs
	// we need to consider both inputs and outputs enrollment IDs because the record can refer to a redeem
	eIDs := joinIOEIDs(record)
	logger.DebugfContext(ctx, "eIDs [%v]", eIDs)
	tokenTypes := outputs.TokenTypes()

	for _, eID := range eIDs {
		for _, tokenType := range tokenTypes {
			received := outputs.ByEnrollmentID(eID).ByType(tokenType).Sum()
			sent := inputs.ByEnrollmentID(eID).ByType(tokenType).Sum()
			diff := new(big.Int).Sub(received, sent)
			if sent.Cmp(received) == 0 {
				continue
			}

			logger.DebugfContext(ctx, "adding movement [%s:%d]", eID, diff.Int64())
			mv = append(mv, dbdriver.MovementRecord{
				TxID:         string(record.Anchor),
				EnrollmentID: eID,
				Amount:       diff,
				TokenType:    tokenType,
				Timestamp:    created,
				Status:       dbdriver.Pending,
			})
		}
	}
	logger.DebugfContext(ctx, "finished to parse sent movements for tx [%s]", record.Anchor)

	return mv, err
}

// joinIOEIDs joins enrollment IDs of inputs and outputs
func joinIOEIDs(record *token.AuditRecord) []string {
	iEIDs := record.Inputs.EnrollmentIDs()
	oEIDs := record.Outputs.EnrollmentIDs()
	eIDs := append(iEIDs, oEIDs...)
	eIDs = deduplicate(eIDs)

	return eIDs
}

// deduplicate removes duplicate entries from a slice
func deduplicate(source []string) []string {
	support := make(map[string]bool)
	var res []string
	for _, item := range source {
		if _, value := support[item]; !value {
			support[item] = true
			res = append(res, item)
		}
	}

	return res
}
