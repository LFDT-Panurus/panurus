/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"
	"time"

	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"

	driver2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
)

func TestTransactionSql(t *testing.T) {
	now := time.Now().Local().UTC()
	lastYear := now.AddDate(-1, 0, 0)
	testCases := []struct {
		name         string
		params       driver2.QueryTransactionsParams
		expectedArgs []common2.Param
		expectedSql  string
	}{
		{
			name:         "No params",
			params:       driver2.QueryTransactionsParams{},
			expectedSql:  "1 = 1",
			expectedArgs: []common2.Param{},
		},
		{
			name: "Only confirmed",
			params: driver2.QueryTransactionsParams{
				Statuses: []driver2.TxStatus{driver2.Confirmed},
			},
			expectedSql:  "(status = $1)",
			expectedArgs: []common2.Param{driver2.Confirmed},
		},
		{
			name: "Pending or deleted",
			params: driver2.QueryTransactionsParams{
				Statuses: []driver2.TxStatus{driver2.Pending, driver2.Deleted},
			},
			expectedSql:  "((status) IN (($1), ($2)))",
			expectedArgs: []common2.Param{driver2.Pending, driver2.Deleted},
		},
		{
			name: "Confirmed from any (only setting sender should return all)",
			params: driver2.QueryTransactionsParams{
				SenderWallet: "alice",
				Statuses:     []driver2.TxStatus{driver2.Confirmed},
			},
			expectedSql:  "(status = $1)",
			expectedArgs: []common2.Param{driver2.Confirmed},
		},
		{
			name: "Sender OR recipient matches",
			params: driver2.QueryTransactionsParams{
				SenderWallet:    "alice",
				RecipientWallet: "bob",
			},
			expectedSql:  "((sender_eid = $1) OR (recipient_eid = $2))",
			expectedArgs: []common2.Param{"alice", "bob"},
		},
		{
			name: "Sender OR recipient matches, from last year",
			params: driver2.QueryTransactionsParams{
				SenderWallet:    "alice",
				RecipientWallet: "alice",
				From:            &lastYear,
			},
			expectedSql:  "((tbl.stored_at >= $1)) AND ((sender_eid = $2) OR (recipient_eid = $3))",
			expectedArgs: []common2.Param{&lastYear, "alice", "alice"},
		},
		{
			name: "From last year to now",
			params: driver2.QueryTransactionsParams{
				To:   &now,
				From: &lastYear,
			},
			expectedSql:  "((tbl.stored_at >= $1) AND (tbl.stored_at <= $2))",
			expectedArgs: []common2.Param{&lastYear, &now},
		},
		{
			name: "Sender OR recipient matches, specific tx",
			params: driver2.QueryTransactionsParams{
				SenderWallet:    "alice",
				RecipientWallet: "bob",
				IDs:             []string{"transactionID"},
			},
			expectedSql:  "(tbl.tx_id = $1) AND ((sender_eid = $2) OR (recipient_eid = $3))",
			expectedArgs: []common2.Param{"transactionID", "alice", "bob"},
		},
		{
			name: "Sender OR recipient matches, specific tx ids",
			params: driver2.QueryTransactionsParams{
				SenderWallet:    "alice",
				RecipientWallet: "bob",
				IDs:             []string{"transactionID1", "transactionID2", "transactionID3"},
			},
			expectedSql:  "((tbl.tx_id) IN (($1), ($2), ($3))) AND ((sender_eid = $4) OR (recipient_eid = $5))",
			expectedArgs: []common2.Param{"transactionID1", "transactionID2", "transactionID3", "alice", "bob"},
		},
		{
			name: "With Token Types",
			params: driver2.QueryTransactionsParams{
				TokenTypes: []token.Type{"Pineapple"},
			},
			expectedSql:  "(token_type = $1)",
			expectedArgs: []common2.Param{"Pineapple"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualSql, actualArgs := evalCondition(HasTransactionParams(tc.params, q.Table("tbl")))
			assert.Equal(t, tc.expectedSql, actualSql)
			compareArgs(t, tc.expectedArgs, actualArgs)
		})
	}
}

