/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
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

// TestWalletStoreGetWalletID asserts the not-found contract that the role Registry relies on to
// tell a transient storage error apart from "this identity has no binding": an unbound identity
// must resolve to ("", nil), and a bound identity must round-trip its wallet id. If GetWalletID
// returned an error for a missing key (as kvs.Get does), the registry would abort every lookup
// for a genuinely-unregistered identity instead of creating its wallet.
func TestWalletStoreGetWalletID(t *testing.T) {
	backend, err := NewInMemory()
	require.NoError(t, err)
	// NewInMemory shares a global in-memory backing store across the package's tests, so use a
	// tmsID and identities unique to this test to stay isolated from any other stored bindings.
	tmsID := token.TMSID{Network: "getwalletid", Channel: "getwalletid", Namespace: "getwalletid"}
	db := NewWalletStore(backend, tmsID)
	ctx := t.Context()

	// miss: never bound -> ("", nil), NOT an error
	got, err := db.GetWalletID(ctx, []byte("gwid-grace"), 0)
	require.NoError(t, err)
	assert.Empty(t, got)

	// bound under role 0 -> round-trips the wallet id
	require.NoError(t, db.StoreIdentity(ctx, []byte("gwid-grace"), "eID", "grace_wallet", 0, nil, "conf-1"))
	got, err = db.GetWalletID(ctx, []byte("gwid-grace"), 0)
	require.NoError(t, err)
	assert.Equal(t, "grace_wallet", got)

	// the same identity under a different role is still an independent miss
	got, err = db.GetWalletID(ctx, []byte("gwid-grace"), 1)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// failingKVS is a KVS whose Get always returns getErr. It lets a test drive GetWalletID's
// error classification directly, without depending on the real backend's internals.
type failingKVS struct {
	KVS
	getErr error
}

func (k *failingKVS) Get(context.Context, string, any) error { return k.getErr }

// TestWalletStoreGetWalletIDStoreFailurePropagates is the regression guard for #2063: a genuine
// storage failure must NOT collapse into the ("", nil) "no binding" answer, or the role Registry
// would treat a transient DB blip as an unregistered identity and create a duplicate wallet. Only
// a "does not exist" error is an authoritative miss; every other error must propagate.
func TestWalletStoreGetWalletIDStoreFailurePropagates(t *testing.T) {
	tmsID := token.TMSID{Network: "fail", Channel: "fail", Namespace: "fail"}
	ctx := context.Background()

	// A real store failure (timeout, connection reset, ...) must surface as an error.
	failing := &failingKVS{getErr: errors.Errorf("failed retrieving state [ns,id]: connection reset")}
	got, err := NewWalletStore(failing, tmsID).GetWalletID(ctx, []byte("erin"), 0)
	require.Error(t, err)
	assert.Empty(t, got)

	// A failure whose cause happens to end in "does not exist" - a missing or not-yet-migrated
	// table, as Postgres reports it - is still a failure, not a miss.
	missingTable := &failingKVS{getErr: errors.Wrapf(
		errors.New(`pq: relation "kvs" does not exist`), "failed retrieving state [ns,id]")}
	got, err = NewWalletStore(missingTable, tmsID).GetWalletID(ctx, []byte("erin"), 0)
	require.Error(t, err)
	assert.Empty(t, got)

	// A "not found" error is an authoritative miss: ("", nil).
	notFound := &failingKVS{getErr: errors.Errorf("state [ns,id] does not exist")}
	got, err = NewWalletStore(notFound, tmsID).GetWalletID(ctx, []byte("erin"), 0)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestIsNotFoundErr pins the classification both GetWalletID and IdentityExists hang their
// not-found contract on. The match has to be anchored at both ends, because "does not exist" is
// not the exclusive property of an absence error: a missing or not-yet-migrated table makes
// Postgres answer `relation "kvs" does not exist`, which arrives wrapped in FSC's
// "failed retrieving state [...]" and must stay an error. Reading it as a miss is the #2063
// duplicate-wallet bug, so the wrapped cases below are the ones worth having.
func TestIsNotFoundErr(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			"in-tree kvs miss",
			errors.Errorf("state [%s,%s] does not exist", "ns", "id"),
			true,
		},
		{
			// the vault backend now reports a miss as a nil error with the destination
			// untouched, so this is the shape its released module returns rather than one
			// produced in this tree - kept because that module is versioned independently
			"vault kvs miss",
			errors.Errorf("state of id [%s] does not exist", "kv1/data/panurus/id"),
			true,
		},
		{
			"store failure",
			errors.Wrapf(errors.New("connection reset by peer"), "failed retrieving state [ns,id]"),
			false,
		},
		{
			// the regression this anchoring exists for: the phrase is in the cause, not in a
			// miss, and an unanchored match answered "not bound" for an unreadable table
			"missing table reported as a cause",
			errors.Wrapf(errors.New(`pq: relation "kvs" does not exist`), "failed retrieving state [ns,id]"),
			false,
		},
		{
			"unmarshal failure",
			errors.Wrapf(errors.New("json: cannot unmarshal object into Go value of type string"),
				"failed retrieving state [ns,id], cannot unmarshal state"),
			false,
		},
		{
			// a miss that is wrapped is no longer an authoritative answer about absence: it
			// travelled through a layer that may have failed, so it propagates
			"wrapped miss",
			errors.Wrapf(errors.Errorf("state [ns,id] does not exist"), "failed to load wallet"),
			false,
		},
		{"unrelated error", errors.New("context deadline exceeded"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isNotFoundErr(tc.err))
		})
	}
}

