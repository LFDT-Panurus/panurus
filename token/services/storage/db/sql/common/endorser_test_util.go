/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"database/sql"
	driver2 "database/sql/driver"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/onsi/gomega"
)

type endorserStoreConstructor func(*sql.DB) *EndorserStore

func TestQueryValidations(t *testing.T, store endorserStoreConstructor) {
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

	it, err := store(db).QueryValidations(t.Context(),
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

func TestGetStatusEndorser(t *testing.T, store endorserStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	input := "1234"
	output := []driver2.Value{3, "some_message"}

	mockDB.
		ExpectQuery("SELECT status, status_message FROM VALIDATIONS WHERE \\(tx_id = \\$1\\) AND \\(rejected = \\$2\\)").
		WithArgs(input, false).
		WillReturnRows(mockDB.NewRows([]string{"status", "status_message"}).AddRow(output...))

	status, statusMessage, err := store(db).GetStatus(t.Context(), input)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(status).To(gomega.Equal(output[0]))
	gomega.Expect(statusMessage).To(gomega.Equal(output[1]))
}

func TestAWAddValidationRecord(t *testing.T, store endorserStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	txID := "txid"
	tokenRequest := []byte("token_request_data")
	ppHash := []byte("pp_hash_data")
	now := sqlmock.AnyArg()

	mockDB.ExpectBegin()
	// Expect the conditional upsert into VALIDATIONS table (approved row, rejected = false),
	// overwriting only a prior rejected row via ON CONFLICT ... WHERE rejected = true.
	mockDB.
		ExpectExec("INSERT INTO VALIDATIONS \\(tx_id, request, metadata, pp_hash, rejected, reason, status, status_message, stored_at\\) "+
			"VALUES \\(\\$1, \\$2, \\$3, \\$4, \\$5, \\$6, \\$7, \\$8, \\$9\\) "+
			"ON CONFLICT \\(tx_id\\) DO UPDATE SET request=excluded.request, metadata=excluded.metadata, "+
			"pp_hash=excluded.pp_hash, rejected=excluded.rejected, reason=excluded.reason, status=excluded.status, "+
			"status_message=excluded.status_message, stored_at=excluded.stored_at WHERE VALIDATIONS.rejected = \\$10").
		WithArgs(txID, tokenRequest, "null", ppHash, false, "", driver.Pending, "", now, true).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mockDB.ExpectCommit()

	aw, err := store(db).NewEndorserStoreTransaction()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(aw.AddValidationRecord(t.Context(), txID, tokenRequest, nil, ppHash)).To(gomega.Succeed())
	gomega.Expect(aw.Commit()).To(gomega.Succeed())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
}

func TestDeleteValidationRecordEndorser(t *testing.T, store endorserStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	txID := "txid"

	mockDB.ExpectBegin()
	mockDB.ExpectExec("DELETE FROM VALIDATIONS WHERE tx_id = \\$1").
		WithArgs(txID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mockDB.ExpectCommit()

	aw, err := store(db).NewEndorserStoreTransaction()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(aw.DeleteValidationRecord(t.Context(), txID)).To(gomega.Succeed())
	gomega.Expect(aw.Commit()).To(gomega.Succeed())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
}

func TestRecordValidationFailure(t *testing.T, store endorserStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	txID := "txid"
	tokenRequest := []byte("malformed_request")
	ppHash := []byte("pp_hash_data")
	reason := "failed to verify token request: boom"
	now := sqlmock.AnyArg()

	// Direct conditional upsert into the shared VALIDATIONS table as a rejected row (no
	// surrounding BEGIN/COMMIT). ON CONFLICT ... WHERE rejected = true overwrites a prior
	// rejection but never an approved row; metadata is the JSON null the approved path stores
	// for a nil map.
	mockDB.
		ExpectExec("INSERT INTO VALIDATIONS \\(tx_id, request, metadata, pp_hash, rejected, reason, status, status_message, stored_at\\) "+
			"VALUES \\(\\$1, \\$2, \\$3, \\$4, \\$5, \\$6, \\$7, \\$8, \\$9\\) "+
			"ON CONFLICT \\(tx_id\\) DO UPDATE SET request=excluded.request, pp_hash=excluded.pp_hash, "+
			"reason=excluded.reason, stored_at=excluded.stored_at WHERE VALIDATIONS.rejected = \\$10").
		WithArgs(txID, tokenRequest, "null", ppHash, true, reason, driver.Unknown, "", now, true).
		WillReturnResult(sqlmock.NewResult(0, 1))

	gomega.Expect(store(db).RecordValidationFailure(t.Context(), txID, tokenRequest, ppHash, reason)).To(gomega.Succeed())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
}

func TestRecordValidationFailureRejectsEmpty(t *testing.T, store endorserStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, _, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	// An empty tx id or empty request is refused before any query is issued, so no
	// expectations are registered on the mock.
	gomega.Expect(store(db).RecordValidationFailure(t.Context(), "", []byte("req"), []byte("pp"), "reason")).ToNot(gomega.Succeed())
	gomega.Expect(store(db).RecordValidationFailure(t.Context(), "txid", nil, []byte("pp"), "reason")).ToNot(gomega.Succeed())
}

func TestQueryValidationFailures(t *testing.T, store endorserStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	timeFrom := time.Date(2025, time.June, 8, 10, 0, 0, 0, time.UTC)
	timeTo := time.Date(2025, time.June, 9, 10, 0, 0, 0, time.UTC)
	record := driver.ValidationFailureRecord{
		TxID:         "1234",
		TokenRequest: []byte("bad request"),
		PPHash:       []byte("pp_hash_data"),
		Reason:       "boom",
		Timestamp:    timeFrom,
	}
	output := []driver2.Value{
		record.TxID, record.TokenRequest, record.PPHash, record.Reason, record.Timestamp,
	}
	query := "SELECT VALIDATIONS.tx_id, VALIDATIONS.request, VALIDATIONS.pp_hash, VALIDATIONS.reason, VALIDATIONS.stored_at " +
		"FROM VALIDATIONS " +
		"WHERE \\(\\(VALIDATIONS.stored_at >= \\$1\\) AND \\(VALIDATIONS.stored_at <= \\$2\\)\\) AND \\(VALIDATIONS.rejected = \\$3\\)"
	mockDB.
		ExpectQuery(query).
		WithArgs(timeFrom, timeTo, true).
		WillReturnRows(mockDB.NewRows([]string{"tx_id", "request", "pp_hash", "reason", "stored_at"}).AddRow(output...))

	it, err := store(db).QueryValidationFailures(t.Context(),
		driver.QueryValidationFailureRecordsParams{
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

func TestSetStatusEndorser(t *testing.T, store endorserStoreConstructor) {
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

	aw, err := store(db).NewEndorserStoreTransaction()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(aw.SetStatus(t.Context(), txID, status, message)).To(gomega.Succeed())
	gomega.Expect(aw.Commit()).To(gomega.Succeed())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
}
