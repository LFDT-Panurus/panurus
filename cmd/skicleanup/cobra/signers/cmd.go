/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package signers

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/spf13/cobra"
)

const defaultBatchSize = 1000

// Cmd returns the Cobra Command for the signers subcommand.
func Cmd() *cobra.Command {
	c := &command{}

	cmd := &cobra.Command{
		Use:   "signers",
		Short: "List orphaned signer entries and their derived SKIs.",
		Long: `Iterates all entries in the Signers identity table.
For each entry whose identity is not referenced by any token in the Tokens
table, the command prints the identity hash and its derived SKIs to stdout.`,
		RunE: c.run,
	}

	flags := cmd.Flags()
	flags.StringVar(&c.configPath, "config", "", "Path to the YAML configuration file (required)")
	flags.IntVar(&c.batchSize, "batch-size", defaultBatchSize, "Number of signer entries to read per database query")

	if err := cmd.MarkFlagRequired("config"); err != nil {
		// MarkFlagRequired only errors if the flag does not exist — this is a programming error.
		panic(err)
	}

	return cmd
}

type command struct {
	configPath string
	batchSize  int
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

	return Run(context.Background(), stores, c.batchSize)
}
