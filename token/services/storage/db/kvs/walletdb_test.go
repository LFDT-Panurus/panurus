/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWalletStoreGetConfID asserts that GetConfID round-trips the confID passed to
// StoreIdentity for a given identity, regardless of which role it was bound under, and returns
// an empty string with no error for an identity that was never bound. This is the KVS read side
// that SignerRouter relies on to pin a signer to exactly one KeyManager without probing every
// KeyManager registered under the identity's type.
func TestWalletStoreGetConfID(t *testing.T) {
	backend, err := NewInMemory()
	require.NoError(t, err)
	tmsID := token.TMSID{Network: "apple", Channel: "pears", Namespace: "strawberries"}
	db := NewWalletStore(backend, tmsID)
	ctx := t.Context()

	// miss: never bound
	got, err := db.GetConfID(ctx, []byte("erin"))
	require.NoError(t, err)
	assert.Empty(t, got)

	const confID = "wallet-test-conf-id"

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

	// a different identity never bound in this TMS still misses cleanly
	got, err = db.GetConfID(ctx, []byte("frank"))
	require.NoError(t, err)
	assert.Empty(t, got)
}
