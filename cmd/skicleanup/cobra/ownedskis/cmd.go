/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ownedskis

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/signers"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/spf13/cobra"
)

// Cmd returns the Cobra Command for the owned-skis subcommand.
func Cmd() *cobra.Command {
	c := &command{}

	cmd := &cobra.Command{
		Use:   "owned-skis",
		Short: "List the SKIs derived from the identities that own tokens.",
		Long: `Reads every token owned by this node (spent and unspent) from the Tokens
table, derives the SKIs of each owner identity, and prints the sorted,
de-duplicated list to stdout, one SKI per line. Statistics and errors go to
stderr. On any error no list is produced and the command exits non-zero.

The SKIs come from the runtime cleanup extractor. htlc, multisig and policy
owners are unwrapped into their component identities. The list is not a
complete keep-list: x509 owners yield no SKIs. Never delete every key that is
not in this list.`,
		RunE: c.run,
	}

	flags := cmd.Flags()
	flags.StringVar(&c.configPath, "config", "", "Path to the YAML configuration file (required)")
	flags.StringVar(&c.outputPath, "output", "", "Also write the list to this file (mode 0600, replaced atomically; a symlink is replaced, not followed)")

	if err := cmd.MarkFlagRequired("config"); err != nil {
		// MarkFlagRequired only errors if the flag does not exist — this is a programming error.
		panic(err)
	}

	return cmd
}

type command struct {
	configPath string
	outputPath string
}

func (c *command) run(cmd *cobra.Command, _ []string) error {
	cmd.SilenceUsage = true

	if c.outputPath == "-" {
		return errors.New("--output - is not supported: the list already goes to stdout")
	}
	if c.outputPath != "" {
		if fi, err := os.Stat(c.outputPath); err == nil && fi.IsDir() {
			return errors.Errorf("--output [%s] is a directory", c.outputPath)
		}
	}

	cfg, err := signers.LoadConfig(c.configPath)
	if err != nil {
		return errors.Wrap(err, "failed to load config")
	}

	stores, err := signers.NewStores(cfg)
	if err != nil {
		return errors.Wrap(err, "failed to open stores")
	}
	defer func() {
		if err := stores.Close(); err != nil {
			cmd.PrintErrf("warning: failed to close stores: %v\n", err)
		}
	}()

	src := &SQLOwnerSource{
		DB:              stores.ReadDB,
		TokensTable:     stores.TableNames.Tokens,
		CondInterpreter: stores.CondInterpreter,
	}
	skis, err := Run(cmd.Context(), src, newSKIExtractor(stores.Identity), cmd.ErrOrStderr())
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	for _, ski := range skis {
		buf.WriteString(ski)
		buf.WriteByte('\n')
	}

	// Write stdout first: if it fails, the existing output file stays untouched.
	if _, err := cmd.OutOrStdout().Write(buf.Bytes()); err != nil {
		return errors.Wrap(err, "write list to stdout")
	}

	if c.outputPath != "" {
		if err := writeFileAtomic(c.outputPath, buf.Bytes()); err != nil {
			return errors.Wrapf(err, "write list to [%s]", c.outputPath)
		}
	}

	return nil
}

// writeFileAtomic writes data to a temp file next to path, then renames it over path.
// The file has mode 0600. On any error the temp file is removed and path is untouched.
func writeFileAtomic(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return errors.Wrap(err, "create temp file")
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if cerr := tmp.Close(); cerr != nil && !errors.Is(cerr, os.ErrClosed) {
			err = errors.Join(err, errors.Wrap(cerr, "close temp file"))
		}
		if rerr := os.Remove(tmp.Name()); rerr != nil {
			err = errors.Join(err, errors.Wrap(rerr, "remove temp file"))
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return errors.Wrap(err, "write temp file")
	}
	if err := tmp.Sync(); err != nil {
		return errors.Wrap(err, "sync temp file")
	}
	if err := tmp.Close(); err != nil {
		return errors.Wrap(err, "close temp file")
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.Wrap(err, "rename temp file")
	}
	committed = true

	return nil
}
