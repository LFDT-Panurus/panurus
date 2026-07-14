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

func TestQueryValidations(t *testing.T, store endorserStoreConstructor, traits QueryConstructorTraits) {
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
	var query string
	if traits.MultipleParenthesis {
		query = "SELECT VALIDATIONS.tx_id, VALIDATIONS.request, VALIDATIONS.metadata, VALIDATIONS.stored_at " +
			"FROM VALIDATIONS " +
			"WHERE \\(\\(VALIDATIONS.stored_at >= \\$1\\) AND \\(VALIDATIONS.stored_at <= \\$2\\)\\)"
	} else {
		query = "SELECT VALIDATIONS.tx_id, VALIDATIONS.request, VALIDATIONS.metadata, VALIDATIONS.stored_at " +
			"FROM VALIDATIONS " +
			"WHERE \\(\\(VALIDATIONS.stored_at >= \\$1\\) AND \\(VALIDATIONS.stored_at <= \\$2\\)\\)"
	}
	mockDB.
		ExpectQuery(query).
		WithArgs(timeFrom, timeTo).
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
		ExpectQuery("SELECT status, status_message FROM VALIDATIONS WHERE tx_id = \\$1").
		WithArgs(input).
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
	// Expect the INSERT into VALIDATIONS table
	mockDB.
		ExpectExec("INSERT INTO VALIDATIONS \\(tx_id, request, metadata, pp_hash, status, status_message, stored_at\\) "+
			"VALUES \\(\\$1, \\$2, \\$3, \\$4, \\$5, \\$6, \\$7\\)").
		WithArgs(txID, tokenRequest, "null", ppHash, driver.Pending, "", now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mockDB.ExpectCommit()

	aw, err := store(db).NewEndorserStoreTransaction()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(aw.AddValidationRecord(t.Context(), txID, tokenRequest, nil, ppHash)).To(gomega.Succeed())
	gomega.Expect(aw.Commit()).To(gomega.Succeed())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
}

func TestSetStatusEndorser(t *testing.T, store endorserStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	txID := "1234"
	status := driver.Confirmed
	message := "message"

	mockDB.ExpectBegin()
	mockDB.ExpectExec("UPDATE VALIDATIONS SET status = \\$1, status_message = \\$2 WHERE tx_id = \\$3").
		WithArgs(status, message, txID).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mockDB.ExpectCommit()

	aw, err := store(db).NewEndorserStoreTransaction()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(aw.SetStatus(t.Context(), txID, status, message)).To(gomega.Succeed())
	gomega.Expect(aw.Commit()).To(gomega.Succeed())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
}
