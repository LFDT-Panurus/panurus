/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"database/sql"
	"strconv"

	scommon "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"

	tokensdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

// TokenStore wraps common.TokenStore to add advisory lock to schema creation
type TokenStore struct {
	*sqlcommon.TokenStore
	writeDB *sql.DB
	lockID  int64
}

// GetSchema overrides the base GetSchema to prefix with advisory lock
func (s *TokenStore) GetSchema() string {
	baseSchema := s.TokenStore.GetSchema()

	return prefixSchemaWithLock(baseSchema, s.lockID)
}

// CreateSchema overrides the base CreateSchema to ensure GetSchema is called on the correct receiver
func (s *TokenStore) CreateSchema() error {
	return common.InitSchema(s.writeDB, s.GetSchema())
}

// TokenNotifier handles notifications for tokens.
type TokenNotifier struct {
	*Notifier
}

// NewTokenNotifier returns a new TokenNotifier for the given RWDB and table names.
func NewTokenNotifier(dbs *scommon.RWDB, tableNames sqlcommon.TableNames, dataSource string) (*TokenNotifier, error) {
	return &TokenNotifier{
		Notifier: NewNotifier(
			dbs.WriteDB,
			tableNames.Tokens,
			dataSource,
			AllOperations,
			*NewSimplePrimaryKey("tx_id"),
			*NewSimplePrimaryKey("idx"),
		),
	}, nil
}

// Subscribe registers a callback function to be called when a token is inserted, updated, or deleted.
func (n *TokenNotifier) Subscribe(callback func(tokensdriver.Operation, tokensdriver.TokenRecordReference)) error {
	return n.Notifier.Subscribe(func(operation tokensdriver.Operation, m map[tokensdriver.ColumnKey]string) {
		idx, err := strconv.ParseUint(m["idx"], 10, 64)
		if err != nil {
			logger.Errorf("failed to parse token index [%s]: %s", m["idx"], err)

			return
		}
		callback(operation, tokensdriver.TokenRecordReference{
			TxID:  m["tx_id"],
			Index: idx,
		})
	})
}

func NewTokenStoreWithNotifier(dbs *scommon.RWDB, tableNames sqlcommon.TableNames, notifier *TokenNotifier) (*TokenStore, error) {
	// Create cleanup leader factory using PostgreSQL advisory locks
	cleanupLeaderFactory := NewCleanupLeaderFactory()

	baseStore, err := sqlcommon.NewTokenStoreWithNotifierAndCleanup(
		dbs.ReadDB,
		dbs.WriteDB,
		tableNames,
		NewConditionInterpreter(),
		notifier,
		cleanupLeaderFactory,
	)
	if err != nil {
		return nil, err
	}

	// Wrap with postgres-specific store that adds advisory lock to schema

	return &TokenStore{
		TokenStore: baseStore,
		writeDB:    dbs.WriteDB,
		lockID:     createTableLockID("tokens"),
	}, nil
}
