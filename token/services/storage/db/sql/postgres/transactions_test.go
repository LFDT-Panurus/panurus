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

func mockTransactionsStore(db *sql.DB) *common2.TransactionStore {
	store, _ := common2.NewOwnerTransactionStore(db, db, common2.TableNames{
		Movements:             "MOVEMENTS",
		Transactions:          "TRANSACTIONS",
		Requests:              "REQUESTS",
		Validations:           "VALIDATIONS",
		TransactionEndorseAck: "TRANSACTION_ENDORSE_ACK",
	}, NewConditionInterpreter(), NewPaginationInterpreter())

	return store
}

var queryConstructorTraits = common2.QueryConstructorTraits{
	SupportsIN:          false,
	MultipleParenthesis: true,
}

func TestGetTokenRequest(t *testing.T) {
	common2.TestGetTokenRequest(t, mockTransactionsStore)
}

func TestQueryMovements(t *testing.T) {
	common2.TestQueryMovements(t, mockTransactionsStore, queryConstructorTraits)
}

func TestQueryTransactions(t *testing.T) {
	common2.TestQueryTransactions(t, mockTransactionsStore)
}

func TestGetStatus(t *testing.T) {
	common2.TestGetStatus(t, mockTransactionsStore)
}

func TestQueryTokenRequests(t *testing.T) {
	common2.TestQueryTokenRequests(t, mockTransactionsStore, queryConstructorTraits)
}

func TestGetTransactionEndorsementAcks(t *testing.T) {
	common2.TestGetTransactionEndorsementAcks(t, mockTransactionsStore)
}

func TestAddTransactionEndorsementAck(t *testing.T) {
	common2.TestAddTransactionEndorsementAck(t, mockTransactionsStore)
}

func TestSetStatus(t *testing.T) {
	common2.TestSetStatus(t, mockTransactionsStore)
}

func TestAWAddTransaction(t *testing.T) {
	common2.TestAWAddTransaction(t, mockTransactionsStore)
}

func TestAWAddTokenRequest(t *testing.T) {
	common2.TestAWAddTokenRequest(t, mockTransactionsStore)
}

func TestAWAddMovement(t *testing.T) {
	common2.TestAWAddMovement(t, mockTransactionsStore)
}