func TestMovementConditions(t *testing.T) {
	testCases := []struct {
		name         string
		params       driver2.QueryMovementsParams
		expectedArgs []common2.Param
		expectedSql  string
	}{
		{
			name: "All",
			params: driver2.QueryMovementsParams{
				MovementDirection: driver2.All,
			},
			expectedSql:  "(status != $1)",
			expectedArgs: []common2.Param{3},
		},
		{
			name: "Max 5",
			params: driver2.QueryMovementsParams{
				NumRecords:        5,
				MovementDirection: driver2.All,
			},
			expectedSql:  "(status != $1)",
			expectedArgs: []common2.Param{3},
		},
		{
			name: "Only enrollment ids",
			params: driver2.QueryMovementsParams{
				EnrollmentIDs:     []string{"eid1", "eid2", "eid3"},
				MovementDirection: driver2.All,
			},
			expectedSql:  "((enrollment_id) IN (($1), ($2), ($3))) AND (status != $4)",
			expectedArgs: []common2.Param{"eid1", "eid2", "eid3", 3},
		},
		{
			name: "Only confirmed",
			params: driver2.QueryMovementsParams{
				TxStatuses:        []driver2.TxStatus{driver2.Confirmed},
				MovementDirection: driver2.All,
			},
			expectedSql:  "(status = $1)",
			expectedArgs: []common2.Param{driver2.Confirmed},
		},
		{
			name: "Pending and deleted",
			params: driver2.QueryMovementsParams{
				TxStatuses:        []driver2.TxStatus{driver2.Pending, driver2.Deleted},
				MovementDirection: driver2.All,
			},
			expectedSql:  "((status) IN (($1), ($2)))",
			expectedArgs: []common2.Param{driver2.Pending, driver2.Deleted},
		},
		{
			name: "Confirmed from alice",
			params: driver2.QueryMovementsParams{
				EnrollmentIDs:     []string{"alice"},
				TxStatuses:        []driver2.TxStatus{driver2.Confirmed},
				MovementDirection: driver2.All,
			},
			expectedSql:  "(enrollment_id = $1) AND (status = $2)",
			expectedArgs: []common2.Param{"alice", driver2.Confirmed},
		},
		{
			name: "Confirmed ABC and XYZ from alice",
			params: driver2.QueryMovementsParams{
				EnrollmentIDs:     []string{"alice"},
				TxStatuses:        []driver2.TxStatus{driver2.Confirmed},
				TokenTypes:        []token.Type{"ABC", "XYZ"},
				MovementDirection: driver2.All,
			},
			expectedSql:  "(enrollment_id = $1) AND ((token_type) IN (($2), ($3))) AND (status = $4)",
			expectedArgs: []common2.Param{"alice", "ABC", "XYZ", driver2.Confirmed},
		},
		{
			name: "Max 5 confirmed ABC and XYZ from alice",
			params: driver2.QueryMovementsParams{
				EnrollmentIDs:     []string{"alice"},
				TxStatuses:        []driver2.TxStatus{driver2.Confirmed},
				TokenTypes:        []token.Type{"ABC", "XYZ"},
				NumRecords:        5,
				MovementDirection: driver2.All,
			},
			expectedSql:  "(enrollment_id = $1) AND ((token_type) IN (($2), ($3))) AND (status = $4)",
			expectedArgs: []common2.Param{"alice", "ABC", "XYZ", driver2.Confirmed},
		},
		{
			name: "Sent XYZ from alice",
			params: driver2.QueryMovementsParams{
				EnrollmentIDs:     []string{"alice"},
				TokenTypes:        []token.Type{"XYZ"},
				MovementDirection: driver2.Sent,
			},
			expectedSql:  "(enrollment_id = $1) AND (token_type = $2) AND (status != $3) AND (amount < $4)",
			expectedArgs: []common2.Param{"alice", "XYZ", 3, 0},
		},
		{
			name: "2 last pending received",
			params: driver2.QueryMovementsParams{
				TxStatuses:        []driver2.TxStatus{driver2.Pending},
				SearchDirection:   driver2.FromLast,
				MovementDirection: driver2.Received,
				NumRecords:        2,
			},
			expectedSql:  "(status = $1) AND (amount > $2)",
			expectedArgs: []common2.Param{driver2.Pending, 0},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualSql, actualArgs := evalCondition(HasMovementsParams(tc.params))
			assert.Equal(t, tc.expectedSql, actualSql)
			compareArgs(t, tc.expectedArgs, actualArgs)
		})
	}
}

