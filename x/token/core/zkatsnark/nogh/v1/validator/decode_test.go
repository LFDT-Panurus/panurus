/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"math/big"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/edwards"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

// validSpendBytes builds a SpendDescription with genuinely valid,
// self-consistent canonical bytes, a real fr.Element and a real on-curve
// Jubjub point, not just correctly-sized zero-filled slices (structural.go's
// tests already cover length; these tests specifically exercise the
// canonical-decode and on-curve logic that length checks can't catch).
func validSpendBytes(t *testing.T) snarktoken.SpendDescription {
	t.Helper()
	rcv, err := edwards.Jubjub().RandomScalar()
	require.NoError(t, err)
	cv, err := edwards.Jubjub().ValueCommit(100, rcv)
	require.NoError(t, err)

	var cm fr.Element
	_, err = cm.SetRandom()
	require.NoError(t, err)

	cmBytes := cm.Bytes()
	cxBytes := cv.X.Bytes()
	cyBytes := cv.Y.Bytes()
	ttBytes, err := snarktoken.EncodeTokenTypeBytes("USD", ecc.BLS12_381)
	require.NoError(t, err)

	return snarktoken.SpendDescription{
		CommitmentIn:   cmBytes[:],
		ValueCommitInX: cxBytes[:],
		ValueCommitInY: cyBytes[:],
		TypeCommitment: ttBytes[:],
		SpendProof:     make([]byte, 192),
	}
}

func TestDecodeSpendDescription_Valid(t *testing.T) {
	d := validSpendBytes(t)
	decoded, err := decodeSpendDescription(0, d, ecc.BLS12_381)
	require.NoError(t, err)

	var expectedCommitment fr.Element
	require.NoError(t, expectedCommitment.SetBytesCanonical(d.CommitmentIn))
	require.Zero(t, decoded.Commitment.Cmp(expectedCommitment.BigInt(new(big.Int))))
}

// TestDecodeSpendDescription_NonCanonicalFieldElement is the single most
// important test in this file: it confirms a byte sequence representing an
// integer >= the field modulus is rejected, not silently reduced.
func TestDecodeSpendDescription_NonCanonicalFieldElement(t *testing.T) {
	d := validSpendBytes(t)
	nonCanonical := make([]byte, 32)

	// All-0xFF is far larger than the BLS12-381 Fr modulus.
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	d.CommitmentIn = nonCanonical

	_, err := decodeSpendDescription(0, d, ecc.BLS12_381)
	require.Error(t, err, "a non-canonical field element must be rejected, not silently reduced")
}

func TestDecodeSpendDescription_PointNotOnCurve(t *testing.T) {
	d := validSpendBytes(t)

	// Corrupt only Y, leaves X unchanged, so (X, Y) very likely no longer
	// satisfies the Jubjub curve equation.
	var badY fr.Element
	badY.SetUint64(999999)
	badYBytes := badY.Bytes()
	d.ValueCommitInY = badYBytes[:]

	_, err := decodeSpendDescription(0, d, ecc.BLS12_381)
	require.Error(t, err, "a value commitment point off the Jubjub curve must be rejected")
}

func TestDecodeAllSpends_StopsAtFirstFailure(t *testing.T) {
	good := validSpendBytes(t)
	bad := validSpendBytes(t)
	bad.CommitmentIn = make([]byte, 31) // wrong length, SetBytesCanonical will error

	_, err := decodeAllSpends([]snarktoken.SpendDescription{good, bad}, ecc.BLS12_381)
	require.Error(t, err)
}

// ── Additional decode tests ─────────────────────────────────────────────────

func validOutputBytes(t *testing.T) snarktoken.OutputDescription {
	t.Helper()
	rcv, err := edwards.Jubjub().RandomScalar()
	require.NoError(t, err)
	cv, err := edwards.Jubjub().ValueCommit(100, rcv)
	require.NoError(t, err)

	var cm fr.Element
	_, err = cm.SetRandom()
	require.NoError(t, err)

	cmBytes := cm.Bytes()
	cxBytes := cv.X.Bytes()
	cyBytes := cv.Y.Bytes()
	ttBytes, err := snarktoken.EncodeTokenTypeBytes("USD", ecc.BLS12_381)
	require.NoError(t, err)

	return snarktoken.OutputDescription{
		CommitmentOut:   cmBytes[:],
		ValueCommitOutX: cxBytes[:],
		ValueCommitOutY: cyBytes[:],
		TypeCommitment:  ttBytes[:],
		OutputProof:     make([]byte, 192),
	}
}

func TestDecodeOutputDescription_Valid(t *testing.T) {
	d := validOutputBytes(t)
	decoded, err := decodeOutputDescription(0, d, ecc.BLS12_381)
	require.NoError(t, err)

	var expectedCommitment fr.Element
	require.NoError(t, expectedCommitment.SetBytesCanonical(d.CommitmentOut))
	require.Zero(t, decoded.Commitment.Cmp(expectedCommitment.BigInt(new(big.Int))))
}

