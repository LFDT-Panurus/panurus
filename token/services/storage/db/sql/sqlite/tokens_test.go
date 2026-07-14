/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"database/sql"
	"testing"

	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

func mockTokenStore(db *sql.DB) *common2.TokenStore {
	tables, _ := common2.GetTableNames("")
	store, _ := common2.NewTokenStoreWithNotifier(db, db, tables, NewConditionInterpreter(), nil)

	return store
}

func TestDeleteTokensContextCancelled(t *testing.T) {
	common2.TestDeleteTokensContextCancelled(t, mockTokenStore)
}
