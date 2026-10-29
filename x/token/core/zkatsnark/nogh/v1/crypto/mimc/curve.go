/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package mimc

import (
	"crypto/sha256"

	"github.com/consensys/gnark-crypto/ecc"
	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	mimcbn254 "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/mimc/bn254"
)

// tokenTypeDomain separates the token-type encoding from every other use of
// SHA-256 in the protocol.
const tokenTypeDomain = "zkatsnark/token-type/v1" //nolint:gosec // G101: domain separation tag, not a credential

// TokenTypeEncodingLen is the byte length of the token-type encoding: 248 bits,
// strictly below the scalar-field modulus of every supported curve (BN254 and
// BLS12-381 both have moduli above 2^253), so the encoding is always canonical
// and never reduced.
const TokenTypeEncodingLen = 31

// TokenTypeBytes maps a token type string to the TokenTypeEncodingLen-byte
// big-endian integer that represents it in note and type commitments:
// SHA-256(tokenTypeDomain || tokenType) truncated to its first 31 bytes.
//
// The previous encoding, SetBytes([]byte(tokenType)), reduced mod r and dropped
// leading zero bytes, so distinct types ("\x00USD" and "USD", or any two types
// congruent mod r) shared a commitment. Hashing makes the map injective up to
// SHA-256 collisions and lifts any length limit on token types.
func TokenTypeBytes(tokenType string) []byte {
	h := sha256.New()
	h.Write([]byte(tokenTypeDomain))
	h.Write([]byte(tokenType))

	return h.Sum(nil)[:TokenTypeEncodingLen]
}

// Suite is the curve-independent, byte-oriented view of the scalar-field
// operations needed to build note and type commitments. Implementations run
// natively on gnark-crypto's fr.Element, so callers stay curve-agnostic
// without paying for big.Int conversions. All field elements are exchanged
// as canonical 32-byte big-endian encodings.
type Suite interface {
	// FieldElement reduces arbitrary-length big-endian bytes into the scalar
	// field and returns the canonical encoding. Canonical 32-byte input is
	// returned unchanged.
	FieldElement(b []byte) []byte
	// RandomFieldElement returns a uniform random field element.
	RandomFieldElement() ([]byte, error)
	// EncodeTokenType maps a token type string to a field element via
	// TokenTypeBytes.
	EncodeTokenType(tokenType string) []byte
	// TypeCommitment returns MiMC(EncodeTokenType(tokenType), randomness).
	TypeCommitment(tokenType string, randomness []byte) ([]byte, error)
	// NoteCommitment returns MiMC(value, EncodeTokenType(tokenType), randomness).
	NoteCommitment(value uint64, tokenType string, randomness []byte) ([]byte, error)
}

// field is the constraint satisfied by *E for gnark-crypto's fr.Element.
type field[E any] interface {
	*E
	SetUint64(uint64) *E
	SetBytes([]byte) *E
	SetRandom() (*E, error)
	Marshal() []byte
}

// ForCurve returns the Suite of the given pairing curve's scalar field.
//
//nolint:ireturn // callers must stay curve-agnostic; the concrete types differ per curve
func ForCurve(id ecc.ID) (Suite, error) {
	switch id {
	case ecc.BLS12_381:
		return suite[bls12381fr.Element, *bls12381fr.Element]{hash: Hash}, nil
	case ecc.BN254:
		return suite[bn254fr.Element, *bn254fr.Element]{hash: mimcbn254.Hash}, nil
	default:
		return nil, errors.Errorf("mimc: unsupported curve %s", id)
	}
}

type suite[E any, PE field[E]] struct {
	hash func(...E) (E, error)
}

func (s suite[E, PE]) element(b []byte) E {
	var e E
	PE(&e).SetBytes(b)

	return e
}

func (s suite[E, PE]) FieldElement(b []byte) []byte {
	e := s.element(b)

	return PE(&e).Marshal()
}

func (s suite[E, PE]) RandomFieldElement() ([]byte, error) {
	var e E
	if _, err := PE(&e).SetRandom(); err != nil {
		return nil, errors.Wrapf(err, "mimc: randomness generation failed")
	}

	return PE(&e).Marshal(), nil
}

func (s suite[E, PE]) EncodeTokenType(tokenType string) []byte {
	return s.FieldElement(TokenTypeBytes(tokenType))
}

func (s suite[E, PE]) TypeCommitment(tokenType string, randomness []byte) ([]byte, error) {
	h, err := s.hash(s.element(TokenTypeBytes(tokenType)), s.element(randomness))
	if err != nil {
		return nil, err
	}

	return PE(&h).Marshal(), nil
}

func (s suite[E, PE]) NoteCommitment(value uint64, tokenType string, randomness []byte) ([]byte, error) {
	var v E
	PE(&v).SetUint64(value)
	h, err := s.hash(v, s.element(TokenTypeBytes(tokenType)), s.element(randomness))
	if err != nil {
		return nil, err
	}

	return PE(&h).Marshal(), nil
}
