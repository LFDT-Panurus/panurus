/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ownedskis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/signers"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// testDB is a SQLite database with the Tokens schema, opened the way the command opens it.
type testDB struct {
	t          *testing.T
	path       string
	configPath string
	stores     *signers.Stores
}

func newTestDB(t *testing.T) *testDB {
	t.Helper()
	dir := t.TempDir()
	db := &testDB{t: t, path: filepath.Join(dir, "data.db"), configPath: filepath.Join(dir, "config.yaml")}
	require.NoError(t, os.WriteFile(db.configPath, []byte("driver: sqlite\ndataSource: "+db.path+"\n"), 0o600))

	cfg, err := signers.LoadConfig(db.configPath)
	require.NoError(t, err)
	db.stores, err = signers.NewStores(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.stores.Close()) })
	require.NoError(t, db.stores.Token.CreateSchema())

	return db
}

func (db *testDB) source() *SQLOwnerSource {
	return &SQLOwnerSource{
		DB:              db.stores.ReadDB,
		TokensTable:     db.stores.TableNames.Tokens,
		CondInterpreter: db.stores.CondInterpreter,
	}
}

// storeToken stores a token the production way: owner wallet set, no Ownership rows.
func (db *testDB) storeToken(txID string, ownerType string, identity []byte, owner bool) {
	db.storeTokenWithOwners(txID, ownerType, identity, owner, nil)
}

func (db *testDB) storeTokenWithOwners(txID string, ownerType string, identity []byte, owner bool, owners []string) {
	db.t.Helper()
	tr := driver.TokenRecord{
		TxID:           txID,
		Index:          0,
		OwnerRaw:       identity,
		OwnerType:      ownerType,
		OwnerIdentity:  identity,
		Ledger:         []byte("ledger"),
		LedgerFormat:   "fmt",
		LedgerMetadata: []byte("meta"),
		IssuerRaw:      []byte("issuer"),
		Quantity:       "0x01",
		Type:           "USD",
		Amount:         big.NewInt(1),
		Owner:          owner,
	}
	if owner {
		tr.OwnerWalletID = "w"
	}
	require.NoError(db.t, db.stores.Token.StoreToken(context.Background(), tr, owners))
}

func (db *testDB) spend(txID string) {
	db.t.Helper()
	require.NoError(db.t, db.stores.Token.DeleteTokens(context.Background(), "spender", &token.ID{TxId: txID, Index: 0}))
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)

	return hex.EncodeToString(h[:])
}
