/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabtokenv1

import (
	"os"
	"path/filepath"
	"testing"

	setupv1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCmd tests the Cmd function.
func TestCmd(t *testing.T) {
	cmd := Cmd()
	assert.NotNil(t, cmd)

	t.Run("x509_owner_flags", func(t *testing.T) {
		for _, name := range []string{"x509-owner-roots", "x509-owner-intermediates"} {
			f := cmd.Flags().Lookup(name)
			require.NotNil(t, f, name)
			assert.Empty(t, f.Shorthand, name)
			assert.Equal(t, "stringSlice", f.Value.Type(), name)
		}
	})

	t.Run("trailing_args", func(t *testing.T) {
		cmd.SetArgs([]string{"extra"})
		err := cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "trailing args detected")
	})
}

// TestGen tests the Gen function.
func TestGen(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("success_no_issuers", func(t *testing.T) {
		args := &GeneratorArgs{
			OutputDir: tempDir,
		}
		raw, err := Gen(args)
		require.NoError(t, err)
		assert.NotNil(t, raw)
		assert.FileExists(t, filepath.Join(tempDir, "fabtoken1_pp.json"))
	})

	t.Run("success_with_version", func(t *testing.T) {
		Version = 2
		args := &GeneratorArgs{
			OutputDir: tempDir,
		}
		raw, err := Gen(args)
		require.NoError(t, err)
		assert.NotNil(t, raw)
		assert.FileExists(t, filepath.Join(tempDir, "fabtoken2_pp.json"))
		Version = 0 // reset
	})

	t.Run("fail_setup_issuers", func(t *testing.T) {
		args := &GeneratorArgs{
			OutputDir: tempDir,
			Issuers:   []string{"nonexistent"},
		}
		raw, err := Gen(args)
		require.Error(t, err)
		assert.Nil(t, raw)
	})

	t.Run("success_full", func(t *testing.T) {
		// Create dummy cert
		certDir := filepath.Join(tempDir, "cert")
		err := os.MkdirAll(filepath.Join(certDir, "signcerts"), 0750)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(certDir, "signcerts", "cert.crt"), generateFabTestCertificate(t), 0644)
		require.NoError(t, err)

		extraFile := filepath.Join(tempDir, "extra.txt")
		err = os.WriteFile(extraFile, []byte("extra content"), 0644)
		require.NoError(t, err)

		Extras = []string{"key=" + extraFile}
		args := &GeneratorArgs{
			OutputDir: tempDir,
			Issuers:   []string{certDir},
			Auditors:  []string{certDir},
		}
		raw, err := Gen(args)
		require.NoError(t, err)
		assert.NotNil(t, raw)
		Extras = nil // reset
	})

	t.Run("success_cc", func(t *testing.T) {
		args := &GeneratorArgs{
			OutputDir:         tempDir,
			GenerateCCPackage: true,
		}
		raw, err := Gen(args)
		require.NoError(t, err)
		assert.NotNil(t, raw)
	})
}

// TestGenX509OwnerCertificates tests that Gen loads, validates, and stores the x509 owner CA certificates.
func TestGenX509OwnerCertificates(t *testing.T) {
	root := crypto.NewTestCertificate(t, crypto.NewTestCATemplate("root"), nil)
	inter := crypto.NewTestCertificate(t, crypto.NewTestCATemplate("inter"), root)
	leaf := crypto.NewTestCertificate(t, crypto.NewTestLeafTemplate("leaf"), root)

	dir := t.TempDir()
	write := func(name string, content []byte) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))
		require.NoError(t, os.WriteFile(path, content, 0600))

		return path
	}
	write(filepath.Join("msp", "cacerts", "root.pem"), root.PEM)
	write(filepath.Join("msp", "intermediatecerts", "inter.pem"), inter.PEM)
	interFile := write("inter.pem", inter.PEM)
	leafFile := write("leaf.pem", leaf.PEM)

	t.Run("success_msp_dirs", func(t *testing.T) {
		out := t.TempDir()
		raw, err := Gen(&GeneratorArgs{
			OutputDir:              out,
			X509OwnerRoots:         []string{filepath.Join(dir, "msp", "cacerts")},
			X509OwnerIntermediates: []string{filepath.Join(dir, "msp", "intermediatecerts")},
		})
		require.NoError(t, err)
		pp, err := setupv1.NewPublicParamsFromBytes(raw, setupv1.FabTokenDriverName, setupv1.ProtocolV1)
		require.NoError(t, err)
		assert.Equal(t, [][]byte{root.PEM}, pp.X509OwnerRoots)
		assert.Equal(t, [][]byte{inter.PEM}, pp.X509OwnerIntermediates)
		stored, err := os.ReadFile(filepath.Join(out, "fabtoken1_pp.json"))
		require.NoError(t, err)
		assert.Equal(t, raw, stored)
	})

	t.Run("success_files", func(t *testing.T) {
		raw, err := Gen(&GeneratorArgs{
			OutputDir:              t.TempDir(),
			X509OwnerRoots:         []string{filepath.Join(dir, "msp", "cacerts", "root.pem")},
			X509OwnerIntermediates: []string{interFile},
		})
		require.NoError(t, err)
		pp, err := setupv1.NewPublicParamsFromBytes(raw, setupv1.FabTokenDriverName, setupv1.ProtocolV1)
		require.NoError(t, err)
		assert.Equal(t, [][]byte{root.PEM}, pp.X509OwnerRoots)
		assert.Equal(t, [][]byte{inter.PEM}, pp.X509OwnerIntermediates)
	})

	t.Run("fail_missing_roots", func(t *testing.T) {
		_, err := Gen(&GeneratorArgs{
			OutputDir:      t.TempDir(),
			X509OwnerRoots: []string{filepath.Join(dir, "missing")},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed loading x509 owner roots")
	})

	t.Run("fail_missing_intermediates", func(t *testing.T) {
		_, err := Gen(&GeneratorArgs{
			OutputDir:              t.TempDir(),
			X509OwnerRoots:         []string{filepath.Join(dir, "msp", "cacerts")},
			X509OwnerIntermediates: []string{filepath.Join(dir, "missing")},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed loading x509 owner intermediates")
	})

	t.Run("fail_validate_leaf_as_root", func(t *testing.T) {
		out := t.TempDir()
		_, err := Gen(&GeneratorArgs{
			OutputDir:      out,
			X509OwnerRoots: []string{leafFile},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to validate public parameters")
		assert.NoFileExists(t, filepath.Join(out, "fabtoken1_pp.json"))
	})

	t.Run("fail_validate_intermediates_without_roots", func(t *testing.T) {
		out := t.TempDir()
		_, err := Gen(&GeneratorArgs{
			OutputDir:              out,
			X509OwnerIntermediates: []string{interFile},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to validate public parameters")
		assert.NoFileExists(t, filepath.Join(out, "fabtoken1_pp.json"))
	})
}

// generateFabTestCertificate generates a test certificate for FabToken tests.
func generateFabTestCertificate(t *testing.T) []byte {
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
