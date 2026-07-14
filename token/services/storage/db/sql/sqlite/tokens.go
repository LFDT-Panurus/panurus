/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	scommon "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"

	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

type TokenStore = sqlcommon.TokenStore

func NewTokenStore(
	dbs *scommon.RWDB,
	tableNames sqlcommon.TableNames,
) (*TokenStore, error) {
	return sqlcommon.NewTokenStoreWithNotifier(
		dbs.ReadDB,
		dbs.WriteDB,
		tableNames,
		NewConditionInterpreter(),
		nil,
	)
}
