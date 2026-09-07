/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package guard_test

import (
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	driver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/guard"
	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/pagination"
	"github.com/stretchr/testify/require"
)

func ownerStore(t *testing.T, db *sql.DB, p guard.Policy) driver.TokenTransactionStore {
	t.Helper()
	store, err := sqlcommon.NewOwnerTransactionStore(db, db, sqlcommon.TableNames{
		Movements:             "MOVEMENTS",
		Transactions:          "TRANSACTIONS",
		Requests:              "REQUESTS",
		TransactionEndorseAck: "TRANSACTION_ENDORSE_ACK",
	}, nil, nil)
	require.NoError(t, err)

	return guard.WrapOwnerTransaction(store, p)
}

// TestGuardRejectsOversizeWrite verifies the guard rejects an oversize write
// on the atomic transaction before it reaches the database.
func TestGuardRejectsOversizeWrite(t *testing.T) {
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)

	store := ownerStore(t, db, guard.Policy{MaxPayloadSize: 20, MaxPageSize: 1000})
	mockDB.ExpectBegin()

	w, err := store.NewTransactionStoreTransaction()
	require.NoError(t, err)

	err = w.AddTokenRequest(t.Context(), "tx", make([]byte, 100), nil, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
	require.NoError(t, mockDB.ExpectationsWereMet())
}

// TestGuardDisabledWritePassesThrough verifies a zero payload limit disables
// the check and the write proceeds to the database.
func TestGuardDisabledWritePassesThrough(t *testing.T) {
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)

	store := ownerStore(t, db, guard.Policy{MaxPayloadSize: 0, MaxPageSize: 1000})
	mockDB.ExpectBegin()
	mockDB.ExpectExec("INSERT INTO REQUESTS").WillReturnResult(sqlmock.NewResult(1, 1))

	w, err := store.NewTransactionStoreTransaction()
	require.NoError(t, err)

	require.NoError(t, w.AddTokenRequest(t.Context(), "tx", make([]byte, 100), nil, nil, nil))
	require.NoError(t, mockDB.ExpectationsWereMet())
}

// TestGuardRejectsOversizeAddTransaction verifies the guard rejects an oversize
// AddTransaction record before it reaches the database.
func TestGuardRejectsOversizeAddTransaction(t *testing.T) {
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)

	store := ownerStore(t, db, guard.Policy{MaxPayloadSize: 20, MaxPageSize: 1000})
	mockDB.ExpectBegin()

	w, err := store.NewTransactionStoreTransaction()
	require.NoError(t, err)

	err = w.AddTransaction(t.Context(), driver.TransactionRecord{TxID: "tx", SenderEID: string(make([]byte, 100))})
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
	require.NoError(t, mockDB.ExpectationsWereMet())
}

// TestGuardRejectsOversizeAddMovement verifies the guard rejects an oversize
// AddMovement record before it reaches the database.
func TestGuardRejectsOversizeAddMovement(t *testing.T) {
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)

	store := ownerStore(t, db, guard.Policy{MaxPayloadSize: 20, MaxPageSize: 1000})
	mockDB.ExpectBegin()

	w, err := store.NewTransactionStoreTransaction()
	require.NoError(t, err)

	err = w.AddMovement(t.Context(), driver.MovementRecord{TxID: "tx", EnrollmentID: string(make([]byte, 100))})
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
	require.NoError(t, mockDB.ExpectationsWereMet())
}

// TestGuardRejectsUnlimitedQuery verifies the read guard rejects nil and
// unlimited (None) pagination without querying the database.
func TestGuardRejectsUnlimitedQuery(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)

	store := ownerStore(t, db, guard.DefaultPolicy())

	_, err = store.QueryTransactions(t.Context(), driver.QueryTransactionsParams{}, nil)
	require.Error(t, err)

	_, err = store.QueryTransactions(t.Context(), driver.QueryTransactionsParams{}, pagination.None())
	require.Error(t, err)
}
