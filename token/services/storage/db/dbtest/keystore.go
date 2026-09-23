/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbtest

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func KeyStoreTest(t *testing.T, cfgProvider cfgProvider) {
	t.Helper()
	for _, c := range KeyStoreCases {
		driver := cfgProvider(c.Name)
		db, err := driver.NewKeyStore("", c.Name)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(c.Name, func(xt *testing.T) {
			defer utils.IgnoreError(db.Close)
			c.Fn(xt, db)
		})
	}
}

var KeyStoreCases = []struct {
	Name string
	Fn   func(*testing.T, driver.KeyStore)
}{
	{"TKeyStoreAddGet", TKeyStoreAddGet},
	{"TKeyStorePutConflict", TKeyStorePutConflict},
}

func TKeyStoreAddGet(t *testing.T, db driver.KeyStore) {
	t.Helper()

	keys := []string{"v1", "v2", "v3"}
	for _, k := range keys {
		require.NoError(t, db.Put(k, &Value{V: k + "_value"}))
	}

	for _, k := range keys {
		v := &Value{}
		require.NoError(t, db.Get(k, v))
		assert.Equal(t, k+"_value", v.V)
	}
}

// TKeyStorePutConflict asserts how Put treats a key that is already present:
// re-storing the identical value succeeds (a node restarting must not fail),
// while storing a different value under the same key is a data-integrity
// conflict and has to be reported. Exercised against the real driver so that the
// unique-key violation is actually raised and mapped, not just simulated.
func TKeyStorePutConflict(t *testing.T, db driver.KeyStore) {
	t.Helper()

	require.NoError(t, db.Put("k", &Value{V: "original"}))

	// Idempotent: same key, identical value.
	require.NoError(t, db.Put("k", &Value{V: "original"}))

	// Conflict: same key, different value.
	require.Error(t, db.Put("k", &Value{V: "different"}))

	// The stored value must be untouched by the rejected write.
	v := &Value{}
	require.NoError(t, db.Get("k", v))
	assert.Equal(t, "original", v.V)
}

type Value struct {
	V string
}
