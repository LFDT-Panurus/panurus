/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"os"
	"strings"

	"github.com/LFDT-Panurus/panurus/cmd/artifactgen/gen"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// CmdRoot is the prefix for environment variables.
const CmdRoot = "core"

var mainCmd = &cobra.Command{Use: "artifactgen"}

func main() {
	if err := Execute(); err != nil {
		os.Exit(1)
	}
}

// Execute wires subcommands and runs the root command. It mirrors the shape
// used by cmd/tokengen so flag and environment handling stay consistent
// across the two binaries.
func Execute() error {
	viper.SetEnvPrefix(CmdRoot)
	viper.AutomaticEnv()
	replacer := strings.NewReplacer(".", "_")
	viper.SetEnvKeyReplacer(replacer)

	mainFlags := mainCmd.PersistentFlags()

	mainFlags.String("logging-level", "", "Legacy logging level flag")
	if err := viper.BindPFlag("logging_level", mainFlags.Lookup("logging-level")); err != nil {
		return err
	}
	if err := mainFlags.MarkHidden("logging-level"); err != nil {
		return err
	}

	mainCmd.AddCommand(gen.Cmd())

	return mainCmd.Execute()
}
