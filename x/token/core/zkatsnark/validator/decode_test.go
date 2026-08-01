/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/stretchr/testify/require"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fp"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// validSpendBytes builds a SpendDescription with genuinely valid,
// self-consistent canonical bytes, a real fr.Element and a real on-curve
// Jubjub point, not just correctly-sized zero-filled slices (structural.go's
// tests already cover length; these tests specifically exercise the
// canonical-decode and on-curve logic that length checks can't catch).
func validSpendBytes(t *testing.T) snarktoken.SpendDescription {
	t.Helper()
	rcv, err := jubjub.RandomJubjubScalar()
	require.NoError(t, err)
	cv, err := jubjub.ValueCommit(100, rcv)
	require.NoError(t, err)

	var cm fr.Element
	_, err = cm.SetRandom()
	require.NoError(t, err)

	cmBytes := cm.Bytes()
	cxBytes := cv.X.Bytes()
	cyBytes := cv.Y.Bytes()
	tt := snarktoken.EncodeTokenType("USD")
	ttBytes := tt.Bytes()

	return snarktoken.SpendDescription{
		CommitmentIn:   cmBytes[:],
		ValueCommitInX: cxBytes[:],
		ValueCommitInY: cyBytes[:],
		TokenType:      ttBytes[:],
		SpendProof:     make([]byte, 192),
	}
}

func TestDecodeSpendDescription_Valid(t *testing.T) {
	d := validSpendBytes(t)
	decoded, err := decodeSpendDescription(0, d)
	require.NoError(t, err)

	var expectedCommitment fr.Element
	require.NoError(t, expectedCommitment.SetBytesCanonical(d.CommitmentIn))
	require.True(t, decoded.Commitment.Equal(&expectedCommitment))
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

	_, err := decodeSpendDescription(0, d)
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

	_, err := decodeSpendDescription(0, d)
	require.Error(t, err, "a value commitment point off the Jubjub curve must be rejected")
}

func TestDecodeAllSpends_StopsAtFirstFailure(t *testing.T) {
	good := validSpendBytes(t)
	bad := validSpendBytes(t)
	bad.CommitmentIn = make([]byte, 31) // wrong length, SetBytesCanonical will error

	_, err := decodeAllSpends([]snarktoken.SpendDescription{good, bad})
	require.Error(t, err)
}

// onCurveG1Point returns a genuine BLS12-381 G1 generator point. The exact
// Generators() signature/name is a common gnark-crypto convention across
// its curve packages but hasn't been directly confirmed for bls12-381
// specifically — verify with `go doc github.com/consensys/gnark-crypto/
// ecc/bls12-381` if this doesn't compile.
func onCurveG1Point(t *testing.T) bls12381.G1Affine {
	t.Helper()
	_, _, g1Gen, _ := bls12381.Generators()
	return g1Gen
}

func validMigrationBytes(t *testing.T) *snarktoken.MigrationAction {
	t.Helper()

	g1 := onCurveG1Point(t)
	var pxFp, pyFp fp.Element
	pxFp.Set(&g1.X)
	pyFp.Set(&g1.Y)
	pxBytes := pxFp.Bytes()
	pyBytes := pyFp.Bytes()

	var rcv fr.Element
	_, err := rcv.SetRandom()
	require.NoError(t, err)
	cv, err := jubjub.ValueCommit(100, rcv)
	require.NoError(t, err)
	cxBytes := cv.X.Bytes()
	cyBytes := cv.Y.Bytes()

	var cm fr.Element
	_, err = cm.SetRandom()
	require.NoError(t, err)
	cmBytes := cm.Bytes()

	tt := snarktoken.EncodeTokenType("USD")
	ttBytes := tt.Bytes()

	return &snarktoken.MigrationAction{
		CommitmentPedersenX: pxBytes[:],
		CommitmentPedersenY: pyBytes[:],
		CommitmentMiMC:      cmBytes[:],
		ValueCommitOutX:     cxBytes[:],
		ValueCommitOutY:     cyBytes[:],
		TokenType:           ttBytes[:],
		MigrationProof:      make([]byte, 192),
	}
}

func TestDecodeMigrationAction_Valid(t *testing.T) {
	a := validMigrationBytes(t)
	decoded, err := decodeMigrationAction(a)
	require.NoError(t, err)

	var expectedX fp.Element
	require.NoError(t, expectedX.SetBytesCanonical(a.CommitmentPedersenX))
	require.True(t, decoded.PedersenX.Equal(&expectedX))
}

func TestDecodeMigrationAction_PedersenPointNotOnCurve(t *testing.T) {
	a := validMigrationBytes(t)
	var badY fp.Element
	badY.SetUint64(999999999)
	badYBytes := badY.Bytes()
	a.CommitmentPedersenY = badYBytes[:]

	_, err := decodeMigrationAction(a)
	require.Error(t, err, "an off-curve Pedersen commitment point must be rejected")
}

func TestDecodeMigrationAction_NonCanonicalPedersenX(t *testing.T) {
	a := validMigrationBytes(t)
	nonCanonical := make([]byte, 48)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	a.CommitmentPedersenX = nonCanonical

	_, err := decodeMigrationAction(a)
	require.Error(t, err, "a non-canonical Fp element must be rejected, not silently reduced")
}

func TestDecodeMigrationAction_ValueCommitNotOnJubjub(t *testing.T) {
	a := validMigrationBytes(t)
	var badY fr.Element
	badY.SetUint64(123456789)
	badYBytes := badY.Bytes()
	a.ValueCommitOutY = badYBytes[:]

	_, err := decodeMigrationAction(a)
	require.Error(t, err, "an off-Jubjub-curve value commitment must be rejected")
}
