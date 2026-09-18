/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package membership_test

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reloadIdentityType = identity.Type(99)

// newReloadableMembership builds a LocalMembership whose KeyManager resolves whatever raw identity
// bytes currentIdentity currently points at, so a test can Load twice with a different identity set
// each time.
func newReloadableMembership(t *testing.T, currentIdentity *[]byte) *membership.LocalMembership {
	t.Helper()

	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)

	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)
	iss.NotifierReturns(nil, storage.ErrNotSupported)

	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("e1")
	km.AnonymousReturns(false)
	km.IsRemoteReturns(false)
	km.IdentityTypeReturns(reloadIdentityType)
	km.IdentityStub = func(context.Context, []byte) (*idriver.IdentityDescriptor, error) {
		return &idriver.IdentityDescriptor{Identity: *currentIdentity, AuditInfo: []byte("ai")}, nil
	}

	kmp := &mock.KeyManagerProvider{}
	kmp.GetReturns(km, nil)

	return membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		&mock.SignerDeserializerManager{},
		iss,
		"testType",
		false,
		ip,
		kmp,
	)
}

// wrappedIdentity returns the identity bytes LocalMembership actually indexes for a KeyManager
// reporting reloadIdentityType, i.e. the raw bytes wrapped with the type tag.
func wrappedIdentity(t *testing.T, raw string) []byte {
	t.Helper()
	wrapped, err := identity.WrapWithType(reloadIdentityType, []byte(raw))
	require.NoError(t, err)

	return wrapped
}

// TestLoad_ResetsIdentityMap covers the stale-read reported in issue #2073: Load used to reset
// localIdentitiesByName, localIdentitiesByConfig and localIdentities but not
// localIdentitiesByIdentity, so an identity present only in an earlier Load kept resolving through
// that fourth map as if it were part of the set just loaded.
func TestLoad_ResetsIdentityMap(t *testing.T) {
	ctx := t.Context()

	raw := []byte("id-alice")
	lm := newReloadableMembership(t, &raw)

	require.NoError(t, lm.Load(ctx, []idriver.ConfiguredIdentity{
		{ID: "alice", Path: "/tmp/alice", Default: true},
	}, nil))

	alice := wrappedIdentity(t, "id-alice")
	label, err := lm.GetIdentifier(ctx, alice)
	require.NoError(t, err)
	assert.Equal(t, "alice", label)

	// Second Load with a disjoint identity set.
	raw = []byte("id-bob")
	require.NoError(t, lm.Load(ctx, []idriver.ConfiguredIdentity{
		{ID: "bob", Path: "/tmp/bob", Default: true},
	}, nil))

	bob := wrappedIdentity(t, "id-bob")
	label, err = lm.GetIdentifier(ctx, bob)
	require.NoError(t, err)
	assert.Equal(t, "bob", label)

	// alice was only in the first load: resolving it must fail rather than return the pre-reload
	// entry from localIdentitiesByIdentity.
	_, err = lm.GetIdentifier(ctx, alice)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identifier not found")
}

// TestLoad_ReloadKeepsSameIdentityResolvable guards the other direction: resetting the map must not
// drop an identity that the second Load registers again.
func TestLoad_ReloadKeepsSameIdentityResolvable(t *testing.T) {
	ctx := t.Context()

	raw := []byte("id-alice")
	lm := newReloadableMembership(t, &raw)

	configured := []idriver.ConfiguredIdentity{{ID: "alice", Path: "/tmp/alice", Default: true}}
	require.NoError(t, lm.Load(ctx, configured, nil))
	require.NoError(t, lm.Load(ctx, configured, nil))

	label, err := lm.GetIdentifier(ctx, wrappedIdentity(t, "id-alice"))
	require.NoError(t, err)
	assert.Equal(t, "alice", label)
}

// TestClose_UnregistersOwnConfIDsFromSignerRouter covers the byConfID teardown reported in issue
// #2073: Close releases the KeyManagers it loaded, so the routes pinning them must go with them
// instead of keeping closed KeyManagers reachable - and alive - for the router's lifetime.
func TestClose_UnregistersOwnConfIDsFromSignerRouter(t *testing.T) {
	ctx := t.Context()

	raw := []byte("id-alice")
	lm := newReloadableMembership(t, &raw)

	router := identity.NewSignerRouter(nil)
	lm.SetSignerRouter(router)

	require.NoError(t, lm.Load(ctx, []idriver.ConfiguredIdentity{
		{ID: "alice", Path: "/tmp/alice", Default: true},
	}, nil))
	require.Equal(t, 1, router.Len(), "the loaded identity must have pinned its KeyManager")

	lm.Close()
	assert.Equal(t, 0, router.Len(), "Close must drop the routes to the KeyManagers it released")
}
