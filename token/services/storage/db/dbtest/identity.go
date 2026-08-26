/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbtest

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func IdentityTest(t *testing.T, cfgProvider cfgProvider) {
	t.Helper()
	for _, c := range IdentityCases {
		driver := cfgProvider(c.Name)
		db, err := driver.NewIdentity("", c.Name)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(c.Name, func(xt *testing.T) {
			defer utils.IgnoreError(db.Close)
			c.Fn(xt, db)
		})
	}

	for _, c := range IdentityNotificationCases {
		driver := cfgProvider(c.Name)
		db, err := driver.NewIdentity("", c.Name)
		if err != nil {
			t.Fatal(err)
		}

		t.Run(c.Name, func(xt *testing.T) {
			defer utils.IgnoreError(db.Close)
			c.Fn(xt, db)
		})
	}
}

var IdentityCases = []struct {
	Name string
	Fn   func(*testing.T, driver.IdentityStore)
}{
	{"IdentityInfo", TIdentityInfo},
	{"SignerInfo", TSignerInfo},
	{"Configurations", TConfigurations},
	{"GetConfiguration", TGetConfiguration},
	{"GetConfigurationID", TGetConfigurationID},
	{"SignerInfoConcurrent", TSignerInfoConcurrent},
	{"GetExistingSignerInfo", TGetExistingSignerInfo},
	{"RegisterIdentityDescriptor", TRegisterIdentityDescriptor},
	// Keep the name short: it is used as a SQL table prefix and PostgreSQL truncates
	// identifiers at 63 characters, which would collide the store's tables.
	{"RegisterPartialConflict", TRegisterIdentityDescriptorPartialConflict},
}

var IdentityNotificationCases = []struct {
	Name string
	Fn   func(*testing.T, driver.IdentityStore)
}{
	{"IdentityNotifier", TIdentityNotifier},
}

func TConfigurations(t *testing.T, db driver.IdentityStore) {
	t.Helper()
	ctx := t.Context()
	expected := driver.IdentityConfiguration{
		ID:     "pineapple",
		Type:   "core",
		URL:    "look here",
		Config: []byte("config"),
		Raw:    []byte("raw"),
	}
	require.NoError(t, db.AddConfiguration(ctx, expected))

	it, err := db.IteratorConfigurations(ctx, expected.Type)
	require.NoError(t, err)
	c, err := it.Next()
	require.NoError(t, err)
	assert.True(t, reflect.DeepEqual(expected, *c))
	it.Close()

	exists, err := db.ConfigurationExists(ctx, expected.ID, expected.Type, expected.URL)
	require.NoError(t, err)
	assert.True(t, exists)

	_, err = db.IteratorConfigurations(ctx, "no core")
	require.NoError(t, err)
	next, err := it.Next()
	require.NoError(t, err)
	assert.Nil(t, next)

	exists, err = db.ConfigurationExists(ctx, "pineapple", "no core", expected.URL)
	require.NoError(t, err)
	assert.False(t, exists)

	expected = driver.IdentityConfiguration{
		ID:     "pineapple",
		Type:   "no core",
		URL:    "look here",
		Config: []byte("config"),
		Raw:    []byte("raw"),
	}
	require.NoError(t, db.AddConfiguration(ctx, expected))
}

func TGetConfiguration(t *testing.T, db driver.IdentityStore) {
	t.Helper()
	ctx := t.Context()
	expected := driver.IdentityConfiguration{
		ID:     "pineapple",
		Type:   "core",
		URL:    "look here",
		Config: []byte("config"),
		Raw:    []byte("raw"),
	}
	require.NoError(t, db.AddConfiguration(ctx, expected))

	c, err := db.GetConfiguration(ctx, expected.ID, expected.Type, expected.URL)
	require.NoError(t, err)
	assert.NotNil(t, c)
	assert.Equal(t, expected, *c)

	// Test not found
	c, err = db.GetConfiguration(ctx, "non-existent", expected.Type, expected.URL)
	require.NoError(t, err)
	assert.Nil(t, c)

	c, err = db.GetConfiguration(ctx, expected.ID, "non-existent", expected.URL)
	require.NoError(t, err)
	assert.Nil(t, c)

	c, err = db.GetConfiguration(ctx, expected.ID, expected.Type, "non-existent")
	require.NoError(t, err)
	assert.Nil(t, c)
}

