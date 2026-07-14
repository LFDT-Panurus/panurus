/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"database/sql"

	scommon "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"

	driver2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

// EndorserStore wraps common.EndorserStore to add advisory lock to schema creation
type EndorserStore struct {
	*common3.EndorserStore
	writeDB *sql.DB
	lockID  int64
}

// GetSchema overrides the base GetSchema to prefix with advisory lock
func (s *EndorserStore) GetSchema() string {
	baseSchema := s.EndorserStore.GetSchema()

	return prefixSchemaWithLock(baseSchema, s.lockID)
}

// CreateSchema overrides the base CreateSchema to ensure GetSchema is called on the correct receiver
func (s *EndorserStore) CreateSchema() error {
	return common.InitSchema(s.writeDB, s.GetSchema())
}

// NewEndorserStore creates a new EndorserStore for Postgres
func NewEndorserStore(dbs *scommon.RWDB, tables common3.TableNames) (*EndorserStore, error) {
	baseStore, err := common3.NewEndorserStore(
		dbs.ReadDB,
		dbs.WriteDB,
		tables,
		NewConditionInterpreter(),
		NewPaginationInterpreter(),
	)
	if err != nil {
		return nil, err
	}

	return &EndorserStore{
		EndorserStore: baseStore,
		writeDB:       dbs.WriteDB,
		lockID:        createTableLockID("endorser"),
	}, nil
}

var _ driver2.EndorserStore = (*EndorserStore)(nil)
