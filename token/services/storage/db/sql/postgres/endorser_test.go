/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"database/sql"
	"testing"

	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

func mockEndorserStore(db *sql.DB) *common2.EndorserStore {
	store, _ := common2.NewEndorserStore(db, db, common2.TableNames{
		Movements:             "MOVEMENTS",
		Transactions:          "TRANSACTIONS",
		Requests:              "REQUESTS",
		Validations:           "VALIDATIONS",
		ValidationFailures:    "VALIDATION_FAILURES",
		TransactionEndorseAck: "TRANSACTION_ENDORSE_ACK",
	}, NewConditionInterpreter(), NewPaginationInterpreter())

	return store
}

func TestQueryValidationsEndorser(t *testing.T) {
	common2.TestQueryValidations(t, mockEndorserStore)
}

func TestGetStatusEndorser(t *testing.T) {
	common2.TestGetStatusEndorser(t, mockEndorserStore)
}

func TestAWAddValidationRecordEndorser(t *testing.T) {
	common2.TestAWAddValidationRecord(t, mockEndorserStore)
}

func TestSetStatusEndorser(t *testing.T) {
	common2.TestSetStatusEndorser(t, mockEndorserStore)
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

func TestDeleteValidationRecordEndorser(t *testing.T) {
	common2.TestDeleteValidationRecordEndorser(t, mockEndorserStore)
}
