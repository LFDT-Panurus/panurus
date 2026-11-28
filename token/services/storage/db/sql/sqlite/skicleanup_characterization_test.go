/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Characterization tests for the store behaviour the SKI cleanup relies on.

func newCharTokenStore(t *testing.T) *TokenStore {
	t.Helper()
	db, err := NewDriver(sqliteCfg(t.TempDir(), "char")).NewToken("")
	require.NoError(t, err)
	store, ok := db.(*TokenStore)
	require.True(t, ok, "unexpected token store type [%T]", db)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	return store
}

func storeOwnedToken(t *testing.T, ctx context.Context, db *TokenStore, txID, ownerType string, ownerIdentity []byte) {
	t.Helper()
	require.NoError(t, db.StoreToken(ctx, driver.TokenRecord{
		TxID:           txID,
		Index:          0,
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      ownerType,
		OwnerIdentity:  ownerIdentity,
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x01",
		Type:           "ABC",
		Amount:         big.NewInt(1),
		Owner:          true,
	}, []string{"alice"}))
}

// KeystoreStore.Delete of a key that was never stored returns nil.
func TestKeystoreDeleteMissingKeyReturnsNil(t *testing.T) {
	ks, err := NewDriver(sqliteCfg(t.TempDir(), "char")).NewKeyStore("", "char")
	require.NoError(t, err)
	store, ok := ks.(*KeystoreStore)
	require.True(t, ok, "unexpected keystore type [%T]", ks)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	require.NoError(t, store.Delete("missing-key"))

	n, err := store.Count(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

// HasTokenForIdentity returns true for a token that was deleted (spent).
func TestHasTokenForIdentityTrueForDeletedToken(t *testing.T) {
	ctx := t.Context()
	db := newCharTokenStore(t)
	id := []byte("owner-deleted")
	storeOwnedToken(t, ctx, db, "tx_del", "idemix", id)
	require.NoError(t, db.DeleteTokens(ctx, "spender", &token.ID{TxId: "tx_del", Index: 0}))

	has, err := db.HasTokenForIdentity(ctx, id, "idemix")
	require.NoError(t, err)
	assert.True(t, has, "spent tokens still count")
}

// Documents a gap: HasTokenForIdentity(id, "policy") misses a row stored with the legacy owner_type "Type (6)".
func TestHasTokenForIdentityPolicyMissesLegacyType6Row(t *testing.T) {
	ctx := t.Context()
	db := newCharTokenStore(t)
	id := []byte("owner-policy-legacy")
	storeOwnedToken(t, ctx, db, "tx_legacy", "Type (6)", id)

	has, err := db.HasTokenForIdentity(ctx, id, "policy")
	require.NoError(t, err)
	assert.False(t, has)

	has, err = db.HasTokenForIdentity(ctx, id, "Type (6)")
	require.NoError(t, err)
	assert.True(t, has, "exact owner_type matches")
}

// GetDeletedTokensPendingSKICleanup returns deleted owned multisig/htlc/policy rows with their owner_identity.
func TestGetDeletedTokensPendingSKICleanupScriptOwnerTypes(t *testing.T) {
	ctx := t.Context()
	db := newCharTokenStore(t)
	want := map[string][]byte{}
	for _, ot := range []string{"multisig", "htlc", "policy"} {
		txID := "tx_" + ot
		want[txID] = []byte("inner-" + ot)
		storeOwnedToken(t, ctx, db, txID, ot, want[txID])
		require.NoError(t, db.DeleteTokens(ctx, "spender", &token.ID{TxId: txID, Index: 0}))
	}

	// A negative TTL puts the cutoff in the future, so every deleted row qualifies.
	toks, err := db.GetDeletedTokensPendingSKICleanup(ctx, -time.Minute, 100)
	require.NoError(t, err)
	require.Len(t, toks, 3)
	for _, tok := range toks {
		assert.Equal(t, want[tok.TxID], tok.OwnerIdentity, tok.TxID)
		assert.Equal(t, "tx_"+tok.OwnerType, tok.TxID)
	}
}
