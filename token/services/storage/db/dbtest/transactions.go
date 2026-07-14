/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbtest

import (
	"fmt"
	"math/big"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	driver2 "github.com/LFDT-Panurus/panurus/token/driver"
	driver3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TransactionsTest(t *testing.T, cfgProvider cfgProvider) {
	t.Helper()
	for _, c := range tokenTransactionDBCases {
		driver := cfgProvider(c.Name)
		db, err := driver.NewOwnerTransaction("", c.Name)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(c.Name, func(xt *testing.T) {
			defer utils.IgnoreError(db.Close)
			c.Fn(xt, db)
		})
	}
}

var tokenTransactionDBCases = []struct {
	Name string
	Fn   func(*testing.T, driver3.TokenTransactionStore)
}{
	{"FailsIfRequestDoesNotExist", TFailsIfRequestDoesNotExist},
	{"Status", TStatus},
	{"StoresTimestamp", TStoresTimestamp},
	{"Movements", TMovements},
	{"Transaction", TTransaction},
	{"TokenRequest", TTokenRequest},
	{"AllowsSameTxID", TAllowsSameTxID},
	{"Rollback", TRollback},
	{"TransactionQueries", TTransactionQueries},
	{"TEndorserAcks", TEndorserAcks},
}

func TFailsIfRequestDoesNotExist(t *testing.T, db driver3.TokenTransactionStore) {
	t.Helper()
	ctx := t.Context()
	tx := driver3.TransactionRecord{
		TxID:         "tx1",
		ActionType:   driver3.Transfer,
		SenderEID:    "bob",
		RecipientEID: "alice",
		TokenType:    "magic",
		Amount:       big.NewInt(10),
		Timestamp:    time.Now(),
		Status:       driver3.Pending,
	}
	mv := driver3.MovementRecord{
		TxID:         "tx1",
		EnrollmentID: "alice",
		TokenType:    "magic",
		Amount:       big.NewInt(-10),
		Status:       driver3.Pending,
	}
	w, _ := db.NewTransactionStoreTransaction()
	err := w.AddTransaction(ctx, tx)
	require.Error(t, err)
	require.ErrorIs(t, err, driver3.ErrTokenRequestDoesNotExist)
	w.Rollback()

	// AddValidationRecord now creates the token request itself, so this test is no longer valid
	// The test was checking that AddValidationRecord fails without a pre-existing token request
	// but now AddValidationRecord creates the token request as part of its operation

	w, _ = db.NewTransactionStoreTransaction()
	err = w.AddMovement(ctx, mv)
	require.Error(t, err)
	require.ErrorIs(t, err, driver3.ErrTokenRequestDoesNotExist)
	w.Rollback()
}

func TStatus(t *testing.T, db driver3.TokenTransactionStore) {
	t.Helper()
	ctx := t.Context()
	tx := driver3.TransactionRecord{
		TxID:         "tx1",
		ActionType:   driver3.Transfer,
		SenderEID:    "bob",
		RecipientEID: "alice",
		TokenType:    "magic",
		Amount:       big.NewInt(10),
		Timestamp:    time.Now(),
		Status:       driver3.Pending,
	}
	mv := driver3.MovementRecord{
		TxID:         "tx1",
		EnrollmentID: "alice",
		TokenType:    "magic",
		Amount:       big.NewInt(-10),
		Status:       driver3.Pending,
	}

	w, err := db.NewTransactionStoreTransaction()
	require.NoError(t, err, "begin")
	require.NoError(t, w.AddTokenRequest(ctx, "tx1", []byte("request"), nil, nil, driver2.PPHash("tr")), "add token request")
	require.NoError(t, w.AddTransaction(ctx, tx))
	require.NoError(t, w.AddMovement(ctx, mv))
	require.NoError(t, w.Commit())

	s, mess, err := db.GetStatus(ctx, "tx1")
	require.NoError(t, err, "get status error")
	assert.Equal(t, driver3.Pending, s, "status should be pending after first creation")
	assert.Empty(t, mess)

	txn := getTransactions(t, db, driver3.QueryTransactionsParams{})[0]
	assert.Equal(t, driver3.Pending, txn.Status, "transaction status should be pending")
	mvs, err := db.QueryMovements(ctx, driver3.QueryMovementsParams{})
	require.NoError(t, err, "error getting movements")
	assert.Len(t, mvs, 1)
	assert.Equal(t, driver3.Pending, mvs[0].Status, "movement status should be pending")

	require.NoError(t, db.SetStatus(t.Context(), "tx1", driver3.Confirmed, "message"))
	s, mess, err = db.GetStatus(ctx, "tx1")
	require.NoError(t, err)
	assert.Equal(t, driver3.Confirmed, s, "status should be changed to confirmed")
	assert.Equal(t, "message", mess)

	txn = getTransactions(t, db, driver3.QueryTransactionsParams{})[0]
	assert.Equal(t, driver3.Confirmed, txn.Status, "transaction status should be confirmed")
	mvs, err = db.QueryMovements(ctx, driver3.QueryMovementsParams{})
	require.NoError(t, err, "error getting movements")
	assert.Len(t, mvs, 1)
	assert.Equal(t, driver3.Confirmed, mvs[0].Status, "movement status should be confirmed")
}

func TStoresTimestamp(t *testing.T, db driver3.TokenTransactionStore) {
	t.Helper()
	ctx := t.Context()
	w, err := db.NewTransactionStoreTransaction()
	require.NoError(t, err)
	require.NoError(t, w.AddTokenRequest(ctx, "tx1", []byte("request"), nil, nil, driver2.PPHash("tr")))
	require.NoError(t, w.AddTransaction(ctx, driver3.TransactionRecord{
		TxID:         "tx1",
		ActionType:   driver3.Transfer,
		SenderEID:    "bob",
		RecipientEID: "alice",
		TokenType:    "magic",
		Amount:       big.NewInt(10),
		Timestamp:    time.Now(),
		Status:       driver3.Pending,
	}))
	require.NoError(t, w.Commit())

	now := time.Now()

	// Transaction (timestamp provided)
	txs := getTransactions(t, db, driver3.QueryTransactionsParams{})
	assert.Len(t, txs, 1)
	assert.WithinDuration(t, now, txs[0].Timestamp, 3*time.Second)
}

func TMovements(t *testing.T, db driver3.TokenTransactionStore) {
	t.Helper()
	ctx := t.Context()
	w, err := db.NewTransactionStoreTransaction()
	require.NoError(t, err)
	require.NoError(t, w.AddTokenRequest(ctx, "0", []byte{}, map[string][]byte{}, nil, driver2.PPHash("tr")))
	require.NoError(t, w.AddTokenRequest(ctx, "1", []byte{}, map[string][]byte{}, nil, driver2.PPHash("tr")))
	require.NoError(t, w.AddTokenRequest(ctx, "2", []byte{}, map[string][]byte{}, nil, driver2.PPHash("tr")))
	require.NoError(t, w.AddMovement(ctx, driver3.MovementRecord{
		TxID:         "0",
		EnrollmentID: "alice",
		TokenType:    "magic",
		Amount:       big.NewInt(10),
	}))
	require.NoError(t, w.AddMovement(ctx, driver3.MovementRecord{
		TxID:         "1",
		EnrollmentID: "alice",
		TokenType:    "magic",
		Amount:       big.NewInt(20),
	}))
	require.NoError(t, w.AddMovement(ctx, driver3.MovementRecord{
		TxID:         "2",
		EnrollmentID: "alice",
		TokenType:    "magic",
		Amount:       big.NewInt(-30),
	}))
	require.NoError(t, w.Commit())

	// All pending
	records, err := db.QueryMovements(ctx, driver3.QueryMovementsParams{
		MovementDirection: driver3.All,
		SearchDirection:   driver3.FromLast,
		TxStatuses:        []driver3.TxStatus{driver3.Pending},
	})
	require.NoError(t, err)
	assert.Len(t, records, 3)

	// Received
	records, err = db.QueryMovements(ctx, driver3.QueryMovementsParams{
		TxStatuses:        []driver3.TxStatus{driver3.Pending},
		MovementDirection: driver3.Received,
		NumRecords:        2,
	})
	require.NoError(t, err)
	assert.Len(t, records, 2)

	// NumRecords
	records, err = db.QueryMovements(ctx, driver3.QueryMovementsParams{
		TxStatuses: []driver3.TxStatus{driver3.Pending},
		NumRecords: 1,
	})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	require.NoError(t, db.SetStatus(t.Context(), "2", driver3.Confirmed, "message"))
	records, err = db.QueryMovements(ctx, driver3.QueryMovementsParams{TxStatuses: []driver3.TxStatus{driver3.Pending}, SearchDirection: driver3.FromLast, MovementDirection: driver3.Received, NumRecords: 3})
	require.NoError(t, err)
	assert.Len(t, records, 2)

	// setting same status twice should not change the results
	require.NoError(t, db.SetStatus(t.Context(), "2", driver3.Confirmed, ""))

	records, err = db.QueryMovements(ctx, driver3.QueryMovementsParams{TxStatuses: []driver3.TxStatus{driver3.Confirmed}})
	require.NoError(t, err)
	assert.Len(t, records, 1)
}

func TTransaction(t *testing.T, db driver3.TokenTransactionStore) {
	t.Helper()
	ctx := t.Context()
	var txs []driver3.TransactionRecord

	t0 := time.Now()
	lastYear := t0.AddDate(-1, 0, 0)

	w, err := db.NewTransactionStoreTransaction()
	require.NoError(t, err)
	tr1 := driver3.TransactionRecord{
		TxID:                fmt.Sprintf("tx%d", 99),
		ActionType:          driver3.Transfer,
		SenderEID:           "bob",
		RecipientEID:        "alice",
		TokenType:           "magic",
		Amount:              big.NewInt(10),
		ApplicationMetadata: map[string][]byte{},
		Timestamp:           lastYear,
	}
	require.NoError(t, w.AddTokenRequest(ctx, tr1.TxID, []byte("token request for "+tr1.TxID), map[string][]byte{}, nil, driver2.PPHash("tr")))
	require.NoError(t, w.AddTransaction(ctx, tr1))

	pm := map[string][]byte{"key": []byte("val")}

	for i := range 20 {
		now := time.Now()
		ctx := t.Context()
		tr := driver3.TransactionRecord{
			TxID:         fmt.Sprintf("tx%d", i),
			ActionType:   driver3.Issue,
			SenderEID:    "",
			RecipientEID: "alice",
			TokenType:    "magic",
			Amount:       big.NewInt(10),
			Timestamp:    now,
			ApplicationMetadata: map[string][]byte{
				"this is the first key":  {99, 33, 22, 11},
				"this is the second key": []byte("with some text as the value " + fmt.Sprintf("tx%d", i)),
			},
			PublicMetadata: pm,
		}
		require.NoError(t, w.AddTokenRequest(ctx, tr.TxID, []byte("token request for "+tr.TxID), tr.ApplicationMetadata, pm, driver2.PPHash("tr")))
		require.NoError(t, w.AddTransaction(ctx, tr))
		txs = append(txs, tr)
	}
	require.NoError(t, w.Commit())

	// get one
	one := getTransactions(t, db, driver3.QueryTransactionsParams{IDs: []string{"tx10"}})
	assert.Len(t, one, 1)
	assert.Equal(t, "tx10", one[0].TxID)

	// get all except last year's
	t1 := time.Now().Add(time.Second * 3)
	it, err := db.QueryTransactions(ctx, driver3.QueryTransactionsParams{From: &t0, To: &t1, SearchDirection: driver3.FromBeginning}, nil)
	require.NoError(t, err)
	for _, exp := range txs {
		act, err := it.Items.Next()
		require.NoError(t, err)
		assertTxEqual(t, &exp, act)
	}
	it.Items.Close()

	// get all tx from before the first
	yesterday := t0.AddDate(0, 0, -1).Local().UTC().Truncate(time.Second)
	it, err = db.QueryTransactions(ctx, driver3.QueryTransactionsParams{To: &yesterday}, nil)
	require.NoError(t, err)
	defer it.Items.Close()

	// find 1 transaction from last year
	tr, err := it.Items.Next()
	require.NoError(t, err)
	assertTxEqual(t, &tr1, tr)

	// find no other transactions
	tr, err = it.Items.Next()
	require.NoError(t, err)
	assert.Empty(t, tr)

	// update status
	require.NoError(t, db.SetStatus(t.Context(), "tx2", driver3.Confirmed, "pineapple"))
	require.NoError(t, db.SetStatus(t.Context(), "tx3", driver3.Confirmed, ""))

	status, message, err := db.GetStatus(t.Context(), "tx2")
	require.NoError(t, err)
	assert.Equal(t, driver3.Confirmed, status)
	assert.Equal(t, "pineapple", message)

	records := getTransactions(t, db, driver3.QueryTransactionsParams{Statuses: []driver3.TxStatus{driver3.Pending}})
	assert.Len(t, records, 19, "expect 19 pending")

	records = getTransactions(t, db, driver3.QueryTransactionsParams{Statuses: []driver3.TxStatus{driver3.Confirmed}})
	assert.Len(t, records, 2, "expect 2 confirmed")

	status, _, err = db.GetStatus(ctx, "nonexistenttx")
	require.NoError(t, err, "a non existent transaction should return Unknown status but no error")
	assert.Equal(t, driver3.Unknown, status)

	// exclude to self
	w, err = db.NewTransactionStoreTransaction()
	require.NoError(t, err)
	tr1 = driver3.TransactionRecord{
		TxID:                "1234",
		ActionType:          driver3.Transfer,
		SenderEID:           "alice",
		RecipientEID:        "alice",
		TokenType:           "magic",
		Amount:              big.NewInt(10),
		ApplicationMetadata: map[string][]byte{},
		Timestamp:           lastYear,
	}
	require.NoError(t, w.AddTokenRequest(ctx, tr1.TxID, fmt.Appendf(nil, "token request for %s", tr1.TxID), map[string][]byte{}, nil, driver2.PPHash("tr")))
	require.NoError(t, w.AddTransaction(ctx, tr1))
	require.NoError(t, w.Commit())
	noChange := getTransactions(t, db, driver3.QueryTransactionsParams{ExcludeToSelf: true})
	assert.Len(t, noChange, 21)
	for _, tr := range noChange {
		assert.NotEqual(t, tr.TxID, tr1.TxID, "transaction to self should not be included")
	}
}

const explanation = "transactions [%s]=[%s]"

func assertTxEqual(t *testing.T, exp *driver3.TransactionRecord, act *driver3.TransactionRecord) {
	t.Helper()
	if act == nil {
		t.Errorf("expected tx %q, got nil", exp.TxID)

		return
	}
	expl := fmt.Sprintf(explanation, exp.TxID, act.TxID)
	if exp.TxID != act.TxID {
		t.Errorf("expected tx %q, got %q", exp.TxID, act.TxID)

		return
	}

	assert.Equal(t, exp.TxID, act.TxID, expl)
	assert.Equal(t, exp.ActionType, act.ActionType, expl)
	assert.Equal(t, exp.SenderEID, act.SenderEID, expl)
	assert.Equal(t, exp.RecipientEID, act.RecipientEID, expl)
	assert.Equal(t, exp.TokenType, act.TokenType, expl)
	assert.Equal(t, exp.Amount, act.Amount, expl)
	assert.Equal(t, exp.ApplicationMetadata, act.ApplicationMetadata, expl)
	assert.WithinDuration(t, exp.Timestamp, act.Timestamp, 3*time.Second, expl)
	if len(exp.PublicMetadata) > 0 {
		assert.True(t, reflect.DeepEqual(exp.PublicMetadata, act.PublicMetadata), expl)
	}
}

func TTokenRequest(t *testing.T, db driver3.TokenTransactionStore) {
	t.Helper()
	ctx := t.Context()
	w, err := db.NewTransactionStoreTransaction()
	require.NoError(t, err)
	tr1 := []byte("arbitrary bytes")
	err = w.AddTokenRequest(ctx, "id1", tr1, map[string][]byte{}, nil, []byte("tr"))
	require.NoError(t, err)
	tr2 := []byte("arbitrary bytes 2")
	err = w.AddTokenRequest(ctx, "id2", tr2, map[string][]byte{}, nil, []byte("tr"))
	require.NoError(t, err)
	require.NoError(t, w.Commit())
	require.NoError(t, db.SetStatus(t.Context(), "id2", driver3.Confirmed, ""))

	trq, err := db.GetTokenRequest(ctx, "id1")
	require.NoError(t, err)
	assert.Equal(t, tr1, trq)
	trq, err = db.GetTokenRequest(ctx, "id2")
	require.NoError(t, err)
	assert.Equal(t, tr2, trq)

	// iterate over all
	it, err := db.QueryTokenRequests(ctx, driver3.QueryTokenRequestsParams{})
	require.NoError(t, err)
	counter := 0
	for {
		record, err := it.Next()
		require.NoError(t, err)
		if record == nil {
			break
		}
		if record.TxID == "id1" {
			assert.Equal(t, tr1, record.TokenRequest)
			assert.Equal(t, driver3.Pending, record.Status)
			counter++

			continue
		}
		if record.TxID == "id2" {
			assert.Equal(t, tr2, record.TokenRequest)
			assert.Equal(t, driver3.Confirmed, record.Status)
			counter++

			continue
		}
	}
	assert.Equal(t, 2, counter)
	it.Close()

	// iterate over pending and confirmed
	it, err = db.QueryTokenRequests(ctx, driver3.QueryTokenRequestsParams{Statuses: []driver3.TxStatus{driver3.Confirmed, driver3.Pending}})
	require.NoError(t, err)
	counter = 0
	for {
		record, err := it.Next()
		require.NoError(t, err)
		if record == nil {
			break
		}
		if record.TxID == "id1" {
			assert.Equal(t, tr1, record.TokenRequest)
			assert.Equal(t, driver3.Pending, record.Status)
			counter++

			continue
		}
		if record.TxID == "id2" {
			assert.Equal(t, tr2, record.TokenRequest)
			assert.Equal(t, driver3.Confirmed, record.Status)
			counter++

			continue
		}
	}
	assert.Equal(t, 2, counter)
	it.Close()

	// iterator over confirmed
	it, err = db.QueryTokenRequests(ctx, driver3.QueryTokenRequestsParams{Statuses: []driver3.TxStatus{driver3.Confirmed}})
	require.NoError(t, err)
	record, err := it.Next()
	require.NoError(t, err)
	assert.Equal(t, tr2, record.TokenRequest)
	assert.Equal(t, driver3.Confirmed, record.Status)
	record, err = it.Next()
	require.NoError(t, err)
	assert.Nil(t, record)
	it.Close()

	// iterator over pending
	it, err = db.QueryTokenRequests(ctx, driver3.QueryTokenRequestsParams{Statuses: []driver3.TxStatus{driver3.Pending}})
	require.NoError(t, err)
	record, err = it.Next()
	require.NoError(t, err)
	assert.Equal(t, tr1, record.TokenRequest)
	assert.Equal(t, driver3.Pending, record.Status)
	record, err = it.Next()
	require.NoError(t, err)
	assert.Nil(t, record)
	it.Close()

	// iterator over deleted
	it, err = db.QueryTokenRequests(ctx, driver3.QueryTokenRequestsParams{Statuses: []driver3.TxStatus{driver3.Deleted}})
	require.NoError(t, err)
	record, err = it.Next()
	require.NoError(t, err)
	assert.Nil(t, record)
	it.Close()

	// Batch GetTokenRequests: present, missing, and empty-input paths.
	batch, err := db.GetTokenRequests(ctx, []string{"id1", "id2", "missing"})
	require.NoError(t, err)
	assert.Len(t, batch, 2, "missing tx ids must not occupy a map entry")
	assert.Equal(t, tr1, batch["id1"])
	assert.Equal(t, tr2, batch["id2"])
	_, ok := batch["missing"]
	assert.False(t, ok)

	empty, err := db.GetTokenRequests(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, empty, "nil input must return an empty map without querying")

	empty, err = db.GetTokenRequests(ctx, []string{})
	require.NoError(t, err)
	assert.Empty(t, empty, "empty-slice input must return an empty map without querying")
}

func TAllowsSameTxID(t *testing.T, db driver3.TokenTransactionStore) {
	t.Helper()
	ctx := t.Context()

	// bob sends 10 to alice
	tr1 := driver3.TransactionRecord{
		TxID:                "1",
		ActionType:          driver3.Transfer,
		SenderEID:           "bob",
		RecipientEID:        "alice",
		TokenType:           "magic",
		ApplicationMetadata: map[string][]byte{},
		Amount:              big.NewInt(10),
		Timestamp:           time.Now(),
	}
	// 1 is sent back to bobs wallet as change
	tr2 := driver3.TransactionRecord{
		TxID:                "1",
		ActionType:          driver3.Transfer,
		SenderEID:           "bob",
		RecipientEID:        "bob",
		TokenType:           "magic",
		ApplicationMetadata: map[string][]byte{},
		Amount:              big.NewInt(1),
		Timestamp:           time.Now().Add(2 * time.Second),
	}
	w, err := db.NewTransactionStoreTransaction()
	require.NoError(t, err)
	require.NoError(t, w.AddTokenRequest(ctx, tr1.TxID, []byte{}, map[string][]byte{}, nil, driver2.PPHash("tr")))
	require.NoError(t, w.AddTransaction(ctx, tr1))
	require.NoError(t, w.AddTransaction(ctx, tr2))
	require.NoError(t, w.Commit())

	// Explicit ASC: tr1 first, tr2 second
	txs := getTransactions(t, db, driver3.QueryTransactionsParams{SearchDirection: driver3.FromBeginning})
	assert.Len(t, txs, 2)
	assertTxEqual(t, &tr1, txs[0])
	assertTxEqual(t, &tr2, txs[1])
}

func TRollback(t *testing.T, db driver3.TokenTransactionStore) {
	t.Helper()
	ctx := t.Context()
	w, err := db.NewTransactionStoreTransaction()
	require.NoError(t, err)
	require.NoError(t, w.AddTokenRequest(ctx, "1", []byte("arbitrary bytes"), map[string][]byte{}, nil, driver2.PPHash("tr")))

	mr1 := driver3.MovementRecord{
		TxID:         "1",
		EnrollmentID: "bob",
		TokenType:    "magic",
		Amount:       big.NewInt(10),
		Status:       driver3.Pending,
	}
	tr1 := driver3.TransactionRecord{
		TxID:         "1",
		ActionType:   driver3.Transfer,
		SenderEID:    "bob",
		RecipientEID: "alice",
		TokenType:    "magic",
		Amount:       big.NewInt(10),
		Timestamp:    time.Now().Local().UTC(),
		Status:       driver3.Pending,
	}
	require.NoError(t, w.AddTransaction(ctx, tr1))
	require.NoError(t, w.AddMovement(ctx, mr1))
	w.Rollback()
	assert.Empty(t, getTransactions(t, db, driver3.QueryTransactionsParams{}))
	mvm, err := db.QueryMovements(ctx, driver3.QueryMovementsParams{})
	require.NoError(t, err)
	assert.Empty(t, mvm)
}

func TTransactionQueries(t *testing.T, db driver3.TokenTransactionStore) {
	t.Helper()
	now := time.Now()
	justBefore := now.Add(-time.Millisecond)
	justAfter := now.Add(time.Millisecond)
	lastYear := now.AddDate(-1, 0, 0)
	ctx := t.Context()

	tr := []driver3.TransactionRecord{
		{
			TxID:         "1",
			ActionType:   driver3.Issue,
			SenderEID:    "",
			RecipientEID: "bob",
			TokenType:    "magic",
			Amount:       big.NewInt(10),
			Timestamp:    now,
			Status:       driver3.Confirmed,
		},
		{
			TxID:         "2",
			ActionType:   driver3.Transfer,
			SenderEID:    "bob",
			RecipientEID: "alice",
			TokenType:    "magic",
			Amount:       big.NewInt(10),
			Timestamp:    justBefore.Add(-time.Millisecond),
			Status:       driver3.Confirmed,
		},
		{
			TxID:         "2",
			ActionType:   driver3.Transfer,
			SenderEID:    "bob",
			RecipientEID: "bob",
			TokenType:    "magic",
			Amount:       big.NewInt(1),
			Timestamp:    now,
			Status:       driver3.Confirmed,
		},
		{
			TxID:         "3",
			ActionType:   driver3.Transfer,
			SenderEID:    "bob",
			RecipientEID: "alice",
			TokenType:    "magic",
			Amount:       big.NewInt(1),
			Timestamp:    now,
			Status:       driver3.Pending,
		},
		{
			TxID:         "4",
			ActionType:   driver3.Transfer,
			SenderEID:    "bob",
			RecipientEID: "alice",
			TokenType:    "magic",
			Amount:       big.NewInt(1),
			Timestamp:    now,
			Status:       driver3.Confirmed,
		},
		{
			TxID:         "5",
			ActionType:   driver3.Transfer,
			SenderEID:    "bob",
			RecipientEID: "alice",
			TokenType:    "magic",
			Amount:       big.NewInt(1),
			Timestamp:    now,
			Status:       driver3.Deleted,
		},
		{
			TxID:         "6",
			ActionType:   driver3.Transfer,
			SenderEID:    "alice",
			RecipientEID: "bob",
			TokenType:    "abc",
			Amount:       big.NewInt(1),
			Timestamp:    now,
			Status:       driver3.Confirmed,
		},
		{
			TxID:         "7",
			ActionType:   driver3.Transfer,
			SenderEID:    "alice",
			RecipientEID: "bob",
			TokenType:    "abc",
			Amount:       big.NewInt(1),
			Timestamp:    justBefore,
			Status:       driver3.Confirmed,
		},
		{
			TxID:         "7",
			ActionType:   driver3.Transfer,
			SenderEID:    "alice",
			RecipientEID: "dan",
			TokenType:    "abc",
			Amount:       big.NewInt(1),
			Timestamp:    now.AddDate(0, 0, -1),
			Status:       driver3.Confirmed,
		},
		{
			TxID:         "8",
			ActionType:   driver3.Redeem,
			SenderEID:    "dan",
			RecipientEID: "carlos",
			TokenType:    "abc",
			Amount:       big.NewInt(1),
			Timestamp:    now.AddDate(0, 0, -1),
			Status:       driver3.Confirmed,
		},
		{
			TxID:         "9",
			ActionType:   driver3.Transfer,
			SenderEID:    "alice",
			RecipientEID: "dan",
			TokenType:    "abc",
			Amount:       big.NewInt(1),
			Timestamp:    now.AddDate(0, 0, 1),
			Status:       driver3.Confirmed,
		},
		{
			TxID:         "10",
			ActionType:   driver3.Redeem,
			SenderEID:    "alice",
			RecipientEID: "",
			TokenType:    "abc",
			Amount:       big.NewInt(1),
			Timestamp:    now.AddDate(0, 0, 1),
			Status:       driver3.Confirmed,
		},
	}
	testCases := []struct {
		name        string
		params      driver3.QueryTransactionsParams
		expectedLen int
	}{
		{
			name:        "No params",
			params:      driver3.QueryTransactionsParams{},
			expectedLen: len(tr),
		},
		{
			name: "Only driver.Confirmed",
			params: driver3.QueryTransactionsParams{
				Statuses: []driver3.TxStatus{driver3.Confirmed},
			},
			expectedLen: 10,
		},
		{
			name: "Pending and deleted",
			params: driver3.QueryTransactionsParams{
				Statuses: []driver3.TxStatus{driver3.Pending, driver3.Deleted},
			},
			expectedLen: 2,
		},
		{
			name: "Confirmed from alice should return all driver.Confirmed",
			params: driver3.QueryTransactionsParams{
				SenderWallet: "alice",
				Statuses:     []driver3.TxStatus{driver3.Confirmed},
			},
			expectedLen: 10,
		},
		{
			name: "Recipient matches should return all",
			params: driver3.QueryTransactionsParams{
				RecipientWallet: "alice",
			},
			expectedLen: 12,
		},
		{
			name: "Sender OR recipient matches",
			params: driver3.QueryTransactionsParams{
				SenderWallet:    "alice",
				RecipientWallet: "alice",
			},
			expectedLen: 9,
		},
		{
			name: "Sender OR recipient matches, from last year",
			params: driver3.QueryTransactionsParams{
				SenderWallet:    "alice",
				RecipientWallet: "alice",
				From:            &lastYear,
			},
			expectedLen: 9,
		},
		{
			name: "Only this millisecond",
			params: driver3.QueryTransactionsParams{
				From: &justBefore,
				To:   &justAfter,
			},
			expectedLen: 7,
		},
		{
			name: "Only this millisecond for alice",
			params: driver3.QueryTransactionsParams{
				SenderWallet:    "alice",
				RecipientWallet: "alice",
				From:            &justBefore,
				To:              &justAfter,
			},
			expectedLen: 5,
		},
		{
			name: "Get redemption",
			params: driver3.QueryTransactionsParams{
				ActionTypes: []driver3.ActionType{driver3.Redeem},
			},
			expectedLen: 2,
		},
	}

	w, err := db.NewTransactionStoreTransaction()
	require.NoError(t, err)
	var previous string
	for _, r := range tr {
		if r.TxID != previous {
			require.NoError(t, w.AddTokenRequest(ctx, r.TxID, []byte{}, map[string][]byte{}, nil, driver2.PPHash("tr")))
		}
		require.NoError(t, w.AddTransaction(ctx, r))
		previous = r.TxID
	}
	require.NoError(t, w.Commit())
	for _, r := range tr {
		if r.Status != driver3.Pending {
			require.NoError(t, db.SetStatus(t.Context(), r.TxID, r.Status, ""))
		}
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			res := getTransactions(t, db, tc.params)
			assert.Len(t, res, tc.expectedLen, "params: %v", tc.params)
		})
	}

	// SearchDirection: redemptions have distinct stored_at (now-1day vs now+1day)
	t.Run("SearchDirection ASC", func(t *testing.T) {
		res := getTransactions(t, db, driver3.QueryTransactionsParams{
			ActionTypes:     []driver3.ActionType{driver3.Redeem},
			Statuses:        []driver3.TxStatus{driver3.Confirmed},
			SearchDirection: driver3.FromBeginning,
		})
		require.Len(t, res, 2)
		assert.Equal(t, "8", res[0].TxID, "ASC: older redemption first")
		assert.Equal(t, "10", res[1].TxID, "ASC: newer redemption second")
	})
	t.Run("SearchDirection DESC", func(t *testing.T) {
		res := getTransactions(t, db, driver3.QueryTransactionsParams{
			ActionTypes:     []driver3.ActionType{driver3.Redeem},
			Statuses:        []driver3.TxStatus{driver3.Confirmed},
			SearchDirection: driver3.FromLast,
		})
		require.Len(t, res, 2)
		assert.Equal(t, "10", res[0].TxID, "DESC: newer redemption first")
		assert.Equal(t, "8", res[1].TxID, "DESC: older redemption second")
	})
}

