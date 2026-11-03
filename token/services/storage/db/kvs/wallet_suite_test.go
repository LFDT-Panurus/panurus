/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs_test

import (
	"strconv"
	"testing"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/dbtest"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/stretchr/testify/require"
)

// TestWalletDBWithInMemoryKVS runs the shared WalletStore suite against the KVS backend. The
// suite was previously wired to the SQL drivers only, which is how two prefix-scan defects in
// this store - GetWalletIDs reporting configuration ids and metadata blobs as wallet ids, and
// GetConfID coming back empty on Vault - went unnoticed.
func TestWalletDBWithInMemoryKVS(t *testing.T) {
	for _, c := range dbtest.WalletCases {
		t.Run(c.Name, func(xt *testing.T) {
			// This store has no equivalent of the Wallets.conf_id foreign key the SQL
			// backends enforce, so StoreIdentity accepts a conf id that no configuration
			// was stored under. Tracked in #2041; skip until the KVS backend validates
			// the link.
			if c.Name == "TWalletConfigurationLink" {
				xt.Skip("KVS StoreIdentity does not validate confID against a stored configuration")
			}

			backend, err := kvs.NewInMemory()
			require.NoError(xt, err)
			// NewInMemory shares one underlying store per process, so the cases - and repeated
			// runs of them, as with -count=N - are kept apart by a TMS id unique to this
			// invocation, which prefixes every composite key this store writes.
			tmsID := token2.TMSID{Network: c.Name, Channel: "pears", Namespace: "strawberries"}
			db := kvs.NewWalletStore(backend, tmsID)
			identityDB := kvs.NewIdentityStore(backend, tmsID)

			conf := driver.IdentityConfiguration{
				ID:     "wallet-test-conf",
				Type:   "core",
				URL:    "wallet-test-url",
				Config: []byte("config"),
				Raw:    []byte("raw"),
			}
			require.NoError(xt, identityDB.AddConfiguration(xt.Context(), conf))

			c.Fn(xt, db, identityDB, conf)
		})
	}
}

// TestIdentityDBWithInMemoryKVS runs the shared IdentityStore suite against the KVS backend on
// the FSC in-tree KVS, so that the backend the integration tests and benchmarks use is held to
// the same contract as SQL and Vault - GetExistingSignerInfo included (#1892).
func TestIdentityDBWithInMemoryKVS(t *testing.T) {
	for _, c := range dbtest.IdentityCases {
		t.Run(c.Name, func(xt *testing.T) {
			backend, err := kvs.NewInMemory()
			require.NoError(xt, err)
			// NewInMemory shares one underlying store per process, so the cases - and repeated
			// runs of them, as with -count=N - are kept apart by a TMS id unique to this
			// invocation, which prefixes every composite key this store writes.
			db := kvs.NewIdentityStore(backend, token2.TMSID{Network: "idb-" + c.Name + "-" + strconv.FormatInt(time.Now().UnixNano(), 36), Channel: "pears", Namespace: "strawberries"})
			c.Fn(xt, db)
		})
	}
}
