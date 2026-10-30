/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnarkv1

import (
	"os"
	"path/filepath"
	"testing"

	math3 "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/pp/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	zkatsnark "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDriverIdentifier(t *testing.T) {
	assert.Equal(t, string(zkatsnark.NewPPMFactory().Name), DriverIdentifier)
}

// TestCmd tests the Cmd function.
func TestCmd(t *testing.T) {
	cmd := Cmd()
	require.NotNil(t, cmd)
	assert.Equal(t, DriverIdentifier, cmd.Use)

	t.Run("trailing_args", func(t *testing.T) {
		cmd.SetArgs([]string{"extra"})
		err := cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "trailing args detected")
	})

	t.Run("invalid_proof_system", func(t *testing.T) {
		cmd.SetArgs([]string{"--idemix", idemixDir, "--proof-system", "foo", "--output", t.TempDir()})
		err := cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported proof system")
	})

	t.Run("success", func(t *testing.T) {
		out := t.TempDir()
		cmd.SetArgs([]string{
			"--idemix", idemixDir, "--issuers", issuerDir, "--auditors", auditorDir,
			"--proof-system", "plonk", "--curve", "bn254", "--bits", "8", "--output", out,
		})
		require.NoError(t, cmd.Execute())
		raw, err := os.ReadFile(filepath.Join(out, "zkatsnarkv1_pp.json"))
		require.NoError(t, err)
		p, err := pp.DeserializePublicParams(raw)
		require.NoError(t, err)
		assert.Equal(t, "plonk", p.ProofSystem)
		assert.Equal(t, uint64(8), p.PrecisionValue)
	})
}

// TestGen tests the Gen function.
func TestGen(t *testing.T) {
	issuerID, err := common.GetX509Identity(issuerDir)
	require.NoError(t, err)
	auditorID, err := common.GetX509Identity(auditorDir)
	require.NoError(t, err)
	ipk, err := os.ReadFile(filepath.Join(idemixDir, "msp", "IssuerPublicKey"))
	require.NoError(t, err)

	for _, ps := range []string{"groth16", "plonk", "plonkfri"} {
		t.Run("success_"+ps, func(t *testing.T) {
			out := t.TempDir()
			args := fastArgs(out)
			args.ProofSystem = ps
			args.Issuers = []string{issuerDir}
			args.Auditors = []string{auditorDir}
			var raw []byte
			stdout := captureStdout(t, func() {
				raw, err = Gen(args)
			})
			require.NoError(t, err)
			assert.NotContains(t, stdout, "open-policy")
			assert.Contains(t, stdout, ps)

			fileRaw, err := os.ReadFile(filepath.Join(out, "zkatsnarkv1_pp.json"))
			require.NoError(t, err)
			assert.Equal(t, raw, fileRaw)
			p, err := pp.DeserializePublicParams(raw)
			require.NoError(t, err)
			assert.Equal(t, ps, p.ProofSystem)
			assert.Equal(t, ecc.BN254, p.Curve)
			assert.Equal(t, 8, p.MaxBits)
			assert.Equal(t, uint64(8), p.PrecisionValue)
			assert.Equal(t, []driver.Identity{issuerID}, p.Issuers())
			assert.Equal(t, []driver.Identity{auditorID}, p.Auditors())
			require.Len(t, p.IdemixIssuerPublicKeys, 1)
			assert.Equal(t, ipk, p.IdemixIssuerPublicKeys[0].PublicKey)
			assert.Equal(t, math3.BN254, p.IdemixIssuerPublicKeys[0].Curve)
			assert.NotEmpty(t, p.PKSpend)
			assert.NotEmpty(t, p.VKOutput)
		})
	}

	t.Run("success_aries_multi_idemix_version_extras_precision", func(t *testing.T) {
		out := t.TempDir()
		args := fastArgs(out)
		args.Aries = true
		args.IdemixMSPDirs = []string{idemixDir, idemixDir}
		args.Version = 2
		args.Precision = 6
		args.Extras = []string{"f1=" + filepath.Join(extrasDir, "f1.txt")}
		raw, err := Gen(args)
		require.NoError(t, err)
		assert.FileExists(t, filepath.Join(out, "zkatsnarkv2_pp.json"))
		p, err := pp.DeserializePublicParams(raw)
		require.NoError(t, err)
		assert.Equal(t, driver.TokenDriverVersion(2), p.SchemeVersion)
		assert.Equal(t, uint64(6), p.PrecisionValue)
		require.Len(t, p.IdemixIssuerPublicKeys, 2)
		for _, k := range p.IdemixIssuerPublicKeys {
			assert.Equal(t, math3.BLS12_381_BBS_GURVY, k.Curve)
		}
		f1, err := os.ReadFile(filepath.Join(extrasDir, "f1.txt"))
		require.NoError(t, err)
		assert.Equal(t, f1, p.Extras()["f1"])
	})

	t.Run("open_policy_warning", func(t *testing.T) {
		var err error
		stdout := captureStdout(t, func() {
			_, err = Gen(fastArgs(t.TempDir()))
		})
		require.NoError(t, err)
		assert.Contains(t, stdout, "WARNING: open-policy mode")
	})

	negatives := []struct {
		name   string
		mutate func(*GeneratorArgs)
		msg    string
	}{
		{"nil_idemix", func(a *GeneratorArgs) { a.IdemixMSPDirs = nil }, "at least one idemix msp dir"},
		{"missing_idemix", func(a *GeneratorArgs) { a.IdemixMSPDirs = []string{"/nonexistent"} }, "failed to load issuer public key"},
		{"bad_issuer", func(a *GeneratorArgs) { a.Issuers = []string{"/nonexistent"} }, "failed to load issuer identities"},
		{"bad_auditor", func(a *GeneratorArgs) { a.Auditors = []string{"/nonexistent"} }, "failed to load auditor identities"},
		{"bad_extras", func(a *GeneratorArgs) { a.Extras = []string{"noequals"} }, "failed loading extras"},
		{"invalid_curve", func(a *GeneratorArgs) { a.Curve = "foo" }, "invalid curve"},
		{"unsupported_curve", func(a *GeneratorArgs) { a.Curve = "bls12_377" }, "unsupported curve"},
		{"invalid_proof_system", func(a *GeneratorArgs) { a.ProofSystem = "foo" }, "unsupported proof system"},
		{"plonkfri_bls12_381", func(a *GeneratorArgs) { a.ProofSystem = "plonkfri"; a.Curve = "bls12_381" }, "supports only Curve"},
		{"zero_bits", func(a *GeneratorArgs) { a.MaxBits = 0 }, "invalid bits"},
		{"too_many_bits", func(a *GeneratorArgs) { a.MaxBits = 65 }, "invalid bits"},
		{"precision_exceeds_bits", func(a *GeneratorArgs) { a.Precision = 9 }, "must not exceed MaxBits"},
		{"bad_output", func(a *GeneratorArgs) { a.OutputDir = "/nonexistent/dir" }, "failed writing public parameters"},
	}
	for _, tc := range negatives {
		t.Run(tc.name, func(t *testing.T) {
			args := fastArgs(t.TempDir())
			tc.mutate(args)
			var err error
			captureStdout(t, func() { _, err = Gen(args) })
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.msg)
		})
	}

	t.Run("nil_args", func(t *testing.T) {
		_, err := Gen(nil)
		require.Error(t, err)
	})
}
