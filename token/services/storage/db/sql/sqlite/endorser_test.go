/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"context"
	"database/sql"
	driver2 "database/sql/driver"
	"fmt"
	"path"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/onsi/gomega"
	"github.com/stretchr/testify/require"
)

func mockEndorserStore(db *sql.DB) *common2.EndorserStore {
	store, _ := common2.NewEndorserStore(db, db, common2.TableNames{
		Movements:             "MOVEMENTS",
		Transactions:          "TRANSACTIONS",
		Requests:              "REQUESTS",
		Validations:           "VALIDATIONS",
		TransactionEndorseAck: "TRANSACTION_ENDORSE_ACK",
	}, NewConditionInterpreter(), NewPaginationInterpreter())

	return store
}

func TestQueryValidationsEndorser(t *testing.T) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	timeFrom := time.Date(2025, time.June, 8, 10, 0, 0, 0, time.UTC)
	timeTo := time.Date(2025, time.June, 9, 10, 0, 0, 0, time.UTC)
	record := driver.ValidationRecord{
		TxID:         "1234",
		TokenRequest: []byte("some request"),
		Timestamp:    timeFrom,
	}
	output := []driver2.Value{
		record.TxID, record.TokenRequest, nil, record.Timestamp,
	}

	query := "SELECT VALIDATIONS.tx_id, VALIDATIONS.request, VALIDATIONS.metadata, VALIDATIONS.stored_at " +
		"FROM VALIDATIONS " +
		"WHERE \\(\\(VALIDATIONS.stored_at >= \\$1\\) AND \\(VALIDATIONS.stored_at <= \\$2\\)\\) AND \\(VALIDATIONS.rejected = \\$3\\)"

	mockDB.
		ExpectQuery(query).
		WithArgs(timeFrom, timeTo, false).
		WillReturnRows(mockDB.NewRows([]string{"tx_id", "request", "metadata", "stored_at"}).AddRow(output...))

	it, err := mockEndorserStore(db).QueryValidations(context.Background(),
		driver.QueryValidationRecordsParams{
			From: &timeFrom,
			To:   &timeTo,
		},
	)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	records, err := iterators.ReadAllValues(it)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(records).To(gomega.ConsistOf(record))
}

func TestGetStatusEndorser(t *testing.T) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	input := "1234"
	output := []driver2.Value{3, "some_message"}

	mockDB.
		ExpectQuery("SELECT status, status_message FROM VALIDATIONS WHERE \\(tx_id = \\$1\\) AND \\(rejected = \\$2\\)").
		WithArgs(input, false).
		WillReturnRows(mockDB.NewRows([]string{"status", "status_message"}).AddRow(output...))

	status, statusMessage, err := mockEndorserStore(db).GetStatus(context.Background(), input)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(status).To(gomega.Equal(output[0]))
	gomega.Expect(statusMessage).To(gomega.Equal(output[1]))
}

func TestAWAddValidationRecordEndorser(t *testing.T) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	txID := "txid"
	tokenRequest := []byte("token_request_data")
	ppHash := []byte("pp_hash_data")
	now := sqlmock.AnyArg()

	mockDB.ExpectBegin()
	// Expect the conditional upsert into VALIDATIONS table (approved row, rejected = false)
	mockDB.
		ExpectExec("INSERT INTO VALIDATIONS \\(tx_id, request, metadata, pp_hash, rejected, reason, status, status_message, stored_at\\) "+
			"VALUES \\(\\$1, \\$2, \\$3, \\$4, \\$5, \\$6, \\$7, \\$8, \\$9\\) "+
			"ON CONFLICT \\(tx_id\\) DO UPDATE SET request=excluded.request, metadata=excluded.metadata, "+
			"pp_hash=excluded.pp_hash, rejected=excluded.rejected, reason=excluded.reason, status=excluded.status, "+
			"status_message=excluded.status_message, stored_at=excluded.stored_at WHERE VALIDATIONS.rejected = \\$10").
		WithArgs(txID, tokenRequest, "null", ppHash, false, "", driver.Pending, "", now, true).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mockDB.ExpectCommit()

	aw, err := mockEndorserStore(db).NewEndorserStoreTransaction()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(aw.AddValidationRecord(context.Background(), txID, tokenRequest, nil, ppHash)).To(gomega.Succeed())
	gomega.Expect(aw.Commit()).To(gomega.Succeed())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
}

