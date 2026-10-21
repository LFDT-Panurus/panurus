/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"crypto/rand"
	"encoding/binary"
	"io"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/mimc"
)

// DefaultRandomnessLength is 64 bytes (512 bits), providing at least 256 bits
// of entropy and negligible modular reduction bias (< 2^-256) across all
// supported 254/255-bit elliptic curves.
const DefaultRandomnessLength = 64

// Note is the complete private description of a token, everything needed
// to spend it. It is never published on-chain in cleartext.
//
// Note stores only what must persist for the token's entire lifetime:
// Randomness is the exact value that must be resupplied to open the token's
// commitment whenever it is spent, however far in the future that is.
//
// It deliberately does not store any value-commitment randomness (RCV),
// that is generated fresh every time the token is used in a proof, by the
// WitnessBuilder, and has no relationship to anything stored here.
//
// Randomness is stored as raw bytes to avoid locking Note to a single
// curve's field element type. It is reduced into the appropriate curve's
// scalar field at usage sites.
type Note struct {
	Value      uint64
	TokenType  string
	Randomness []byte
	Issuer     []byte
}

// FieldElementFromBytes converts raw randomness or element bytes into the
// canonical 32-byte encoding of an element of curve's scalar field. Canonical
// 32-byte inputs are returned unchanged; longer byte sequences (e.g. 48 or 64
// bytes wide randomness) and non-canonical 32-byte sequences are reduced
// modulo the field order, ensuring uniform sampling across any curve.
func FieldElementFromBytes(b []byte, curve ecc.ID) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("note: empty bytes for field element")
	}
	s, err := mimc.ForCurve(curve)
	if err != nil {
		return nil, errors.Wrapf(err, "note: field element conversion failed")
	}

	return s.FieldElement(b), nil
}

// EncodeTokenTypeBytes maps a token type string to the canonical 32-byte
// encoding of an element of curve's scalar field.
func EncodeTokenTypeBytes(tokenType string, curve ecc.ID) ([]byte, error) {
	s, err := mimc.ForCurve(curve)
	if err != nil {
		return nil, errors.Wrapf(err, "note: token type encoding failed")
	}

	return s.EncodeTokenType(tokenType), nil
}

// SampleRandomness generates cryptographically secure uniform random bytes.
// If byteLen is omitted or non-positive, it defaults to DefaultRandomnessLength (64 bytes).
func SampleRandomness(byteLen ...int) ([]byte, error) {
	n := DefaultRandomnessLength
	if len(byteLen) > 0 && byteLen[0] > 0 {
		n = byteLen[0]
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, errors.Wrapf(err, "note: randomness sampling failed")
	}

	return b, nil
}

// SampleFieldRandomness samples a uniform random element of curve's scalar
// field and returns its canonical 32-byte encoding.
func SampleFieldRandomness(curve ecc.ID) ([]byte, error) {
	s, err := mimc.ForCurve(curve)
	if err != nil {
		return nil, errors.Wrapf(err, "note: randomness generation failed")
	}

	return s.RandomFieldElement()
}

// NewRandomNote constructs a Note for a newly created token whose commitment
// randomness is a uniform element of curve's scalar field. The curve is
// mandatory so the randomness is always valid for the curve the note is
// committed under.
func NewRandomNote(value uint64, tokenType string, curve ecc.ID) (*Note, error) {
	rBytes, err := SampleFieldRandomness(curve)
	if err != nil {
		return nil, err
	}

	return &Note{Value: value, TokenType: tokenType, Randomness: rBytes, Issuer: nil}, nil
}

// ComputeTypeCommitment computes a hiding commitment to the token type:
// MiMC(EncodeTokenType(tokenType), typeRandomness). All inputs and outputs
// in a single action share the same typeRandomness, so they produce the
// same TypeCommitment, letting the validator confirm type homogeneity
// without learning the plaintext type.
func ComputeTypeCommitment(tokenType string, typeRandomness []byte, curve ecc.ID) ([]byte, error) {
	s, err := mimc.ForCurve(curve)
	if err != nil {
		return nil, errors.Wrapf(err, "note: type commitment computation failed")
	}
	if len(typeRandomness) == 0 {
		return nil, errors.New("note: invalid type randomness bytes: empty")
	}
	tc, err := s.TypeCommitment(tokenType, typeRandomness)
	if err != nil {
		return nil, errors.Wrapf(err, "note: type commitment computation failed")
	}

	return tc, nil
}

