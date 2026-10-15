/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"encoding/binary"

	"github.com/consensys/gnark-crypto/ecc"
	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/mimc"
	mimcbn254 "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/mimc/bn254"
)

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
// Randomness is stored as raw bytes (32) to avoid locking Note to a single
// curve's fr.Element type. It is decoded into the appropriate curve's field
// element at usage sites.
type Note struct {
	Value      uint64
	TokenType  string
	Randomness [32]byte
	Issuer     []byte
}

// EncodeTokenType maps a token type string to a canonical field element
// encoding as raw bytes. The bytes can be loaded into either curve's
// fr.Element via SetBytes.
func EncodeTokenType(tokenType string) []byte {
	var t bls12381fr.Element
	t.SetBytes([]byte(tokenType))
	b := t.Bytes()

	return b[:]
}

// EncodeTokenTypeBLS12381 maps a token type to a BLS12-381 fr.Element.
func EncodeTokenTypeBLS12381(tokenType string) bls12381fr.Element {
	var t bls12381fr.Element
	t.SetBytes([]byte(tokenType))

	return t
}

// EncodeTokenTypeBN254 maps a token type to a BN254 fr.Element.
func EncodeTokenTypeBN254(tokenType string) bn254fr.Element {
	var t bn254fr.Element
	t.SetBytes([]byte(tokenType))

	return t
}

// ComputeTypeCommitment computes a hiding commitment to the token type:
// MiMC(EncodeTokenType(tokenType), typeRandomness). All inputs and outputs
// in a single action share the same typeRandomness, so they produce the
// same TypeCommitment, letting the validator confirm type homogeneity
// without learning the plaintext type.
//
// Returns the commitment as raw bytes (32). The curve parameter determines
// which MiMC implementation is used.
func ComputeTypeCommitment(tokenType string, typeRandomness []byte, curve ecc.ID) ([]byte, error) {
	switch curve {
	case ecc.BLS12_381:
		var t, tr bls12381fr.Element
		t.SetBytes([]byte(tokenType))
		if err := tr.SetBytesCanonical(typeRandomness); err != nil {
			return nil, errors.Wrapf(err, "note: invalid type randomness bytes")
		}
		tc, err := mimc.Hash(t, tr)
		if err != nil {
			return nil, errors.Wrapf(err, "note: type commitment computation failed")
		}
		b := tc.Bytes()

		return b[:], nil

	case ecc.BN254:
		var t, tr bn254fr.Element
		t.SetBytes([]byte(tokenType))
		if err := tr.SetBytesCanonical(typeRandomness); err != nil {
			return nil, errors.Wrapf(err, "note: invalid type randomness bytes")
		}
		tc, err := mimcbn254.Hash(t, tr)
		if err != nil {
			return nil, errors.Wrapf(err, "note: type commitment computation failed")
		}
		b := tc.Bytes()

		return b[:], nil

	default:
		return nil, errors.Errorf("note: unsupported curve %s for type commitment", curve)
	}
}

// Commitment computes cm = MiMC(Value, TokenType, Randomness) and returns
// the result as raw bytes.
//
// This must match Constraint Group 1 in circuit.SpendCircuit.Define and
// circuit.OutputCircuit.Define exactly, same three inputs, same order.
//
// The curve parameter selects the MiMC variant (BLS12-381 or BN254).
// Returning []byte instead of a typed fr.Element is consistent with how
// the wire format already treats every commitment as opaque bytes at the
// SpendDescription/OutputDescription level (Decision G, Option 2).
func (n *Note) Commitment(curve ecc.ID) ([]byte, error) {
	switch curve {
	case ecc.BLS12_381:
		var v bls12381fr.Element
		v.SetUint64(n.Value)
		var t bls12381fr.Element
		t.SetBytes([]byte(n.TokenType))
		var r bls12381fr.Element
		if err := r.SetBytesCanonical(n.Randomness[:]); err != nil {
			return nil, errors.Wrapf(err, "note: invalid randomness bytes")
		}
		cm, err := mimc.Hash(v, t, r)
		if err != nil {
			return nil, errors.Wrapf(err, "note: commitment computation failed")
		}
		b := cm.Bytes()

		return b[:], nil

	case ecc.BN254:
		var v bn254fr.Element
		v.SetUint64(n.Value)
		var t bn254fr.Element
		t.SetBytes([]byte(n.TokenType))
		var r bn254fr.Element
		if err := r.SetBytesCanonical(n.Randomness[:]); err != nil {
			return nil, errors.Wrapf(err, "note: invalid randomness bytes")
		}
		cm, err := mimcbn254.Hash(v, t, r)
		if err != nil {
			return nil, errors.Wrapf(err, "note: commitment computation failed")
		}
		b := cm.Bytes()

		return b[:], nil

	default:
		return nil, errors.Errorf("note: unsupported curve %s", curve)
	}
}

