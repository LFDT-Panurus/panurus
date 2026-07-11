/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbtest

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func WalletTest(t *testing.T, cfgProvider cfgProvider) {
	t.Helper()
	for _, c := range walletCases {
		drv := cfgProvider(c.Name)
		db, err := drv.NewWallet("", c.Name)
		if err != nil {
			t.Fatal(err)
		}

		// Wallets.conf_id is a hard, NOT NULL foreign key into
		// IdentityConfigurations.conf_id, so every StoreIdentity call in these tests
		// needs a real configuration row to point at. Seed one via the IdentityStore
		// obtained from the same driver instance (same underlying datasource shared
		// with WalletStore — do not close it before db, closing either closes both),
		// and pass it (plus its UniqueID()) down to the tests below.
		identityDB, err := drv.NewIdentity("", c.Name)
		if err != nil {
			t.Fatal(err)
		}
		conf := driver.IdentityConfiguration{
			ID:     "wallet-test-conf",
			Type:   "core",
			URL:    "wallet-test-url",
			Config: []byte("config"),
			Raw:    []byte("raw"),
		}
		require.NoError(t, identityDB.AddConfiguration(t.Context(), conf))

		t.Run(c.Name, func(xt *testing.T) {
			c.Fn(xt, db, identityDB, conf)
		})
		require.NoError(t, db.Close())
		require.NoError(t, identityDB.Close())
	}
}

var walletCases = []struct {
	Name string
	Fn   func(*testing.T, driver.WalletStore, driver.IdentityStore, driver.IdentityConfiguration)
}{
	{"TDuplicate", TDuplicate},
	{"TWalletIdentities", TWalletIdentities},
	{"TWalletConfigurationLink", TWalletConfigurationLink},
	{"TGetConfID", TGetConfID},
}

func TDuplicate(t *testing.T, db driver.WalletStore, _ driver.IdentityStore, conf driver.IdentityConfiguration) {
	t.Helper()
	ctx := t.Context()
	id := []byte{254, 0, 155, 1}
	confID := conf.UniqueID()

	err := db.StoreIdentity(ctx, id, "eID", "duplicate", 0, []byte("meta"), confID)
	require.NoError(t, err)

	meta, err := db.LoadMeta(ctx, id, "duplicate", 0)
	require.NoError(t, err)
	assert.Equal(t, "meta", string(meta))

	err = db.StoreIdentity(ctx, id, "eID", "duplicate", 0, nil, confID)
	require.NoError(t, err)

	meta, err = db.LoadMeta(ctx, id, "duplicate", 0)
	require.NoError(t, err)
	assert.Equal(t, "meta", string(meta))
}

func TWalletIdentities(t *testing.T, db driver.WalletStore, _ driver.IdentityStore, conf driver.IdentityConfiguration) {
	t.Helper()
	ctx := t.Context()
	confID := conf.UniqueID()
	require.NoError(t, db.StoreIdentity(ctx, []byte("alice"), "eID", "alice_wallet", 0, nil, confID))
	require.NoError(t, db.StoreIdentity(ctx, []byte("alice"), "eID", "alice_wallet", 1, nil, confID))
	require.NoError(t, db.StoreIdentity(ctx, []byte("bob"), "eID", "bob_wallet", 0, nil, confID))
	require.NoError(t, db.StoreIdentity(ctx, []byte("alice"), "eID", "alice_wallet", 0, nil, confID))

	ids, err := db.GetWalletIDs(ctx, 0)
	require.NoError(t, err)
	assert.Equal(t, []driver.WalletID{"alice_wallet", "bob_wallet"}, ids)

	ids, err = db.GetWalletIDs(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, []driver.WalletID{"alice_wallet"}, ids)
}

// TWalletConfigurationLink asserts that a wallet's conf_id, once stored, resolves
// back to the exact IdentityConfiguration it was linked to — i.e. that
// IdentityConfiguration.UniqueID() round-trips through GetConfiguration and can be
// used to join a Wallets row back to its originating IdentityConfigurations row.
func TWalletConfigurationLink(t *testing.T, db driver.WalletStore, identityDB driver.IdentityStore, conf driver.IdentityConfiguration) {
	t.Helper()
	ctx := t.Context()
	confID := conf.UniqueID()

	require.NoError(t, db.StoreIdentity(ctx, []byte("carol"), "eID", "carol_wallet", 0, nil, confID))
	require.True(t, db.IdentityExists(ctx, []byte("carol"), "carol_wallet", 0))

	// The configuration this wallet links to must exist, under the exact
	// (id, type, url) the wallet's confID was derived from.
	fetched, err := identityDB.GetConfiguration(ctx, conf.ID, conf.Type, conf.URL)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	assert.Equal(t, confID, fetched.UniqueID())

	// A confID that does not correspond to any stored configuration must be
	// rejected — this is what makes the link a real (FK-backed, on SQL) join and
	// not just an opaque, unverified string.
	err = db.StoreIdentity(ctx, []byte("dave"), "eID", "dave_wallet", 0, nil, "unknown-conf-id")
	assert.Error(t, err)
}

// TGetConfID asserts that GetConfID round-trips the confID passed to StoreIdentity for a given
// identity, regardless of which role it was bound under, and returns an empty string with no
// error for an identity that was never bound. This is the read side that SignerRouter relies on
// to pin a signer to exactly one KeyManager without probing every KeyManager registered under the
// identity's type.
func TGetConfID(t *testing.T, db driver.WalletStore, _ driver.IdentityStore, conf driver.IdentityConfiguration) {
	t.Helper()
	ctx := t.Context()
	confID := conf.UniqueID()

	// miss: never bound
	got, err := db.GetConfID(ctx, []byte("erin"))
	require.NoError(t, err)
	assert.Empty(t, got)

	// bound under role 0
	require.NoError(t, db.StoreIdentity(ctx, []byte("erin"), "eID", "erin_wallet", 0, nil, confID))
	got, err = db.GetConfID(ctx, []byte("erin"))
	require.NoError(t, err)
	assert.Equal(t, confID, got)

	// bound again under a different role: still resolves to the same confID
	require.NoError(t, db.StoreIdentity(ctx, []byte("erin"), "eID", "erin_wallet_2", 1, nil, confID))
	got, err = db.GetConfID(ctx, []byte("erin"))
	require.NoError(t, err)
	assert.Equal(t, confID, got)
}
