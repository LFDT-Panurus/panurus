/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"strings"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

func GetEnrollmentID(id []byte) (string, error) {
	cert, err := PemDecodeCert(id)
	if err != nil {
		return "", err
	}

	return EnrollmentIDFromCert(cert), nil
}

// EnrollmentIDFromCert returns the enrollment ID of cert: its CommonName up to the first '@'.
func EnrollmentIDFromCert(cert *x509.Certificate) string {
	cn := cert.Subject.CommonName
	// if cn contains a @, then return only the left part of the string
	index := strings.Index(cn, "@")
	if index != -1 {
		cn = cn[:index]
	}

	return cn
}

func GetRevocationHandle(id []byte) ([]byte, error) {
	cert, err := PemDecodeCert(id)
	if err != nil {
		return nil, err
	}

	return RevocationHandleFromCert(cert)
}

// RevocationHandleFromCert returns the revocation handle of cert:
// the base64 encoding of the SHA-256 hash of its PKIX public key.
func RevocationHandleFromCert(cert *x509.Certificate) ([]byte, error) {
	encoded, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return nil, errors.WithMessagef(err, "Failed to marshal PKI public key")
	}
	h := sha256.Sum256(encoded)

	return []byte(base64.StdEncoding.EncodeToString(h[:])), nil
}
