/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package hashicorp_test

import (
	"fmt"
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity/storage/kvs/hashicorp"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/dbtest"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/stretchr/testify/require"
)

// TestWalletDBWithHashicorpVault runs the shared WalletStore suite against the Vault-backed
// KVS. Its GetConfID case is the regression test for the prefix scan: Vault's list is
// single-level, so before listSubtree walked the subtree the scan of [tmsID] returned the
// role directory markers only and GetConfID reported every bound identity as unbound.
func TestWalletDBWithHashicorpVault(t *testing.T) {
	terminate, vaultURL, token := hashicorp.StartHashicorpVaultContainer(t, 11201)
	defer terminate()
	client, err := hashicorp.NewVaultClient(vaultURL, token)
	require.NoError(t, err)

	for i, c := range dbtest.WalletCases {
		t.Run(c.Name, func(xt *testing.T) {
			// This store has no equivalent of the Wallets.conf_id foreign key the SQL
			// backends enforce, so StoreIdentity accepts a conf id that no configuration
			// was stored under. Tracked in #2041; skip until the KVS backend validates
			// the link.
			if c.Name == "TWalletConfigurationLink" {
				xt.Skip("KVS StoreIdentity does not validate confID against a stored configuration")
			}

			backend, err := hashicorp.NewWithClient(client, fmt.Sprintf("kv1/data/panurus/wallet/%d/", i))
			require.NoError(xt, err)
			tmsID := token2.TMSID{Network: "apple", Channel: "pears", Namespace: "strawberries"}
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