// NewRandomNote constructs a Note for a newly created token, with fresh
// cryptographically random commitment randomness. The randomness is stored
// as raw bytes, curve-agnostic.
func NewRandomNote(value uint64, tokenType string) (*Note, error) {
	// Generate randomness in the smaller of the two supported scalar fields (BN254 < BLS12-381)
	// so the byte representation is strictly canonical in both curves.
	var r bn254fr.Element
	if _, err := r.SetRandom(); err != nil {
		return nil, errors.Wrapf(err, "note: randomness generation failed")
	}
	rBytes := r.Bytes()

	return &Note{Value: value, TokenType: tokenType, Randomness: rBytes, Issuer: nil}, nil
}

// Serialize encodes the Note as CLEARTEXT bytes: 8-byte big-endian Value,
// then TokenType length-prefixed, then 32-byte canonical Randomness.
func (n *Note) Serialize() ([]byte, error) {
	out := make([]byte, 0, 8+8+len(n.TokenType)+32)
	var valueBuf [8]byte
	binary.BigEndian.PutUint64(valueBuf[:], n.Value)
	out = append(out, valueBuf[:]...)

	var typeLenbuf [8]byte
	binary.BigEndian.PutUint64(typeLenbuf[:], uint64(len(n.TokenType)))
	out = append(out, typeLenbuf[:]...)

	out = append(out, []byte(n.TokenType)...)

	out = append(out, n.Randomness[:]...)

	var issuerLenBuf [8]byte
	binary.BigEndian.PutUint64(issuerLenBuf[:], uint64(len(n.Issuer)))
	out = append(out, issuerLenBuf[:]...)
	out = append(out, n.Issuer...)

	return out, nil
}

// DeserializeNote reconstructs a Note from bytes produced by Serialize.
func Deserialize(raw []byte) (*Note, error) {
	if len(raw) < 16 {
		return nil, errors.Errorf("note: serialized note too short: %d bytes", len(raw))
	}

	value := binary.BigEndian.Uint64(raw[0:8])
	typeLen := binary.BigEndian.Uint64(raw[8:16])

	expectedLen := 16 + typeLen + 32
	if uint64(len(raw)) < expectedLen {
		return nil, errors.Errorf("note: length mismatch, expected at least %d got %d", expectedLen, len(raw))
	}

	tokenType := string(raw[16 : 16+typeLen])

	var r [32]byte
	copy(r[:], raw[16+typeLen:16+typeLen+32])

	offset := 16 + typeLen + 32
	var issuer []byte
	if uint64(len(raw)) >= offset+8 {
		issuerLen := binary.BigEndian.Uint64(raw[offset : offset+8])
		if uint64(len(raw)) != offset+8+issuerLen {
			return nil, errors.Errorf("note: length mismatch, expected %d got %d", offset+8+issuerLen, len(raw))
		}
		issuer = raw[offset+8:]
	} else if uint64(len(raw)) != offset {
		return nil, errors.Errorf("note: length mismatch, expected %d got %d", offset, len(raw))
	}

	return &Note{Value: value, TokenType: tokenType, Randomness: r, Issuer: issuer}, nil
}
