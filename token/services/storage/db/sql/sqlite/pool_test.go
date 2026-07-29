/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"path"
	"testing"

	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/multiplexed"
	fscSqlite "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"
	"github.com/stretchr/testify/require"
)

// TestMaxOpenConnsIsBounded asserts that a persistence configuration which does not set
// maxOpenConns gets common2.DefaultMaxOpenConns rather than database/sql's "unlimited" (0), and
// that an explicit value is left alone.
func TestMaxOpenConnsIsBounded(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured int
		expected   int
	}{
		{"unset means bounded, not unlimited", 0, common2.DefaultMaxOpenConns},
		{"negative means bounded", -1, common2.DefaultMaxOpenConns},
		{"an explicit value is respected", 3, 3},
		{"an explicit value above the default is respected", common2.DefaultMaxOpenConns * 2, common2.DefaultMaxOpenConns * 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := multiplexed.MockTypeConfig(fscSqlite.Persistence, fscSqlite.Config{
				DataSource:   "file:" + path.Join(t.TempDir(), "db.sqlite"),
				TablePrefix:  "pool_test",
				MaxOpenConns: tc.configured,
			})

			opts, err := NewDriver(cfg).cp.GetOpts("")
			require.NoError(t, err)
			require.Equal(t, tc.expected, opts.MaxOpenConns)
		})
	}
}
