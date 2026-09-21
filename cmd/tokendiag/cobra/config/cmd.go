/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package config provides CLI commands for working with tokendiag configuration.
package config

import (
	"fmt"

	"github.com/spf13/cobra"
)

// exampleConfig is a fully-annotated YAML configuration that users can adapt.
//
//nolint:gosec
const exampleConfig = `# tokendiag configuration file
#
# driver selects the database backend.
# Supported values: "sqlite", "postgres"
driver: postgres

# dataSource is the DSN (Data Source Name) passed directly to the database driver.
#
# PostgreSQL DSN formats:
#   URL format:  "postgres://user:pass@host:5432/dbname?sslmode=disable"
#   Key=value:   "host=localhost port=5432 user=panurus password=secret dbname=panurus sslmode=require"
#
# SQLite format (file path):
#   dataSource: /var/lib/panurus/node/data.db
dataSource: "postgres://user:pass@localhost:5432/panurus?sslmode=disable"

# tablePrefix is the optional prefix that was used when the Panurus node created
# its database tables. Leave empty if the node was configured without a prefix.
tablePrefix: ""

# skipPrefix controls whether the FSC-generated prefix is omitted from all table
# names. Set this to true when the Panurus node was started with
# token.storage.skipPrefix: true. Default is false.
skipPrefix: false

# tableNames is an optional map of short-code overrides for SQL table names.
# Each key is a canonical short code and the value is the replacement short code
# that will be used when generating the final SQL table name. The FSC-generated
# prefix and params are still applied around the replacement value (unless
# skipPrefix is true). Unknown keys produce a warning and are ignored.
#
# The locks command reads the tkn_locks and requests tables:
#   tkn_locks -> fsc_tkn_locks_<prefix>_<params>
#   requests  -> fsc_requests_<prefix>_<params>
#
# Example — rename the lock table:
# tableNames:
#   tkn_locks: my_token_locks
tableNames: {}
`

// Cmd returns the Cobra Command for the config subcommand group.
func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration helpers.",
		Long:  `Commands for working with the tokendiag configuration file.`,
	}

	cmd.AddCommand(exampleCmd())

	return cmd
}

func exampleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "example",
		Short: "Print an annotated example configuration file.",
		Long: `Print a fully-annotated YAML configuration to stdout.

Redirect the output to a file to create a starting configuration:

  tokendiag config example > config.yaml`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), exampleConfig)

			return err
		},
	}
}
