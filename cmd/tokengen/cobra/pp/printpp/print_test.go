/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package printpp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/pp/zkatsnarkv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCmd tests the Cmd function.
func TestCmd(t *testing.T) {
	cmd := Cmd()
	assert.NotNil(t, cmd)

	t.Run("success", func(t *testing.T) {
		// Use a temporary output buffer to capture stdout
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		// We need to provide a valid input file.
		// Let's find the project root and then the testdata.
		wd, _ := os.Getwd()
		// wd should be .../cmd/tokengen/cobra/pp/printpp
		testdataPath := filepath.Join(wd, "..", "..", "..", "testdata", "zkatdlognoghv1_pp.json")

		cmd.SetArgs([]string{"--input", testdataPath})
		err := cmd.Execute()
		require.NoError(t, err)

		err = w.Close()
		require.NoError(t, err)
		os.Stdout = oldStdout
		var buf bytes.Buffer
		_, err = buf.ReadFrom(r)
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "zkatdlognogh")
	})

	t.Run("file_not_found", func(t *testing.T) {
		cmd.SetArgs([]string{"--input", "nonexistent.json"})
		err := cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to generate public parameters")
	})

	t.Run("trailing_args", func(t *testing.T) {
		cmd.SetArgs([]string{"--input", "input.json", "extra"})
		err := cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "trailing args detected")
	})
}

// TestPrint tests the Print function.
func TestPrint(t *testing.T) {
	wd, _ := os.Getwd()
	testdataPath := filepath.Join(wd, "..", "..", "..", "testdata", "zkatdlognoghv1_pp.json")

	t.Run("success", func(t *testing.T) {
		err := Print(&Args{InputFile: testdataPath})
		require.NoError(t, err)
	})

	t.Run("success_zkatsnark", func(t *testing.T) {
		out := t.TempDir()
		testdata := filepath.Join(wd, "..", "..", "..", "testdata")
		_, err := zkatsnarkv1.Gen(&zkatsnarkv1.GeneratorArgs{
			IdemixMSPDirs: []string{filepath.Join(testdata, "idemix")},
			Issuers:       []string{filepath.Join(testdata, "issuers", "msp")},
			OutputDir:     out,
			ProofSystem:   "plonkfri",
			Curve:         "bn254",
			MaxBits:       8,
		})
		require.NoError(t, err)

		oldStdout := os.Stdout
		r, w, err := os.Pipe()
		require.NoError(t, err)
		os.Stdout = w
		err = Print(&Args{InputFile: filepath.Join(out, "zkatsnarkv1_pp.json")})
		require.NoError(t, w.Close())
		os.Stdout = oldStdout
		require.NoError(t, err)
		var buf bytes.Buffer
		_, err = buf.ReadFrom(r)
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "zkatsnark public params")
		assert.Contains(t, buf.String(), "proofSystem=plonkfri")
	})

	t.Run("read_fail", func(t *testing.T) {
		err := Print(&Args{InputFile: "nonexistent.json"})
		require.Error(t, err)
	})

	t.Run("unmarshal_fail", func(t *testing.T) {
		tempDir := t.TempDir()
		invalidFile := filepath.Join(tempDir, "invalid.json")
		err := os.WriteFile(invalidFile, []byte("invalid content"), 0644)
		require.NoError(t, err)
		err = Print(&Args{InputFile: invalidFile})
		require.Error(t, err)
	})
}
