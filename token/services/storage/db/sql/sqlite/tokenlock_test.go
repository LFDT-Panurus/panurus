/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"database/sql"
	"testing"
	"time"

	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	. "github.com/onsi/gomega"
)

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

// TestCleanupSQLShape guards the shape of the cleanup statement rendered with the
// SQLite interpreter: the status branch must correlate on the consuming transaction
// and the deletion must not be scoped by a partial-key IN on tx_id. It asserts the
// parts that carry the semantics rather than the whole string - the string equality
// this replaced encoded a join on the wrong column as the expected output. The
// behaviour itself is covered by the shared suite in dbtest. See #2018.
func TestCleanupSQLShape(t *testing.T) {
	RegisterTestingT(t)

	tokenLocks, requests := q.Table("TokenLocks"), q.Table("Requests")
	query, args := q.DeleteFrom("TokenLocks").
		Where(common3.IsStaleLock(requests, tokenLocks, 5*time.Second)).
		Format(NewConditionInterpreter())

	Expect(query).To(ContainSubstring("Requests.tx_id = TokenLocks.consumer_tx_id"))
	Expect(query).To(ContainSubstring("EXISTS (SELECT 1 FROM Requests WHERE"))
	Expect(query).To(ContainSubstring("(Requests.status) IN"))
	Expect(query).To(ContainSubstring("TokenLocks.created_at < datetime('now', '-5 seconds')"))
	Expect(query).ToNot(ContainSubstring("tx_id IN ("))
	Expect(query).ToNot(ContainSubstring("TokenLocks.tx_id = Requests.tx_id"))
	Expect(args).To(ConsistOf(driver.Deleted, driver.Orphan))
}

func TestLock(t *testing.T) {
	common3.TestLock(t, mockTokenLockStore)
}

func TestUnlockByTxID(t *testing.T) {
	common3.TestUnlockByTxID(t, mockTokenLockStore)
}

func TestLockContextCancelled(t *testing.T) {
	common3.TestLockContextCancelled(t, mockTokenLockStore)
}

func TestUnlockByTxIDContextCancelled(t *testing.T) {
	common3.TestUnlockByTxIDContextCancelled(t, mockTokenLockStore)
}
