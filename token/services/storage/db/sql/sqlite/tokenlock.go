/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"context"

	common3 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	fscSqlite "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	common4 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

// TokenLockStore implements the token lock storage for SQLite. Cleanup is inherited
// from the embedded store, so lease expiry is identical to the other SQL backends.
type TokenLockStore struct {
	*common4.TokenLockStore
}

// AcquireCleanupLeadership always grants leadership locally - sqlite is a
// non-distributed backend with only one instance, so there is nothing to
// coordinate. See #1798.
func (db *TokenLockStore) AcquireCleanupLeadership(_ context.Context) (driver.CleanupLeadership, bool, error) {
	return driver.NoopCleanupLeadership{}, true, nil
}

// NewTokenLockStore returns a new TokenLockStore for the given RWDB and table names.
func NewTokenLockStore(dbs *common3.RWDB, tableNames common4.TableNames) (*TokenLockStore, error) {
	tldb, err := common4.NewTokenLockStore(dbs.ReadDB, dbs.WriteDB, tableNames, NewConditionInterpreter(), &fscSqlite.ErrorMapper{})
	if err != nil {
		return nil, err
	}

	return &TokenLockStore{TokenLockStore: tldb}, nil
}
