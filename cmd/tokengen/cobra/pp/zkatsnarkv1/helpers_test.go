/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnarkv1

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	testdataDir = filepath.Join("..", "..", "..", "testdata")
	idemixDir   = filepath.Join(testdataDir, "idemix")
	issuerDir   = filepath.Join(testdataDir, "issuers", "msp")
	auditorDir  = filepath.Join(testdataDir, "auditors", "msp")
	extrasDir   = filepath.Join(testdataDir, "extras")
)

// fastArgs returns generator arguments with the cheapest valid setup parameters.
func fastArgs(outputDir string) *GeneratorArgs {
	return &GeneratorArgs{
		IdemixMSPDirs: []string{idemixDir},
		OutputDir:     outputDir,
		ProofSystem:   "groth16",
		Curve:         "bn254",
		MaxBits:       8,
	}
}

// captureStdout runs f and returns what it printed on stdout.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = old }()
	f()
	require.NoError(t, w.Close())
	var buf bytes.Buffer
	_, err = buf.ReadFrom(r)
	require.NoError(t, err)

	return buf.String()
}
