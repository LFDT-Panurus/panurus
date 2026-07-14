/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"

	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

func TestNoteCommitmentDeterminism(t *testing.T) {
	note, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)

	cm1, err := note.Commitment()
	require.NoError(t, err)

	cm2, err := note.Commitment()
	require.NoError(t, err)

	require.Equal(t, cm1.Bytes(), cm2.Bytes(), "commitment must be deterministic")
}

func TestEncodeTokenTypeMatchesCircuitTests(t *testing.T) {
	encoded := snarktoken.EncodeTokenType("USD")

	var inline fr.Element
	inline.SetBytes([]byte("USD"))

	require.Equal(t, encoded.Bytes(), inline.Bytes(), "EncodeTokenType must match circuit tests")
}

func TestNoteRandomnessUniqueness(t *testing.T) {
	note1, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)

	note2, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)

	require.NotEqual(t, note1.Randomness.Bytes(), note2.Randomness.Bytes(), "randomness must be unique")

	cm1, _ := note1.Commitment()
	cm2, _ := note2.Commitment()

	require.NotEqual(t, cm1.Bytes(), cm2.Bytes(), "different randomness must produce different commitments even for identical value/type")
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
	require.Equal(t, note.Randomness.Bytes(), deserialized.Randomness.Bytes())

	cm1, _ := note.Commitment()
	cm2, _ := deserialized.Commitment()
	require.Equal(t, cm1.Bytes(), cm2.Bytes(), "deserialized note must have same commitment")
}