func TestTokenSql(t *testing.T) {
	testCases := []struct {
		name         string
		params       driver2.QueryTokenDetailsParams
		expectedArgs []common2.Param
		expectedSql  string
	}{
		{
			name:         "no filter",
			params:       driver2.QueryTokenDetailsParams{},
			expectedSql:  "(owner = $1) AND (is_deleted = $2)",
			expectedArgs: []common2.Param{true, false},
		},
		{
			name: "no filter with deleted",
			params: driver2.QueryTokenDetailsParams{
				IncludeDeleted: true,
			},
			expectedSql:  "(owner = $1)",
			expectedArgs: []common2.Param{true},
		},
		{
			name:         "owner unspent",
			params:       driver2.QueryTokenDetailsParams{WalletID: "me"},
			expectedSql:  "(owner = $1) AND (owner_wallet_id = $2) AND (is_deleted = $3)",
			expectedArgs: []common2.Param{true, "me", false},
		},
		{
			name: "owner with deleted",
			params: driver2.QueryTokenDetailsParams{
				WalletID:       "me",
				IncludeDeleted: true,
			},
			expectedSql:  "(owner = $1) AND (owner_wallet_id = $2)",
			expectedArgs: []common2.Param{true, "me"},
		},
		{
			name: "owner and htlc with deleted",
			params: driver2.QueryTokenDetailsParams{
				WalletID:       "me",
				OwnerType:      "htlc",
				IncludeDeleted: true,
			},
			expectedSql:  "(owner = $1) AND (owner_type = $2) AND (owner_wallet_id = $3)",
			expectedArgs: []common2.Param{true, "htlc", "me"},
		},
		{
			name:         "owner and type",
			params:       driver2.QueryTokenDetailsParams{TokenType: "tok", WalletID: "me"},
			expectedSql:  "(owner = $1) AND (token_type = $2) AND (owner_wallet_id = $3) AND (is_deleted = $4)",
			expectedArgs: []common2.Param{true, "tok", "me", false},
		},
		{
			name: "owner and type and id",
			params: driver2.QueryTokenDetailsParams{
				TokenType: token.Type("tok"),
				WalletID:  "me",
				IDs:       []*token.ID{{TxId: "a", Index: 1}},
			},
			expectedSql:  "(owner = $1) AND (token_type = $2) AND (owner_wallet_id = $3) AND ((tx_id, idx) IN (($4, $5))) AND (is_deleted = $6)",
			expectedArgs: []common2.Param{true, "tok", "me", "a", 1, false},
		},
		{
			name: "type and ids",
			params: driver2.QueryTokenDetailsParams{
				TokenType:      token.Type("tok"),
				IDs:            []*token.ID{{TxId: "a", Index: 1}, {TxId: "b", Index: 2}},
				IncludeDeleted: true,
			},
			expectedSql:  "(owner = $1) AND (token_type = $2) AND ((tx_id, idx) IN (($3, $4), ($5, $6)))",
			expectedArgs: []common2.Param{true, "tok", "a", uint64(1), "b", uint64(2)},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualSql, actualArgs := evalCondition(HasTokenDetails(tc.params, nil))
			assert.Equal(t, tc.expectedSql, actualSql, tc.name)
			compareArgs(t, tc.expectedArgs, actualArgs)
		})
	}
	// with join
	where, args := evalCondition(HasTokenDetails(driver2.QueryTokenDetailsParams{
		IDs:      []*token.ID{{TxId: "a", Index: 1}},
		WalletID: "me",
	}, q.Table("A")))
	assert.Equal(t, "(owner = $1) AND ((wallet_id = $2) OR (owner_wallet_id = $3)) AND ((A.tx_id, A.idx) IN (($4, $5))) AND (is_deleted = $6)", where, "join")
	assert.Len(t, args, 6)
}

func evalCondition(condition cond.Condition) (string, []common2.Param) {
	sb := common2.NewBuilder()
	condition.WriteString(newTestInterpreter(), sb)
	actualSql, actualArgs := sb.Build()

	return actualSql, actualArgs
}

