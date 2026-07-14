/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"database/sql"
	"encoding/json"
	errors2 "errors"
	"fmt"
	"strings"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	driver2 "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	_select "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/select"
	"github.com/hashicorp/go-uuid"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	driver3 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"
)

// maxAmountBits is the maximum bit length supported by NUMERIC(78, 0).
// NUMERIC(78, 0) stores up to 10^78 values, fitting in ~259 bits.
// 255 is used as a conservative safe upper bound.
const maxAmountBits = 255

type transactionTables struct {
	Movements             string
	Transactions          string
	Requests              string
	TransactionEndorseAck string
}

type TransactionStore struct {
	readDB                *sql.DB
	writeDB               *sql.DB
	table                 transactionTables
	tablePrefix           string
	tableParams           []string
	ci                    common3.CondInterpreter
	pi                    common3.PagInterpreter
	notifier              dbdriver.TransactionNotifier
	recoveryLeaderFactory func(context.Context, *sql.DB, int64) (dbdriver.RecoveryLeadership, bool, error)
}

func newTransactionStore(
	readDB, writeDB *sql.DB,
	tablePrefix string,
	tableParams []string,
	tables transactionTables,
	ci common3.CondInterpreter,
	pi common3.PagInterpreter,
	notifier dbdriver.TransactionNotifier,
	recoveryLeaderFactory func(context.Context, *sql.DB, int64) (dbdriver.RecoveryLeadership, bool, error),
) *TransactionStore {
	return &TransactionStore{
		readDB:                readDB,
		writeDB:               writeDB,
		table:                 tables,
		tablePrefix:           tablePrefix,
		tableParams:           tableParams,
		ci:                    ci,
		pi:                    pi,
		notifier:              notifier,
		recoveryLeaderFactory: recoveryLeaderFactory,
	}
}

// PrefixedTableName returns the formatted table name for the given logical name.
func (s *TransactionStore) PrefixedTableName(name string) string {
	nc, err := ncProvider.GetFormatter(s.tablePrefix)
	if err != nil {
		return name
	}

	return nc.MustFormat(name, s.tableParams...)
}

func NewAuditTransactionStore(readDB, writeDB *sql.DB, tables TableNames, ci common3.CondInterpreter, pi common3.PagInterpreter) (*TransactionStore, error) {
	return NewOwnerTransactionStore(readDB, writeDB, tables, ci, pi)
}

func NewOwnerTransactionStore(readDB, writeDB *sql.DB, tables TableNames, ci common3.CondInterpreter, pi common3.PagInterpreter) (*TransactionStore, error) {
	return newTransactionStore(readDB, writeDB, tables.Prefix, tables.Params, transactionTables{
		Movements:             tables.Movements,
		Transactions:          tables.Transactions,
		Requests:              tables.Requests,
		TransactionEndorseAck: tables.TransactionEndorseAck,
	}, ci, pi, nil, nil), nil
}

func NewTransactionStoreWithNotifierAndRecovery(
	readDB, writeDB *sql.DB,
	tables TableNames,
	ci common3.CondInterpreter,
	pi common3.PagInterpreter,
	notifier dbdriver.TransactionNotifier,
	recoveryLeaderFactory func(context.Context, *sql.DB, int64) (dbdriver.RecoveryLeadership, bool, error),
) (*TransactionStore, error) {
	return newTransactionStore(readDB, writeDB, tables.Prefix, tables.Params, transactionTables{
		Movements:             tables.Movements,
		Transactions:          tables.Transactions,
		Requests:              tables.Requests,
		TransactionEndorseAck: tables.TransactionEndorseAck,
	}, ci, pi, notifier, recoveryLeaderFactory), nil
}

func (db *TransactionStore) CreateSchema() error {
	return common.InitSchema(db.writeDB, db.GetSchema())
}

func (db *TransactionStore) GetTokenRequest(ctx context.Context, txID string) ([]byte, error) {
	query, args := q.Select().
		FieldsByName("request").
		From(q.Table(db.table.Requests)).
		Where(cond.Eq("tx_id", txID)).
		Format(db.ci)

	return common.QueryUniqueContext[[]byte](ctx, db.readDB, query, args...)
}

