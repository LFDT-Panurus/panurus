/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package locks provides the tokendiag "locks" diagnostic subcommand: a read-only
// inspection of the token_locks table, added for #2395 to let an operator answer
// "which tokens are locked right now, for how long, and is any of that a leak"
// without racing a second Lock call against the primary key.
package locks

import (
	"context"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/spf13/cobra"
)

// Cmd returns the Cobra Command for the locks subcommand.
func Cmd() *cobra.Command {
	c := &command{}

	cmd := &cobra.Command{
		Use:   "locks",
		Short: "Inspect currently held token locks.",
		Long: `Reads every currently held row in the token_locks table, joined with the
status of its consuming transaction, and prints:
  - every held lock, with its age and the consumer's status;
  - locks whose consumer has already reached a terminal status (Confirmed,
    Deleted, or Orphan) - these are leaked locks: nothing released them on
    settlement, so they will sit until the next lease-age sweep;
  - a summary line suitable for scripting.

This is a read-only operation. No data is modified or deleted.`,
		RunE: c.run,
	}

	flags := cmd.Flags()
	flags.StringVar(&c.configPath, "config", "", "Path to the YAML configuration file (required)")

	if err := cmd.MarkFlagRequired("config"); err != nil {
		// MarkFlagRequired only errors if the flag does not exist — this is a programming error.
		panic(err)
	}

	return cmd
}

type command struct {
	configPath string
}

func (c *command) run(cmd *cobra.Command, _ []string) error {
	cmd.SilenceUsage = true

	cfg, err := LoadConfig(c.configPath)
	if err != nil {
		return errors.Wrap(err, "failed to load config")
	}

	stores, err := NewStores(cfg)
	if err != nil {
		return errors.Wrap(err, "failed to open stores")
	}
	defer func() {
		if err := stores.Close(); err != nil {
			cmd.PrintErrf("warning: failed to close stores: %v\n", err)
		}
	}()

	return Run(context.Background(), cmd.OutOrStdout(), stores, time.Now())
}
