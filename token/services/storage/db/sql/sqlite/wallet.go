/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"

	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

type WalletStore = common3.WalletStore

func NewWalletStore(dbs *common2.RWDB, tableNames common3.TableNames) (*WalletStore, error) {
	return common3.NewWalletStore(dbs.ReadDB, dbs.WriteDB, tableNames, NewConditionInterpreter())
}
