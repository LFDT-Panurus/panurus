/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateTestCertificate generates a test certificate for common tests.
func generateTestCertificate(t *testing.T) []byte {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test Org"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(time.Hour),

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	out := &bytes.Buffer{}
	if err := pem.Encode(out, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		t.Fatalf("failed to encode certificate: %v", err)
	}

	return out.Bytes()
}

// TestLoadExtras tests the LoadExtras function.
func TestLoadExtras(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Test case 1: Successfully load multiple files
	t.Run("success_multiple_files", func(t *testing.T) {
		// Create test files
		file1Path := filepath.Join(tempDir, "test1.json")
		file1Content := []byte(`{"key": "value1"}`)
		if err := os.WriteFile(file1Path, file1Content, 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		file2Path := filepath.Join(tempDir, "test2.json")
		file2Content := []byte(`{"key": "value2"}`)
		if err := os.WriteFile(file2Path, file2Content, 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		extraFiles := []string{
			"foo=" + file1Path,
			"bar=" + file2Path,
		}

		result, err := LoadExtras(extraFiles)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}

		if len(result) != 2 {
			t.Errorf("expected 2 entries, got %d", len(result))
		}

		if !bytes.Equal(result["foo"], file1Content) {
			t.Errorf("expected %q for foo, got %q", string(file1Content), string(result["foo"]))
		}

		if !bytes.Equal(result["bar"], file2Content) {
			t.Errorf("expected %q for bar, got %q", string(file2Content), string(result["bar"]))
		}
	})

	// Test case 2: Empty input slice
	t.Run("empty_input", func(t *testing.T) {
		extraFiles := []string{}

		result, err := LoadExtras(extraFiles)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}

		if len(result) != 0 {
			t.Errorf("expected empty map, got %d entries", len(result))
		}
	})

	// Test case 3: File does not exist
	t.Run("file_not_found", func(t *testing.T) {
		extraFiles := []string{
			"missing=" + filepath.Join(tempDir, "nonexistent.json"),
		}

		result, err := LoadExtras(extraFiles)
		if err == nil {
			t.Fatal("expected error for missing file, got nil")
		}

		if result != nil {
			t.Errorf("expected nil result on error, got: %v", result)
		}
	})

	// Test case 4: Invalid format - no colon
	t.Run("invalid_format_no_colon", func(t *testing.T) {
		extraFiles := []string{"foobar"}

		result, err := LoadExtras(extraFiles)
		if err == nil {
			t.Fatal("expected error for invalid format, got nil")
		}

		if result != nil {
			t.Errorf("expected nil result on error, got: %v", result)
		}
	})

	// Test case 5: Invalid format - empty key
	t.Run("invalid_format_empty_key", func(t *testing.T) {
		extraFiles := []string{"=" + filepath.Join(tempDir, "test.json")}

		result, err := LoadExtras(extraFiles)
		if err == nil {
			t.Fatal("expected error for empty key, got nil")
		}

		if result != nil {
			t.Errorf("expected nil result on error, got: %v", result)
		}
	})

	// Test case 6: Invalid format - empty filepath
	t.Run("invalid_format_empty_filepath", func(t *testing.T) {
		extraFiles := []string{"key="}

		result, err := LoadExtras(extraFiles)
		if err == nil {
			t.Fatal("expected error for empty filepath, got nil")
		}

		if result != nil {
			t.Errorf("expected nil result on error, got: %v", result)
		}
	})

	// Test case 7: Filepath with colons (e.g., Windows paths or URLs)
	t.Run("filepath_with_colons", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "test.json")
		fileContent := []byte("content")
		if err := os.WriteFile(filePath, fileContent, 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		// Simulate a key with filepath that might have colons
		extraFiles := []string{
			"mykey=" + filePath,
		}

		result, err := LoadExtras(extraFiles)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}

		if !bytes.Equal(result["mykey"], fileContent) {
			t.Errorf("expected %q, got %q", string(fileContent), string(result["mykey"]))
		}
	})

	// Test case 8: Binary file content
	t.Run("binary_content", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "binary.dat")
		binaryContent := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD}
		if err := os.WriteFile(filePath, binaryContent, 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		extraFiles := []string{"binary=" + filePath}

		result, err := LoadExtras(extraFiles)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}

		if len(result["binary"]) != len(binaryContent) {
			t.Errorf("expected length %d, got %d", len(binaryContent), len(result["binary"]))
		}

		for i, b := range binaryContent {
			if result["binary"][i] != b {
				t.Errorf("byte mismatch at index %d: expected %x, got %x", i, b, result["binary"][i])
			}
		}
	})
}

