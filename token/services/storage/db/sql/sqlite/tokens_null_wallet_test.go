/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"database/sql"
	"math/big"
	"path"
	"testing"

	driver2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestQueryTokenDetailsNullOwnerWalletID checks QueryTokenDetails on rows whose
// owner_wallet_id is NULL. StoreToken never writes NULL, so the rows are edited directly.
func TestQueryTokenDetailsNullOwnerWalletID(t *testing.T) {
	const name = "nullwallet"
	ctx := t.Context()
	dir := t.TempDir()

	store, err := NewDriver(sqliteCfg(dir, name)).NewToken("", name)
	require.NoError(t, err)
	tokenDB, ok := store.(*TokenStore)
	require.True(t, ok)
	t.Cleanup(func() { _ = tokenDB.Close() })

	record := func(txID, ownerWalletID string) driver2.TokenRecord {
		return driver2.TokenRecord{
			TxID:           txID,
			OwnerRaw:       []byte{1, 2, 3},
			OwnerType:      "idemix",
			OwnerIdentity:  []byte{},
			IssuerRaw:      []byte{},
			OwnerWalletID:  ownerWalletID,
			Ledger:         []byte("ledger"),
			LedgerMetadata: []byte{},
			Quantity:       "0x01",
			Type:           "TST",
			Amount:         big.NewInt(1),
			Owner:          true,
		}
	}
	// tx1: no Ownership row; tx2: Ownership row for bob.
	require.NoError(t, tokenDB.StoreToken(ctx, record("tx1", "alice"), nil))
	require.NoError(t, tokenDB.StoreToken(ctx, record("tx2", ""), []string{"bob"}))

	tables, err := common2.GetTableNames(name, name)
	require.NoError(t, err)
	raw, err := sql.Open("sqlite", "file:"+path.Join(dir, "db.sqlite")+"?_pragma=busy_timeout(20000)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	res, err := raw.ExecContext(ctx, "UPDATE "+tables.Tokens+" SET owner_wallet_id = NULL") //nolint:gosec // table name comes from GetTableNames, not input
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(2), n)

	for _, tc := range []struct {
		params   driver2.QueryTokenDetailsParams
		expected map[string]string
	}{
		{driver2.QueryTokenDetailsParams{}, map[string]string{"tx1": "", "tx2": "bob"}},
		{driver2.QueryTokenDetailsParams{IncludeDeleted: true}, map[string]string{"tx1": "", "tx2": "bob"}},
		{driver2.QueryTokenDetailsParams{IDs: []*token.ID{{TxId: "tx1"}}}, map[string]string{"tx1": ""}},
		{driver2.QueryTokenDetailsParams{WalletID: "bob"}, map[string]string{"tx2": "bob"}},
		{driver2.QueryTokenDetailsParams{WalletID: "alice"}, map[string]string{}},
	} {
		it, err := tokenDB.QueryTokenDetails(ctx, tc.params)
		require.NoError(t, err, "params %+v", tc.params)
		got := map[string]string{}
		for {
			d, err := it.Next()
			require.NoError(t, err, "params %+v", tc.params)
			if d == nil {
				break
			}
			got[d.TxID] = d.OwnerEnrollment
		}
		it.Close()
		assert.Equal(t, tc.expected, got, "params %+v", tc.params)
	}
}
