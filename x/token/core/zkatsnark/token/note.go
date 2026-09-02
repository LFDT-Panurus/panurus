/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"encoding/binary"
	"fmt"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/mimc"
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
type Note struct {
	Value      uint64
	TokenType  string
	Randomness fr.Element
	Issuer     []byte
}

// EncodeTokenType maps a token type string to a canonical BLS12-381 field
// element.
func EncodeTokenType(tokenType string) fr.Element {
	var t fr.Element
	t.SetBytes([]byte(tokenType))

	return t
}

// ComputeTypeCommitment computes a hiding commitment to the token type:
// MiMC(EncodeTokenType(tokenType), typeRandomness). All inputs and outputs
// in a single action share the same typeRandomness, so they produce the
// same TypeCommitment, letting the validator confirm type homogeneity
// without learning the plaintext type.
func ComputeTypeCommitment(tokenType string, typeRandomness fr.Element) (fr.Element, error) {
	t := EncodeTokenType(tokenType)

	tc, err := mimc.Hash(t, typeRandomness)
	if err != nil {
		return fr.Element{}, fmt.Errorf("note: type commitment computation failed: %w", err)
	}

	return tc, nil
}

// Commitment computes cm = MiMC(Value, TokenType, Randomness).
// This must match Constraint Group 1 in circuit.SpendCircuit.Define and
// circuit.OutputCircuit.Define exactly, same three inputs, same order.
func (n *Note) Commitment() (fr.Element, error) {
	var v fr.Element
	v.SetUint64(n.Value)
	t := EncodeTokenType(n.TokenType)

	cm, err := mimc.Hash(v, t, n.Randomness)
	if err != nil {
		return fr.Element{}, fmt.Errorf("note: commitment computation failed: %w", err)
	}

	return cm, nil
}

// NewRandomNote constructs a Note for a newly created token, with fresh
// cryptographically random commitment randomness.
func NewRandomNote(value uint64, tokenType string) (*Note, error) {
	var r fr.Element
	if _, err := r.SetRandom(); err != nil {
		return nil, fmt.Errorf("note: randomness generation failed: %w", err)
	}

	return &Note{Value: value, TokenType: tokenType, Randomness: r, Issuer: nil}, nil
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

	rBytes := n.Randomness.Bytes()
	out = append(out, rBytes[:]...)

	var issuerLenBuf [8]byte
	binary.BigEndian.PutUint64(issuerLenBuf[:], uint64(len(n.Issuer)))
	out = append(out, issuerLenBuf[:]...)
	out = append(out, n.Issuer...)

	return out, nil
}

// DeserializeNote reconstructs a Note from bytes produced by Serialize.
func Deserialize(raw []byte) (*Note, error) {
	if len(raw) < 16 {
		return nil, fmt.Errorf("note: serialized note too short: %d bytes", len(raw))
	}

	value := binary.BigEndian.Uint64(raw[0:8])
	typeLen := binary.BigEndian.Uint64(raw[8:16])

	expectedLen := 16 + typeLen + 32
	if uint64(len(raw)) < expectedLen {
		return nil, fmt.Errorf("note: length mismatch, expected at least %d got %d", expectedLen, len(raw))
	}

	tokenType := string(raw[16 : 16+typeLen])

	var r fr.Element
	if err := r.SetBytesCanonical(raw[16+typeLen : 16+typeLen+32]); err != nil {
		return nil, fmt.Errorf("note: invalid randomness bytes: %w", err)
	}

	offset := 16 + typeLen + 32
	var issuer []byte
	if uint64(len(raw)) >= offset+8 {
		issuerLen := binary.BigEndian.Uint64(raw[offset : offset+8])
		if uint64(len(raw)) != offset+8+issuerLen {
			return nil, fmt.Errorf("note: length mismatch, expected %d got %d", offset+8+issuerLen, len(raw))
		}
		issuer = raw[offset+8:]
	} else if uint64(len(raw)) != offset {
		return nil, fmt.Errorf("note: length mismatch, expected %d got %d", offset, len(raw))
	}

	return &Note{Value: value, TokenType: tokenType, Randomness: r, Issuer: issuer}, nil
}
