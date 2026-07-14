/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"database/sql"
	driver2 "database/sql/driver"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/LFDT-Panurus/panurus/token/token"
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	. "github.com/onsi/gomega"
)

func mockTokenLockStorePostgress(db *sql.DB) *TokenLockStore {
	var dbs = common2.RWDB{
		ReadDB: db, WriteDB: db,
	}

	store, _ := NewTokenLockStore(&dbs, common3.TableNames{
		TokenLocks: "TOKEN_LOCKS",
		Tokens:     "TOKENS",
		Requests:   "REQUESTS",
	})

	return store
}

func mockTokenLockStore(db *sql.DB) *common3.TokenLockStore {
	var dbs = common2.RWDB{
		ReadDB: db, WriteDB: db,
	}

	store, _ := NewTokenLockStore(&dbs, common3.TableNames{
		TokenLocks: "TOKEN_LOCKS",
		Tokens:     "TOKENS",
		Requests:   "REQUESTS",
	})

	return store.TokenLockStore
}

func TestCleanup(t *testing.T) {
	RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	Expect(err).ToNot(HaveOccurred())

	input := driver.Deleted

	consumerTxID := "1234"
	tokenID := token.ID{TxId: "5678", Index: 5}
	status := &input
	createdAt := time.Date(2025, time.June, 8, 10, 0, 0, 0, time.UTC)

	var timeNow time.Time
	output := []driver2.Value{
		consumerTxID, tokenID.TxId, tokenID.Index, *status, createdAt, timeNow,
	}
	mockDB.
		ExpectQuery("SELECT TOKEN_LOCKS.consumer_tx_id, TOKEN_LOCKS.tx_id, TOKEN_LOCKS.idx, REQUESTS.status, TOKEN_LOCKS.created_at, NOW\\(\\) AS now "+
			"FROM TOKEN_LOCKS LEFT JOIN REQUESTS ON TOKEN_LOCKS.consumer_tx_id = REQUESTS.tx_id "+
			"WHERE \\(\\(\\(REQUESTS.status = \\$1\\)\\) OR \\(\\(REQUESTS.status = \\$2\\)\\)\\) OR \\(TOKEN_LOCKS.created_at < NOW\\(\\) - INTERVAL '1 seconds'\\)").
		WithArgs(driver.Deleted, driver.Orphan).
		WillReturnRows(mockDB.NewRows([]string{"consumer_tx_id", "tx_id", "idx", "status", "created_at", "now"}).AddRow(output...))

	mockDB.ExpectExec("DELETE FROM TOKEN_LOCKS WHERE "+
		"\\(TOKEN_LOCKS\\.created_at < NOW\\(\\) - INTERVAL '1 seconds'\\)"+
		" OR "+
		"\\(EXISTS \\(SELECT 1 FROM REQUESTS WHERE \\(REQUESTS\\.tx_id = TOKEN_LOCKS\\.consumer_tx_id\\) AND \\(\\(\\(REQUESTS\\.status = \\$1\\)\\) OR \\(\\(REQUESTS\\.status = \\$2\\)\\)\\)\\)\\)").
		WithArgs(driver.Deleted, driver.Orphan).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = mockTokenLockStorePostgress(db).Cleanup(t.Context(), time.Second)

	Expect(mockDB.ExpectationsWereMet()).To(Succeed())
	Expect(err).ToNot(HaveOccurred())
}

func TestLock(t *testing.T) {
	common3.TestLock(t, mockTokenLockStore)
}

func TestUnlockByTxID(t *testing.T) {
	common3.TestUnlockByTxID(t, mockTokenLockStore)
}

func TestCleanupOrphan(t *testing.T) {
	RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	Expect(err).ToNot(HaveOccurred())

	input := driver.Orphan

	consumerTxID := "orphan-tx-1234"
	tokenID := token.ID{TxId: "5678", Index: 5}
	status := &input
	createdAt := time.Date(2025, time.June, 8, 10, 0, 0, 0, time.UTC)

	var timeNow time.Time
	output := []driver2.Value{
		consumerTxID, tokenID.TxId, tokenID.Index, *status, createdAt, timeNow,
	}

	// Expect the SELECT query for logging - now includes both Deleted and Orphan
	mockDB.
		ExpectQuery("SELECT TOKEN_LOCKS.consumer_tx_id, TOKEN_LOCKS.tx_id, TOKEN_LOCKS.idx, REQUESTS.status, TOKEN_LOCKS.created_at, NOW\\(\\) AS now "+
			"FROM TOKEN_LOCKS LEFT JOIN REQUESTS ON TOKEN_LOCKS.consumer_tx_id = REQUESTS.tx_id "+
			"WHERE \\(\\(\\(REQUESTS.status = \\$1\\)\\) OR \\(\\(REQUESTS.status = \\$2\\)\\)\\) OR \\(TOKEN_LOCKS.created_at < NOW\\(\\) - INTERVAL '1 seconds'\\)").
		WithArgs(driver.Deleted, driver.Orphan).
		WillReturnRows(mockDB.NewRows([]string{"consumer_tx_id", "tx_id", "idx", "status", "created_at", "now"}).AddRow(output...))

	// Expect the DELETE query with both Deleted (3) and Orphan (4) statuses
	mockDB.ExpectExec("DELETE FROM TOKEN_LOCKS WHERE "+
		"\\(TOKEN_LOCKS\\.created_at < NOW\\(\\) - INTERVAL '1 seconds'\\)"+
		" OR "+
		"\\(EXISTS \\(SELECT 1 FROM REQUESTS WHERE \\(REQUESTS\\.tx_id = TOKEN_LOCKS\\.consumer_tx_id\\) AND \\(\\(\\(REQUESTS\\.status = \\$1\\)\\) OR \\(\\(REQUESTS\\.status = \\$2\\)\\)\\)\\)\\)").
		WithArgs(driver.Deleted, driver.Orphan).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = mockTokenLockStorePostgress(db).Cleanup(t.Context(), time.Second)

	Expect(mockDB.ExpectationsWereMet()).To(Succeed())
	Expect(err).ToNot(HaveOccurred())
}
