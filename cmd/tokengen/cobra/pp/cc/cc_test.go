/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGeneratePackage tests the GeneratePackage function.
func TestGeneratePackage(t *testing.T) {
	t.Run("fail_package", func(t *testing.T) {
		// Providing an invalid path should make PackageChaincode fail.
		err := GeneratePackage([]byte("dummy raw"), "/nonexistent/path/tcc.tar")
		assert.Error(t, err)
	})
}

// TestParamsReplacer checks that the replacer passed to the chaincode packager
// swaps the public parameters file, and only that file. The packager hands out
// the absolute path of each file it packages.
func TestParamsReplacer(t *testing.T) {
	params := []byte("package tcc\n\nvar Params = \"cGFyYW1z\"\n")
	replace := paramsReplacer(params)

	t.Run("replaces_the_params_file", func(t *testing.T) {
		_, raw := replace(
			"/home/user/panurus/token/services/network/fabric/tcc/params.go",
			"src/token/services/network/fabric/tcc/params.go",
		)
		assert.Equal(t, params, raw)
	})

	t.Run("keeps_every_other_file", func(t *testing.T) {
		_, raw := replace(
			"/home/user/panurus/token/services/network/fabric/tcc/tcc.go",
			"src/token/services/network/fabric/tcc/tcc.go",
		)
		assert.Nil(t, raw)
	})
}
