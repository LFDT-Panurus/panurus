/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/pp/zkatsnarkv1"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

// assertHasSubcommand checks that cmd has a subcommand named name.
func assertHasSubcommand(t *testing.T, cmd *cobra.Command, name string) {
	t.Helper()
	for _, c := range cmd.Commands() {
		if c.Name() == name {
			return
		}
	}
	assert.Failf(t, "missing subcommand", "command [%s] has no subcommand [%s]", cmd.Use, name)
}

// TestGenCmd tests the GenCmd function.
func TestGenCmd(t *testing.T) {
	cmd := GenCmd()
	assert.NotNil(t, cmd)
	assert.Equal(t, "gen", cmd.Use)
	assertHasSubcommand(t, cmd, zkatsnarkv1.DriverIdentifier)
}

// TestUpdateCmd tests the UpdateCmd function.
func TestUpdateCmd(t *testing.T) {
	cmd := UpdateCmd()
	assert.NotNil(t, cmd)
	assert.Equal(t, "update", cmd.Use)
	assertHasSubcommand(t, cmd, zkatsnarkv1.DriverIdentifier)
}

// TestUtilsCmd tests the UtilsCmd function.
func TestUtilsCmd(t *testing.T) {
	cmd := UtilsCmd()
	assert.NotNil(t, cmd)
	assert.Equal(t, "pp", cmd.Use)
}