// GetTokenRequests fetches the token requests for the given tx ids in a
// single SELECT. Missing tx ids are absent from the returned map — callers
// should treat a missing key identically to GetTokenRequest returning nil.
// Empty input returns an empty map without querying.
func (db *TransactionStore) GetTokenRequests(ctx context.Context, txIDs []string) (map[string][]byte, error) {
	if len(txIDs) == 0 {
		return map[string][]byte{}, nil
	}
	query, args := q.Select().
		FieldsByName("tx_id", "request").
		From(q.Table(db.table.Requests)).
		Where(cond.In("tx_id", txIDs...)).
		Format(db.ci)

	logging.Debug(logger, query, args)
	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer Close(rows)

	result := make(map[string][]byte, len(txIDs))
	for rows.Next() {
		var txID string
		var request []byte
		if err := rows.Scan(&txID, &request); err != nil {
			return nil, err
		}
		result[txID] = request
	}

	return result, rows.Err()
}

func (db *TransactionStore) QueryMovements(ctx context.Context, params dbdriver.QueryMovementsParams) (res []*dbdriver.MovementRecord, err error) {
	movementsTable, requestsTable := q.Table(db.table.Movements), q.Table(db.table.Requests)
	query, args := q.Select().
		Fields(
			movementsTable.Field("tx_id"), common3.FieldName("enrollment_id"), common3.FieldName("token_type"),
			common3.FieldName("amount"), requestsTable.Field("status"),
		).
		From(movementsTable.Join(requestsTable,
			cond.Cmp(movementsTable.Field("tx_id"), "=", requestsTable.Field("tx_id"))),
		).
		Where(HasMovementsParams(params)).
		OrderBy(orderBy(movementsTable.Field("stored_at"), params.SearchDirection)).
		Limit(params.NumRecords).
		Format(db.ci)

	logging.Debug(logger, query, args)
	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	it := common.NewIterator(rows, func(r *dbdriver.MovementRecord) error {
		var amount BigInt
		if err := rows.Scan(&r.TxID, &r.EnrollmentID, &r.TokenType, &amount, &r.Status); err != nil {
			return err
		}
		r.Amount = amount.Int
		logger.DebugfContext(ctx, "movement [%s:%s:%s]", r.TxID, r.Status, r.Amount)

		return nil
	})

	return iterators.ReadAllPointers(it)
}

func (db *TransactionStore) QueryTransactions(ctx context.Context, params dbdriver.QueryTransactionsParams, pagination driver3.Pagination) (*driver3.PageIterator[*dbdriver.TransactionRecord], error) {
	transactionsTable, requestsTable := q.Table(db.table.Transactions), q.Table(db.table.Requests)
	query, args := q.Select().
		Fields(
			transactionsTable.Field("tx_id"), common3.FieldName("action_type"), common3.FieldName("sender_eid"),
			common3.FieldName("recipient_eid"), common3.FieldName("token_type"), common3.FieldName("amount"),
			requestsTable.Field("status"), requestsTable.Field("application_metadata"),
			requestsTable.Field("public_metadata"), transactionsTable.Field("stored_at"),
		).
		From(transactionsTable.Join(requestsTable,
			cond.Cmp(transactionsTable.Field("tx_id"), "=", requestsTable.Field("tx_id"))),
		).
		Where(HasTransactionParams(params, transactionsTable)).
		OrderBy(orderBy(transactionsTable.Field("stored_at"), params.SearchDirection)).
		Paginated(pagination).
		FormatPaginated(db.ci, db.pi)

	logging.Debug(logger, query, args)
	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	results := common.NewIterator(rows, func(r *dbdriver.TransactionRecord) error {
		var amount BigInt
		var appMeta []byte
		var pubMeta []byte
		if err := rows.Scan(&r.TxID, &r.ActionType, &r.SenderEID, &r.RecipientEID, &r.TokenType, &amount, &r.Status, &appMeta, &pubMeta, &r.Timestamp); err != nil {
			return err
		}
		r.Amount = amount.Int

		return errors2.Join(
			unmarshal(appMeta, &r.ApplicationMetadata),
			unmarshal(pubMeta, &r.PublicMetadata),
		)
	})

	return &driver3.PageIterator[*dbdriver.TransactionRecord]{
		Items:      results,
		Pagination: pagination,
	}, nil
}