func TestQueryValidationsWithStatuses(t *testing.T) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	timeFrom := time.Date(2025, time.June, 8, 10, 0, 0, 0, time.UTC)
	timeTo := time.Date(2025, time.June, 9, 10, 0, 0, 0, time.UTC)
	record := driver.ValidationRecord{
		TxID:         "1234",
		TokenRequest: []byte("some request"),
		Timestamp:    timeFrom,
	}
	output := []driver2.Value{
		record.TxID, record.TokenRequest, nil, record.Timestamp,
	}

	// The actual query format with parentheses around each IN value
	query := "SELECT VALIDATIONS.tx_id, VALIDATIONS.request, VALIDATIONS.metadata, VALIDATIONS.stored_at " +
		"FROM VALIDATIONS " +
		"WHERE \\(\\(VALIDATIONS.stored_at >= \\$1\\) AND \\(VALIDATIONS.stored_at <= \\$2\\)\\) AND " +
		"\\(VALIDATIONS.rejected = \\$3\\) AND " +
		"\\(\\(status\\) IN \\(\\(\\$4\\), \\(\\$5\\)\\)\\)"

	mockDB.
		ExpectQuery(query).
		WithArgs(timeFrom, timeTo, false, driver.Pending, driver.Confirmed).
		WillReturnRows(mockDB.NewRows([]string{"tx_id", "request", "metadata", "stored_at"}).AddRow(output...))

	it, err := mockEndorserStore(db).QueryValidations(context.Background(),
		driver.QueryValidationRecordsParams{
			From:     &timeFrom,
			To:       &timeTo,
			Statuses: []driver.TxStatus{driver.Pending, driver.Confirmed},
		},
	)

	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	records, err := iterators.ReadAllValues(it)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(records).To(gomega.ConsistOf(record))
}

func TestGetStatusNotFound(t *testing.T) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	input := "nonexistent"

	mockDB.
		ExpectQuery("SELECT status, status_message FROM VALIDATIONS WHERE \\(tx_id = \\$1\\) AND \\(rejected = \\$2\\)").
		WithArgs(input, false).
		WillReturnError(sql.ErrNoRows)

	status, statusMessage, err := mockEndorserStore(db).GetStatus(context.Background(), input)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(status).To(gomega.Equal(driver.Unknown))
	gomega.Expect(statusMessage).To(gomega.Equal(""))
}

func TestAddValidationRecordWithMetadata(t *testing.T) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	txID := "txid"
	tokenRequest := []byte("token_request_data")
	ppHash := []byte("pp_hash_data")
	meta := map[string][]byte{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
	}
	now := sqlmock.AnyArg()

	mockDB.ExpectBegin()
	// Expect the conditional upsert into VALIDATIONS table with metadata (approved row)
	mockDB.
		ExpectExec("INSERT INTO VALIDATIONS \\(tx_id, request, metadata, pp_hash, rejected, reason, status, status_message, stored_at\\) "+
			"VALUES \\(\\$1, \\$2, \\$3, \\$4, \\$5, \\$6, \\$7, \\$8, \\$9\\) "+
			"ON CONFLICT \\(tx_id\\) DO UPDATE SET request=excluded.request, metadata=excluded.metadata, "+
			"pp_hash=excluded.pp_hash, rejected=excluded.rejected, reason=excluded.reason, status=excluded.status, "+
			"status_message=excluded.status_message, stored_at=excluded.stored_at WHERE VALIDATIONS.rejected = \\$10").
		WithArgs(txID, tokenRequest, sqlmock.AnyArg(), ppHash, false, "", driver.Pending, "", now, true).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mockDB.ExpectCommit()

	aw, err := mockEndorserStore(db).NewEndorserStoreTransaction()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(aw.AddValidationRecord(context.Background(), txID, tokenRequest, meta, ppHash)).To(gomega.Succeed())
	gomega.Expect(aw.Commit()).To(gomega.Succeed())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
}

