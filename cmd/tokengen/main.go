/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"os"
	"strings"

	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/certfier"
	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/pp"
	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/version"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// CmdRoot is the prefix for environment variables.
const CmdRoot = "core"

// mainCmd describes the service and
// defaults to printing the help message.
var mainCmd = &cobra.Command{Use: "tokengen"}

// main is the entry point for the tokengen command.
func main() {
	if err := Execute(); err != nil {
		os.Exit(1)
	}
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the mainCmd.
func Execute() error {
	// For environment variables.
	viper.SetEnvPrefix(CmdRoot)
	viper.AutomaticEnv()
	replacer := strings.NewReplacer(".", "_")
	viper.SetEnvKeyReplacer(replacer)

	// Define command-line flags that are valid for all peer commands and
	// subcommands.
	mainFlags := mainCmd.PersistentFlags()

	mainFlags.String("logging-level", "", "Legacy logging level flag")
	if err := viper.BindPFlag("logging_level", mainFlags.Lookup("logging-level")); err != nil {
		return err
	}
	if err := mainFlags.MarkHidden("logging-level"); err != nil {
		return err
	}

	mainCmd.AddCommand(pp.GenCmd())
	mainCmd.AddCommand(pp.UpdateCmd())
	mainCmd.AddCommand(pp.UtilsCmd())
	mainCmd.AddCommand(certfier.KeyPairGenCmd())
	mainCmd.AddCommand(version.Cmd())

	return mainCmd.Execute()
}
