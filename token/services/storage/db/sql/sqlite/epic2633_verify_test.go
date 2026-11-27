/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"context"
	"path"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/multiplexed"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common/migrate"
	fscSqlite "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var verifyTMS = []string{"net", "ch", "ns"}

// #2636: every Panurus store should implement migrate.Versioned.
func TestVerify2636StoresAreVersioned(t *testing.T) {
	d := NewDriver(sqliteCfg(t.TempDir(), "vstores"))
	stores := map[string]func() (any, error){
		"token":     func() (any, error) { return d.NewToken("", verifyTMS...) },
		"ownertx":   func() (any, error) { return d.NewOwnerTransaction("", verifyTMS...) },
		"audittx":   func() (any, error) { return d.NewAuditTransaction("", verifyTMS...) },
		"identity":  func() (any, error) { return d.NewIdentity("", verifyTMS...) },
		"wallet":    func() (any, error) { return d.NewWallet("", verifyTMS...) },
		"tokenlock": func() (any, error) { return d.NewTokenLock("", verifyTMS...) },
		"keystore":  func() (any, error) { return d.NewKeyStore("", verifyTMS...) },
		"endorser":  func() (any, error) { return d.NewEndorser("", verifyTMS...) },
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			s, err := open()
			require.NoError(t, err)
			_, ok := s.(migrate.Versioned)
			assert.True(t, ok, "store %s (%T) does not implement migrate.Versioned", name, s)
		})
	}
}

// #2636: with skipCreateTable on an empty database, opening a store should fail
// (as FSC stores do with migrate.ErrPending) instead of succeeding.
func TestVerify2636SkipCreateTableEmptyDBShouldFailAtOpen(t *testing.T) {
	d := NewDriver(skipCfg(t.TempDir()))
	_, err := d.NewToken("", verifyTMS...)
	assert.Error(t, err, "token store opened on an empty DB with skipCreateTable")
}

// #2636: documents the current behaviour. The store opens, and the first query fails.
func TestVerify2636SkipCreateTableEmptyDBFailsAtRuntime(t *testing.T) {
	d := NewDriver(skipCfg(t.TempDir()))
	s, err := d.NewToken("", verifyTMS...)
	require.NoError(t, err)
	_, err = s.IsMine(context.Background(), "tx", 0)
	require.Error(t, err)
	t.Logf("runtime error: %v", err)
	assert.Contains(t, err.Error(), "no such table")
}

// #2695: the RWDB handed to a store constructor should carry the persistence queryTimeout.
func TestVerify2695SQLiteQueryTimeoutReachesStore(t *testing.T) {
	const timeout = 5 * time.Second
	var got *common.RWDB
	p := newProviderWithKeyMapper(fscSqlite.NewDbProvider(),
		func(dbs *common.RWDB, tn common2.TableNames) (*TokenLockStore, error) {
			got = dbs

			return NewTokenLockStore(dbs, tn)
		}, common2.TableNamesConfig{}, common2.Limits{})
	cfg := fscSqlite.Config{
		DataSource:      "file:" + path.Join(t.TempDir(), "db.sqlite"),
		TablePrefix:     "vtimeout",
		MaxIdleConns:    common.CopyPtr(1),
		MaxIdleTime:     common.CopyPtr(time.Minute),
		TableNameParams: verifyTMS,
		QueryTimeout:    timeout,
	}
	_, err := p.Get(cfg)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, timeout, got.QueryTimeout, "queryTimeout dropped before the store constructor")
}

func skipCfg(dir string) driver.Config {
	return multiplexed.MockTypeConfig(fscSqlite.Persistence, fscSqlite.Config{
		DataSource:      "file:" + path.Join(dir, "empty.sqlite"),
		TablePrefix:     "vstores",
		MaxOpenConns:    10,
		SkipCreateTable: true,
	})
}