// TGetConfigurationID checks that the conf_id a store reports for a configuration is the one it
// persisted for it, and that an unknown configuration reports the empty string rather than an
// error. LocalMembership.confIDFor relies on both: it binds identities under the returned value
// and treats "" as "not stored yet, mint a new one".
func TGetConfigurationID(t *testing.T, db driver.IdentityStore) {
	t.Helper()
	ctx := t.Context()

	// the separator appears inside two fields on purpose: this is the shape whose conf_id
	// changed when field escaping was introduced, so it is the shape that must survive a
	// round-trip through the store unchanged
	expected := idriver.IdentityConfiguration{
		ID:     "alice@org1",
		Type:   "core",
		URL:    "/msp/alice@org1",
		Config: []byte("config"),
		Raw:    []byte("raw"),
	}
	require.NoError(t, db.AddConfiguration(ctx, expected))

	confID, err := db.GetConfigurationID(ctx, expected.ID, expected.Type, expected.URL)
	require.NoError(t, err)
	assert.Equal(t, expected.UniqueID(), confID)

	for _, missing := range []idriver.IdentityConfiguration{
		{ID: "non-existent", Type: expected.Type, URL: expected.URL},
		{ID: expected.ID, Type: "non-existent", URL: expected.URL},
		{ID: expected.ID, Type: expected.Type, URL: "non-existent"},
	} {
		confID, err := db.GetConfigurationID(ctx, missing.ID, missing.Type, missing.URL)
		require.NoError(t, err)
		assert.Empty(t, confID, "an unstored configuration must report no conf_id, not an error")
	}

	// A store is free to key rows by a concatenation of id and url, and the kvs one does
	// (base64(id||url), no separator), so these two configurations - which differ only in where
	// the "/msp" prefix sits - share a row key there. Only the first is stored: the second must
	// report no conf_id rather than the first one's. Handing back a colliding configuration's
	// identifier would bind identities under it and take over its SignerRouter entry,
	// reintroducing the wrong-KeyManager route with the cryptographic probe skipped.
	stored := idriver.IdentityConfiguration{ID: "bob", Type: "core", URL: "/msp/alice", Config: []byte("config")}
	require.NoError(t, db.AddConfiguration(ctx, stored))

	confID, err = db.GetConfigurationID(ctx, "bob/msp", stored.Type, "/alice")
	require.NoError(t, err)
	assert.Empty(t, confID, "a configuration that was never stored must not report a colliding configuration's conf_id")

	confID, err = db.GetConfigurationID(ctx, stored.ID, stored.Type, stored.URL)
	require.NoError(t, err)
	assert.Equal(t, stored.UniqueID(), confID, "the stored configuration must still report its own conf_id")

	// Reporting no conf_id makes commitLocalIdentity treat the colliding configuration as not
	// stored and insert it. Whether that insert succeeds is a property of the store - a store
	// with a row per (id, type, url) holds both, one that shares a row key cannot - but either
	// way the configuration that was already stored must come back intact. Overwriting it would
	// drop its Config and Raw and leave it unable to reload from the store.
	colliding := idriver.IdentityConfiguration{ID: "bob/msp", Type: stored.Type, URL: "/alice", Config: []byte("colliding")}
	if err := db.AddConfiguration(ctx, colliding); err == nil {
		confID, err = db.GetConfigurationID(ctx, colliding.ID, colliding.Type, colliding.URL)
		require.NoError(t, err)
		assert.Equal(t, colliding.UniqueID(), confID, "a store that accepted both must report each configuration's own conf_id")
	}

	c, err := db.GetConfiguration(ctx, stored.ID, stored.Type, stored.URL)
	require.NoError(t, err)
	require.NotNil(t, c, "the previously stored configuration must survive an attempt to store a colliding one")
	assert.Equal(t, stored.ID, c.ID)
	assert.Equal(t, stored.URL, c.URL)
	assert.Equal(t, stored.Config, c.Config)

	confID, err = db.GetConfigurationID(ctx, stored.ID, stored.Type, stored.URL)
	require.NoError(t, err)
	assert.Equal(t, stored.UniqueID(), confID)
}

func TIdentityInfo(t *testing.T, db driver.IdentityStore) {
	t.Helper()
	ctx := t.Context()
	id := []byte("alice")
	auditInfo := []byte("alice_audit_info")
	tokMeta := []byte("tok_meta")
	tokMetaAudit := []byte("tok_meta_audit")
	require.NoError(t, db.StoreIdentityData(ctx, id, auditInfo, tokMeta, tokMetaAudit))

	auditInfo2, err := db.GetAuditInfo(ctx, id)
	require.NoError(t, err, "failed to retrieve audit info for [%s]", id)
	assert.Equal(t, auditInfo, auditInfo2)

	tokMeta2, tokMetaAudit2, err := db.GetTokenInfo(ctx, id)
	require.NoError(t, err, "failed to retrieve token info for [%s]", id)
	assert.Equal(t, tokMeta, tokMeta2)
	assert.Equal(t, tokMetaAudit, tokMetaAudit2)

	// should not fail
	require.NoError(t, db.StoreIdentityData(ctx, id, auditInfo, tokMeta, tokMetaAudit))
}

