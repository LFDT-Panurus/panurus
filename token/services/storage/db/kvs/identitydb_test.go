/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIdentityStoreGetConfigurationID asserts that GetConfigurationID reports a configuration's
// own conf_id, and reports none for a configuration that was never stored.
//
// The second case is not merely a miss. This store keys a configuration by
// mergeIDURL(id, url) = base64(id||url), with no separator between the two fields, so distinct
// (id, url) pairs whose concatenations agree land on the same key: {ID: "bob", URL: "/msp/alice"}
// and {ID: "bob/msp", URL: "/alice"} both key on base64("bob/msp/alice"), which is why a path
// prefix moving between the two fields is enough. The underlying Get then returns whichever
// record is stored there,
// whatever its own ID and URL are. Handing that record's conf_id back would make
// LocalMembership.confIDFor treat the second configuration as already stored and bind its
// identities under the first one's conf_id, and SignerRouter.Register would overwrite the first
// configuration's entry with the second one's KeyManager. Identities of the first would then be
// deserialized by the wrong KeyManager with the cryptographic probe skipped - the failure that
// deriving conf_id from an unambiguous encoding of (ID, Type, URL) exists to prevent.
func TestIdentityStoreGetConfigurationID(t *testing.T) {
	backend, err := NewInMemory()
	require.NoError(t, err)
	tmsID := token.TMSID{Network: "apple", Channel: "pears", Namespace: "strawberries"}
	db := NewIdentityStore(backend, tmsID)
	ctx := t.Context()

	// a separator inside two fields: the shape whose conf_id changed when field escaping was
	// introduced, so the shape that must survive a round-trip through the store unchanged
	stored := storage.IdentityConfiguration{
		ID:     "alice@org1",
		Type:   "Owner",
		URL:    "/msp/alice@org1",
		Config: []byte("config"),
		Raw:    []byte("raw"),
	}
	require.NoError(t, db.AddConfiguration(ctx, stored))

	confID, err := db.GetConfigurationID(ctx, stored.ID, stored.Type, stored.URL)
	require.NoError(t, err)
	assert.Equal(t, stored.UniqueID(), confID)

	// plain misses
	for _, missing := range []storage.IdentityConfiguration{
		{ID: "non-existent", Type: stored.Type, URL: stored.URL},
		{ID: stored.ID, Type: "non-existent", URL: stored.URL},
		{ID: stored.ID, Type: stored.Type, URL: "non-existent"},
	} {
		confID, err := db.GetConfigurationID(ctx, missing.ID, missing.Type, missing.URL)
		require.NoError(t, err)
		assert.Empty(t, confID, "an unstored configuration must report no conf_id, not an error")
	}

	// a configuration that collides with a stored one on base64(id||url) must still report no
	// conf_id, never the stored configuration's
	first := storage.IdentityConfiguration{ID: "bob", Type: stored.Type, URL: "/msp/alice", Config: []byte("config")}
	colliding := storage.IdentityConfiguration{ID: "bob/msp", Type: stored.Type, URL: "/alice"}
	require.Equal(t, mergeIDURL(first.ID, first.URL), mergeIDURL(colliding.ID, colliding.URL),
		"precondition: the two configurations must share a row key, otherwise this test proves nothing")
	require.NoError(t, db.AddConfiguration(ctx, first))

	confID, err = db.GetConfigurationID(ctx, colliding.ID, colliding.Type, colliding.URL)
	require.NoError(t, err)
	assert.Empty(t, confID, "a configuration that shares a row key with a stored one must not report its conf_id")

	confID, err = db.GetConfigurationID(ctx, first.ID, first.Type, first.URL)
	require.NoError(t, err)
	assert.Equal(t, first.UniqueID(), confID, "the stored configuration must still report its own conf_id")
	assert.NotEqual(t, first.UniqueID(), colliding.UniqueID(), "the two configurations must have distinct conf_ids")

	// Reporting no conf_id makes commitLocalIdentity treat the colliding configuration as not
	// stored and go on to insert it. That insert must not resolve the collision by overwriting:
	// a Put on the shared key would replace the stored record, dropping its Config and Raw and
	// leaving it unable to reload from the store.
	require.Error(t, db.AddConfiguration(ctx, colliding),
		"storing a configuration that shares a row key with a stored one must be refused, not overwrite it")

	c, err := db.GetConfiguration(ctx, first.ID, first.Type, first.URL)
	require.NoError(t, err)
	require.NotNil(t, c, "the stored configuration must survive the refused insert")
	assert.Equal(t, first, *c)

	all, err := db.ConfigurationsByID(ctx, first.ID, first.Type)
	require.NoError(t, err)
	assert.Equal(t, []storage.IdentityConfiguration{first}, all)
}
