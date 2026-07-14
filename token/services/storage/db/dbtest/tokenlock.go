/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbtest

import (
	"testing"
	"time"

	driver2 "github.com/LFDT-Panurus/panurus/token/driver"
	driver3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

func TokenLocksTest(t *testing.T, cfgProvider cfgProvider) {
	t.Helper()
	for _, c := range tokenLockDBCases {
		driver := cfgProvider(c.Name)

		// Create token store first to ensure the tokens table exists
		// This is required because token locks now have a foreign key constraint
		// referencing the tokens table
		tokenDB, err := driver.NewToken("", c.Name)
		if err != nil {
			t.Fatal(err)
		}

		tokenLockDB, err := driver.NewTokenLock("", c.Name)
		if err != nil {
			utils.IgnoreError(tokenDB.Close)
			t.Fatal(err)
		}
		tokenTransactionDB, err := driver.NewOwnerTransaction("", c.Name)
		if err != nil {
			utils.IgnoreError(tokenDB.Close)
			utils.IgnoreError(tokenLockDB.Close)
			t.Fatal(err)
		}
		t.Run(c.Name, func(xt *testing.T) {
			defer utils.IgnoreError(tokenDB.Close)
			defer utils.IgnoreError(tokenLockDB.Close)
			defer utils.IgnoreError(tokenTransactionDB.Close)
			c.Fn(xt, tokenDB, tokenLockDB, tokenTransactionDB)
		})
	}
}

var tokenLockDBCases = []struct {
	Name string
	Fn   func(*testing.T, driver3.TokenStore, driver3.TokenLockStore, driver3.TokenTransactionStore)
}{
	{"TestFully", TestFully},
}

func TestFully(t *testing.T, tokenDB driver3.TokenStore, tokenLockDB driver3.TokenLockStore, tokenTransactionDB driver3.TokenTransactionStore) {
	ctx := t.Context()

	// First, create a token request in the transaction store
	txReq, err := tokenTransactionDB.NewTransactionStoreTransaction()
	require.NoError(t, err)
	require.NoError(t, txReq.AddTokenRequest(ctx, "apple", []byte("apple_tx_content"), nil, nil, driver2.PPHash("tr")))
	require.NoError(t, txReq.Commit())

	// Create a token in the tokens table so the foreign key constraint is satisfied
	tokenTx, err := tokenDB.NewTokenDBTransaction()
	require.NoError(t, err)
	tokenRecord := driver3.TokenRecord{
		TxID:           "apple",
		Index:          0,
		OwnerRaw:       []byte("owner1"),
		OwnerType:      "idemix",
		OwnerIdentity:  []byte("owner1"),
		Ledger:         []byte("ledger_data"),
		LedgerMetadata: []byte{}, // Empty metadata
		Quantity:       "0x64",   // 100 in hex
		Type:           "USD",
		Amount:         100,
		Owner:          true,
	}
	err = tokenTx.StoreToken(ctx, tokenRecord, []string{"owner1"})
	require.NoError(t, err, "Store token should succeed")
	require.NoError(t, tokenTx.Commit())

	// Lock the token - this will now succeed because the token exists in the tokens table
	err = tokenLockDB.Lock(ctx, &token.ID{TxId: "apple", Index: 0}, "pineapple")
	require.NoError(t, err, "Lock should succeed")

	// Unlock the token by transaction ID
	err = tokenLockDB.UnlockByTxID(ctx, "pineapple")
	require.NoError(t, err, "Unlock should succeed")

	// Cleanup should work correctly
	require.NoError(t, tokenLockDB.Cleanup(ctx, 1*time.Second))
}
