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
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"
)

// EndorserStore manages validation records for token transaction endorsements
type EndorserStore struct {
	readDB      *sql.DB
	writeDB     *sql.DB
	table       string
	tablePrefix string
	tableParams []string
	ci          common3.CondInterpreter
	pi          common3.PagInterpreter
}

// NewEndorserStore creates a new EndorserStore
func NewEndorserStore(
	readDB, writeDB *sql.DB,
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

// GetSchema returns the SQL schema for creating the endorser store tables
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
	`, db.table)
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

// Rollback rolls back the transaction
func (w *EndorserStoreTransaction) Rollback() {
	_ = w.tx.Rollback()
}

// AddValidationRecord adds a validation record to the database
func (w *EndorserStoreTransaction) AddValidationRecord(ctx context.Context, txID string, tokenRequest []byte, meta map[string][]byte, ppHash driver2.PPHash) error {
	logger.DebugfContext(ctx, "adding validation record [%s]", txID)

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
