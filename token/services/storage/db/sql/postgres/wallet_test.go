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

func mockWalletStore(db *sql.DB) *common2.WalletStore {
	store, _ := common2.NewWalletStore(db, db, common2.TableNames{
		Wallets: "WALLETS",
	}, NewConditionInterpreter())

	return store
}

func TestGetWalletID(t *testing.T) {
	common2.TestGetWalletID(t, mockWalletStore)
}

func TestGetWalletIDs(t *testing.T) {
	common2.TestGetWalletIDs(t, mockWalletStore)
}

func TestLoadMeta(t *testing.T) {
	common2.TestLoadMeta(t, mockWalletStore)
}

func TestIdentityExists(t *testing.T) {
	common2.TestIdentityExists(t, mockWalletStore)
}

func TestStoreIdentity(t *testing.T) {
	common2.TestStoreIdentity(t, mockWalletStore)
}

func TestStoreIdentityIdempotent(t *testing.T) {
	common2.TestStoreIdentityIdempotent(t, mockWalletStore)
}