func (db *TransactionStore) GetStatus(ctx context.Context, txID string) (dbdriver.TxStatus, string, error) {
	var status dbdriver.TxStatus
	var statusMessage string
	query, args := q.Select().
		FieldsByName("status", "status_message").
		From(q.Table(db.table.Requests)).
		Where(cond.Eq("tx_id", txID)).
		Format(db.ci)
	logging.Debug(logger, query, txID)

	row := db.readDB.QueryRowContext(ctx, query, args...)
	if err := row.Scan(&status, &statusMessage); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			logger.DebugfContext(ctx, "tried to get status for non-existent tx [%s], returning unknown", txID)

			return dbdriver.Unknown, "", nil
		}

		return dbdriver.Unknown, "", errors.Wrapf(err, "error querying db")
	}

	return status, statusMessage, nil
}

func (db *TransactionStore) Notifier() (dbdriver.TransactionNotifier, error) {
	if db.notifier == nil {
		return nil, storage.ErrNotSupported
	}

	return db.notifier, nil
}

// QueryTokenRequests returns an iterator over the token requests matching the passed params
func (db *TransactionStore) QueryTokenRequests(ctx context.Context, params dbdriver.QueryTokenRequestsParams) (dbdriver.TokenRequestIterator, error) {
	query, args := q.Select().
		FieldsByName("tx_id", "request", "status").
		From(q.Table(db.table.Requests)).
		Where(cond.In("status", params.Statuses...)).
		Format(db.ci)

	logging.Debug(logger, query, args)
	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	// TODO: AF remove r.TokenRequest. Not used

	return common.NewIterator(rows, func(r *dbdriver.TokenRequestRecord) error { return rows.Scan(&r.TxID, &r.TokenRequest, &r.Status) }), nil
}

// AcquireRecoveryLeadership returns a leadership handle for recovery sweeping.
// When no leader factory is configured, leadership is granted locally.
func (db *TransactionStore) AcquireRecoveryLeadership(ctx context.Context, lockID int64) (dbdriver.RecoveryLeadership, bool, error) {
	if db.recoveryLeaderFactory == nil {
		return noopRecoveryLeadership{}, true, nil
	}

	return db.recoveryLeaderFactory(ctx, db.writeDB, lockID)
}

// ClaimPendingTransactions returns a claimed batch of Pending transactions.
// The default SQL implementation is permissive and does not persist recovery claims.
// tx_id and stored_at are projected directly from the requests table — the
// transactions table is no longer joined since it carries no information
// the recovery loop needs and adding it would re-introduce a fan-out by
// movement/output that the caller would have to dedupe by tx_id.
func (db *TransactionStore) ClaimPendingTransactions(ctx context.Context, params dbdriver.RecoveryClaimParams) ([]*dbdriver.RecoveryClaim, error) {
	query, args := q.Select().
		FieldsByName("tx_id", "stored_at").
		From(q.Table(db.table.Requests)).
		Where(cond.And(
			cond.Eq("status", dbdriver.Pending),
			cond.Lt("stored_at", params.OlderThan),
		)).
		OrderBy(q.Asc(common3.FieldName("stored_at"))).
		Limit(params.Limit).
		Format(db.ci)

	logging.Debug(logger, query, args)
	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	results := common.NewIterator(rows, func(r *dbdriver.RecoveryClaim) error {
		return rows.Scan(&r.TxID, &r.StoredAt)
	})

	return iterators.ReadAllPointers(results)
}

// ReleaseRecoveryClaim clears persisted recovery claim metadata and stores the recovery message.
// The default SQL implementation is a no-op.
func (db *TransactionStore) ReleaseRecoveryClaim(context.Context, string, string, string) error {
	return nil
}

