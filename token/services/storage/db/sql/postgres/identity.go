/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"database/sql"

	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/cache/secondcache"
	scommon "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"

	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	fscPostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
)

// IdentityStore wraps common.IdentityStore to add advisory lock to schema creation
type IdentityStore struct {
	*sqlcommon.IdentityStore
	writeDB *sql.DB
	lockID  int64
}

// GetSchema overrides the base GetSchema to prefix with advisory lock
func (s *IdentityStore) GetSchema() string {
	baseSchema := s.IdentityStore.GetSchema()

	return prefixSchemaWithLock(baseSchema, s.lockID)
}

// CreateSchema overrides the base CreateSchema to ensure GetSchema is called on the correct receiver
func (s *IdentityStore) CreateSchema() error {
	return common.InitSchema(s.writeDB, s.GetSchema())
}

// NewIdentityStore creates a new IdentityStore with advisory lock support
func NewIdentityStore(dbs *scommon.RWDB, tableNames sqlcommon.TableNames, dataSource string) (*IdentityStore, error) {
	notifier, err := NewIdentityNotifier(dbs, tableNames, dataSource)
	if err != nil {
		return nil, err
	}

	baseStore, err := sqlcommon.NewIdentityStoreWithNotifier(
		dbs.ReadDB,
		dbs.WriteDB,
		tableNames,
		secondcache.NewTyped[bool](5000),
		secondcache.NewTyped[[]byte](5000),
		NewConditionInterpreter(),
		&fscPostgres.ErrorMapper{},
		notifier,
	)
	if err != nil {
		return nil, err
	}

	return &IdentityStore{
		IdentityStore: baseStore,
		writeDB:       dbs.WriteDB,
		lockID:        createTableLockID("identity"),
	}, nil
}

// IdentityNotifier handles notifications for identity configurations.
type IdentityNotifier struct {
	*Notifier
}

// NewIdentityNotifier returns a new IdentityNotifier for the given RWDB and table names.
func NewIdentityNotifier(dbs *scommon.RWDB, tableNames sqlcommon.TableNames, dataSource string) (*IdentityNotifier, error) {
	return &IdentityNotifier{
		Notifier: NewNotifier(
			dbs.WriteDB,
			tableNames.IdentityConfigurations,
			dataSource,
			AllOperations,
			*NewSimplePrimaryKey("id"),
			*NewSimplePrimaryKey("type"),
			*NewSimplePrimaryKey("url"),
		),
	}, nil
}

// Subscribe registers a callback function to be called when an identity configuration is inserted or updated.
func (n *IdentityNotifier) Subscribe(callback func(idriver.Operation, idriver.IdentityConfigurationRecord)) error {
	return n.Notifier.Subscribe(func(operation idriver.Operation, m map[idriver.ColumnKey]string) {
		callback(operation, idriver.IdentityConfigurationRecord{
			ID:   m["id"],
			Type: m["type"],
			URL:  m["url"],
		})
	})
}
