/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token"
	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
)

// These tests exercise the store-level resource limits (limits.go) end to end
// against a real SQLite store, verifying that a read/write within the ceiling
// succeeds unchanged and that one past the ceiling fails closed rather than
// returning a truncated (and therefore wrong) result.

func newLimitedWalletStore(t *testing.T, limits common2.Limits) *WalletStore {
	t.Helper()
	db := openSQLiteDB(t)
	store, err := common2.NewWalletStore(db, db, common2.TableNames{
		Wallets:                "WALLETS",
		IdentityConfigurations: "IDCONFIGS",
	}, NewConditionInterpreter())
	require.NoError(t, err)
	store.ApplyLimits(limits)
	require.NoError(t, store.CreateSchema())

	return store
}

func newLimitedKeystore(t *testing.T, limits common2.Limits) *KeystoreStore {
	t.Helper()
	db := openSQLiteDB(t)
	store, err := common2.NewKeystoreStore(db, db, common2.TableNames{
		KeyStore: "KEYSTORE",
	}, NewConditionInterpreter(), &errorMapperForTest{})
	require.NoError(t, err)
	store.ApplyLimits(limits)
	require.NoError(t, store.CreateSchema())

	return store
}

// errorMapperForTest is a no-op SQL error wrapper; the keystore payload check
// fires before any DB round trip, so no real error mapping is needed here.
type errorMapperForTest struct{}

func (e *errorMapperForTest) WrapError(err error) error { return err }

// TestGetWalletIDsFailsClosedOverLimit stores more distinct wallet ids than the
// MaxResultRows ceiling and asserts the read errors instead of returning a
// silently truncated set, while a read at the ceiling succeeds in full.
func TestGetWalletIDsFailsClosedOverLimit(t *testing.T) {
	const roleID = 1
	const limit = 5

	store := newLimitedWalletStore(t, common2.Limits{MaxResultRows: limit})

	// Insert exactly the ceiling: the read must succeed and return them all.
	for i := range limit {
		require.NoError(t, store.StoreIdentity(t.Context(),
			token.Identity([]byte{byte(i)}), "eid", walletIDf(i), roleID, nil, ""))
	}
	ids, err := store.GetWalletIDs(t.Context(), roleID)
	require.NoError(t, err)
	require.Len(t, ids, limit)

	// One more distinct wallet tips the result past the ceiling: fail closed.
	require.NoError(t, store.StoreIdentity(t.Context(),
		token.Identity([]byte{byte(limit)}), "eid", walletIDf(limit), roleID, nil, ""))
	_, err = store.GetWalletIDs(t.Context(), roleID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
}

// TestGetWalletIDsUnlimited confirms that a disabled ceiling (0) reads an
// arbitrarily large set without error.
func TestGetWalletIDsUnlimited(t *testing.T) {
	const roleID = 1

	store := newLimitedWalletStore(t, common2.Limits{MaxResultRows: 0})
	for i := range 50 {
		require.NoError(t, store.StoreIdentity(t.Context(),
			token.Identity([]byte{byte(i)}), "eid", walletIDf(i), roleID, nil, ""))
	}
	ids, err := store.GetWalletIDs(t.Context(), roleID)
	require.NoError(t, err)
	require.Len(t, ids, 50)
}

// TestKeystorePutPayloadLimit rejects an opaque value whose marshaled size
// exceeds MaxPayloadSize, and accepts one at the ceiling.
func TestKeystorePutPayloadLimit(t *testing.T) {
	// A JSON string marshals to len+2 bytes (the quotes), so a ceiling of 34
	// admits a 32-char string ("..."+2) and rejects a 33-char one.
	store := newLimitedKeystore(t, common2.Limits{MaxPayloadSize: 34})

	require.NoError(t, store.Put("ok", strings.Repeat("a", 32)))

	err := store.Put("toobig", strings.Repeat("a", 64))
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")

	// The rejected write stored nothing.
	var got string
	require.Error(t, store.Get("toobig", &got))
}

// TestTokenIDInputCap rejects a variadic id input larger than MaxQueryIDs
// before the IN (...) clause is built, and accepts one at the ceiling. The cap
// fires ahead of any DB round trip, so an empty store is enough to exercise it;
// the at-limit call reaching "token not found" proves it passed the cap and
// ran the query.
func TestTokenIDInputCap(t *testing.T) {
	const limit = 3

	db := openSQLiteDB(t)
	tables, err := common2.GetTableNames("")
	require.NoError(t, err)
	store, err := common2.NewTokenStoreWithNotifier(db, db, tables, NewConditionInterpreter(), nil)
	require.NoError(t, err)
	store.ApplyLimits(common2.Limits{MaxQueryIDs: limit})
	require.NoError(t, store.CreateSchema())

	ids := make([]*token2.ID, 0, limit+1)
	for i := 0; i <= limit; i++ {
		ids = append(ids, &token2.ID{TxId: "tx", Index: uint64(i)})
	}

	// DeleteTokens is a mutation: an oversized batch must be rejected outright
	// (a LIMIT would silently leave some tokens unspent).
	require.Error(t, store.DeleteTokens(t.Context(), "spender", ids...))
	require.NoError(t, store.DeleteTokens(t.Context(), "spender", ids[:limit]...))

	// GetTokens: oversized input is rejected with the id-count error; an
	// at-limit input passes the cap and reaches the (empty-store) lookup.
	_, err = store.GetTokens(t.Context(), ids...)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
	_, err = store.GetTokens(t.Context(), ids[:limit]...)
	require.Error(t, err) // not the cap: the tokens simply do not exist
	require.NotContains(t, err.Error(), "exceeds maximum")
}

func walletIDf(i int) string {
	return fmt.Sprintf("wallet-%d", i)
}