func TSignerInfo(t *testing.T, db driver.IdentityStore) {
	t.Helper()
	tSignerInfo(t, db, 0)
}

func TSignerInfoConcurrent(t *testing.T, db driver.IdentityStore) {
	t.Helper()
	wg := sync.WaitGroup{}
	n := 100
	wg.Add(n)

	for i := range n {
		go func(i int) {
			tSignerInfo(t, db, i)
			t.Log(i)
			wg.Done()
		}(i)
	}
	wg.Wait()

	for i := range n {
		alice := fmt.Appendf(nil, "alice_%d", i)
		exists, err := db.SignerInfoExists(t.Context(), alice)
		require.NoError(t, err, "failed to check signer info existence for [%s]", alice)
		assert.True(t, exists)
	}
}

//nolint:testifylint
func tSignerInfo(t *testing.T, db driver.IdentityStore, index int) {
	t.Helper()
	ctx := t.Context()
	alice := fmt.Appendf(nil, "alice_%d", index)
	bob := fmt.Appendf(nil, "bob_%d", index)
	signerInfo := []byte("signer_info")
	assert.NoError(t, db.StoreSignerInfo(ctx, alice, signerInfo))
	exists, err := db.SignerInfoExists(ctx, alice)
	assert.NoError(t, err, "failed to check signer info existence for [%s]", alice)
	assert.True(t, exists)
	signerInfo2, err := db.GetSignerInfo(ctx, alice)
	assert.NoError(t, err, "failed to retrieve signer info for [%s]", alice)
	assert.Equal(t, signerInfo, signerInfo2)

	exists, err = db.SignerInfoExists(ctx, bob)
	assert.NoError(t, err, "failed to check signer info existence for [%s]", bob)
	assert.False(t, exists)
}

// TGetExistingSignerInfo checks that GetExistingSignerInfo returns the hashes
// of exactly the identities for which signer info was stored, when queried with
// a mix of known and unknown identities.
func TGetExistingSignerInfo(t *testing.T, db driver.IdentityStore) {
	t.Helper()
	ctx := t.Context()
	alice := tdriver.Identity("alice")
	bob := tdriver.Identity("bob")
	carol := tdriver.Identity("carol")
	signerInfo := []byte("signer_info")

	require.NoError(t, db.StoreSignerInfo(ctx, alice, signerInfo))
	require.NoError(t, db.StoreSignerInfo(ctx, carol, signerInfo))

	// bob was never stored, so it must be reported as missing.
	existing, err := db.GetExistingSignerInfo(ctx, alice, bob, carol)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{alice.UniqueID(), carol.UniqueID()}, existing)

	// Querying only the unknown identity returns nothing.
	existing, err = db.GetExistingSignerInfo(ctx, bob)
	require.NoError(t, err)
	assert.Empty(t, existing)
}

func TRegisterIdentityDescriptor(t *testing.T, db driver.IdentityStore) {
	t.Helper()
	ctx := t.Context()
	id := []byte("alice")
	aliasID := []byte("pineapple")
	auditInfo := []byte("alice_audit_info")
	SignerInfo := []byte("signer_info")

	signer := &mock.Signer{}
	verifier := &mock.Verifier{}

	descriptor := &idriver.IdentityDescriptor{
		Identity:   id,
		AuditInfo:  auditInfo,
		Signer:     signer,
		SignerInfo: SignerInfo,
		Verifier:   verifier,
	}
	require.NoError(t, db.RegisterIdentityDescriptor(ctx, descriptor, aliasID))
	assertIdentityDescriptorRegistered(t, db, id, aliasID, SignerInfo, auditInfo)

	// registering the very same descriptor again must be a no-op success
	require.NoError(t, db.RegisterIdentityDescriptor(ctx, descriptor, aliasID))
	assertIdentityDescriptorRegistered(t, db, id, aliasID, SignerInfo, auditInfo)
}