func TestTokenSqlNoJoin(t *testing.T) {
	testCases := []struct {
		name         string
		params       driver2.QueryTokenDetailsParams
		expectedArgs []common2.Param
		expectedSql  string
	}{
		{
			name:         "no filter",
			params:       driver2.QueryTokenDetailsParams{},
			expectedSql:  "(owner = $1) AND (is_deleted = $2)",
			expectedArgs: []common2.Param{true, false},
		},
		{
			name: "no filter with deleted",
			params: driver2.QueryTokenDetailsParams{
				IncludeDeleted: true,
			},
			expectedSql:  "(owner = $1)",
			expectedArgs: []common2.Param{true},
		},
		{
			name:         "owner unspent",
			params:       driver2.QueryTokenDetailsParams{WalletID: "me"},
			expectedSql:  "(owner = $1) AND (owner_wallet_id = $2) AND (is_deleted = $3)",
			expectedArgs: []common2.Param{true, "me", false},
		},
		{
			name: "owner with deleted",
			params: driver2.QueryTokenDetailsParams{
				WalletID:       "me",
				IncludeDeleted: true,
			},
			expectedSql:  "(owner = $1) AND (owner_wallet_id = $2)",
			expectedArgs: []common2.Param{true, "me"},
		},
		{
			name: "owner and htlc with deleted",
			params: driver2.QueryTokenDetailsParams{
				WalletID:       "me",
				OwnerType:      "htlc",
				IncludeDeleted: true,
			},
			expectedSql:  "(owner = $1) AND (owner_type = $2) AND (owner_wallet_id = $3)",
			expectedArgs: []common2.Param{true, "htlc", "me"},
		},
		{
			name:         "owner and type",
			params:       driver2.QueryTokenDetailsParams{TokenType: "tok", WalletID: "me"},
			expectedSql:  "(owner = $1) AND (token_type = $2) AND (owner_wallet_id = $3) AND (is_deleted = $4)",
			expectedArgs: []common2.Param{true, "tok", "me", false},
		},
		{
			name: "owner and type and id",
			params: driver2.QueryTokenDetailsParams{
				TokenType: "tok",
				WalletID:  "me",
				IDs:       []*token.ID{{TxId: "a", Index: 1}},
			},
			expectedSql:  "(owner = $1) AND (token_type = $2) AND (owner_wallet_id = $3) AND ((tx_id, idx) IN (($4, $5))) AND (is_deleted = $6)",
			expectedArgs: []common2.Param{true, "tok", "me", "a", 1, false},
		},
		{
			name: "type and ids",
			params: driver2.QueryTokenDetailsParams{
				TokenType:      "tok",
				IDs:            []*token.ID{{TxId: "a", Index: 1}, {TxId: "b", Index: 2}},
				IncludeDeleted: true,
			},
			expectedSql:  "(owner = $1) AND (token_type = $2) AND ((tx_id, idx) IN (($3, $4), ($5, $6)))",
			expectedArgs: []common2.Param{true, "tok", "a", uint64(1), "b", uint64(2)},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualSql, actualArgs := evalCondition(HasTokenDetails(tc.params, nil))
			assert.Equal(t, tc.expectedSql, actualSql, tc.name)
			compareArgs(t, tc.expectedArgs, actualArgs)
		})
	}
}

func TestIn(t *testing.T) {
	// 0
	w, args := evalCondition(cond.In[string]("enrollment_id"))
	assert.Equal(t, "1 = 1", w)
	assert.Equal(t, []any{}, args)

	// 1
	w, args = evalCondition(cond.In("enrollment_id", "eid1"))
	assert.Equal(t, "enrollment_id = $1", w)
	assert.Equal(t, []any{"eid1"}, args)

	// 3
	w, args = evalCondition(cond.In("enrollment_id", "eid1", "eid2", "eid3"))
	assert.Equal(t, "(enrollment_id) IN (($1), ($2), ($3))", w)
	assert.Equal(t, []any{"eid1", "eid2", "eid3"}, args)
}

func compareArgs(t *testing.T, expected, actual []any) {
	t.Helper()
	assert.Len(t, actual, len(expected))
	// assert.Equal(t, tc.expectedArgs, actualArgs)

	for i := range expected {
		switch expected[i].(type) {
		case *time.Time:
			exp, _ := expected[i].(*time.Time)
			act, _ := actual[i].(time.Time)
			assert.True(t, exp.Equal(act), "timestamps not equal: %v != %v", exp, act)
		default:
			assert.EqualValues(t, expected[i], actual[i])
		}
	}
}
