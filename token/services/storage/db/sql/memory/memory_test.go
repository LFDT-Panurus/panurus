/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package memory

import (
	"testing"

	"github.com/stretchr/testify/require"

	dbtest2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/dbtest"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
)

func TestTokens(t *testing.T) {
	dbtest2.TokensTest(t, func(string) driver.Driver { return NewDriver() })
}

func TestTransactions(t *testing.T) {
	dbtest2.TransactionsTest(t, func(string) driver.Driver { return NewDriver() })
}

func TestTokenLocks(t *testing.T) {
	dbtest2.TokenLocksTest(t, func(string) driver.Driver { return NewDriver() })
}

func TestIdentity(t *testing.T) {
	dbtest2.IdentityTest(t, func(string) driver.Driver { return NewDriver() })
}

func TestKeyStore(t *testing.T) {
	dbtest2.KeyStoreTest(t, func(string) driver.Driver { return NewDriver() })
}

func TestWallet(t *testing.T) {
	dbtest2.WalletTest(t, func(string) driver.Driver { return NewDriver() })
}

func TestEndorser(t *testing.T) {
	dbtest2.EndorserTest(t, func(string) driver.Driver { return NewDriver() })
}

// TestMemoryDriverInheritsBusyRetry pins what #2043 item 4 pointed out about the
// memory driver: it is the SQLite driver on file::memory:?cache=shared, not a
// separate implementation, so it inherits SQLite's write-lock contention - and
// with it the bounded SQLITE_BUSY retry. Every store's write pool contends for
// that one shared cache, which makes this the driver that needs the retry most.
func TestMemoryDriverInheritsBusyRetry(t *testing.T) {
	t.Parallel()

	store, err := NewDriver().NewTokenLock("")
	require.NoError(t, err)

	tokenLock, ok := store.(*sqlite.TokenLockStore)
	require.True(t, ok, "the memory driver must build the SQLite token lock store")
	require.IsType(t, &sqlite.BusyRetryWriteDB{}, tokenLock.WriteDB,
		"the memory driver must inherit SQLite's SQLITE_BUSY retry on writes")
}
