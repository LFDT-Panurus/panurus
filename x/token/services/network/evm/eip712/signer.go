/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package eip712

import (
	"bytes"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
)

const (
	// SignatureLength is the length in bytes of an endorsement signature: r (32) || s (32) || v (1).
	SignatureLength = 65
	// PrivateKeyLength is the length in bytes of a secp256k1 private-key scalar.
	PrivateKeyLength = 32

	// vOffset is the value added to the recovery id to obtain v, per the Ethereum convention.
	vOffset = 27
)

// secp256k1HalfN is secp256k1's group order divided by two, big-endian. Signatures whose s exceeds
// it are malleable (EIP-2) and are rejected by the EndorsementVerifier contract; the signer enforces
// the same bound so the two sides can never disagree.
var secp256k1HalfN = [32]byte{
	0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0x5d, 0x57, 0x6e, 0x73, 0x57, 0xa4, 0x50, 0x1d, 0xdf, 0xe9, 0x2f, 0x46, 0x68, 0x1b, 0x20, 0xa0,
}

// Signer produces the endorsement signatures the EndorsementVerifier contract accepts: 65-byte
// {r,s,v} with v in {27,28} and low-s, over an EIP-712 digest. It wraps a secp256k1 private key
// (decred implementation; go-ethereum must not be linked, design §15.6).
//
// Byte-format notes (design §8): decred's SignCompact returns the recovery byte FIRST ({v,r,s}),
// so Sign reorders to the Ethereum wire format {r,s,v}; signing always uses the uncompressed-pubkey
// recovery convention so v stays in {27,28}; and dcrd signatures are canonical (low-s) by
// construction, but Sign asserts it anyway so a library change cannot silently reintroduce
// malleable signatures.
type Signer struct {
	key     *secp256k1.PrivateKey
	address client.Address
}

// NewSigner returns a Signer over the given private key.
func NewSigner(key *secp256k1.PrivateKey) *Signer {
	return &Signer{key: key, address: PubKeyToAddress(key.PubKey())}
}

// NewSignerFromBytes returns a Signer over a raw 32-byte private-key scalar. It rejects scalars of
// the wrong length and the zero scalar (whose public key is the point at infinity).
func NewSignerFromBytes(raw []byte) (*Signer, error) {
	if len(raw) != PrivateKeyLength {
		return nil, errors.Errorf("invalid private key length: expected %d bytes, got %d", PrivateKeyLength, len(raw))
	}
	key := secp256k1.PrivKeyFromBytes(raw)
	if key.Key.IsZero() {
		return nil, errors.New("invalid private key: zero scalar")
	}

	return NewSigner(key), nil
}

// Address returns the Ethereum address of the signer, as registered in the EndorsementVerifier.
func (s *Signer) Address() client.Address {
	return s.address
}

// Sign signs an EIP-712 digest and returns the 65-byte {r,s,v} signature the contract verifies.
func (s *Signer) Sign(digest [32]byte) ([]byte, error) {
	// SignCompact returns {v, r, s} with v = 27 + recovery id (+4 if the compressed-pubkey
	// convention were requested; it never is here, keeping v in {27,28}).
	compact := ecdsa.SignCompact(s.key, digest[:], false)
	if len(compact) != SignatureLength {
		return nil, errors.Errorf("unexpected compact signature length %d", len(compact))
	}

	sig := make([]byte, SignatureLength)
	copy(sig[:64], compact[1:])
	sig[64] = compact[0]

	if err := checkSignatureFormat(sig); err != nil {
		return nil, errors.Wrapf(err, "signing produced a non-canonical signature")
	}

	return sig, nil
}

// PubKeyToAddress derives the Ethereum address of a secp256k1 public key:
// keccak256 of the 64-byte uncompressed key X || Y (the leading 0x04 byte stripped), low 20 bytes.
func PubKeyToAddress(pub *secp256k1.PublicKey) client.Address {
	uncompressed := pub.SerializeUncompressed() // 65 bytes: 0x04 || X || Y
	digest := crypto.Keccak256(uncompressed[1:])

	return client.BytesToAddress(digest[12:])
}

// RecoverAddress recovers the signer address of a 65-byte {r,s,v} signature over digest. It
// enforces the same format rules as the EndorsementVerifier contract (length, v in {27,28}, low-s),
// so an endorsement accepted here is accepted on-chain and vice versa.
func RecoverAddress(digest [32]byte, sig []byte) (client.Address, error) {
	if err := checkSignatureFormat(sig); err != nil {
		return client.Address{}, err
	}

	// RecoverCompact expects decred's {v, r, s} layout.
	compact := make([]byte, SignatureLength)
	compact[0] = sig[64]
	copy(compact[1:], sig[:64])

	pub, _, err := ecdsa.RecoverCompact(compact, digest[:])
	if err != nil {
		return client.Address{}, errors.Wrapf(err, "failed to recover public key")
	}

	return PubKeyToAddress(pub), nil
}

// checkSignatureFormat enforces the contract's signature rules: 65 bytes, v in {27,28}, low-s.
func checkSignatureFormat(sig []byte) error {
	if len(sig) != SignatureLength {
		return errors.Errorf("invalid signature length: expected %d bytes, got %d", SignatureLength, len(sig))
	}
	if v := sig[64]; v != vOffset && v != vOffset+1 {
		return errors.Errorf("invalid signature v: expected 27 or 28, got %d", sig[64])
	}
	if bytes.Compare(sig[32:64], secp256k1HalfN[:]) > 0 {
		return errors.New("invalid signature: s is in the upper half of the group order (malleable)")
	}

	return nil
}
