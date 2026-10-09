/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"os"
	"testing"

	fscpostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	"github.com/stretchr/testify/require"
)

// requirePostgresEnv names the environment variable that turns a missing Postgres from a
// skip into a failure. Every test in this package that needs a real server goes through
// startPostgres, so setting it in an environment that is supposed to have Docker (CI, or a
// developer verifying a change to the lock strategies) makes a suite that cannot start one
// fail loudly instead of reporting a green run in which the strategy tests never executed -
// which is exactly how a silent skip hides a regression in SKIP LOCKED, in the mixed-strategy
// handling or in the advisory-lock ordering.
const requirePostgresEnv = "PANURUS_REQUIRE_POSTGRES"

// startPostgres starts an ephemeral Postgres for cfg and registers its teardown. When the
// container cannot be started it skips the test with the underlying reason, unless
// requirePostgresEnv is set, in which case it fails.
func startPostgres(t *testing.T, cfg *fscpostgres.ContainerConfig) {
	t.Helper()

	terminate, _, err := fscpostgres.StartPostgres(t.Context(), cfg, nil)
	if err != nil {
		if _, required := os.LookupEnv(requirePostgresEnv); required {
			require.NoError(t, err,
				"%s is set, so a Postgres-backed test may not be skipped: this suite's "+
					"lock-strategy coverage only runs against a real server", requirePostgresEnv)
		}
		t.Skipf("postgres not available (set %s to make this a failure): %v", requirePostgresEnv, err)
	}
	t.Cleanup(terminate)
}