// TestReadSingleCertificateFromFile tests the ReadSingleCertificateFromFile function.
func TestReadSingleCertificateFromFile(t *testing.T) {
	tempDir := t.TempDir()
	certPath := filepath.Join(tempDir, "test.crt")
	certContent := generateTestCertificate(t)
	err := os.WriteFile(certPath, certContent, 0644)
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		res, err := ReadSingleCertificateFromFile(certPath)
		require.NoError(t, err)
		assert.Equal(t, certContent, res)
	})

	t.Run("not_found", func(t *testing.T) {
		res, err := ReadSingleCertificateFromFile(filepath.Join(tempDir, "not_found"))
		require.Error(t, err)
		assert.Nil(t, res)
	})

	t.Run("invalid_pem", func(t *testing.T) {
		invalidPath := filepath.Join(tempDir, "invalid.pem")
		err := os.WriteFile(invalidPath, []byte("not a pem"), 0644)
		require.NoError(t, err)
		res, err := ReadSingleCertificateFromFile(invalidPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no pem content")
		assert.Nil(t, res)
	})

	t.Run("extra_content", func(t *testing.T) {
		extraPath := filepath.Join(tempDir, "extra.pem")
		err := os.WriteFile(extraPath, append(certContent, []byte("extra")...), 0644)
		require.NoError(t, err)
		res, err := ReadSingleCertificateFromFile(extraPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "extra content")
		assert.Nil(t, res)
	})

	t.Run("not_a_certificate", func(t *testing.T) {
		keyPath := filepath.Join(tempDir, "test.key")
		priv, _ := rsa.GenerateKey(rand.Reader, 2048)
		keyBytes := x509.MarshalPKCS1PrivateKey(priv)
		out := &bytes.Buffer{}
		err := pem.Encode(out, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyBytes})
		require.NoError(t, err)
		err = os.WriteFile(keyPath, out.Bytes(), 0644)
		require.NoError(t, err)

		res, err := ReadSingleCertificateFromFile(keyPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is not a certificate")
		assert.Nil(t, res)
	})
}

// TestGetCertificatesFromDir tests the GetCertificatesFromDir function.
func TestGetCertificatesFromDir(t *testing.T) {
	tempDir := t.TempDir()
	certContent := generateTestCertificate(t)

	t.Run("success", func(t *testing.T) {
		dir := filepath.Join(tempDir, "success")
		err := os.MkdirAll(dir, 0750)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(dir, "cert1.crt"), certContent, 0644)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(dir, "cert2.crt"), certContent, 0644)
		require.NoError(t, err)

		res, err := GetCertificatesFromDir(dir)
		require.NoError(t, err)
		assert.Len(t, res, 2)
	})

	t.Run("not_found", func(t *testing.T) {
		res, err := GetCertificatesFromDir(filepath.Join(tempDir, "not_found"))
		require.Error(t, err)
		assert.True(t, os.IsNotExist(err))
		assert.Nil(t, res)
	})

	t.Run("no_certs", func(t *testing.T) {
		dir := filepath.Join(tempDir, "no_certs")
		err := os.MkdirAll(dir, 0750)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(dir, "not_a_cert.txt"), []byte("hello"), 0644)
		require.NoError(t, err)

		res, err := GetCertificatesFromDir(dir)
		require.Error(t, err)
		assert.Empty(t, res)
	})

	t.Run("with_subdir", func(t *testing.T) {
		dir := filepath.Join(tempDir, "with_subdir")
		err := os.MkdirAll(dir, 0750)
		require.NoError(t, err)
		err = os.MkdirAll(filepath.Join(dir, "subdir"), 0750)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(dir, "cert1.crt"), certContent, 0644)
		require.NoError(t, err)

		res, err := GetCertificatesFromDir(dir)
		require.NoError(t, err)
		assert.Len(t, res, 1)
	})

	t.Run("not_a_dir", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "not_a_dir_file")
		err := os.WriteFile(filePath, []byte("file"), 0644)
		require.NoError(t, err)
		res, err := GetCertificatesFromDir(filePath)
		require.Error(t, err)
		assert.Nil(t, res)
	})

	t.Run("with_subdir_branch", func(t *testing.T) {
		dir := filepath.Join(tempDir, "with_subdir_branch")
		err := os.MkdirAll(dir, 0750)
		require.NoError(t, err)
		err = os.MkdirAll(filepath.Join(dir, "subdir"), 0750)
		require.NoError(t, err)
		// This should trigger the f.IsDir() branch but still return error if no certs found
		res, err := GetCertificatesFromDir(dir)
		require.Error(t, err)
		assert.Empty(t, res)
	})
}

