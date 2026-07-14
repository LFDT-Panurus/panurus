/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"database/sql"

	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"

	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

// WalletStore wraps common.WalletStore to add advisory lock to schema creation
type WalletStore struct {
	*common3.WalletStore
	writeDB *sql.DB
	lockID  int64
}

// GetSchema overrides the base GetSchema to prefix with advisory lock
func (s *WalletStore) GetSchema() string {
	baseSchema := s.WalletStore.GetSchema()

	return prefixSchemaWithLock(baseSchema, s.lockID)
}

// CreateSchema overrides the base CreateSchema to ensure GetSchema is called on the correct receiver
func (s *WalletStore) CreateSchema() error {
	return common.InitSchema(s.writeDB, s.GetSchema())
}

// NewWalletStore returns a new WalletStore for the given RWDB and table names.
func NewWalletStore(dbs *common2.RWDB, tableNames common3.TableNames) (*WalletStore, error) {
	baseStore, err := common3.NewWalletStore(dbs.ReadDB, dbs.WriteDB, tableNames, NewConditionInterpreter())
	if err != nil {
		return nil, err
	}

	return &WalletStore{
		WalletStore: baseStore,
		writeDB:     dbs.WriteDB,
		lockID:      createTableLockID("wallet"),
	}, nil
}
