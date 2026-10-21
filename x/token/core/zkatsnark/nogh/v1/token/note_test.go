/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token_test

import (
	"math/big"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/stretchr/testify/require"

	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

var supportedCurves = []ecc.ID{ecc.BLS12_381, ecc.BN254}

func TestNoteCommitmentDeterminism(t *testing.T) {
	for _, curve := range supportedCurves {
		t.Run(curve.String(), func(t *testing.T) {
			note, err := snarktoken.NewRandomNote(100, "USD", curve)
			require.NoError(t, err)

			cm1, err := note.Commitment(curve)
			require.NoError(t, err)

			cm2, err := note.Commitment(curve)
			require.NoError(t, err)

			require.Equal(t, cm1, cm2, "commitment must be deterministic on %s", curve)
		})
	}
}

func TestNoteCommitmentDeterminism_WideRandomness(t *testing.T) {
	// A curve-agnostic note with 64 bytes of randomness must be deterministic
	// across all supported curves.
	r, err := snarktoken.SampleRandomness(snarktoken.DefaultRandomnessLength)
	require.NoError(t, err)
	note := &snarktoken.Note{Value: 100, TokenType: "USD", Randomness: r}
	require.Len(t, note.Randomness, 64)

	for _, curve := range supportedCurves {
		t.Run(curve.String(), func(t *testing.T) {
			cm1, err := note.Commitment(curve)
			require.NoError(t, err)
			require.Len(t, cm1, 32)

			cm2, err := note.Commitment(curve)
			require.NoError(t, err)

			require.Equal(t, cm1, cm2, "wide randomness commitment must be deterministic on %s", curve)
		})
	}
}

func TestEncodeTokenType_Generic(t *testing.T) {
	for _, curve := range supportedCurves {
		t.Run(curve.String(), func(t *testing.T) {
			want := new(big.Int).Mod(new(big.Int).SetBytes([]byte("USD")), curve.ScalarField())

			got, err := snarktoken.EncodeTokenTypeBytes("USD", curve)
			require.NoError(t, err)
			require.Equal(t, want.FillBytes(make([]byte, 32)), got)
		})
	}

	_, err := snarktoken.EncodeTokenTypeBytes("USD", ecc.BW6_761)
	require.Error(t, err)
}

func TestNoteRandomnessUniqueness(t *testing.T) {
	for _, curve := range supportedCurves {
		t.Run(curve.String(), func(t *testing.T) {
			note1, err := snarktoken.NewRandomNote(100, "USD", curve)
			require.NoError(t, err)

			note2, err := snarktoken.NewRandomNote(100, "USD", curve)
			require.NoError(t, err)

			require.NotEqual(t, note1.Randomness, note2.Randomness, "randomness must be unique")

			cm1, err := note1.Commitment(curve)
			require.NoError(t, err)
			cm2, err := note2.Commitment(curve)
			require.NoError(t, err)

			require.NotEqual(t, cm1, cm2, "different randomness must produce different commitments on %s", curve)
		})
	}
}

func TestNewRandomNote(t *testing.T) {
	for _, curve := range supportedCurves {
		t.Run(curve.String(), func(t *testing.T) {
			note, err := snarktoken.NewRandomNote(100, "USD", curve)
			require.NoError(t, err)
			require.Len(t, note.Randomness, 32)
			cm, err := note.Commitment(curve)
			require.NoError(t, err)
			require.Len(t, cm, 32)
		})
	}

	_, err := snarktoken.NewRandomNote(100, "USD", ecc.BW6_761)
	require.Error(t, err)
}

func TestNoteSerializeDeserialize_AllCurvesAndLengths(t *testing.T) {
	// Test notes with 32, 48, and 64 bytes of randomness across all curves
	lengths := []int{32, 48, 64}

	for _, curve := range supportedCurves {
		t.Run(curve.String(), func(t *testing.T) {
			for _, rLen := range lengths {
				rBytes, err := snarktoken.SampleRandomness(rLen)
				require.NoError(t, err)

				note := &snarktoken.Note{
					Value:      250,
					TokenType:  "EUR",
					Randomness: rBytes,
					Issuer:     []byte("org-issuer-1"),
				}

				serialized, err := note.Serialize()
				require.NoError(t, err)

				deserialized, err := snarktoken.Deserialize(serialized)
				require.NoError(t, err)

				require.Equal(t, note.Value, deserialized.Value)
				require.Equal(t, note.TokenType, deserialized.TokenType)
				require.Equal(t, note.Randomness, deserialized.Randomness)
				require.Equal(t, note.Issuer, deserialized.Issuer)

				cm1, err := note.Commitment(curve)
				require.NoError(t, err)
				cm2, err := deserialized.Commitment(curve)
				require.NoError(t, err)
				require.Equal(t, cm1, cm2, "deserialized note must have identical commitment on %s", curve)
			}
		})
	}
}

func TestNoteCommitmentCrossCurveDistinction(t *testing.T) {
	// Same value and token type must produce distinct commitments under BLS12-381 vs BN254
	note, err := snarktoken.NewRandomNote(42, "EUR", ecc.BLS12_381)
	require.NoError(t, err)

	cmBLS, err := note.Commitment(ecc.BLS12_381)
	require.NoError(t, err)
	require.Len(t, cmBLS, 32)

	cmBN, err := note.Commitment(ecc.BN254)
	require.NoError(t, err)
	require.Len(t, cmBN, 32)

	require.NotEqual(t, cmBLS, cmBN, "BLS12-381 and BN254 commitments must differ")
}

func TestFieldElementFromBytes_WideAndCanonical(t *testing.T) {
	wide, err := snarktoken.SampleRandomness(64)
	require.NoError(t, err)

	for _, curve := range supportedCurves {
		t.Run(curve.String(), func(t *testing.T) {
			// 32-byte canonical: identity.
			rBytes, err := snarktoken.SampleFieldRandomness(curve)
			require.NoError(t, err)
			require.Len(t, rBytes, 32)
			parsed, err := snarktoken.FieldElementFromBytes(rBytes, curve)
			require.NoError(t, err)
			require.Equal(t, rBytes, parsed)

			// Wide 64-byte randomness: reduced modulo the field order.
			f, err := snarktoken.FieldElementFromBytes(wide, curve)
			require.NoError(t, err)
			want := new(big.Int).Mod(new(big.Int).SetBytes(wide), curve.ScalarField())
			require.Equal(t, want.FillBytes(make([]byte, 32)), f)

			// Empty bytes error.
			_, err = snarktoken.FieldElementFromBytes(nil, curve)
			require.Error(t, err)
		})
	}

	_, err = snarktoken.FieldElementFromBytes(wide, ecc.BW6_761)
	require.Error(t, err)
}
