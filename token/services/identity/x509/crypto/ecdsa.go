/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"math/big"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var (
	// curveHalfOrders contains the precomputed curve group orders halved.
	// It is used to ensure that signature' S value is lower or equal to the
	// curve group order halved. We accept only low-S signatures.
	// They are precomputed for efficiency reasons.
	curveHalfOrders = map[elliptic.Curve]*big.Int{
		elliptic.P224(): new(big.Int).Rsh(elliptic.P224().Params().N, 1),
		elliptic.P256(): new(big.Int).Rsh(elliptic.P256().Params().N, 1),
		elliptic.P384(): new(big.Int).Rsh(elliptic.P384().Params().N, 1),
		elliptic.P521(): new(big.Int).Rsh(elliptic.P521().Params().N, 1),
	}
)

type ECDSASignature struct {
	R, S *big.Int
}

type ecdsaVerifier struct {
	pk *ecdsa.PublicKey
}

func NewECDSAVerifier(pk *ecdsa.PublicKey) *ecdsaVerifier {
	return &ecdsaVerifier{pk: pk}
}

func (d *ecdsaVerifier) Verify(message, sigma []byte) error {
	signature := &ECDSASignature{}
	_, err := asn1.Unmarshal(sigma, signature)
	if err != nil {
		return err
	}

	digest := sha256.Sum256(message)

	lowS, err := IsLowS(d.pk, signature.S)
	if err != nil {
		return err
	}
	if !lowS {
		return errors.New("signature is not in lowS")
	}

	valid := ecdsa.Verify(d.pk, digest[:], signature.R, signature.S)
	if !valid {
		return errors.Errorf("signature not valid")
	}

	return nil
}

type ecdsaSigner struct {
	sk *ecdsa.PrivateKey
}

func NewEcdsaSigner(sk *ecdsa.PrivateKey) *ecdsaSigner {
	return &ecdsaSigner{sk: sk}
}

func (d *ecdsaSigner) Sign(message []byte) ([]byte, error) {
	dgst := sha256.Sum256(message)

	r, s, err := ecdsa.Sign(rand.Reader, d.sk, dgst[:])
	if err != nil {
		return nil, err
	}

	s, _, err = ToLowS(&d.sk.PublicKey, s)
	if err != nil {
		return nil, err
	}

	return asn1.Marshal(ECDSASignature{R: r, S: s})
}

// IsLowS checks that s is a low-S
func IsLowS(k *ecdsa.PublicKey, s *big.Int) (bool, error) {
	halfOrder, ok := curveHalfOrders[k.Curve]
	if !ok {
		return false, errors.Errorf("curve not recognized [%s]", k.Curve)
	}

	return s.Cmp(halfOrder) != 1, nil
}

func ToLowS(k *ecdsa.PublicKey, s *big.Int) (*big.Int, bool, error) {
	lowS, err := IsLowS(k, s)
	if err != nil {
		return nil, false, err
	}

	if !lowS {
		// Set s to N - s that will be then in the lower part of signature space
		// less or equal to half order
		s.Sub(k.Params().N, s)

		return s, true, nil
	}

	return s, false, nil
}

// PemDecodeKey takes bytes and returns a Go key
func PemDecodeKey(keyBytes []byte) (any, error) {
	block, _ := pem.Decode(keyBytes)
	if block == nil {
		return nil, errors.New("bytes are not PEM encoded")
	}

	var key any
	var err error
	switch block.Type {
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.WithMessagef(err, "pem bytes are not PKCS8 encoded ")
		}
	case "CERTIFICATE":
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.WithMessagef(err, "pem bytes are not cert encoded ")
		}

		return cert.PublicKey, nil
	case "PUBLIC KEY":
		key, err = x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, errors.WithMessagef(err, "pem bytes are not PKIX encoded ")
		}
	default:
		return nil, errors.Errorf("bad key type %s", block.Type)
	}

	return key, nil
}
