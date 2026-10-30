/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnarkv1

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/pp/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// genPP generates a zkatsnark PP with the testdata issuer and auditor and returns its path.
func genPP(t *testing.T) (string, *pp.PublicParams) {
	t.Helper()
	dir := t.TempDir()
	args := fastArgs(dir)
	args.Issuers = []string{issuerDir}
	args.Auditors = []string{auditorDir}
	var raw []byte
	var err error
	captureStdout(t, func() { raw, err = Gen(args) })
	require.NoError(t, err)
	p, err := pp.DeserializePublicParams(raw)
	require.NoError(t, err)

	return filepath.Join(dir, "zkatsnarkv1_pp.json"), p
}

func readPP(t *testing.T, path string) *pp.PublicParams {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	p, err := pp.DeserializePublicParams(raw)
	require.NoError(t, err)

	return p
}

// assertCircuitPreserved checks that update did not touch anything circuit-related.
func assertCircuitPreserved(t *testing.T, want, got *pp.PublicParams) {
	t.Helper()
	assert.Equal(t, want.PKSpend, got.PKSpend)
	assert.Equal(t, want.PKOutput, got.PKOutput)
	assert.Equal(t, want.VKSpend, got.VKSpend)
	assert.Equal(t, want.VKOutput, got.VKOutput)
	assert.Equal(t, want.Curve, got.Curve)
	assert.Equal(t, want.ProofSystem, got.ProofSystem)
	assert.Equal(t, want.MaxBits, got.MaxBits)
	assert.Equal(t, want.PrecisionValue, got.PrecisionValue)
	assert.Equal(t, want.IdemixIssuerPublicKeys, got.IdemixIssuerPublicKeys)
}

// TestUpdateCmd tests the UpdateCmd function.
func TestUpdateCmd(t *testing.T) {
	cmd := UpdateCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, DriverIdentifier, cmd.Use)

	t.Run("trailing_args", func(t *testing.T) {
		cmd.SetArgs([]string{"extra"})
		err := cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "trailing args detected")
	})
}

// TestUpdate tests the Update function.
func TestUpdate(t *testing.T) {
	input, orig := genPP(t)
	issuerID, err := common.GetX509Identity(issuerDir)
	require.NoError(t, err)
	auditorID, err := common.GetX509Identity(auditorDir)
	require.NoError(t, err)

	t.Run("replace", func(t *testing.T) {
		out := t.TempDir()
		// swap: auditor cert becomes the issuer and vice versa
		require.NoError(t, Update(&UpdateArgs{InputFile: input, OutputDir: out, Issuers: []string{auditorDir}, Auditors: []string{issuerDir}}))
		p := readPP(t, filepath.Join(out, "zkatsnarkv1_pp.json"))
		assert.Equal(t, []driver.Identity{auditorID}, p.Issuers())
		assert.Equal(t, []driver.Identity{issuerID}, p.Auditors())
		assertCircuitPreserved(t, orig, p)
	})

	t.Run("append_deduplicates", func(t *testing.T) {
		out := t.TempDir()
		require.NoError(t, Update(&UpdateArgs{InputFile: input, OutputDir: out, Append: true, Issuers: []string{auditorDir, issuerDir}, Auditors: []string{auditorDir}}))
		p := readPP(t, filepath.Join(out, "zkatsnarkv1_pp.json"))
		assert.Equal(t, []driver.Identity{issuerID, auditorID}, p.Issuers())
		assert.Equal(t, []driver.Identity{auditorID}, p.Auditors())
		assertCircuitPreserved(t, orig, p)
	})

	t.Run("no_flags_keeps_identities_and_version", func(t *testing.T) {
		out := t.TempDir()
		require.NoError(t, Update(&UpdateArgs{InputFile: input, OutputDir: out}))
		p := readPP(t, filepath.Join(out, "zkatsnarkv1_pp.json"))
		assert.Equal(t, orig.Issuers(), p.Issuers())
		assert.Equal(t, orig.Auditors(), p.Auditors())
		assert.Equal(t, orig.SchemeVersion, p.SchemeVersion)
		assertCircuitPreserved(t, orig, p)
	})

	t.Run("version_and_extras", func(t *testing.T) {
		out := t.TempDir()
		require.NoError(t, Update(&UpdateArgs{
			InputFile: input, OutputDir: out, Version: 3,
			Extras: []string{"f1=" + filepath.Join(extrasDir, "f1.txt"), "f2=" + filepath.Join(extrasDir, "f2.txt")},
		}))
		p := readPP(t, filepath.Join(out, "zkatsnarkv3_pp.json"))
		assert.Equal(t, driver.TokenDriverVersion(3), p.SchemeVersion)
		assert.Len(t, p.Extras(), 2)
		assertCircuitPreserved(t, orig, p)
	})

	t.Run("output_exists", func(t *testing.T) {
		err := Update(&UpdateArgs{InputFile: input, OutputDir: filepath.Dir(input)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exists in the output folder")
	})

	t.Run("missing_input", func(t *testing.T) {
		err := Update(&UpdateArgs{InputFile: "/nonexistent.json", OutputDir: t.TempDir()})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read input file")
	})

	t.Run("garbage_input", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "garbage.json")
		require.NoError(t, os.WriteFile(f, []byte("garbage"), 0600))
		err := Update(&UpdateArgs{InputFile: f, OutputDir: t.TempDir()})
		require.Error(t, err)
	})

	t.Run("zkatdlog_input", func(t *testing.T) {
		err := Update(&UpdateArgs{InputFile: filepath.Join(testdataDir, "zkatdlognoghv1_pp.json"), OutputDir: t.TempDir()})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected driver [zkatsnark.v*], got [zkatdlognogh.v1]")
	})

	t.Run("keyless_input", func(t *testing.T) {
		raw, err := pp.DefaultPublicParams().Serialize()
		require.NoError(t, err)
		f := filepath.Join(t.TempDir(), "keyless.json")
		require.NoError(t, os.WriteFile(f, raw, 0600))
		err = Update(&UpdateArgs{InputFile: f, OutputDir: t.TempDir()})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "circuit proving/verification keys are missing")
	})

	t.Run("bad_issuer", func(t *testing.T) {
		err := Update(&UpdateArgs{InputFile: input, OutputDir: t.TempDir(), Issuers: []string{"/nonexistent"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load issuer identities")
	})

	t.Run("bad_extras", func(t *testing.T) {
		err := Update(&UpdateArgs{InputFile: input, OutputDir: t.TempDir(), Extras: []string{"bad"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed loading extras")
	})

	t.Run("nil_args", func(t *testing.T) {
		require.Error(t, Update(nil))
	})
}
