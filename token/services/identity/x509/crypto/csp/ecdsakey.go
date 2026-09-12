/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger/fabric-lib-go/bccsp"
)

type ecdsaPrivateKey struct {
	privKey *ecdsa.PrivateKey
}

// Bytes converts this key to its byte representation,
// if this operation is allowed.
func (k *ecdsaPrivateKey) Bytes() ([]byte, error) {
	return nil, errors.New("not supported")
}

// SKI returns the subject key identifier of this key.
func (k *ecdsaPrivateKey) SKI() []byte {
	if k.privKey == nil {
		return nil
	}

	// Marshall the public key
	ecdh, err := k.privKey.PublicKey.ECDH()
	if err != nil {
		return nil
	}
	raw := ecdh.Bytes()

	// Hash it
	hash := sha256.New()
	_, _ = hash.Write(raw)

	return hash.Sum(nil)
}

// Symmetric returns true if this key is a symmetric key,
// false if this key is asymmetric
func (k *ecdsaPrivateKey) Symmetric() bool {
	return false
}

// Private returns true if this key is a private key,
// false otherwise.
func (k *ecdsaPrivateKey) Private() bool {
	return true
}

// PublicKey returns the corresponding public key part of an asymmetric public/private key pair.
// This method returns an error in symmetric key schemes.
func (k *ecdsaPrivateKey) PublicKey() (bccsp.Key, error) {
	return &ecdsaPublicKey{&k.privKey.PublicKey}, nil
}

// marshall returns a PEM encoded version of the private key
func (k *ecdsaPrivateKey) marshall() ([]byte, error) {
	derBytes, err := x509.MarshalECPrivateKey(k.privKey)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to marshal private key [%s]", k.privKey.PublicKey)
	}

	// Encode the DER bytes into PEM format
	pemBlock := &pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: derBytes,
	}

	return pem.EncodeToMemory(pemBlock), nil
}

// unmarshall decodes a PEM version of an ECDSA private key
func (k *ecdsaPrivateKey) unmarshall(raw []byte) error {
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return errors.Errorf("failed to decode PEM block containing EC private key")
	}
	var err error
	k.privKey, err = x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return errors.Wrap(err, "failed to unmarshal private key")
	}

	return nil
}

type ecdsaPublicKey struct {
	pubKey *ecdsa.PublicKey
}

// Bytes converts this key to its byte representation,
// if this operation is allowed.
func (k *ecdsaPublicKey) Bytes() (raw []byte, err error) {
	raw, err = x509.MarshalPKIXPublicKey(k.pubKey)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling key")
	}

	return
}

// SKI returns the subject key identifier of this key.
func (k *ecdsaPublicKey) SKI() []byte {
	if k.pubKey == nil {
		return nil
	}

	// Marshall the public key
	ecdh, err := k.pubKey.ECDH()
	if err != nil {
		return nil
	}
	raw := ecdh.Bytes()

	// Hash it
	hash := sha256.New()
	_, _ = hash.Write(raw)

	return hash.Sum(nil)
}

// Symmetric returns true if this key is a symmetric key,
// false if this key is asymmetric
func (k *ecdsaPublicKey) Symmetric() bool {
	return false
}

// Private returns true if this key is a private key,
// false otherwise.
func (k *ecdsaPublicKey) Private() bool {
	return false
}

// PublicKey returns the corresponding public key part of an asymmetric public/private key pair.
// This method returns an error in symmetric key schemes.
func (k *ecdsaPublicKey) PublicKey() (bccsp.Key, error) {
	return k, nil
}

// marshall returns a PEM encoded version of the public key
func (k *ecdsaPublicKey) marshall() ([]byte, error) {
	raw, err := x509.MarshalPKIXPublicKey(k.pubKey)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling key")
	}
	pemBlock := &pem.Block{
		Type:  "EC PUBLIC KEY",
		Bytes: raw,
	}

	return pem.EncodeToMemory(pemBlock), nil
}

// unmarshall decodes a PEM version of an ECDSA public key
func (k *ecdsaPublicKey) unmarshall(raw []byte) error {
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "EC PUBLIC KEY" {
		return errors.Errorf("failed to decode PEM block containing EC public key")
	}
	var err error
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return errors.Wrapf(err, "failed to parse public key")
	}

	// Type assert to *ecdsa.PublicKey
	publicKey, ok := key.(*ecdsa.PublicKey)
	if !ok {
		return errors.Errorf("key is not of type *ecdsa.PublicKey")
	}
	k.pubKey = publicKey

	return nil
}
