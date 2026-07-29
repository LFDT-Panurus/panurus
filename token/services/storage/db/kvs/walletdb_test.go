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

// TestWalletStoreIdentityCount asserts that IdentityCount reports the identities bound to one
// wallet for one role, and only those. StoreIdentity writes several entries per identity (the
// wallet reference plus "meta" and "configid" companions), so the count must select exactly the
// wallet-reference entries or it would multiply every binding.
func TestWalletStoreIdentityCount(t *testing.T) {
	backend, err := NewInMemory()
	require.NoError(t, err)
	tmsID := token.TMSID{Network: "apple", Channel: "pears", Namespace: "strawberries"}
	db := NewWalletStore(backend, tmsID)
	ctx := t.Context()

	const confID = "wallet-test-conf-id"

	// A wallet with nothing bound to it counts zero rather than failing.
	count, err := db.IdentityCount(ctx, "frank_wallet", 0)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Metadata is stored alongside the binding; the count must still be one per identity.
	require.NoError(t, db.StoreIdentity(ctx, []byte("frank-1"), "eID", "frank_wallet", 0, []byte("meta"), confID))
	require.NoError(t, db.StoreIdentity(ctx, []byte("frank-2"), "eID", "frank_wallet", 0, nil, confID))
	count, err = db.IdentityCount(ctx, "frank_wallet", 0)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Re-storing an identity that is already bound must not move the count.
	require.NoError(t, db.StoreIdentity(ctx, []byte("frank-1"), "eID", "frank_wallet", 0, []byte("meta"), confID))
	count, err = db.IdentityCount(ctx, "frank_wallet", 0)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// The same identity under a different role is a distinct binding, counted separately.
	require.NoError(t, db.StoreIdentity(ctx, []byte("frank-1"), "eID", "frank_wallet", 1, nil, confID))
	count, err = db.IdentityCount(ctx, "frank_wallet", 0)
	require.NoError(t, err)
	assert.Equal(t, 2, count, "role 0 must not see the role 1 binding")
	count, err = db.IdentityCount(ctx, "frank_wallet", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Another wallet's identities must not be counted.
	require.NoError(t, db.StoreIdentity(ctx, []byte("grace-1"), "eID", "grace_wallet", 0, nil, confID))
	count, err = db.IdentityCount(ctx, "frank_wallet", 0)
	require.NoError(t, err)
	assert.Equal(t, 2, count, "another wallet's identities must not be counted")
	count, err = db.IdentityCount(ctx, "grace_wallet", 0)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}
