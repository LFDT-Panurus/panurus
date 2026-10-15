/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token_test

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/stretchr/testify/require"

	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

func TestNoteCommitmentDeterminism(t *testing.T) {
	note, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)

	cm1, err := note.Commitment(ecc.BLS12_381)
	require.NoError(t, err)

	cm2, err := note.Commitment(ecc.BLS12_381)
	require.NoError(t, err)

	require.Equal(t, cm1, cm2, "commitment must be deterministic")
}

func TestEncodeTokenTypeMatchesCircuitTests(t *testing.T) {
	encoded := snarktoken.EncodeTokenType("USD")

	var inline fr.Element
	inline.SetBytes([]byte("USD"))
	inlineBytes := inline.Bytes()

	require.Equal(t, encoded, inlineBytes[:], "EncodeTokenType must match circuit tests")
}

func TestNoteRandomnessUniqueness(t *testing.T) {
	note1, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)

	note2, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)

	require.NotEqual(t, note1.Randomness, note2.Randomness, "randomness must be unique")

	cm1, _ := note1.Commitment(ecc.BLS12_381)
	cm2, _ := note2.Commitment(ecc.BLS12_381)

	require.NotEqual(t, cm1, cm2, "different randomness must produce different commitments even for identical value/type")
}

func TestNoteSerializeDeserialize(t *testing.T) {
	note, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)

	serialized, err := note.Serialize()
	require.NoError(t, err)

	deserialized, err := snarktoken.Deserialize(serialized)
	require.NoError(t, err)

	require.Equal(t, note.Value, deserialized.Value)
	require.Equal(t, note.TokenType, deserialized.TokenType)
	require.Equal(t, note.Randomness, deserialized.Randomness)

	cm1, _ := note.Commitment(ecc.BLS12_381)
	cm2, _ := deserialized.Commitment(ecc.BLS12_381)
	require.Equal(t, cm1, cm2, "deserialized note must have same commitment")
}

func TestNoteCommitmentBN254(t *testing.T) {
	note, err := snarktoken.NewRandomNote(42, "EUR")
	require.NoError(t, err)

	cm, err := note.Commitment(ecc.BN254)
	require.NoError(t, err)
	require.Len(t, cm, 32, "BN254 commitment must be 32 bytes")

	// Must differ from BLS12-381 commitment (different MiMC round constants).
	cmBLS, err := note.Commitment(ecc.BLS12_381)
	require.NoError(t, err)
	require.NotEqual(t, cm, cmBLS, "BN254 and BLS12-381 commitments must differ")
}
