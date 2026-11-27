/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"net/url"
	"os"
	"path"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common/mock"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/multiplexed"

	dbtest2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/dbtest"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	fscSqlite "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"
	"github.com/stretchr/testify/require"
)

func TestTokens(t *testing.T) {
	dbtest2.TokensTest(t, func(name string) driver.Driver { return NewDriver(sqliteCfg(t.TempDir(), name)) })
}

func TestTransactions(t *testing.T) {
	dbtest2.TransactionsTest(t, func(name string) driver.Driver { return NewDriver(sqliteCfg(t.TempDir(), name)) })
}

func TestTokenLocks(t *testing.T) {
	dbtest2.TokenLocksTest(t, func(name string) driver.Driver { return NewDriver(sqliteCfg(t.TempDir(), name)) })
}

func TestIdentity(t *testing.T) {
	dbtest2.IdentityTest(t, func(name string) driver.Driver { return NewDriver(sqliteCfg(t.TempDir(), name)) })
}

func TestWallet(t *testing.T) {
	dbtest2.WalletTest(t, func(name string) driver.Driver { return NewDriver(sqliteCfg(t.TempDir(), name)) })
}

func TestKeyStore(t *testing.T) {
	dbtest2.KeyStoreTest(t, func(name string) driver.Driver { return NewDriver(sqliteCfg(t.TempDir(), name)) })
}

func TestEndorser(t *testing.T) {
	dbtest2.EndorserTest(t, func(name string) driver.Driver { return NewDriver(sqliteCfg(t.TempDir(), name)) })
}

// sqliteCfg returns a config for a sqlite DB at tempDir/db.sqlite.
// The path is URI-escaped: t.TempDir() can contain '#' (e.g. subtest "x#01"),
// which SQLite would read as the start of a URI fragment.
func sqliteCfg(tempDir string, name string) *mock.ConfigProvider {
	return multiplexed.MockTypeConfig(fscSqlite.Persistence, fscSqlite.Config{
		DataSource:   sqliteDataSource(path.Join(tempDir, "db.sqlite"), "_pragma=busy_timeout(20000)"),
		TablePrefix:  name,
		MaxOpenConns: 10,
	})
}

// sqliteDataSource returns a file URI for dbPath with the given raw query. It
// escapes the path, so a '#' or '?' in it cannot truncate the file name.
func sqliteDataSource(dbPath, query string) string {
	return (&url.URL{Scheme: "file", Path: dbPath, RawQuery: query}).String()
}

// TestSqliteCfgEscapesPath checks that a TempDir path with '#' or '%' keeps
// the DB file inside that directory (see issue #2785).
func TestSqliteCfgEscapesPath(t *testing.T) {
	for _, name := range []string{"dup", "dup", "pct%41"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			d := NewDriver(sqliteCfg(dir, "tsdk"))
			_, err := d.NewToken("", "n", "c", "ns")
			require.NoError(t, err)
			_, err = os.Stat(path.Join(dir, "db.sqlite"))
			require.NoError(t, err, "db file not created in %s", dir)
		})
	}
}
