/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package membership_test

import (
	"context"
	"testing"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership/mock"
	identitymock "github.com/LFDT-Panurus/panurus/token/services/identity/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storedConfID stands for a conf_id persisted by an earlier release, back when the composite
// key behind IdentityConfiguration.UniqueID was encoded differently. Any configuration with a
// separator or an escape character in ID, Type or URL - such as the ID below - gets a different
// UniqueID once field escaping is introduced, while identity_configurations keeps this value and
// wallets.conf_id references it through a foreign key.
//
// The exact bytes are irrelevant; what matters is that the stored value, whatever it is, is the
// one identities get bound under. An opaque sentinel makes that explicit and avoids pinning the
// test to a superseded encoder.
const storedConfID = "conf-id-persisted-by-an-earlier-release"

// confIDTestID contains the separator on purpose: it is the realistic case, since local identity
// names come from directory entries (registerLocalIdentities) and URLs are filesystem paths.
const confIDTestID = "alice@org1"

const confIDTestURL = "/msp/alice@org1"

// confIDFixture builds a LocalMembership over a mocked identity store and a single non-anonymous
// KeyManager, ready to Load the configuration described by confIDTestID/confIDTestURL.
type confIDFixture struct {
	lm     *membership.LocalMembership
	store  *mock.IdentityStoreService
	km     *mock.KeyManager
	router *identity.SignerRouter
}

func newConfIDFixture(t *testing.T) *confIDFixture {
	t.Helper()

	cfg := &mock.Config{}
	// keep the configured path intact, so the store lookup sees the URL under test
	cfg.TranslatePathStub = func(p string) string { return p }

	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)

	store := &mock.IdentityStoreService{}
	store.NotifierReturns(nil, storage.ErrNotSupported)
	store.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)
	store.AddConfigurationReturns(nil)

	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("e1")
	km.AnonymousReturns(false)
	km.IsRemoteReturns(false)
	km.IdentityTypeReturns(identity.Type(99))
	km.IdentityReturns(&idriver.IdentityDescriptor{Identity: []byte("id1"), AuditInfo: []byte("ai1")}, nil)

	kmp := &mock.KeyManagerProvider{}
	kmp.GetReturns(km, nil)

	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		cfg,
		[]byte("netid"),
		&mock.SignerDeserializerManager{},
		store,
		"testType",
		false,
		ip,
		kmp,
	)

	router := identity.NewSignerRouter(nil)
	lm.SetSignerRouter(router)

	return &confIDFixture{lm: lm, store: store, km: km, router: router}
}

func (f *confIDFixture) load(t *testing.T, ctx context.Context) idriver.IdentityInfo {
	t.Helper()

	require.NoError(t, f.lm.Load(ctx, []idriver.ConfiguredIdentity{{
		ID:   confIDTestID,
		Path: confIDTestURL,
	}}, nil))

	info, err := f.lm.GetIdentityInfo(ctx, confIDTestID, nil)
	require.NoError(t, err)
	require.NotNil(t, info)

	return info
}

// TestLocalMembership_BindsUnderStoredConfID is the regression test for the upgrade path: when a
// configuration is already persisted, its identities must be bound under the conf_id on disk and
// not under a freshly computed UniqueID.
//
// Without this, an upgrade that changes the composite-key encoding leaves
// identity_configurations holding the old conf_id (commitLocalIdentity looks the configuration up
// by (id, type, url), so it is never rewritten) while LocalIdentity.ConfigurationID carries the
// new one. That value reaches WalletStore.StoreIdentity via BindIdentity and violates the foreign
// key on wallets.conf_id, so the node can no longer register recipients or mint pseudonyms.
func TestLocalMembership_BindsUnderStoredConfID(t *testing.T) {
	ctx := t.Context()

	f := newConfIDFixture(t)
	f.store.GetConfigurationIDReturns(storedConfID, nil)

	info := f.load(t, ctx)

	assert.Equal(t, storedConfID, info.ConfigurationID(),
		"identities must be bound under the persisted conf_id, not a recomputed UniqueID")

	// the configuration is already stored, so it must not be inserted again
	assert.Zero(t, f.store.AddConfigurationCallCount())

	// and it must be looked up by the tuple the store is keyed on
	require.Equal(t, 1, f.store.GetConfigurationIDCallCount())
	_, id, typ, url := f.store.GetConfigurationIDArgsForCall(0)
	assert.Equal(t, confIDTestID, id)
	assert.Equal(t, "testType", typ)
	assert.Equal(t, confIDTestURL, url)
}

// TestLocalMembership_ComputesConfIDForNewConfiguration covers the other branch: a configuration
// the store does not know yet has no persisted conf_id to honour, so the value is computed from
// the configuration - and it must be the same value the AddConfiguration that follows writes.
func TestLocalMembership_ComputesConfIDForNewConfiguration(t *testing.T) {
	ctx := t.Context()

	f := newConfIDFixture(t)
	f.store.GetConfigurationIDReturns("", nil)

	info := f.load(t, ctx)

	require.Equal(t, 1, f.store.AddConfigurationCallCount())
	_, persisted := f.store.AddConfigurationArgsForCall(0)
	assert.Equal(t, persisted.UniqueID(), info.ConfigurationID(),
		"a new configuration must be bound under the conf_id AddConfiguration persists for it")

	expected := idriver.IdentityConfiguration{ID: confIDTestID, Type: "testType", URL: confIDTestURL}
	assert.Equal(t, expected.UniqueID(), info.ConfigurationID())
}

// TestLocalMembership_SignerRouterKeyedByStoredConfID pins the second consumer of the conf_id.
// SignerRouter.Resolve looks its KeyManager up by the conf_id that WalletStore.GetConfID reports
// for the identity - the stored one - so registration has to use the same value. If the two ever
// drift apart again, routing silently stops resolving and every signer reconstruction falls back
// to the probing deserializer.
func TestLocalMembership_SignerRouterKeyedByStoredConfID(t *testing.T) {
	ctx := t.Context()

	f := newConfIDFixture(t)
	f.store.GetConfigurationIDReturns(storedConfID, nil)
	f.km.DeserializeSignerReturns(&stubSigner{}, nil)

	info := f.load(t, ctx)

	id, _, err := info.Get(ctx)
	require.NoError(t, err)

	// the wallet store reports the conf_id the identity was persisted under
	resolver := &identitymock.ConfIDResolver{}
	resolver.GetConfIDReturns(storedConfID, nil)
	f.router.SetConfIDResolver(resolver)

	signer, ok := f.router.Resolve(ctx, id)
	assert.True(t, ok, "the KeyManager must be registered under the persisted conf_id")
	assert.NotNil(t, signer)
}

type stubSigner struct{}

func (*stubSigner) Sign([]byte) ([]byte, error) { return []byte("sigma"), nil }

var _ tdriver.Signer = (*stubSigner)(nil)
