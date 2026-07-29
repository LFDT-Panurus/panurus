/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"testing"

	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/multiplexed"
	fscPostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	"github.com/stretchr/testify/require"
)

// TestMaxOpenConnsIsBounded asserts that a persistence configuration which does not set
// maxOpenConns gets common3.DefaultMaxOpenConns rather than database/sql's "unlimited" (0), and
// that an explicit value is left alone. No live database is needed: only the resolved options are
// under test.
func TestMaxOpenConnsIsBounded(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured int
		expected   int
	}{
		{"unset means bounded, not unlimited", 0, common3.DefaultMaxOpenConns},
		{"negative means bounded", -1, common3.DefaultMaxOpenConns},
		{"an explicit value is respected", 3, 3},
		{"an explicit value above the default is respected", common3.DefaultMaxOpenConns * 2, common3.DefaultMaxOpenConns * 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := multiplexed.MockTypeConfig(fscPostgres.Persistence, fscPostgres.Config{
				// No credentials: GetOpts only resolves options and never dials, and a DSN with
				// an embedded password would trip the hardcoded-credentials linter.
				DataSource:   "postgres://localhost:5432/db?sslmode=disable",
				TablePrefix:  "pool_test",
				MaxOpenConns: tc.configured,
			})

			opts, err := NewDriver(cfg).cp.GetOpts("")
			require.NoError(t, err)
			require.Equal(t, tc.expected, opts.MaxOpenConns)
		})
	}
}