// TestIsNotFoundErrMatchesLiveBackend keeps the literal messages in TestIsNotFoundErr honest.
// That table pins the matcher against strings this package spells out itself, so it stays green
// even if the backend stops returning them; this one asks the in-tree KVS for a key it does not
// hold and classifies the error it really answers with.
//
// It is the guard the anchoring documented on isNotFoundErr needs. The match is tied to FSC's
// exact wording, and FSC owns that wording - if a release rewords its absence error, every
// GetWalletID miss starts surfacing as a hard error and the role Registry aborts wallet
// creation for identities that simply have no binding yet. Failing here on the dependency bump
// is how that is found before a deployment finds it.
func TestIsNotFoundErrMatchesLiveBackend(t *testing.T) {
	backend, err := NewInMemory()
	require.NoError(t, err)

	var absent string
	err = backend.Get(t.Context(), "isnotfounderr/live/never-written", &absent)
	require.Error(t, err, "the in-tree KVS is expected to report a missing key as an error")
	assert.True(t, isNotFoundErr(err),
		"the in-tree KVS absence error is no longer classified as a miss, so every GetWalletID "+
			"miss now propagates as an error: reconcile notFoundHeads/notFoundSuffix with %q", err)
}

// TestWalletStoreIdentityExists asserts the round-trip contract: an identity that was never
// bound reports false with no error, and one bound by StoreIdentity reports true for the wallet
// and role it was bound under — and only for those.
func TestWalletStoreIdentityExists(t *testing.T) {
	backend, err := NewInMemory()
	require.NoError(t, err)
	// NewInMemory shares a global in-memory backing store across the package's tests, so use a
	// tmsID and identities unique to this test to stay isolated from any other stored bindings.
	tmsID := token.TMSID{Network: "idexists", Channel: "idexists", Namespace: "idexists"}
	db := NewWalletStore(backend, tmsID)
	ctx := t.Context()

	// miss: never bound -> (false, nil)
	exists, err := db.IdentityExists(ctx, []byte("ie-heidi"), "heidi_wallet", 0)
	require.NoError(t, err)
	assert.False(t, exists)

	require.NoError(t, db.StoreIdentity(ctx, []byte("ie-heidi"), "eID", "heidi_wallet", 0, nil, "conf-1"))

	// bound -> (true, nil)
	exists, err = db.IdentityExists(ctx, []byte("ie-heidi"), "heidi_wallet", 0)
	require.NoError(t, err)
	assert.True(t, exists)

	// the binding is scoped to the wallet and the role it was stored under
	exists, err = db.IdentityExists(ctx, []byte("ie-heidi"), "other_wallet", 0)
	require.NoError(t, err)
	assert.False(t, exists)

	exists, err = db.IdentityExists(ctx, []byte("ie-heidi"), "heidi_wallet", 1)
	require.NoError(t, err)
	assert.False(t, exists)
}

// TestWalletStoreIdentityExistsStoreFailurePropagates is the IdentityExists half of the #2063
// guard: the (bool, error) signature is only worth anything if a genuine read failure actually
// reaches the caller as an error. If it collapsed into false, Registry.ContainsIdentity would
// report a transient blip as non-membership — exactly what the error return exists to prevent.
func TestWalletStoreIdentityExistsStoreFailurePropagates(t *testing.T) {
	tmsID := token.TMSID{Network: "fail", Channel: "fail", Namespace: "fail"}
	ctx := context.Background()

	// A real store failure (timeout, connection reset, ...) must surface as an error, not false.
	failing := &failingKVS{getErr: errors.Errorf("failed retrieving state [ns,id]: connection reset")}
	exists, err := NewWalletStore(failing, tmsID).IdentityExists(ctx, []byte("erin"), "erin_wallet", 0)
	require.Error(t, err)
	assert.False(t, exists)

	// Same for IdentityExists: a cause that ends in "does not exist" must not read as "not
	// bound", or Registry.ContainsIdentity would report an unreadable table as non-membership.
	missingTable := &failingKVS{getErr: errors.Wrapf(
		errors.New(`pq: relation "kvs" does not exist`), "failed retrieving state [ns,id]")}
	exists, err = NewWalletStore(missingTable, tmsID).IdentityExists(ctx, []byte("erin"), "erin_wallet", 0)
	require.Error(t, err)
	assert.False(t, exists)

	// A "not found" error is an authoritative miss: (false, nil).
	notFound := &failingKVS{getErr: errors.Errorf("state [ns,id] does not exist")}
	exists, err = NewWalletStore(notFound, tmsID).IdentityExists(ctx, []byte("erin"), "erin_wallet", 0)
	require.NoError(t, err)
	assert.False(t, exists)

	// The hashicorp vault backend reports a missing id as a nil error with the destination left
	// untouched, so an empty value is a miss too — not a binding to the empty wallet id.
	absent := &failingKVS{getErr: nil}
	exists, err = NewWalletStore(absent, tmsID).IdentityExists(ctx, []byte("erin"), "erin_wallet", 0)
	require.NoError(t, err)
	assert.False(t, exists)
}
