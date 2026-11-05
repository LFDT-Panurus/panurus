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
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/stretchr/testify/require"
)

func TestIdentityDBWithHashicorpVault(t *testing.T) {
	terminate, vaultURL, token := hashicorp.StartHashicorpVaultContainer(t, 11200)
	defer terminate()
	client, err := hashicorp.NewVaultClient(vaultURL, token)
	require.NoError(t, err)

	for i, c := range dbtest.IdentityCases {
		backend, err := hashicorp.NewWithClient(client, fmt.Sprintf("kv1/data/panurus/%d/", i))
		require.NoError(t, err)
		db := kvs.NewIdentityStore(backend, token2.TMSID{
			Network:   "apple",
			Channel:   "pears",
			Namespace: "strawberries",
		})
		t.Run(c.Name, func(xt *testing.T) {
			c.Fn(xt, db)
		})
	}
}
