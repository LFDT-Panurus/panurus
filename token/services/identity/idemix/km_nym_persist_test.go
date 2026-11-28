/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package idemix

import (
	"testing"

	"github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	kvs2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Finding: each Identity() call derives a fresh nym, and every nym key is
// persisted in the keystore under its SKI (a fresh BCCSP over the same KVS finds it).
func TestKeyManager_IdentityPersistsDistinctNymKeys(t *testing.T) {
	const curveID = math.BLS12_381_BBS_GURVY
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig("./testdata/bls12_381_bbs_gurvy/idemix")
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)
	csp, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)
	km, err := NewKeyManager(config, types.Standard, csp)
	require.NoError(t, err)

	const n = 3
	ids := map[string]bool{}
	skis := make([][]byte, 0, n)
	for range n {
		desc, err := km.Identity(t.Context(), nil)
		require.NoError(t, err)
		ids[string(desc.Identity)] = true
		ski, err := crypto.SKIFromIdentity(desc.Identity)
		require.NoError(t, err)
		skis = append(skis, ski)
	}
	assert.Len(t, ids, n, "every Identity() call yields a different identity")
	assert.NotEqual(t, skis[0], skis[1])
	assert.NotEqual(t, skis[1], skis[2])

	// Reopen the keystore from the same backend: every nym key is still there.
	keyStore2, err := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)
	csp2, err := crypto.NewBCCSP(keyStore2, curveID)
	require.NoError(t, err)
	for _, ski := range skis {
		k, err := csp2.GetKey(ski)
		require.NoError(t, err, "nym key [%x] must be persisted", ski)
		assert.True(t, k.Private())
		pk, err := k.PublicKey()
		require.NoError(t, err)
		assert.Equal(t, ski, pk.SKI())
	}
}
