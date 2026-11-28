/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabtokenv1

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"time"

	idx509 "github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// OwnerCACertFileName is the PEM file, in the directory returned by Setup, holding the owner CA certificate.
	OwnerCACertFileName = "ca-cert.pem"

	ownerCADirName     = "owner-ca"
	ownerCAKeyFileName = "priv_sk"
	certValidity       = 10 * 365 * 24 * time.Hour
)

// ownerCA is an owner CA loaded from disk.
type ownerCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

// setupOwnerCA creates the owner CA in dir, or keeps the one already there.
// The CA is written to a temporary directory and renamed, so concurrent callers agree on one CA.
func setupOwnerCA(dir, cn string) error {
	if _, err := os.Stat(filepath.Join(dir, OwnerCACertFileName)); err == nil {
		_, err := loadOwnerCA(dir)

		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0750); err != nil {
		return errors.Wrapf(err, "failed to create [%s]", filepath.Dir(dir))
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".owner-ca-*")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary owner CA dir")
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := writeOwnerCA(tmp, cn); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		// another caller won the race: use its CA
		if _, statErr := os.Stat(filepath.Join(dir, OwnerCACertFileName)); statErr != nil {
			return errors.Wrapf(err, "failed to move owner CA to [%s]", dir)
		}
	}
	_, err = loadOwnerCA(dir)

	return err
}

// writeOwnerCA writes a new self-signed P-256 owner CA to dir.
func writeOwnerCA(dir, cn string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return errors.Wrap(err, "failed to generate owner CA key")
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return errors.Wrap(err, "failed to create owner CA certificate")
	}

	return writeCertAndKey(filepath.Join(dir, OwnerCACertFileName), filepath.Join(dir, ownerCAKeyFileName), der, key)
}

// loadOwnerCA reads the owner CA in dir and checks that its key matches its certificate.
func loadOwnerCA(dir string) (*ownerCA, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, OwnerCACertFileName))
	if err != nil {
		return nil, errors.Wrap(err, "failed to read owner CA certificate")
	}
	if _, err := crypto.NewOwnerCAs([][]byte{certPEM}, nil); err != nil {
		return nil, errors.WithMessagef(err, "invalid owner CA certificate in [%s]", dir)
	}
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse owner CA certificate")
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, ownerCAKeyFileName))
	if err != nil {
		return nil, errors.Wrap(err, "failed to read owner CA key")
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, errors.Errorf("no PEM block in owner CA key in [%s]", dir)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse owner CA key")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || !key.PublicKey.Equal(cert.PublicKey) {
		return nil, errors.Errorf("owner CA key in [%s] does not match its certificate", dir)
	}

	return &ownerCA{cert: cert, key: key, pem: certPEM}, nil
}

// issueOwnerMSP writes to mspDir an owner MSP with a fresh P-256 key and a certificate for cn signed by the
// owner CA in caDir. The layout matches cryptogen: signcerts/<cn>-cert.pem, keystore/priv_sk (PKCS8)
// and cacerts/<caFileName>.
func issueOwnerMSP(caDir, mspDir, cn, caFileName string) error {
	ca, err := loadOwnerCA(caDir)
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return errors.Wrap(err, "failed to generate owner key")
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return errors.Wrapf(err, "failed to create owner certificate [%s]", cn)
	}
	// start from an empty MSP, as a fresh cryptogen run would
	if err := os.RemoveAll(mspDir); err != nil {
		return errors.Wrapf(err, "failed to clean [%s]", mspDir)
	}
	for _, d := range []string{crypto.SignCertsDirName, idx509.KeystoreFolder, "cacerts"} {
		if err := os.MkdirAll(filepath.Join(mspDir, d), 0750); err != nil {
			return errors.Wrapf(err, "failed to create [%s]", d)
		}
	}
	//nolint:gosec // test network paths
	if err := os.WriteFile(filepath.Join(mspDir, "cacerts", caFileName), ca.pem, 0600); err != nil {
		return errors.Wrap(err, "failed to write owner CA certificate")
	}

	return writeCertAndKey(
		filepath.Join(mspDir, crypto.SignCertsDirName, cn+"-cert.pem"),
		filepath.Join(mspDir, idx509.KeystoreFolder, idx509.PrivateKeyFileName),
		der,
		key,
	)
}

// writeCertAndKey writes der as a PEM certificate and key as a PKCS8 PEM private key.
func writeCertAndKey(certFile, keyFile string, der []byte, key *ecdsa.PrivateKey) error {
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return errors.Wrap(err, "failed to marshal private key")
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		return errors.Wrapf(err, "failed to write [%s]", certFile)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		return errors.Wrapf(err, "failed to write [%s]", keyFile)
	}

	return nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate serial number")
	}

	return serial, nil
}
