/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"

	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger/fabric-lib-go/bccsp"
)

type verifyingIdentity struct {
	bccsp               bccsp.BCCSP
	SignatureHashFamily string
	cert                *x509.Certificate
	pk                  bccsp.Key
}

func (f *verifyingIdentity) Serialize() ([]byte, error) {
	pb := &pem.Block{Bytes: f.cert.Raw, Type: "CERTIFICATE"}
	pemBytes := pem.EncodeToMemory(pb)
	if pemBytes == nil {
		return nil, errors.New("encoding of identity failed")
	}

	return pemBytes, nil
}

func (f *verifyingIdentity) Verify(message, sigma []byte) error {
	hashOpt, err := getHashOpt(f.SignatureHashFamily)
	if err != nil {
		return errors.WithMessagef(err, "failed getting hash function options")
	}

	digest, err := f.bccsp.Hash(message, hashOpt)
	if err != nil {
		return errors.WithMessagef(err, "failed computing digest")
	}

	valid, err := f.bccsp.Verify(f.pk, sigma, digest, nil)
	if err != nil {
		return errors.WithMessagef(err, "could not determine the validity of the signature")
	} else if !valid {
		return errors.New("signature is invalid")
	}

	return nil
}

type fullIdentity struct {
	*verifyingIdentity
	signer crypto.Signer
}

func (f *fullIdentity) Sign(msg []byte) ([]byte, error) {
	// Compute Hash
	hashOpt, err := getHashOpt(f.SignatureHashFamily)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting hash function options")
	}

	digest, err := f.bccsp.Hash(msg, hashOpt)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed computing digest")
	}

	// Sign
	return f.signer.Sign(rand.Reader, digest, nil)
}

func (f *fullIdentity) Serialize() ([]byte, error) {
	pb := &pem.Block{Bytes: f.cert.Raw, Type: "CERTIFICATE"}
	pemBytes := pem.EncodeToMemory(pb)
	if pemBytes == nil {
		return nil, errors.New("encoding of identity failed")
	}

	return pemBytes, nil
}

func (f *fullIdentity) Verify(message, sigma []byte) error {
	hashOpt, err := getHashOpt(f.SignatureHashFamily)
	if err != nil {
		return errors.WithMessagef(err, "failed getting hash function options")
	}

	digest, err := f.bccsp.Hash(message, hashOpt)
	if err != nil {
		return errors.WithMessagef(err, "failed computing digest")
	}

	valid, err := f.bccsp.Verify(f.pk, sigma, digest, nil)
	if err != nil {
		return errors.WithMessagef(err, "could not determine the validity of the signature")
	} else if !valid {
		return errors.New("signature is invalid")
	}

	return nil
}

type IdentityFactory struct {
	bccsp               bccsp.BCCSP
	SignatureHashFamily string
}

func NewIdentityFactory(bccsp bccsp.BCCSP, signatureHashFamily string) *IdentityFactory {
	return &IdentityFactory{bccsp: bccsp, SignatureHashFamily: signatureHashFamily}
}

func (f *IdentityFactory) GetFullIdentity(sidInfo *SigningIdentityInfo) (*fullIdentity, error) {
	if sidInfo == nil {
		return nil, errors.New("nil signing identity info")
	}

	// Extract the public part of the identity
	idPub, pubKey, cryptoPK, err := f.getIdentityFromConf(sidInfo.PublicSigner)
	if err != nil {
		return nil, err
	}

	// Find the matching private key in the BCCSP keystore
	_, err = f.bccsp.GetKey(pubKey.SKI())
	// Less Secure: Attempt to import Private Key from KeyInfo, if BCCSP was not able to find the key
	if err != nil {
		if sidInfo.PrivateSigner == nil || sidInfo.PrivateSigner.KeyMaterial == nil {
			return nil, errors.New("key material not found in SigningIdentityInfo")
		}
		pemKey, _ := pem.Decode(sidInfo.PrivateSigner.KeyMaterial)
		if pemKey == nil {
			return nil, errors.Errorf("%s: wrong PEM encoding", sidInfo.PrivateSigner.KeyIdentifier)
		}
		_, err = f.bccsp.KeyImport(pemKey.Bytes, &bccsp.ECDSAPrivateKeyImportOpts{})
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to import EC private key")
		}
	}

	// get the peer signer
	identitySigner, err := NewSKIBasedSigner(f.bccsp, pubKey.SKI(), cryptoPK)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create identity signer")
	}

	return &fullIdentity{
		verifyingIdentity: &verifyingIdentity{
			bccsp:               f.bccsp,
			SignatureHashFamily: f.SignatureHashFamily,
			cert:                idPub,
			pk:                  pubKey,
		},
		signer: identitySigner,
	}, nil
}

