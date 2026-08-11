/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/stretchr/testify/require"
)

// TestNewStoresWithDigitsInTMSID is a regression test for
// https://github.com/LFDT-Panurus/panurus/issues/2034.
//
// The table names are derived from the TMS identity (network, channel,
// namespace). Digits were missing from the allow-list used while escaping those
// parameters, and the check panicked instead of returning an error, so building
// any store for a channel called e.g. "channel1" — an extremely common name —
// crashed the node. Nothing between here and the escaping function recovers, so
// this exercises the real driver-construction path (including CREATE TABLE) and
// not just the escaping helper in isolation.
func TestNewStoresWithDigitsInTMSID(t *testing.T) {
	for _, tmsID := range [][]string{
		{"testnetwork", "channel1", "ns"},
		{"testnetwork", "testchannel1", "ns"},
		{"testnetwork", "mychannel01", "ns"},
		{"network1", "channel1", "namespace1"},
		{"net0", "ch-1.2", "ns_3"},
		// A leading digit is fine: the table prefix precedes it.
		{"1network", "channel1", "ns"},
	} {
		t.Run(tmsID[1], func(t *testing.T) {
			d := NewDriver(sqliteCfg(t.TempDir(), "tsdk"))

			// Every store type goes through the same table-name construction.
			requireStore(t, "token", func() (driver.TokenStore, error) { return d.NewToken("", tmsID...) })
			requireStore(t, "tokenlock", func() (driver.TokenLockStore, error) { return d.NewTokenLock("", tmsID...) })
			requireStore(t, "wallet", func() (driver.WalletStore, error) { return d.NewWallet("", tmsID...) })
			requireStore(t, "identity", func() (driver.IdentityStore, error) { return d.NewIdentity("", tmsID...) })
			requireStore(t, "keystore", func() (driver.KeyStore, error) { return d.NewKeyStore("", tmsID...) })
			requireStore(t, "audittx", func() (driver.AuditTransactionStore, error) { return d.NewAuditTransaction("", tmsID...) })
			requireStore(t, "ownertx", func() (driver.TokenTransactionStore, error) { return d.NewOwnerTransaction("", tmsID...) })
			requireStore(t, "endorser", func() (driver.EndorserStore, error) { return d.NewEndorser("", tmsID...) })
		})
	}
}

// requireStore runs newStore as a subtest and requires that it neither panics
// nor returns an error.
func requireStore[V any](t *testing.T, name string, newStore func() (V, error)) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		require.NotPanics(t, func() {
			_, err := newStore()
			require.NoError(t, err)
		})
	})
}

// TestNewStoreWithUnsupportedCharsInTMSID checks that a TMS identity that cannot
// be turned into a legal SQL identifier is reported as an error by the driver
// rather than crashing the process.
func TestNewStoreWithUnsupportedCharsInTMSID(t *testing.T) {
	for _, tmsID := range [][]string{
		{"testnetwork", "channel!", "ns"},
		{"testnetwork", "channel 1", "ns"},
		{"testnetwork", "channel;drop table x", "ns"},
		{"testnetwork", "chann€l", "ns"},
	} {
		t.Run(tmsID[1], func(t *testing.T) {
			d := NewDriver(sqliteCfg(t.TempDir(), "tsdk"))
			require.NotPanics(t, func() {
				var store driver.TokenStore
				store, err := d.NewToken("", tmsID...)
				require.Error(t, err)
				require.Nil(t, store)
			})
		})
	}
}
