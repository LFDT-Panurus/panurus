/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"os"
	"strings"

	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/config"
	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/signers"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// CmdRoot is the prefix for environment variables.
const CmdRoot = "core"

// mainCmd is the root command for the skicleanup tool.
var mainCmd = &cobra.Command{Use: "skicleanup"}

// main is the entry point for the skicleanup command.
func main() {
	if err := Execute(); err != nil {
		os.Exit(1)
	}
}

// Execute adds all child commands to the root command and sets flags appropriately.
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

	mainCmd.AddCommand(config.Cmd())
	mainCmd.AddCommand(signers.Cmd())

	return mainCmd.Execute()
}
