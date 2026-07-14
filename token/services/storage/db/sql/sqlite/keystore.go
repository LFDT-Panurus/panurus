/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	fscSqlite "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"

	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

type KeystoreStore = common3.KeystoreStore

func NewKeystoreStore(dbs *common2.RWDB, tableNames common3.TableNames) (*KeystoreStore, error) {
	return common3.NewKeystoreStore(dbs.ReadDB, dbs.WriteDB, tableNames, NewConditionInterpreter(), &fscSqlite.ErrorMapper{})
}