func (f *IdentityFactory) DeserializeFullIdentity(raw []byte) (*fullIdentity, error) {
	if len(raw) == 0 {
		return nil, errors.New("nil identity")
	}

	// Extract the public part of the identity
	idPub, pubKey, cryptoPK, err := f.getIdentityFromConf(raw)
	if err != nil {
		return nil, err
	}

	// Find the matching private key in the BCCSP keystore
	_, err = f.bccsp.GetKey(pubKey.SKI())
	if err != nil {
		return nil, err
	}

	// get the peer signer
	identitySigner, err := NewSKIBasedSigner(f.bccsp, pubKey.SKI(), cryptoPK)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create identity signer")
	}

	return &fullIdentity{
		verifyingIdentity: &verifyingIdentity{
			bccsp:               f.bccsp,
			SignatureHashFamily: f.SignatureHashFamily,
			cert:                idPub,
			pk:                  pubKey,
		},
		signer: identitySigner,
	}, nil
}

func (f *IdentityFactory) GetIdentity(sidInfo *SigningIdentityInfo) (*verifyingIdentity, error) {
	if sidInfo == nil {
		return nil, errors.New("nil signing identity info")
	}

	// Extract the public part of the identity
	idPub, pubKey, _, err := f.getIdentityFromConf(sidInfo.PublicSigner)
	if err != nil {
		return nil, errors.New("failed getting identity from config")
	}

	return &verifyingIdentity{
		bccsp:               f.bccsp,
		SignatureHashFamily: f.SignatureHashFamily,
		cert:                idPub,
		pk:                  pubKey,
	}, nil
}

func (f *IdentityFactory) getIdentityFromConf(idBytes []byte) (*x509.Certificate, bccsp.Key, crypto.PublicKey, error) {
	// get a cert
	cert, err := f.getCertFromPem(idBytes)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed getting certificate")
	}

	// get the public key in the right format
	certPubK, err := f.bccsp.KeyImport(cert, &bccsp.X509PublicKeyImportOpts{Temporary: true})
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to import certificate")
	}

	cryptoPK, ok := cert.PublicKey.(crypto.PublicKey)
	if !ok {
		return nil, nil, nil, errors.Errorf("certificate public key is not a cryptographic public key")
	}

	return cert, certPubK, cryptoPK, nil
}

func (f *IdentityFactory) getCertFromPem(idBytes []byte) (*x509.Certificate, error) {
	if idBytes == nil {
		return nil, errors.New("nil id")
	}

	// Decode the pem bytes
	pemCert, _ := pem.Decode(idBytes)
	if pemCert == nil {
		return nil, errors.Errorf("could not decode pem bytes [%v]", utils.Hashable(idBytes))
	}

	// get a cert
	var cert *x509.Certificate
	cert, err := x509.ParseCertificate(pemCert.Bytes)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse x509 cert")
	}

	return cert, nil
}

func getHashOpt(hashFamily string) (bccsp.HashOpts, error) {
	switch hashFamily {
	case bccsp.SHA2:
		return bccsp.GetHashOpt(bccsp.SHA256)
	case bccsp.SHA3:
		return bccsp.GetHashOpt(bccsp.SHA3_256)
	}

	return nil, errors.Errorf("hash family not recognized [%s]", hashFamily)
}