func TestTransactionRollback(t *testing.T) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	mockDB.ExpectBegin()
	mockDB.ExpectRollback()

	aw, err := mockEndorserStore(db).NewEndorserStoreTransaction()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	aw.Rollback()

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
}

func TestCloseEndorserStore(t *testing.T) {
	gomega.RegisterTestingT(t)
	db, _, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	store := mockEndorserStore(db)
	err = store.Close()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
}

func TestCreateSchemaEndorserStore(t *testing.T) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	// Expect the CREATE TABLE statement
	mockDB.ExpectExec("CREATE TABLE IF NOT EXISTS VALIDATIONS").
		WillReturnResult(sqlmock.NewResult(0, 0))

	store := mockEndorserStore(db)
	err = store.CreateSchema()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
}

func TestSetStatusEndorser(t *testing.T) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	txID := "1234"
	status := driver.Confirmed
	message := "message"

	mockDB.ExpectBegin()
	mockDB.ExpectExec("UPDATE VALIDATIONS SET status = \\$1, status_message = \\$2 WHERE \\(tx_id = \\$3\\) AND \\(rejected = \\$4\\)").
		WithArgs(status, message, txID, false).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mockDB.ExpectCommit()

	aw, err := mockEndorserStore(db).NewEndorserStoreTransaction()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(aw.SetStatus(context.Background(), txID, status, message)).To(gomega.Succeed())
	gomega.Expect(aw.Commit()).To(gomega.Succeed())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
}

func TestDeleteValidationRecordEndorser(t *testing.T) {
	common2.TestDeleteValidationRecordEndorser(t, mockEndorserStore)
}

func TestRecordValidationFailureEndorser(t *testing.T) {
	common2.TestRecordValidationFailure(t, mockEndorserStore)
}

func TestRecordValidationFailureRejectsEmptyEndorser(t *testing.T) {
	common2.TestRecordValidationFailureRejectsEmpty(t, mockEndorserStore)
}

func TestQueryValidationFailuresEndorser(t *testing.T) {
	common2.TestQueryValidationFailures(t, mockEndorserStore)
}

// TestValidationFailureRetentionCap checks that only the most recent rejected rows are kept
// and approved rows are never pruned.
func TestValidationFailureRetentionCap(t *testing.T) {
	dsn := "file:" + path.Join(t.TempDir(), "endorser.sqlite") + "?_pragma=busy_timeout(20000)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	tables, err := common2.GetTableNames("")
	require.NoError(t, err)
	store, err := common2.NewEndorserStore(db, db, tables, NewConditionInterpreter(), NewPaginationInterpreter())
	require.NoError(t, err)
	require.NoError(t, store.CreateSchema())
	store.SetMaxValidationFailureRecords(3)

	ctx := t.Context()
	w, err := store.NewEndorserStoreTransaction()
	require.NoError(t, err)
	require.NoError(t, w.AddValidationRecord(ctx, "approved", []byte("good"), nil, tdriver.PPHash("pp")))
	require.NoError(t, w.Commit())

	for i := range 5 {
		require.NoError(t, store.RecordValidationFailure(ctx, fmt.Sprintf("f%d", i), []byte("bad"), nil, "boom"))
	}

	it, err := store.QueryValidationFailures(ctx, driver.QueryValidationFailureRecordsParams{})
	require.NoError(t, err)
	failures, err := iterators.ReadAllPointers(it)
	require.NoError(t, err)
	ids := make([]string, 0, len(failures))
	for _, f := range failures {
		ids = append(ids, f.TxID)
	}
	require.ElementsMatch(t, []string{"f2", "f3", "f4"}, ids, "only the newest rejected rows are kept")

	status, _, err := store.GetStatus(ctx, "approved")
	require.NoError(t, err)
	require.Equal(t, driver.Pending, status, "approved row must survive pruning")
}
