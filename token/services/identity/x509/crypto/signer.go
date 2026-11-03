/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"crypto"
	"io"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger/fabric-lib-go/bccsp"
)

// SKIBasedSigner implements a crypto.Signer based on the bccsp.
// The private key is resolved once, at construction.
type SKIBasedSigner struct {
	csp bccsp.BCCSP
	SKI []byte
	pk  crypto.PublicKey
	key bccsp.Key
}

// NewSKIBasedSigner returns a new SKIBasedSigner for the key the BCCSP holds under ski.
func NewSKIBasedSigner(csp bccsp.BCCSP, ski []byte, pk crypto.PublicKey) (crypto.Signer, error) {
	if err := checkSignerArgs(csp, ski, pk); err != nil {
		return nil, err
	}
	key, err := csp.GetKey(ski)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to retrieve key for SKI [%s]", ski)
	}

	return newSKIBasedSigner(csp, ski, pk, key)
}

func newSKIBasedSigner(csp bccsp.BCCSP, ski []byte, pk crypto.PublicKey, key bccsp.Key) (*SKIBasedSigner, error) {
	if err := checkSignerArgs(csp, ski, pk); err != nil {
		return nil, err
	}
	if key == nil {
		return nil, errors.New("key is nil")
	}

	return &SKIBasedSigner{csp: csp, SKI: ski, pk: pk, key: key}, nil
}

func checkSignerArgs(csp bccsp.BCCSP, ski []byte, pk crypto.PublicKey) error {
	if csp == nil {
		return errors.New("bccsp instance must be different from nil")
	}
	if len(ski) == 0 {
		return errors.New("SKI is empty")
	}
	if pk == nil {
		return errors.New("PK is nil")
	}

	return nil
}

// Public returns the public key corresponding to the opaque,
// private key.
func (s *SKIBasedSigner) Public() crypto.PublicKey {
	return s.pk
}

// Sign signs digest with the private key, possibly using entropy from rand.
// For an (EC)DSA key, it should be a DER-serialised, ASN.1 signature
// structure.
//
// Hash implements the SignerOpts interface and, in most cases, one can
// simply pass in the hash function used as opts. Sign may also attempt
// to type assert opts to other types in order to obtain algorithm
// specific values. See the documentation in each package for details.
//
// Note that when a signature of a hash of a larger message is needed,
// the caller is responsible for hashing the larger message and passing
// the hash (as digest) and the hash function (as opts) to Sign.
func (s *SKIBasedSigner) Sign(rand io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	return s.csp.Sign(s.key, digest, opts)
}