// Commitment computes cm = MiMC(Value, TokenType, Randomness) and returns
// the result as raw bytes.
//
// This must match Constraint Group 1 in circuit.SpendCircuit.Define and
// circuit.OutputCircuit.Define exactly, same three inputs, same order.
//
// The curve parameter selects the MiMC variant (BLS12-381 or BN254).
func (n *Note) Commitment(curve ecc.ID) ([]byte, error) {
	s, err := mimc.ForCurve(curve)
	if err != nil {
		return nil, errors.Wrapf(err, "note: commitment computation failed")
	}
	if len(n.Randomness) == 0 {
		return nil, errors.New("note: invalid randomness bytes: empty")
	}
	cm, err := s.NoteCommitment(n.Value, n.TokenType, n.Randomness)
	if err != nil {
		return nil, errors.Wrapf(err, "note: commitment computation failed")
	}

	return cm, nil
}

// Serialize encodes the Note as CLEARTEXT bytes:
// 8-byte big-endian Value,
// 8-byte big-endian TokenType length, then TokenType bytes,
// 8-byte big-endian Randomness length, then Randomness bytes,
// 8-byte big-endian Issuer length, then Issuer bytes.
func (n *Note) Serialize() ([]byte, error) {
	out := make([]byte, 0, 8+8+len(n.TokenType)+8+len(n.Randomness)+8+len(n.Issuer))
	var valueBuf [8]byte
	binary.BigEndian.PutUint64(valueBuf[:], n.Value)
	out = append(out, valueBuf[:]...)

	var typeLenBuf [8]byte
	binary.BigEndian.PutUint64(typeLenBuf[:], uint64(len(n.TokenType)))
	out = append(out, typeLenBuf[:]...)
	out = append(out, []byte(n.TokenType)...)

	var randLenBuf [8]byte
	binary.BigEndian.PutUint64(randLenBuf[:], uint64(len(n.Randomness)))
	out = append(out, randLenBuf[:]...)
	out = append(out, n.Randomness...)

	var issuerLenBuf [8]byte
	binary.BigEndian.PutUint64(issuerLenBuf[:], uint64(len(n.Issuer)))
	out = append(out, issuerLenBuf[:]...)
	out = append(out, n.Issuer...)

	return out, nil
}

// Deserialize reconstructs a Note from bytes produced by Serialize.
// It supports both new length-prefixed randomness notes and legacy 32-byte fixed randomness notes.
func Deserialize(raw []byte) (*Note, error) {
	if len(raw) < 16 {
		return nil, errors.Errorf("note: serialized note too short: %d bytes", len(raw))
	}

	value := binary.BigEndian.Uint64(raw[0:8])
	typeLen := binary.BigEndian.Uint64(raw[8:16])

	if uint64(len(raw)) < 16+typeLen {
		return nil, errors.Errorf("note: length mismatch, expected at least %d got %d", 16+typeLen, len(raw))
	}

	tokenType := string(raw[16 : 16+typeLen])
	rem := raw[16+typeLen:]

	var randomness []byte
	var issuer []byte

	isLengthPrefixed := false
	if len(rem) >= 8 {
		rLen := binary.BigEndian.Uint64(rem[0:8])
		if rLen > 0 && rLen <= 1024 && uint64(len(rem)) >= 8+rLen {
			remAfterRand := rem[8+rLen:]
			if len(remAfterRand) == 0 {
				isLengthPrefixed = true
			} else if len(remAfterRand) >= 8 {
				issLen := binary.BigEndian.Uint64(remAfterRand[0:8])
				if uint64(len(remAfterRand)) == 8+issLen {
					isLengthPrefixed = true
				}
			}
		}
	}

	if isLengthPrefixed {
		rLen := binary.BigEndian.Uint64(rem[0:8])
		randomness = make([]byte, rLen)
		copy(randomness, rem[8:8+rLen])
		remAfterRand := rem[8+rLen:]
		if len(remAfterRand) >= 8 {
			issLen := binary.BigEndian.Uint64(remAfterRand[0:8])
			issuer = make([]byte, issLen)
			copy(issuer, remAfterRand[8:8+issLen])
		}
	} else {
		if len(rem) < 32 {
			return nil, errors.Errorf("note: length mismatch, expected at least 32 randomness bytes, got %d", len(rem))
		}
		randomness = make([]byte, 32)
		copy(randomness, rem[:32])
		remAfterRand := rem[32:]
		if len(remAfterRand) >= 8 {
			issLen := binary.BigEndian.Uint64(remAfterRand[0:8])
			if uint64(len(remAfterRand)) != 8+issLen {
				return nil, errors.Errorf("note: length mismatch, expected %d issuer bytes got %d", 8+issLen, len(remAfterRand))
			}
			issuer = make([]byte, issLen)
			copy(issuer, remAfterRand[8:8+issLen])
		} else if len(remAfterRand) != 0 {
			return nil, errors.Errorf("note: invalid trailing bytes: %d", len(remAfterRand))
		}
	}

	return &Note{Value: value, TokenType: tokenType, Randomness: randomness, Issuer: issuer}, nil
}
