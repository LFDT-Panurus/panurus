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
	{"SignerInfoConcurrent", TSignerInfoConcurrent},
	{"RegisterIdentityDescriptor", TRegisterIdentityDescriptor},
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
	require.NoError(t, db.RegisterIdentityDescriptor(ctx, descriptor, aliasID))
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

	require.NoError(t, result.AssertSize(1))
	values := result.Values()
	require.Equal(t, driver2.Insert, values[0].Op)
	require.Equal(t, idriver.IdentityConfigurationRecord{
		ID:   expected.ID,
		Type: expected.Type,
		URL:  expected.URL,
	}, values[0].Val)
}