func getTransactions(t *testing.T, db driver3.TokenTransactionStore, params driver3.QueryTransactionsParams) []*driver3.TransactionRecord {
	t.Helper()
	records, err := db.QueryTransactions(t.Context(), params, nil)
	require.NoError(t, err)
	txs, err := iterators.ReadAllPointers(records.Items)
	require.NoError(t, err)

	return txs
}

func TEndorserAcks(t *testing.T, db driver3.TokenTransactionStore) {
	t.Helper()
	ctx := t.Context()
	createTestTransaction(t, db, "1")
	wg := sync.WaitGroup{}
	n := 100
	wg.Add(n)
	for i := range n {
		go func(i int) {
			assert.NoError(t, db.AddTransactionEndorsementAck(ctx, "1", fmt.Appendf(nil, "alice_%d", i), fmt.Appendf(nil, "sigma_%d", i)))
			acks, err := db.GetTransactionEndorsementAcks(ctx, "1")
			assert.NoError(t, err)
			assert.NotEmpty(t, acks)
			wg.Done()
		}(i)
	}
	wg.Wait()

	acks, err := db.GetTransactionEndorsementAcks(ctx, "1")
	require.NoError(t, err)
	assert.Len(t, acks, n)
	for i := range n {
		assert.Equal(t, fmt.Appendf(nil, "sigma_%d", i), acks[token.Identity(fmt.Sprintf("alice_%d", i)).String()])
	}
}

func createTestTransaction(t *testing.T, db driver3.TokenTransactionStore, txID string) {
	t.Helper()
	w, err := db.NewTransactionStoreTransaction()
	if err != nil {
		t.Fatalf("error creating transaction while trying to test something else: %s", err)
	}
	if err := w.AddTokenRequest(t.Context(), txID, []byte{}, map[string][]byte{}, nil, driver2.PPHash("tr")); err != nil {
		t.Fatalf("error creating token request while trying to test something else: %s", err)
	}
	tr1 := driver3.TransactionRecord{
		TxID:         txID,
		ActionType:   driver3.Transfer,
		SenderEID:    "bob",
		RecipientEID: "alice",
		TokenType:    "magic",
		Amount:       big.NewInt(10),
		Timestamp:    time.Now().Local().UTC(),
		Status:       driver3.Pending,
	}
	if err := w.AddTransaction(t.Context(), tr1); err != nil {
		t.Fatalf("error creating transaction while trying to test something else: %s", err)
	}
	if err := w.Commit(); err != nil {
		t.Fatalf("error committing transaction while trying to test something else: %s", err)
	}
}
