/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	driver2 "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/LFDT-Panurus/panurus/token/services/storage/integrity"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"
)

// EndorserStore manages validation records for token transaction endorsements
type EndorserStore struct {
	readDB      *sql.DB
	writeDB     WriteDB
	table       string
	tablePrefix string
	tableParams []string
	ci          common3.CondInterpreter
	pi          common3.PagInterpreter
}

// NewEndorserStore creates a new EndorserStore
func NewEndorserStore(
	readDB *sql.DB,
	writeDB WriteDB,
	tables TableNames,
	ci common3.CondInterpreter,
	pi common3.PagInterpreter,
) (*EndorserStore, error) {
	return &EndorserStore{
		readDB:      readDB,
		writeDB:     writeDB,
		table:       tables.Validations,
		tablePrefix: tables.Prefix,
		tableParams: tables.Params,
		ci:          ci,
		pi:          pi,
	}, nil
}

// Close closes the database connections
func (db *EndorserStore) Close() error {
	return nil // Connections are managed externally
}

// GetSchema returns the SQL schema for creating the endorser store table.
//
// A single validations table holds one row per transaction, keyed by tx_id. The rejected
// column discriminates approved requests (rejected = false) from requests whose validation
// failed (rejected = true). Approved rows are the single-approval / already-processed guard:
// GetStatus only reads rows with rejected = false, so a rejected row never makes a request
// look processed. Rejected rows additionally carry a free-text reason and the public
// parameters hash the request was evaluated against, so a rejection can be replayed; that
// pp_hash may be empty when validation failed before the endorser resolved it.
func (db *EndorserStore) GetSchema() string {
	return fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			tx_id TEXT NOT NULL PRIMARY KEY,
			request BYTEA NOT NULL,
			metadata BYTEA NOT NULL,
			pp_hash BYTEA NOT NULL,
			rejected BOOL NOT NULL DEFAULT false,
			reason TEXT NOT NULL DEFAULT '',
			status INT NOT NULL,
			status_message TEXT NOT NULL,
			stored_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_status_storedat_%s ON %s ( status, stored_at );
		CREATE INDEX IF NOT EXISTS idx_rejected_storedat_%s ON %s ( rejected, stored_at );
	`, db.table, db.table, db.table, db.table, db.table)
}

// CreateSchema creates the database schema for the endorser store
func (db *EndorserStore) CreateSchema() error {
	_, err := db.writeDB.Exec(db.GetSchema())

	return err
}

// NewEndorserStoreTransaction creates a new transaction for endorser operations
func (db *EndorserStore) NewEndorserStoreTransaction() (dbdriver.EndorserStoreTransaction, error) {
	tx, err := db.writeDB.Begin()
	if err != nil {
		return nil, errors.Wrap(err, "failed to begin transaction")
	}

	return &EndorserStoreTransaction{
		tx:    tx,
		table: db.table,
		ci:    db.ci,
	}, nil
}

// QueryValidations returns an iterator over validation records matching the given params
func (db *EndorserStore) QueryValidations(ctx context.Context, params dbdriver.QueryValidationRecordsParams) (dbdriver.ValidationRecordsIterator, error) {
	validationsTable := q.Table(db.table)
	query, args := q.Select().
		Fields(
			validationsTable.Field("tx_id"), validationsTable.Field("request"), validationsTable.Field("metadata"),
			validationsTable.Field("stored_at"),
		).
		From(validationsTable).
		Where(HasValidationParams(params, db.table)).
		Format(db.ci)

	logging.Debug(logger, query, args)
	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	it := common.NewIterator(rows, func(r *dbdriver.ValidationRecord) error {
		var meta []byte
		if err := rows.Scan(&r.TxID, &r.TokenRequest, &meta, &r.Timestamp); err != nil {
			return err
		}

		return unmarshal(meta, &r.Metadata)
	})
	if params.Filter == nil {
		return it, nil
	}

	return iterators.Filter(it, params.Filter), nil
}

// GetStatus returns the status of a validation record
func (db *EndorserStore) GetStatus(ctx context.Context, txID string) (dbdriver.TxStatus, string, error) {
	var status dbdriver.TxStatus
	var statusMessage string
	// Only approved rows (rejected = false) count as a status: a rejected row is an audit
	// record of a failed request, not an approval, so it must read back as Unknown exactly as
	// a missing row does. This is what keeps a recorded failure from gating re-evaluation via
	// AlreadyProcessed.
	query, args := q.Select().
		FieldsByName("status", "status_message").
		From(q.Table(db.table)).
		Where(cond.And(cond.Eq("tx_id", txID), cond.Eq("rejected", false))).
		Format(db.ci)
	logging.Debug(logger, query, txID)

	row := db.readDB.QueryRowContext(ctx, query, args...)
	if err := row.Scan(&status, &statusMessage); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			logger.DebugfContext(ctx, "tried to get status for non-existent validation record [%s], returning unknown", txID)

			return dbdriver.Unknown, "", nil
		}

		return dbdriver.Unknown, "", errors.Wrapf(err, "error querying db")
	}

	return status, statusMessage, nil
}

// RecordValidationFailure records, for auditing purposes, that validation of the request filed
// under txID failed, storing the request bytes, the public parameters hash it was evaluated
// against, and a free-text reason, so the rejection can be replayed as it happened.
//
// It writes into the shared validations table with rejected = true. Unlike AddValidationRecord
// it applies no token-request well-formedness checks (the request may have failed because it
// was malformed) and accepts an empty ppHash (validation can fail before the endorser resolves
// its own public parameters hash). It never produces an approved row, so it never affects the
// already-processed guard.
//
// The insert is a conditional upsert keyed by tx_id: a fresh anchor inserts, a prior rejection
// is overwritten (latest-wins), but an existing approved row is left untouched by the
// ON CONFLICT ... WHERE rejected = true predicate. It is therefore impossible for a recorded
// failure to clobber an approval.
func (db *EndorserStore) RecordValidationFailure(ctx context.Context, txID string, tokenRequest []byte, ppHash driver2.PPHash, reason string) error {
	logger.DebugfContext(ctx, "recording validation failure [%s]", txID)

	if txID == "" {
		return errors.WithMessage(integrity.ErrEmptyTxID, "refusing to record validation failure")
	}
	if len(tokenRequest) == 0 {
		return errors.Wrapf(integrity.ErrEmptyTokenRequest, "refusing to record validation failure for [%s]", txID)
	}

	// The metadata column is NOT NULL; a rejected row carries no validator metadata, so store
	// the JSON null the approved path also stores for a nil map rather than an empty blob.
	metaBytes, err := marshal(nil)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal metadata for tx [%s]", txID)
	}

	// pp_hash is NOT NULL but may be empty for a rejection (validation can fail before the
	// endorser resolves its own hash). A nil []byte would be stored as SQL NULL, so coerce it
	// to a zero-length blob: still "empty", but satisfying the NOT NULL column.
	if ppHash == nil {
		ppHash = driver2.PPHash{}
	}

	now := time.Now().UTC()
	query, args := q.InsertInto(db.table).
		Fields("tx_id", "request", "metadata", "pp_hash", "rejected", "reason", "status", "status_message", "stored_at").
		Row(txID, tokenRequest, metaBytes, ppHash, true, reason, dbdriver.Unknown, "", now).
		OnConflict(
			[]common3.FieldName{"tx_id"},
			q.OverwriteValue("request"),
			q.OverwriteValue("pp_hash"),
			q.OverwriteValue("reason"),
			q.OverwriteValue("stored_at"),
		).
		Where(cond.Eq(common3.FieldName(db.table+".rejected"), true)).
		Format()

	logging.Debug(logger, query, args)
	if _, err := db.writeDB.ExecContext(ctx, query, args...); err != nil {
		return errors.Wrapf(err, "failed to insert validation failure record for tx [%s]", txID)
	}

	return nil
}

// QueryValidationFailures returns an iterator over validation failure records matching params.
func (db *EndorserStore) QueryValidationFailures(ctx context.Context, params dbdriver.QueryValidationFailureRecordsParams) (dbdriver.ValidationFailureRecordsIterator, error) {
	validationsTable := q.Table(db.table)
	query, args := q.Select().
		Fields(
			validationsTable.Field("tx_id"), validationsTable.Field("request"),
			validationsTable.Field("pp_hash"), validationsTable.Field("reason"),
			validationsTable.Field("stored_at"),
		).
		From(validationsTable).
		Where(HasValidationFailureParams(params, db.table)).
		Format(db.ci)

	logging.Debug(logger, query, args)
	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	it := common.NewIterator(rows, func(r *dbdriver.ValidationFailureRecord) error {
		return rows.Scan(&r.TxID, &r.TokenRequest, &r.PPHash, &r.Reason, &r.Timestamp)
	})
	if params.Filter == nil {
		return it, nil
	}

	return iterators.Filter(it, params.Filter), nil
}

// EndorserStoreTransaction represents a database transaction for endorser operations
type EndorserStoreTransaction struct {
	tx    *sql.Tx
	table string
	ci    common3.CondInterpreter
}

// Impl returns the underlying transaction implementation
func (w *EndorserStoreTransaction) Impl() dbdriver.TransactionImpl {
	return w.tx
}

// Commit commits the transaction
func (w *EndorserStoreTransaction) Commit() error {
	return w.tx.Commit()
}

// Rollback rolls back the transaction. A rollback failure cannot be returned
// (the interface returns nothing) but is logged rather than dropped, so a
// transaction that outlived its rollback leaves a trace. sql.ErrTxDone is
// expected when the transaction has already been committed or rolled back.
func (w *EndorserStoreTransaction) Rollback() {
	if err := w.tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		logger.Errorf("error rolling back endorser transaction (ignoring...): %s", err)
	}
}

// AddValidationRecord adds an approved (rejected = false) validation record to the database.
//
// Verification: as for TransactionStoreTransaction.AddTokenRequest, this layer
// does not interpret tokenRequest — the wire-format check lives in the
// endorserdb service. What is refused here is a row no later check could act
// on: an empty transaction id, empty request bytes, or an empty public
// parameters hash.
//
// The insert is a conditional upsert keyed by tx_id. It overwrites a prior rejected row
// (so a request that was recorded as failed and is later re-submitted and approved becomes
// the approved record), but the ON CONFLICT ... WHERE rejected = true predicate refuses to
// touch an existing approved row. A duplicate approval therefore affects no rows, which is
// reported as an error: this is the atomic single-approval backstop that prevents a second
// endorsement being produced for an anchor this endorser already approved.
func (w *EndorserStoreTransaction) AddValidationRecord(ctx context.Context, txID string, tokenRequest []byte, meta map[string][]byte, ppHash driver2.PPHash) error {
	logger.DebugfContext(ctx, "adding validation record [%s]", txID)

	if err := integrity.CheckTokenRequestForStorage(txID, tokenRequest, ppHash); err != nil {
		return errors.WithMessagef(err, "refusing to add validation record for txid [%s]", txID)
	}

	metaBytes, err := marshal(meta)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal metadata for tx [%s]", txID)
	}

	query, args := q.InsertInto(w.table).
		Fields("tx_id", "request", "metadata", "pp_hash", "rejected", "reason", "status", "status_message", "stored_at").
		Row(txID, tokenRequest, metaBytes, ppHash, false, "", dbdriver.Pending, "", time.Now().UTC()).
		OnConflict(
			[]common3.FieldName{"tx_id"},
			q.OverwriteValue("request"),
			q.OverwriteValue("metadata"),
			q.OverwriteValue("pp_hash"),
			q.OverwriteValue("rejected"),
			q.OverwriteValue("reason"),
			q.OverwriteValue("status"),
			q.OverwriteValue("status_message"),
			q.OverwriteValue("stored_at"),
		).
		Where(cond.Eq(common3.FieldName(w.table+".rejected"), true)).
		Format()

	logging.Debug(logger, query, args)
	res, err := w.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return errors.Wrapf(err, "failed to insert validation record for tx [%s]", txID)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		// The statement ran, but the driver cannot report the row count. Fail closed: an
		// approval whose single-approval guarantee cannot be confirmed must not be treated
		// as recorded, or a second endorsement could still be produced.
		return errors.Wrapf(err, "failed to confirm validation record was stored for tx [%s]", txID)
	}
	if affected == 0 {
		// No row was inserted or updated: an approved row for this anchor already exists and
		// the WHERE rejected = true guard refused to overwrite it. The request was already
		// approved, so this is a duplicate approval and must be rejected.
		return errors.Wrapf(integrity.ErrAlreadyApproved, "refusing to re-approve already-approved tx [%s]", txID)
	}

	return nil
}

// DeleteValidationRecord removes the validation record for txID. Deleting a txID that has
// no record is not an error: the DELETE simply affects no rows.
func (w *EndorserStoreTransaction) DeleteValidationRecord(ctx context.Context, txID string) error {
	logger.DebugfContext(ctx, "deleting validation record [%s]", txID)

	query, args := q.DeleteFrom(w.table).
		Where(cond.Eq("tx_id", txID)).
		Format(w.ci)

	logging.Debug(logger, query, args)
	if _, err := w.tx.ExecContext(ctx, query, args...); err != nil {
		return errors.Wrapf(err, "failed to delete validation record for tx [%s]", txID)
	}

	return nil
}

// SetStatus sets the status of an approved validation record. The rejected = false guard
// keeps it from ever promoting a rejected audit row into something GetStatus would report: a
// status only belongs to an approval.
func (w *EndorserStoreTransaction) SetStatus(ctx context.Context, txID string, status dbdriver.TxStatus, message string) error {
	logger.DebugfContext(ctx, "setting validation record status [%s][%s]", txID, status)

	query, args := q.Update(w.table).
		Set("status", status).
		Set("status_message", message).
		Where(cond.And(cond.Eq("tx_id", txID), cond.Eq("rejected", false))).
		Format(w.ci)

	logging.Debug(logger, query, args)
	if _, err := w.tx.ExecContext(ctx, query, args...); err != nil {
		return errors.Wrapf(err, "failed to update validation record status for tx [%s]", txID)
	}

	return nil
}