func (db *TransactionStore) AddTransactionEndorsementAck(ctx context.Context, txID string, endorser token.Identity, sigma []byte) (err error) {
	logger.DebugfContext(ctx, "adding transaction endorse ack record [%s]", txID)

	now := time.Now().UTC()
	id, err := uuid.GenerateUUID()
	if err != nil {
		return errors.Wrapf(err, "error generating uuid")
	}
	query, args := q.InsertInto(db.table.TransactionEndorseAck).
		Fields("id", "tx_id", "endorser", "sigma", "stored_at").
		Row(id, txID, endorser, sigma, now).
		Format()

	logging.Debug(logger, query, txID, fmt.Sprintf("(%d bytes)", len(endorser)), fmt.Sprintf("(%d bytes)", len(sigma)), now)

	if _, err = db.writeDB.ExecContext(ctx, query, args...); err != nil {
		return ttxDBError(err)
	}

	return
}

func (db *TransactionStore) GetTransactionEndorsementAcks(ctx context.Context, txID string) (map[string][]byte, error) {
	query, args := q.Select().
		FieldsByName("endorser", "sigma").
		From(q.Table(db.table.TransactionEndorseAck)).
		Where(cond.Eq("tx_id", txID)).
		Format(db.ci)
	logging.Debug(logger, query, txID)

	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to query")
	}
	defer Close(rows)
	acks := make(map[string][]byte)
	for rows.Next() {
		var endorser []byte
		var sigma []byte
		if err := rows.Scan(&endorser, &sigma); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// not an error for compatibility with badger.
				logger.DebugfContext(ctx, "tried to get status for non-existent tx [%s], returning unknown", txID)

				continue
			}

			return nil, errors.Wrapf(err, "error querying db")
		}
		acks[token.Identity(endorser).String()] = sigma
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return acks, nil
}

func (db *TransactionStore) Close() error {
	return common2.Close(db.readDB, db.writeDB)
}

func (db *TransactionStore) SetStatus(ctx context.Context, txID string, status dbdriver.TxStatus, message string) error {
	var err error
	if len(message) != 0 {
		query, args := q.Update(db.table.Requests).
			Set("status", status).
			Set("status_message", message).
			Where(cond.Eq("tx_id", txID)).
			Format(db.ci)

		logging.Debug(logger, query, args)
		_, err = db.writeDB.ExecContext(ctx, query, args...)
	} else {
		query, args := q.Update(db.table.Requests).
			Set("status", status).
			Where(cond.Eq("tx_id", txID)).
			Format(db.ci)

		logging.Debug(logger, query, args)
		_, err = db.writeDB.ExecContext(ctx, query, args...)
	}
	if err != nil {
		return errors.Wrapf(err, "error updating tx [%s]", txID)
	}

	return nil
}

func (db *TransactionStore) GetSchema() string {
	return fmt.Sprintf(`
		-- requests
		CREATE TABLE IF NOT EXISTS %s (
			tx_id TEXT NOT NULL PRIMARY KEY,
			request BYTEA NOT NULL,
			status INT NOT NULL,
			status_message TEXT NOT NULL,
			application_metadata JSONB NOT NULL,
			public_metadata JSONB NOT NULL,
			pp_hash BYTEA NOT NULL,
			recovery_claimed_by TEXT,
			recovery_claim_expires_at TIMESTAMP,
			stored_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_status_%s ON %s ( status );
		CREATE INDEX IF NOT EXISTS idx_recovery_claim_%s ON %s ( status, recovery_claim_expires_at, stored_at ) WHERE status = 1;

		-- transactions
		CREATE TABLE IF NOT EXISTS %s (
			id CHAR(36) NOT NULL PRIMARY KEY,
			tx_id TEXT NOT NULL REFERENCES %s,
			action_type INT NOT NULL,
			sender_eid TEXT NOT NULL,
			recipient_eid TEXT NOT NULL,
			token_type TEXT NOT NULL,
			amount NUMERIC(78, 0) NOT NULL,
			stored_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_tx_id_%s ON %s ( tx_id );
		CREATE INDEX IF NOT EXISTS idx_storedat_%s ON %s ( stored_at DESC );
		CREATE INDEX IF NOT EXISTS idx_sender_eid_storedat_%s ON %s ( sender_eid, stored_at DESC );
		CREATE INDEX IF NOT EXISTS idx_recipient_eid_storedat_%s ON %s ( recipient_eid, stored_at DESC );

		-- movements
		CREATE TABLE IF NOT EXISTS %s (
			id CHAR(36) NOT NULL PRIMARY KEY,
			tx_id TEXT NOT NULL REFERENCES %s,
			enrollment_id TEXT NOT NULL,
			token_type TEXT NOT NULL,
			amount NUMERIC(78, 0) NOT NULL,
			stored_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_tx_id_%s ON %s ( tx_id );
		CREATE INDEX IF NOT EXISTS idx_eid_storedat_%s ON %s ( enrollment_id, stored_at );

		-- tea
		CREATE TABLE IF NOT EXISTS %s (
			id CHAR(36) NOT NULL PRIMARY KEY,
			tx_id TEXT NOT NULL,
			endorser BYTEA NOT NULL,
            sigma BYTEA NOT NULL,
			stored_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_tx_id_%s ON %s ( tx_id );
		`,
		db.table.Requests, db.table.Requests, db.table.Requests, db.table.Requests, db.table.Requests,
		db.table.Transactions, db.table.Requests, db.table.Transactions, db.table.Transactions, db.table.Transactions, db.table.Transactions, db.table.Transactions, db.table.Transactions, db.table.Transactions, db.table.Transactions,
		db.table.Movements, db.table.Requests, db.table.Movements, db.table.Movements, db.table.Movements, db.table.Movements,
		db.table.TransactionEndorseAck, db.table.TransactionEndorseAck, db.table.TransactionEndorseAck,
	)
}

