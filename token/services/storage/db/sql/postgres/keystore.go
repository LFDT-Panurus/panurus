/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"database/sql"

	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"
	fscPostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"

	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

// KeystoreStore wraps common.KeystoreStore to add advisory lock to schema creation
type KeystoreStore struct {
	*common3.KeystoreStore
	writeDB *sql.DB
	lockID  int64
}

// GetSchema overrides the base GetSchema to prefix with advisory lock
func (s *KeystoreStore) GetSchema() string {
	baseSchema := s.KeystoreStore.GetSchema()

	return prefixSchemaWithLock(baseSchema, s.lockID)
}

// CreateSchema overrides the base CreateSchema to ensure GetSchema is called on the correct receiver
func (s *KeystoreStore) CreateSchema() error {
	return common.InitSchema(s.writeDB, s.GetSchema())
}

// NewKeystoreStore returns a new KeystoreStore for the given RWDB and table names.
func NewKeystoreStore(dbs *common2.RWDB, tableNames common3.TableNames) (*KeystoreStore, error) {
	baseStore, err := common3.NewKeystoreStore(dbs.ReadDB, dbs.WriteDB, tableNames, NewConditionInterpreter(), &fscPostgres.ErrorMapper{})
	if err != nil {
		return nil, err
	}

	return &KeystoreStore{
		KeystoreStore: baseStore,
		writeDB:       dbs.WriteDB,
		lockID:        createTableLockID("keystore"),
	}, nil
}
