/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package locks

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeConfig writes a config file and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	return path
}

const minimalConfig = `driver: postgres
dataSource: "postgres://user:pass@localhost:5432/from-file?sslmode=disable"
tablePrefix: "pfx"
skipPrefix: false
tableNameParams:
  - testnetwork
  - testchannel
  - tokenns
`

func TestLoadConfig(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, minimalConfig))
	require.NoError(t, err)
	assert.Equal(t, "postgres", cfg.Driver)
	assert.Equal(t, "postgres://user:pass@localhost:5432/from-file?sslmode=disable", cfg.DataSource)
	assert.Equal(t, "pfx", cfg.TablePrefix)
	assert.False(t, cfg.SkipPrefix)
	assert.Equal(t, []string{"testnetwork", "testchannel", "tokenns"}, cfg.TableNameParams)
}

func TestLoadConfigRejectsBadInput(t *testing.T) {
	t.Run("unsupported driver", func(t *testing.T) {
		_, err := LoadConfig(writeConfig(t, "driver: mysql\ndataSource: x\n"))
		require.ErrorContains(t, err, "unsupported driver")
	})

	t.Run("empty dataSource", func(t *testing.T) {
		_, err := LoadConfig(writeConfig(t, "driver: sqlite\n"))
		require.ErrorContains(t, err, "dataSource must not be empty")
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := LoadConfig(filepath.Join(t.TempDir(), "absent.yaml"))
		require.ErrorContains(t, err, "failed to read config file")
	})
}

// TestLoadConfigEnvOverrides pins the CORE_ overrides documented in the README. Binding
// each key explicitly is what makes them work: AutomaticEnv resolves lazily on Get,
// while Unmarshal only walks AllKeys(), so a key present solely in the environment would
// otherwise never be visited - CORE_DATASOURCE would fail the empty-dataSource check and
// the table-name keys would fail silently, resolving the wrong tables.
func TestLoadConfigEnvOverrides(t *testing.T) {
	t.Run("keys supplied only by the environment", func(t *testing.T) {
		t.Setenv("CORE_DATASOURCE", "postgres://user:pass@localhost:5432/from-env?sslmode=disable")
		t.Setenv("CORE_TABLEPREFIX", "envpfx")
		t.Setenv("CORE_SKIPPREFIX", "true")
		// A list arrives as one comma-separated value.
		t.Setenv("CORE_TABLENAMEPARAMS", "envnetwork,envchannel,envns")

		cfg, err := LoadConfig(writeConfig(t, "driver: postgres\n"))
		require.NoError(t, err)
		assert.Equal(t, "postgres://user:pass@localhost:5432/from-env?sslmode=disable", cfg.DataSource)
		assert.Equal(t, "envpfx", cfg.TablePrefix)
		assert.True(t, cfg.SkipPrefix)
		assert.Equal(t, []string{"envnetwork", "envchannel", "envns"}, cfg.TableNameParams)
	})

	t.Run("environment overrides the file", func(t *testing.T) {
		t.Setenv("CORE_TABLEPREFIX", "envpfx")

		cfg, err := LoadConfig(writeConfig(t, minimalConfig))
		require.NoError(t, err)
		assert.Equal(t, "envpfx", cfg.TablePrefix)
	})

	t.Run("file is kept when no variable is set", func(t *testing.T) {
		cfg, err := LoadConfig(writeConfig(t, minimalConfig))
		require.NoError(t, err)
		assert.Equal(t, "pfx", cfg.TablePrefix)
		assert.Equal(t, []string{"testnetwork", "testchannel", "tokenns"}, cfg.TableNameParams)
	})

	// An unprefixed variable must not be picked up: the prefix is what stops an
	// unrelated DATASOURCE in the environment from silently repointing the tool at
	// another database.
	t.Run("unprefixed variable is ignored", func(t *testing.T) {
		t.Setenv("DATASOURCE", "postgres://evil@localhost:5432/other?sslmode=disable")

		cfg, err := LoadConfig(writeConfig(t, minimalConfig))
		require.NoError(t, err)
		assert.Equal(t, "postgres://user:pass@localhost:5432/from-file?sslmode=disable", cfg.DataSource)
	})
}