func (db *TransactionStore) NewTransactionStoreTransaction() (dbdriver.TransactionStoreTransaction, error) {
	txn, err := db.writeDB.Begin()
	if err != nil {
		return nil, err
	}

	return &TransactionStoreTransaction{
		txn:   txn,
		table: &db.table,
		ci:    db.ci,
	}, nil
}

type TransactionStoreTransaction struct {
	txn   *sql.Tx
	table *transactionTables
	ci    common3.CondInterpreter
}

func (w *TransactionStoreTransaction) Impl() dbdriver.TransactionImpl {
	return w.txn
}

func (w *TransactionStoreTransaction) Commit() error {
	if err := w.txn.Commit(); err != nil {
		return fmt.Errorf("could not commit transaction: %w", err)
	}
	w.txn = nil

	return nil
}

func (w *TransactionStoreTransaction) Rollback() {
	if w.txn == nil {
		logging.Debug(logger, "nothing to roll back")

		return
	}
	if err := w.txn.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		logger.Errorf("error rolling back (ignoring...): %s", err.Error())
	}
	w.txn = nil
}

func (w *TransactionStoreTransaction) AddTransaction(ctx context.Context, rs ...dbdriver.TransactionRecord) error {
	if w.txn == nil {
		return errors.New("no db transaction in progress")
	}
	rows := make([]common3.Tuple, len(rs))
	for i, r := range rs {
		logger.DebugfContext(ctx, "adding transaction record [%s:%d,%s:%s:%s:%s]", r.TxID, r.ActionType, r.TokenType, r.SenderEID, r.RecipientEID, r.Amount)
		id, err := uuid.GenerateUUID()
		if err != nil {
			return errors.Wrapf(err, "error generating uuid")
		}
		if r.Amount.BitLen() > maxAmountBits {
			return errors.Errorf("amount [%s] exceeds maximum supported size of %d bits", r.Amount, maxAmountBits)
		}
		rows[i] = common3.Tuple{id, r.TxID, int(r.ActionType), r.SenderEID, r.RecipientEID, r.TokenType, r.Amount.String(), r.Timestamp.UTC()}
	}

	query, args := q.InsertInto(w.table.Transactions).
		Fields("id", "tx_id", "action_type", "sender_eid", "recipient_eid", "token_type", "amount", "stored_at").
		Rows(rows).
		Format()
	logging.Debug(logger, query, args)

	_, err := w.txn.ExecContext(ctx, query, args...)

	return ttxDBError(err)
}

