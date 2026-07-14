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

type Value struct {
	V string
}