func TestDecodeOutputDescription_NonCanonicalFieldElement(t *testing.T) {
	d := validOutputBytes(t)
	nonCanonical := make([]byte, 32)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	d.CommitmentOut = nonCanonical

	_, err := decodeOutputDescription(0, d, ecc.BLS12_381)
	require.Error(t, err, "a non-canonical commitment must be rejected")
}

func TestDecodeOutputDescription_NonCanonicalValueCommitOutX(t *testing.T) {
	d := validOutputBytes(t)
	nonCanonical := make([]byte, 32)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	d.ValueCommitOutX = nonCanonical

	_, err := decodeOutputDescription(0, d, ecc.BLS12_381)
	require.Error(t, err, "a non-canonical ValueCommitOutX must be rejected")
}

func TestDecodeOutputDescription_NonCanonicalValueCommitOutY(t *testing.T) {
	d := validOutputBytes(t)
	nonCanonical := make([]byte, 32)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	d.ValueCommitOutY = nonCanonical

	_, err := decodeOutputDescription(0, d, ecc.BLS12_381)
	require.Error(t, err, "a non-canonical ValueCommitOutY must be rejected")
}

func TestDecodeOutputDescription_NonCanonicalTypeCommitment(t *testing.T) {
	d := validOutputBytes(t)
	nonCanonical := make([]byte, 32)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	d.TypeCommitment = nonCanonical

	_, err := decodeOutputDescription(0, d, ecc.BLS12_381)
	require.Error(t, err, "a non-canonical TypeCommitment must be rejected")
}

func TestDecodeOutputDescription_PointNotOnCurve(t *testing.T) {
	d := validOutputBytes(t)
	var badY fr.Element
	badY.SetUint64(999999)
	badYBytes := badY.Bytes()
	d.ValueCommitOutY = badYBytes[:]

	_, err := decodeOutputDescription(0, d, ecc.BLS12_381)
	require.Error(t, err, "a value commitment point off the Jubjub curve must be rejected")
}

func TestDecodeAllOutputs_Valid(t *testing.T) {
	out1 := validOutputBytes(t)
	out2 := validOutputBytes(t)

	decoded, err := decodeAllOutputs([]snarktoken.OutputDescription{out1, out2}, ecc.BLS12_381)
	require.NoError(t, err)
	require.Len(t, decoded, 2)
}

func TestDecodeAllOutputs_StopsAtFirstFailure(t *testing.T) {
	good := validOutputBytes(t)
	bad := validOutputBytes(t)
	bad.CommitmentOut = make([]byte, 31) // wrong length → SetBytesCanonical error

	_, err := decodeAllOutputs([]snarktoken.OutputDescription{good, bad}, ecc.BLS12_381)
	require.Error(t, err)
}

func TestDecodeSpendDescription_NonCanonicalValueCommitInX(t *testing.T) {
	d := validSpendBytes(t)
	nonCanonical := make([]byte, 32)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	d.ValueCommitInX = nonCanonical

	_, err := decodeSpendDescription(0, d, ecc.BLS12_381)
	require.Error(t, err, "a non-canonical ValueCommitInX must be rejected")
}

func TestDecodeSpendDescription_NonCanonicalValueCommitInY(t *testing.T) {
	d := validSpendBytes(t)
	nonCanonical := make([]byte, 32)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	d.ValueCommitInY = nonCanonical

	_, err := decodeSpendDescription(0, d, ecc.BLS12_381)
	require.Error(t, err, "a non-canonical ValueCommitInY must be rejected")
}

func TestDecodeSpendDescription_NonCanonicalTypeCommitment(t *testing.T) {
	d := validSpendBytes(t)
	nonCanonical := make([]byte, 32)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	d.TypeCommitment = nonCanonical

	_, err := decodeSpendDescription(0, d, ecc.BLS12_381)
	require.Error(t, err, "a non-canonical TypeCommitment must be rejected")
}

// TestParseTotalValue_RejectsValuesAbove64Bits: a canonical field element can
// exceed 64 bits, and big.Int.Uint64 would silently keep only the low 64, so
// TotalValue and TotalValue+2^64 would decode to the same amount.
func TestParseTotalValue_RejectsValuesAbove64Bits(t *testing.T) {
	for _, curve := range []ecc.ID{ecc.BLS12_381, ecc.BN254} {
		t.Run(curve.String(), func(t *testing.T) {
			encode := func(v *big.Int) []byte { return v.FillBytes(make([]byte, 32)) }

			got, err := parseTotalValue(curve, encode(big.NewInt(500)))
			require.NoError(t, err)
			require.Equal(t, uint64(500), got)

			maxU64 := new(big.Int).SetUint64(^uint64(0))
			got, err = parseTotalValue(curve, encode(maxU64))
			require.NoError(t, err)
			require.Equal(t, ^uint64(0), got)

			// 500 + 2^64 is a canonical field element that used to alias 500.
			aliased := new(big.Int).Add(big.NewInt(500), new(big.Int).Lsh(big.NewInt(1), 64))
			_, err = parseTotalValue(curve, encode(aliased))
			require.ErrorIs(t, err, ErrInvalidEncoding)
		})
	}
}