func (w *TransactionStoreTransaction) AddTokenRequest(ctx context.Context, txID string, tr []byte, applicationMetadata, publicMetadata map[string][]byte, ppHash driver2.PPHash) error {
	logger.DebugfContext(ctx, "adding token request [%s]", txID)
	if w.txn == nil {
		return errors.New("no db transaction in progress")
	}
	if applicationMetadata == nil {
		applicationMetadata = make(map[string][]byte)
	}
	ja, err := marshal(applicationMetadata)
	if err != nil {
		return errors.New("error marshaling application metadata")
	}
	if publicMetadata == nil {
		publicMetadata = make(map[string][]byte)
	}
	jp, err := marshal(publicMetadata)
	if err != nil {
		return errors.New("error marshaling application metadata")
	}

	query, args := q.InsertInto(w.table.Requests).
		Fields("tx_id", "request", "status", "status_message", "application_metadata", "public_metadata", "pp_hash", "stored_at").
		Row(txID, tr, dbdriver.Pending, "", ja, jp, ppHash, time.Now().UTC()).
		Format()
	logging.Debug(logger, query, txID, fmt.Sprintf("(%d bytes)", len(tr)), len(applicationMetadata), len(publicMetadata), len(ppHash))

	_, err = w.txn.ExecContext(ctx, query, args...)

	return ttxDBError(err)
}

func (w *TransactionStoreTransaction) AddMovement(ctx context.Context, rs ...dbdriver.MovementRecord) error {
	if w.txn == nil {
		return errors.New("no db transaction in progress")
	}
	if len(rs) == 0 {
		// nothing to do here

		return nil
	}

	now := time.Now().UTC()
	rows := make([]common3.Tuple, len(rs))
	for i, r := range rs {
		logger.DebugfContext(ctx, "adding movement record [%s]", r)
		id, err := uuid.GenerateUUID()
		if err != nil {
			return errors.Wrapf(err, "error generating uuid")
		}
		if r.Amount.BitLen() > maxAmountBits {
			return errors.Errorf("amount [%s] exceeds maximum supported size of %d bits", r.Amount, maxAmountBits)
		}
		rows[i] = common3.Tuple{id, r.TxID, r.EnrollmentID, r.TokenType, r.Amount.String(), now}
	}

	query, args := q.InsertInto(w.table.Movements).
		Fields("id", "tx_id", "enrollment_id", "token_type", "amount", "stored_at").
		Rows(rows).
		Format()
	logging.Debug(logger, query, args)

	_, err := w.txn.ExecContext(ctx, query, args...)

	return ttxDBError(err)
}

func (w *TransactionStoreTransaction) SetStatus(ctx context.Context, txID string, status dbdriver.TxStatus, message string) error {
	var err error
	if len(message) != 0 {
		query, args := q.Update(w.table.Requests).
			Set("status", status).
			Set("status_message", message).
			Where(cond.Eq("tx_id", txID)).
			Format(w.ci)

		logging.Debug(logger, query, args)
		_, err = w.txn.ExecContext(ctx, query, args...)
	} else {
		query, args := q.Update(w.table.Requests).
			Set("status", status).
			Where(cond.Eq("tx_id", txID)).
			Format(w.ci)

		logging.Debug(logger, query, args)
		_, err = w.txn.ExecContext(ctx, query, args...)
	}
	if err != nil {
		return errors.Wrapf(err, "error updating tx [%s]", txID)
	}

	return nil
}

func ttxDBError(err error) error {
	if err == nil {
		return nil
	}
	logger.Error(err)
	e := strings.ToLower(err.Error())
	if strings.Contains(e, "foreign key constraint") {
		return dbdriver.ErrTokenRequestDoesNotExist
	}

	return err
}

func orderBy(f common3.Serializable, direction dbdriver.SearchDirection) _select.OrderBy {
	if direction == dbdriver.FromBeginning {
		return q.Asc(f)
	}

	return q.Desc(f)
}

func marshal(in map[string][]byte) (string, error) {
	if b, err := json.Marshal(in); err != nil {
		return "", err
	} else {
		return string(b), err
	}
}

func unmarshal(in []byte, out *map[string][]byte) error {
	if len(in) == 0 {
		return nil
	}

	return json.Unmarshal(in, out)
}

type noopRecoveryLeadership struct{}

func (noopRecoveryLeadership) Close() error {
	return nil
}