// TRegisterIdentityDescriptorPartialConflict covers the retry-after-partial-write scenarios: only
// some of the rows a descriptor registration writes are already present, as would be left behind by
// a crash in the middle of a previous attempt. All those writes share one transaction, so a
// duplicate key on any of them must neither fail the registration nor stop the still-missing rows
// from being written.
func TRegisterIdentityDescriptorPartialConflict(t *testing.T, db driver.IdentityStore) {
	t.Helper()
	ctx := t.Context()
	auditInfo := []byte("audit_info")
	signerInfo := []byte("signer_info")

	descriptorFor := func(id []byte) *idriver.IdentityDescriptor {
		return &idriver.IdentityDescriptor{
			Identity:   id,
			AuditInfo:  auditInfo,
			Signer:     &mock.Signer{},
			SignerInfo: signerInfo,
			Verifier:   &mock.Verifier{},
		}
	}

	// The audit info of the identity is already there, everything else is missing. On PostgreSQL
	// this is the case where the duplicate key aborts the shared transaction and, unless it is
	// isolated, makes the following alias writes fail with a generic error.
	id, alias := []byte("bob"), []byte("banana")
	require.NoError(t, db.StoreIdentityData(ctx, id, auditInfo, nil, nil))
	require.NoError(t, db.RegisterIdentityDescriptor(ctx, descriptorFor(id), alias))
	assertIdentityDescriptorRegistered(t, db, id, alias, signerInfo, auditInfo)

	// The signer info of the identity is already there, everything else is missing.
	id, alias = []byte("carol"), []byte("cherry")
	require.NoError(t, db.StoreSignerInfo(ctx, id, signerInfo))
	require.NoError(t, db.RegisterIdentityDescriptor(ctx, descriptorFor(id), alias))
	assertIdentityDescriptorRegistered(t, db, id, alias, signerInfo, auditInfo)

	// Only the alias' rows are already there.
	id, alias = []byte("dave"), []byte("date")
	require.NoError(t, db.StoreSignerInfo(ctx, alias, signerInfo))
	require.NoError(t, db.StoreIdentityData(ctx, alias, auditInfo, nil, nil))
	require.NoError(t, db.RegisterIdentityDescriptor(ctx, descriptorFor(id), alias))
	assertIdentityDescriptorRegistered(t, db, id, alias, signerInfo, auditInfo)

	// Both the identity's signer info and the alias' audit info are already there.
	id, alias = []byte("erin"), []byte("elderberry")
	require.NoError(t, db.StoreSignerInfo(ctx, id, signerInfo))
	require.NoError(t, db.StoreIdentityData(ctx, alias, auditInfo, nil, nil))
	require.NoError(t, db.RegisterIdentityDescriptor(ctx, descriptorFor(id), alias))
	assertIdentityDescriptorRegistered(t, db, id, alias, signerInfo, auditInfo)
}

// assertIdentityDescriptorRegistered checks that both the identity and its alias carry the expected
// signer info and audit info. GetSignerInfo is asserted on as well as SignerInfoExists because the
// former bypasses the store's in-memory caches, which would otherwise report a row as present even
// when the registration never wrote it.
func assertIdentityDescriptorRegistered(t *testing.T, db driver.IdentityStore, id, alias, signerInfo, auditInfo []byte) {
	t.Helper()
	ctx := t.Context()
	for _, identity := range [][]byte{id, alias} {
		exists, err := db.SignerInfoExists(ctx, identity)
		require.NoError(t, err)
		assert.True(t, exists, "signer info missing for [%s]", identity)

		storedSignerInfo, err := db.GetSignerInfo(ctx, identity)
		require.NoError(t, err, "failed reading signer info for [%s]", identity)
		assert.Equal(t, signerInfo, storedSignerInfo, "signer info mismatch for [%s]", identity)

		storedAuditInfo, err := db.GetAuditInfo(ctx, identity)
		require.NoError(t, err)
		assert.Equal(t, auditInfo, storedAuditInfo, "audit info mismatch for [%s]", identity)
	}
}

func TIdentityNotifier(t *testing.T, db driver.IdentityStore) {
	t.Helper()
	logging.Init(logging.Config{
		Format:  "%{color}%{time:2006-01-02 15:04:05.000 MST} [%{module}] %{shortfunc} -> %{level:.4s} %{id:03x}%{color:reset} %{message}",
		LogSpec: "debug",
	})
	t.Helper()
	ctx := t.Context()

	notifier, err := db.Notifier()
	if errors.Is(err, storage.ErrNotSupported) {
		t.Skip("notifier not supported")
	}
	require.NoError(t, err)

	result, err := collectDBEvents(notifier)
	require.NoError(t, err)

	expected := driver.IdentityConfiguration{
		ID:     fmt.Sprintf("pineapple-%d", time.Now().UnixNano()),
		Type:   "core",
		URL:    "look here",
		Config: []byte("config"),
		Raw:    []byte("raw"),
	}
	require.NoError(t, db.AddConfiguration(ctx, expected))

	conf, err := db.GetConfiguration(ctx, expected.ID, expected.Type, expected.URL)
	require.NoError(t, err)
	assert.Equal(t, expected, *conf)

	requireSizeOrSkip(t, result, 1)
	values := result.Values()
	require.Equal(t, driver2.Insert, values[0].Op)
	require.Equal(t, idriver.IdentityConfigurationRecord{
		ID:   expected.ID,
		Type: expected.Type,
		URL:  expected.URL,
	}, values[0].Val)
}
