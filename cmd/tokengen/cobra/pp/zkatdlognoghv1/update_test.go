/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatdlognoghv1

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	ppencoding "github.com/LFDT-Panurus/panurus/token/core/common/encoding/pp"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	pp2 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/pp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpdateCmd tests the UpdateCmd function.
func TestUpdateCmd(t *testing.T) {
	cmd := UpdateCmd()
	assert.NotNil(t, cmd)

	t.Run("trailing_args", func(t *testing.T) {
		cmd.SetArgs([]string{"extra"})
		err := cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "trailing args detected")
	})
}

// TestUpdate tests the Update function.
func TestUpdate(t *testing.T) {
	wd, _ := os.Getwd()
	testdataPath := filepath.Join(wd, "..", "..", "..", "testdata", "zkatdlognoghv1_pp.json")

	t.Run("success", func(t *testing.T) {
		tempDir := t.TempDir()
		args := &UpdateArgs{
			InputFile: testdataPath,
			OutputDir: tempDir,
		}
		err := Update(args)
		require.NoError(t, err)
		assert.FileExists(t, filepath.Join(tempDir, "zkatdlognoghv1_pp.json"))
	})

	t.Run("fail_read_input", func(t *testing.T) {
		tempDir := t.TempDir()
		args := &UpdateArgs{
			InputFile: "nonexistent",
			OutputDir: tempDir,
		}
		err := Update(args)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read input file")
	})

	t.Run("success_full", func(t *testing.T) {
		tempDir := t.TempDir()
		// Create dummy cert
		certDir := filepath.Join(tempDir, "cert")
		err := os.MkdirAll(filepath.Join(certDir, "signcerts"), 0750)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(certDir, "signcerts", "cert.crt"), generateUpdateTestCertificate(t), 0644)
		require.NoError(t, err)

		args := &UpdateArgs{
			InputFile: testdataPath,
			OutputDir: tempDir,
			Auditors:  []string{certDir},
			Issuers:   []string{certDir},
		}
		err = Update(args)
		require.NoError(t, err)
		assert.FileExists(t, filepath.Join(tempDir, "zkatdlognoghv1_pp.json"))
	})

	t.Run("panic_recover", func(t *testing.T) {
		// This triggers a panic when accessing args.InputFile, which must be reported as an error
		err := Update(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed updating public parameters")
	})

	t.Run("keep_input_version", func(t *testing.T) {
		tempDir := t.TempDir()
		input := generateUpdateTestPP(t, tempDir, 2)
		outputDir := filepath.Join(tempDir, "out")
		require.NoError(t, os.MkdirAll(outputDir, 0750))

		err := Update(&UpdateArgs{
			InputFile: input,
			OutputDir: outputDir,
		})
		require.NoError(t, err)
		pp := loadUpdateTestPP(t, filepath.Join(outputDir, "zkatdlognoghv2_pp.json"), 2)
		assert.Equal(t, driver.TokenDriverVersion(2), pp.DriverVersion)
	})

	t.Run("update_twice_with_version", func(t *testing.T) {
		tempDir := t.TempDir()
		firstDir := filepath.Join(tempDir, "first")
		secondDir := filepath.Join(tempDir, "second")
		require.NoError(t, os.MkdirAll(firstDir, 0750))
		require.NoError(t, os.MkdirAll(secondDir, 0750))

		err := Update(&UpdateArgs{
			InputFile: testdataPath,
			OutputDir: firstDir,
			Version:   2,
		})
		require.NoError(t, err)

		err = Update(&UpdateArgs{
			InputFile: filepath.Join(firstDir, "zkatdlognoghv2_pp.json"),
			OutputDir: secondDir,
		})
		require.NoError(t, err)
		loadUpdateTestPP(t, filepath.Join(secondDir, "zkatdlognoghv2_pp.json"), 2)
	})

	t.Run("override_input_version", func(t *testing.T) {
		tempDir := t.TempDir()
		input := generateUpdateTestPP(t, tempDir, 2)
		outputDir := filepath.Join(tempDir, "out")
		require.NoError(t, os.MkdirAll(outputDir, 0750))

		err := Update(&UpdateArgs{
			InputFile: input,
			OutputDir: outputDir,
			Version:   3,
		})
		require.NoError(t, err)
		loadUpdateTestPP(t, filepath.Join(outputDir, "zkatdlognoghv3_pp.json"), 3)
	})

	t.Run("extras_from_args", func(t *testing.T) {
		tempDir := t.TempDir()
		extraFile := filepath.Join(wd, "..", "..", "..", "testdata", "extras", "f1.txt")
		expected, err := os.ReadFile(extraFile)
		require.NoError(t, err)

		err = Update(&UpdateArgs{
			InputFile: testdataPath,
			OutputDir: tempDir,
			Extras:    []string{"key=" + extraFile},
		})
		require.NoError(t, err)
		pp := loadUpdateTestPP(t, filepath.Join(tempDir, "zkatdlognoghv1_pp.json"), 1)
		assert.Equal(t, expected, pp.ExtraData["key"])
	})

	t.Run("fail_wrong_driver", func(t *testing.T) {
		tempDir := t.TempDir()
		raw, err := ppencoding.Marshal(&pp2.PublicParameters{Identifier: "fabtoken.v1", Raw: []byte("raw")})
		require.NoError(t, err)
		input := filepath.Join(tempDir, "pp.json")
		require.NoError(t, os.WriteFile(input, raw, 0600))

		err = Update(&UpdateArgs{
			InputFile: input,
			OutputDir: tempDir,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid identifier")
	})

	t.Run("fail_invalid_version", func(t *testing.T) {
		tempDir := t.TempDir()
		raw, err := ppencoding.Marshal(&pp2.PublicParameters{Identifier: string(v1.DLogNoGHDriverName) + ".vX", Raw: []byte("raw")})
		require.NoError(t, err)
		input := filepath.Join(tempDir, "pp.json")
		require.NoError(t, os.WriteFile(input, raw, 0600))

		err = Update(&UpdateArgs{
			InputFile: input,
			OutputDir: tempDir,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid driver version")
	})
}

// generateUpdateTestPP generates public parameters with the given version in dir and returns the file path.
func generateUpdateTestPP(t *testing.T, dir string, version uint32) string {
	t.Helper()
	idemixDir := filepath.Join(dir, "idemix")
	require.NoError(t, os.MkdirAll(filepath.Join(idemixDir, "msp"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(idemixDir, "msp", "IssuerPublicKey"), []byte("dummy ipk"), 0600))

	_, err := Gen(&GeneratorArgs{
		IdemixMSPDir: idemixDir,
		OutputDir:    dir,
		BitLength:    64,
		Version:      version,
	})
	require.NoError(t, err)

	return filepath.Join(dir, fmt.Sprintf("zkatdlognoghv%d_pp.json", version))
}

// loadUpdateTestPP loads the public parameters at path, expecting the given version.
func loadUpdateTestPP(t *testing.T, path string, version driver.TokenDriverVersion) *v1.PublicParams {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	pp, err := v1.NewPublicParamsFromBytes(raw, v1.DLogNoGHDriverName, version)
	require.NoError(t, err)

	return pp
}

// generateUpdateTestCertificate generates a test certificate for update tests.
func generateUpdateTestCertificate(t *testing.T) []byte {
	t.Helper()
	// Simple PEM certificate for testing
	return []byte("-----BEGIN CERTIFICATE-----\n" +
		"MIICQDCCAamgAwIBAgIRAM9vjgm8Ze+1zGO/xI6uiVsFADAKBggqhkjOPQQDAzBz\n" +
		"MQswCQYDVQQGEwJVUzETMBEGA1UECBMKQ2FsaWZvcm5pYTEWMBQGA1UEBxMNU2Fu\n" +
		"IEZyYW5jaXNjbzEZMBcGA1UEChMQSHlwZXJsZWRnZXIgRmFicmljMRkwFwYDVQQD\n" +
		"ExBmYWJyaWMta2Euc2VydmVyMB4XDTI2MDIyMDA3NTY0M1oXDTI3MDIyMDA3NTY0\n" +
		"M1owBzEFMEMGA1UEAxM0Y2VydGlmaWNhdGUgd2l0aCBubyBvcmdhbml6YXRpb24g\n" +
		"dW5pdCBvciBvcmdhbml6YXRpb24wWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAARL\n" +
		"B8WndVQsKgE0JWRBwj1khvVAhXWuss1/bgYnPU66gceTzsdUp54wuXBVdSa+3TYn\n" +
		"YBw5/JZVoP2JKhAH6xXFwo06MDgwDgYDVR0PAQH/BAQDAgWgMB0GA1UdJQQWMBQG\n" +
		"CCsGAQUFBwMBBggrBgEFBQcDAjAMBgNVHRMBAf8EAjAAMAoGCCqGSM49BAMDA2gA\n" +
		"MGUCMQCXz0fXfG+vY+T+I8Kj0fXfG+vY+T+I8Kj0fXfG+vY+T+I8Kj0fXfG+vY+T\n" +
		"+I8Kj0fXfG+vY+T+I8Kj0fXfG+vY+T+I8Kj0fXfG+vY+T+I8Kj0fXfG+vY+T+I8=\n" +
		"-----END CERTIFICATE-----\n")
}
