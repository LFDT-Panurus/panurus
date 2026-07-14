/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"crypto/rand"
	"crypto/rsa"

	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	fscPostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
)

type mockConfig struct {
	values map[string]any
}

func (m *mockConfig) IsSet(key string) bool {
	_, ok := m.values[key]

	return ok
}

func (m *mockConfig) UnmarshalKey(key string, rawVal any) error {
	val, ok := m.values[key]
	if !ok {
		return nil
	}

	return mapstructure.Decode(val, rawVal)
}

type mockConfigProvider struct {
	opts *fscPostgres.Config
}

func (m *mockConfigProvider) GetOpts(name driver2.PersistenceName, params ...string) (*fscPostgres.Config, error) {
	return m.opts, nil
}

func generateSelfSignedCert(t *testing.T, tempDir string) (string, string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test Org"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	derBytes, err := x509.CreateCertificate(nil, &template, &template, privateKey.Public(), privateKey)
	require.NoError(t, err)

	certPath := filepath.Join(tempDir, "cert.pem")
	certOut, err := os.Create(certPath)
	require.NoError(t, err)
	defer func() { _ = certOut.Close() }()
	err = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	require.NoError(t, err)

	keyPath := filepath.Join(tempDir, "key.pem")
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	require.NoError(t, err)
	defer func() { _ = keyOut.Close() }()

	privBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	err = pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
	require.NoError(t, err)

	return certPath, keyPath
}

func TestRegisterTLSConnection(t *testing.T) { //nolint:paralleltest
	tempDir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, tempDir)

	tests := []struct {
		name       string
		dataSource string
		tlsCfg     TLSConfig
		verify     func(t *testing.T, connConfig *pgx.ConnConfig, err error)
	}{
		{
			name:       "SSLMode disable",
			dataSource: "host=localhost port=5432 user=postgres dbname=test",
			tlsCfg: TLSConfig{
				SSLMode: "disable",
			},
			verify: func(t *testing.T, connConfig *pgx.ConnConfig, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.Nil(t, connConfig.TLSConfig)
			},
		},
		{
			name:       "SSLMode require",
			dataSource: "postgres://postgres:password@localhost:5432/test", //nolint:gosec // mock data
			tlsCfg: TLSConfig{
				SSLMode: "require",
			},
			verify: func(t *testing.T, connConfig *pgx.ConnConfig, err error) {
				t.Helper()
				require.NoError(t, err)
				require.NotNil(t, connConfig.TLSConfig)
				assert.False(t, connConfig.TLSConfig.InsecureSkipVerify)
				assert.Equal(t, "localhost", connConfig.TLSConfig.ServerName)
			},
		},
		{
			name:       "SSLMode verify-full with server name override",
			dataSource: "host=127.0.0.1 port=5432 user=postgres dbname=test",
			tlsCfg: TLSConfig{
				SSLMode:    "verify-full",
				ServerName: "custom.domain",
			},
			verify: func(t *testing.T, connConfig *pgx.ConnConfig, err error) {
				t.Helper()
				require.NoError(t, err)
				require.NotNil(t, connConfig.TLSConfig)
				assert.False(t, connConfig.TLSConfig.InsecureSkipVerify)
				assert.Equal(t, "custom.domain", connConfig.TLSConfig.ServerName)
			},
		},
		{
			name:       "SSLMode verify-ca with Root CA and Client Certs",
			dataSource: "host=localhost port=5432 user=postgres dbname=test",
			tlsCfg: TLSConfig{
				SSLMode:      "verify-ca",
				RootCertPath: certPath,
				CertPath:     certPath,
				KeyPath:      keyPath,
			},
			verify: func(t *testing.T, connConfig *pgx.ConnConfig, err error) {
				t.Helper()
				require.NoError(t, err)
				require.NotNil(t, connConfig.TLSConfig)
				assert.False(t, connConfig.TLSConfig.InsecureSkipVerify)
				assert.NotNil(t, connConfig.TLSConfig.RootCAs)
				assert.Len(t, connConfig.TLSConfig.Certificates, 1)
				assert.NotNil(t, connConfig.TLSConfig.VerifyConnection)

				// Test VerifyConnection callback
				cs := tls.ConnectionState{
					PeerCertificates: []*x509.Certificate{},
				}
				err = connConfig.TLSConfig.VerifyConnection(cs)
				assert.ErrorContains(t, err, "no peer certificates presented")
			},
		},
		{
			name:       "Invalid ssl mode",
			dataSource: "host=localhost port=5432 user=postgres dbname=test",
			tlsCfg: TLSConfig{
				SSLMode: "invalid-mode",
			},
			verify: func(t *testing.T, connConfig *pgx.ConnConfig, err error) {
				t.Helper()
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unsupported ssl mode")
			},
		},
		{
			name:       "Invalid certificate path",
			dataSource: "host=localhost port=5432 user=postgres dbname=test",
			tlsCfg: TLSConfig{
				SSLMode:      "verify-full",
				RootCertPath: filepath.Join(tempDir, "nonexistent.pem"),
			},
			verify: func(t *testing.T, connConfig *pgx.ConnConfig, err error) {
				t.Helper()
				require.Error(t, err)
				assert.Contains(t, err.Error(), "failed to read root certificate")
			},
		},
		{
			name:       "Invalid client key path",
			dataSource: "host=localhost port=5432 user=postgres dbname=test",
			tlsCfg: TLSConfig{
				SSLMode:  "verify-full",
				CertPath: certPath,
				KeyPath:  filepath.Join(tempDir, "nonexistent.pem"),
			},
			verify: func(t *testing.T, connConfig *pgx.ConnConfig, err error) {
				t.Helper()
				require.Error(t, err)
				assert.Contains(t, err.Error(), "failed to load client key pair")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { //nolint:paralleltest
			connConfig, err := createTLSConnConfig(tt.dataSource, tt.tlsCfg)
			tt.verify(t, connConfig, err)
		})
	}
}

func TestTLSConfigProvider(t *testing.T) { //nolint:paralleltest
	tempDir := t.TempDir()
	certPath, _ := generateSelfSignedCert(t, tempDir)

	mockCfg := &mockConfig{
		values: map[string]any{
			"fsc.persistences.db.opts.tls": map[string]any{
				"enabled":        true,
				"ssl_mode":       "require",
				"root_cert_path": certPath,
			},
			"fsc.persistences.default.opts.tls": map[string]any{
				"enabled":  true,
				"ssl_mode": "verify-full",
			},
		},
	}

	t.Run("Persistence specific TLS config", func(t *testing.T) { //nolint:paralleltest
		baseOpts := &fscPostgres.Config{
			DataSource: "host=localhost port=5432 dbname=test",
		}
		provider := &tlsConfigProvider{
			wrapped: &mockConfigProvider{opts: baseOpts},
			config:  mockCfg,
		}

		opts, err := provider.GetOpts("db")
		require.NoError(t, err)
		assert.Contains(t, opts.DataSource, "registeredConnConfig")

		stdlib.UnregisterConnConfig(opts.DataSource)
	})

	t.Run("Fallback to default TLS config", func(t *testing.T) { //nolint:paralleltest
		baseOpts := &fscPostgres.Config{
			DataSource: "host=localhost port=5432 dbname=test",
		}
		provider := &tlsConfigProvider{
			wrapped: &mockConfigProvider{opts: baseOpts},
			config:  mockCfg,
		}

		opts, err := provider.GetOpts("other")
		require.NoError(t, err)
		assert.Contains(t, opts.DataSource, "registeredConnConfig")

		stdlib.UnregisterConnConfig(opts.DataSource)
	})
}