// mockPP is a mock implementation of Public Parameters.
type mockPP struct {
	Auditors []driver.Identity
	Issuers  []driver.Identity
}

// AddAuditor adds an auditor identity to the mock.
func (m *mockPP) AddAuditor(raw driver.Identity) {
	m.Auditors = append(m.Auditors, raw)
}

// AddIssuer adds an issuer identity to the mock.
func (m *mockPP) AddIssuer(raw driver.Identity) {
	m.Issuers = append(m.Issuers, raw)
}

// TestGetX509Identity tests the GetX509Identity function.
func TestGetX509Identity(t *testing.T) {
	tempDir := t.TempDir()
	certContent := generateTestCertificate(t)

	t.Run("success", func(t *testing.T) {
		dir := filepath.Join(tempDir, "success")
		err := os.MkdirAll(filepath.Join(dir, "signcerts"), 0750)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(dir, "signcerts", "cert1.crt"), certContent, 0644)
		require.NoError(t, err)

		id, err := GetX509Identity(dir)
		require.NoError(t, err)
		assert.NotNil(t, id)
	})

	t.Run("no_signcerts", func(t *testing.T) {
		dir := filepath.Join(tempDir, "no_signcerts")
		err := os.MkdirAll(dir, 0750)
		require.NoError(t, err)

		id, err := GetX509Identity(dir)
		require.Error(t, err)
		assert.Nil(t, id)
	})

	t.Run("empty_signcerts", func(t *testing.T) {
		dir := filepath.Join(tempDir, "empty_signcerts")
		err := os.MkdirAll(filepath.Join(dir, "signcerts"), 0750)
		require.NoError(t, err)

		id, err := GetX509Identity(dir)
		require.Error(t, err)
		assert.Nil(t, id)
	})

	t.Run("invalid_cert_in_signcerts", func(t *testing.T) {
		dir := filepath.Join(tempDir, "invalid_cert")
		err := os.MkdirAll(filepath.Join(dir, "signcerts"), 0750)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(dir, "signcerts", "invalid.crt"), []byte("invalid"), 0644)
		require.NoError(t, err)

		id, err := GetX509Identity(dir)
		require.Error(t, err)
		assert.Nil(t, id)
	})
}

// TestSetupIssuersAndAuditors tests the SetupIssuersAndAuditors function.
func TestSetupIssuersAndAuditors(t *testing.T) {
	tempDir := t.TempDir()
	certContent := generateTestCertificate(t)

	t.Run("success", func(t *testing.T) {
		auditorDir := filepath.Join(tempDir, "auditor")
		err := os.MkdirAll(filepath.Join(auditorDir, "signcerts"), 0750)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(auditorDir, "signcerts", "cert.crt"), certContent, 0644)
		require.NoError(t, err)

		issuerDir := filepath.Join(tempDir, "issuer")
		err = os.MkdirAll(filepath.Join(issuerDir, "signcerts"), 0750)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(issuerDir, "signcerts", "cert.crt"), certContent, 0644)
		require.NoError(t, err)

		pp := &mockPP{}
		err = SetupIssuersAndAuditors(pp, []string{auditorDir}, []string{issuerDir})
		require.NoError(t, err)
		assert.Len(t, pp.Auditors, 1)
		assert.Len(t, pp.Issuers, 1)
	})

	t.Run("fail_auditor", func(t *testing.T) {
		pp := &mockPP{}
		err := SetupIssuersAndAuditors(pp, []string{"not_found"}, []string{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get auditor identity")
	})

	t.Run("fail_issuer", func(t *testing.T) {
		pp := &mockPP{}
		err := SetupIssuersAndAuditors(pp, []string{}, []string{"not_found"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get issuer identity")
	})
}
