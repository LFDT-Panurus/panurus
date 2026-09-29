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

	"github.com/hashicorp/go-uuid"

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
	readDB       *sql.DB
	writeDB      WriteDB
	table        string
	failureTable string
	tablePrefix  string
	tableParams  []string
	ci           common3.CondInterpreter
	pi           common3.PagInterpreter
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
		readDB:       readDB,
		writeDB:      writeDB,
		table:        tables.Validations,
		failureTable: tables.ValidationFailures,
		tablePrefix:  tables.Prefix,
		tableParams:  tables.Params,
		ci:           ci,
		pi:           pi,
	}, nil
}

// Close closes the database connections
func (db *EndorserStore) Close() error {
	return nil // Connections are managed externally
}

// GetSchema returns the SQL schema for creating the endorser store tables.
//
// The validations table holds one row per approved request, keyed by tx_id, and is the
// single-approval / already-processed guard. The validation-failures table is a separate,
// append-only audit log of requests whose validation failed: it is keyed by a generated id
// (several failures may share a tx_id), carries no pp_hash or status, and is never consulted
// by the already-processed check.
func (db *EndorserStore) GetSchema() string {
	return fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			tx_id TEXT NOT NULL PRIMARY KEY,
			request BYTEA NOT NULL,
			metadata BYTEA NOT NULL,
			pp_hash BYTEA NOT NULL,
			status INT NOT NULL,
			status_message TEXT NOT NULL,
			stored_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_status_storedat_%s ON %s ( status, stored_at );

		CREATE TABLE IF NOT EXISTS %s (
			id CHAR(36) NOT NULL PRIMARY KEY,
			tx_id TEXT NOT NULL,
			request BYTEA NOT NULL,
			reason TEXT NOT NULL,
			stored_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_txid_%s ON %s ( tx_id );
		CREATE INDEX IF NOT EXISTS idx_storedat_%s ON %s ( stored_at );
	`, db.table, db.table, db.table,
		db.failureTable, db.failureTable, db.failureTable, db.failureTable, db.failureTable)
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
	query, args := q.Select().
		FieldsByName("status", "status_message").
		From(q.Table(db.table)).
		Where(cond.Eq("tx_id", txID)).
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

// RecordValidationFailure appends an audit-only record that validation of the request filed
// under txID failed. It is a direct, append-only insert: unlike AddValidationRecord it does
// not open an EndorserStoreTransaction (there is no multi-step atomicity to protect), applies
// no token-request well-formedness checks (the request may have failed because it was
// malformed), and never affects the already-processed guard.
func (db *EndorserStore) RecordValidationFailure(ctx context.Context, txID string, tokenRequest []byte, reason string) error {
	logger.DebugfContext(ctx, "recording validation failure [%s]", txID)

	if txID == "" {
		return errors.WithMessage(integrity.ErrEmptyTxID, "refusing to record validation failure")
	}
	if len(tokenRequest) == 0 {
		return errors.Wrapf(integrity.ErrEmptyTokenRequest, "refusing to record validation failure for [%s]", txID)
	}

	id, err := uuid.GenerateUUID()
	if err != nil {
		return errors.Wrapf(err, "error generating uuid")
	}
	query, args := q.InsertInto(db.failureTable).
		Fields("id", "tx_id", "request", "reason", "stored_at").
		Row(id, txID, tokenRequest, reason, time.Now().UTC()).
		Format()

	logging.Debug(logger, query, args)
	if _, err := db.writeDB.ExecContext(ctx, query, args...); err != nil {
		return errors.Wrapf(err, "failed to insert validation failure record for tx [%s]", txID)
	}

	return nil
}

// QueryValidationFailures returns an iterator over validation failure records matching params.
func (db *EndorserStore) QueryValidationFailures(ctx context.Context, params dbdriver.QueryValidationFailureRecordsParams) (dbdriver.ValidationFailureRecordsIterator, error) {
	failuresTable := q.Table(db.failureTable)
	query, args := q.Select().
		Fields(
			failuresTable.Field("tx_id"), failuresTable.Field("request"),
			failuresTable.Field("reason"), failuresTable.Field("stored_at"),
		).
		From(failuresTable).
		Where(HasValidationFailureParams(params, db.failureTable)).
		Format(db.ci)

	logging.Debug(logger, query, args)
	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	it := common.NewIterator(rows, func(r *dbdriver.ValidationFailureRecord) error {
		return rows.Scan(&r.TxID, &r.TokenRequest, &r.Reason, &r.Timestamp)
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

// AddValidationRecord adds a validation record to the database.
//
// Verification: as for TransactionStoreTransaction.AddTokenRequest, this layer
// does not interpret tokenRequest — the wire-format check lives in the
// endorserdb service. What is refused here is a row no later check could act
// on: an empty transaction id, empty request bytes, or an empty public
// parameters hash.
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
		Fields("tx_id", "request", "metadata", "pp_hash", "status", "status_message", "stored_at").
		Row(txID, tokenRequest, metaBytes, ppHash, dbdriver.Pending, "", time.Now().UTC()).
		Format()

	logging.Debug(logger, query, args)
	if _, err := w.tx.ExecContext(ctx, query, args...); err != nil {
		return errors.Wrapf(err, "failed to insert validation record for tx [%s]", txID)
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

// SetStatus sets the status of a validation record
func (w *EndorserStoreTransaction) SetStatus(ctx context.Context, txID string, status dbdriver.TxStatus, message string) error {
	logger.DebugfContext(ctx, "setting validation record status [%s][%s]", txID, status)

	query, args := q.Update(w.table).
		Set("status", status).
		Set("status_message", message).
		Where(cond.Eq("tx_id", txID)).
		Format(w.ci)

	logging.Debug(logger, query, args)
	if _, err := w.tx.ExecContext(ctx, query, args...); err != nil {
		return errors.Wrapf(err, "failed to update validation record status for tx [%s]", txID)
	}

	return nil
}
