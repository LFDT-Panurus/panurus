/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// generateTestCertWithCNAndKey is the base helper that generates a test certificate with a given CommonName
// and returns both the PEM-encoded certificate and the matching private key.
func generateTestCertWithCNAndKey(t *testing.T, cn string) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: cn,
		},
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(time.Hour),
		KeyUsage:  x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	return certPEM, priv
}

// GenerateTestCertWithCN generates a test certificate with a given CommonName.
// Returns only the PEM-encoded certificate (without the private key).
// This is a convenience wrapper for tests that only need the certificate.
func GenerateTestCertWithCN(t *testing.T, cn string) []byte {
	t.Helper()
	certPEM, _ := generateTestCertWithCNAndKey(t, cn)

	return certPEM
}

// generateTestCert generates a test certificate with a default CommonName and returns both
// the PEM-encoded certificate and the matching private key.
// This is a convenience wrapper for tests that need both the certificate and key.
func GenerateTestCert(t *testing.T) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()

	return generateTestCertWithCNAndKey(t, "test.example.com")
}

// TestCertificate is a test certificate with its key and PEM encoding.
type TestCertificate struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	PEM  []byte
}

// NewTestCATemplate returns a CA certificate template with the given CommonName.
func NewTestCATemplate(cn string) *x509.Certificate {
	return &x509.Certificate{
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
}

// NewTestLeafTemplate returns a non-CA certificate template with the given CommonName.
func NewTestLeafTemplate(cn string) *x509.Certificate {
	return &x509.Certificate{
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
}

// NewTestCertificate creates a certificate from template with a fresh P-256 key and a random serial.
// It is signed by parent, or self-signed if parent is nil.
func NewTestCertificate(tb testing.TB, template *x509.Certificate, parent *TestCertificate) *TestCertificate {
	tb.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(tb, err)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	require.NoError(tb, err)
	tmpl := *template
	tmpl.SerialNumber = serial

	issuer, signer := &tmpl, priv
	if parent != nil {
		issuer, signer = parent.Cert, parent.Key
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, issuer, &priv.PublicKey, signer)
	require.NoError(tb, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(tb, err)

	return &TestCertificate{
		Cert: cert,
		Key:  priv,
		PEM:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}
