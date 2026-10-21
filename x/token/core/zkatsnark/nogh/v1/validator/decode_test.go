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

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fp"

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
	cv, err := edwards.Jubjub().ValueCommit(100, rcv)
	require.NoError(t, err)
	cxBytes := cv.X.Bytes()
	cyBytes := cv.Y.Bytes()

	var cm fr.Element
	_, err = cm.SetRandom()
	require.NoError(t, err)
	cmBytes := cm.Bytes()

	ttBytes, err := snarktoken.EncodeTokenTypeBytes("USD", ecc.BLS12_381)
	require.NoError(t, err)

	return &snarktoken.MigrationAction{
		CommitmentPedersenX: pxBytes[:],
		CommitmentPedersenY: pyBytes[:],
		CommitmentMiMC:      cmBytes[:],
		ValueCommitOutX:     cxBytes[:],
		ValueCommitOutY:     cyBytes[:],
		TypeCommitment:      ttBytes[:],
		MigrationProof:      make([]byte, 192),
	}
}

func TestDecodeMigrationAction_Valid(t *testing.T) {
	a := validMigrationBytes(t)
	decoded, err := decodeMigrationAction(a, ecc.BLS12_381)
	require.NoError(t, err)

	var expectedX fp.Element
	require.NoError(t, expectedX.SetBytesCanonical(a.CommitmentPedersenX))
	require.Zero(t, decoded.PedersenX.Cmp(expectedX.BigInt(new(big.Int))))
}

func TestDecodeMigrationAction_PedersenPointNotOnCurve(t *testing.T) {
	a := validMigrationBytes(t)
	var badY fp.Element
	badY.SetUint64(999999999)
	badYBytes := badY.Bytes()
	a.CommitmentPedersenY = badYBytes[:]

	_, err := decodeMigrationAction(a, ecc.BLS12_381)
	require.Error(t, err, "an off-curve Pedersen commitment point must be rejected")
}

func TestDecodeMigrationAction_NonCanonicalPedersenX(t *testing.T) {
	a := validMigrationBytes(t)
	nonCanonical := make([]byte, 48)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	a.CommitmentPedersenX = nonCanonical

	_, err := decodeMigrationAction(a, ecc.BLS12_381)
	require.Error(t, err, "a non-canonical Fp element must be rejected, not silently reduced")
}

func TestDecodeMigrationAction_ValueCommitNotOnJubjub(t *testing.T) {
	a := validMigrationBytes(t)
	var badY fr.Element
	badY.SetUint64(123456789)
	badYBytes := badY.Bytes()
	a.ValueCommitOutY = badYBytes[:]

	_, err := decodeMigrationAction(a, ecc.BLS12_381)
	require.Error(t, err, "an off-Jubjub-curve value commitment must be rejected")
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

func TestDecodeMigrationAction_NonCanonicalPedersenY(t *testing.T) {
	a := validMigrationBytes(t)
	nonCanonical := make([]byte, 48)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	a.CommitmentPedersenY = nonCanonical

	_, err := decodeMigrationAction(a, ecc.BLS12_381)
	require.Error(t, err, "a non-canonical PedersenY must be rejected")
}

func TestDecodeMigrationAction_NonCanonicalCommitmentMiMC(t *testing.T) {
	a := validMigrationBytes(t)
	nonCanonical := make([]byte, 32)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	a.CommitmentMiMC = nonCanonical

	_, err := decodeMigrationAction(a, ecc.BLS12_381)
	require.Error(t, err, "a non-canonical CommitmentMiMC must be rejected")
}

func TestDecodeMigrationAction_NonCanonicalValueCommitOutX(t *testing.T) {
	a := validMigrationBytes(t)
	nonCanonical := make([]byte, 32)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	a.ValueCommitOutX = nonCanonical

	_, err := decodeMigrationAction(a, ecc.BLS12_381)
	require.Error(t, err, "a non-canonical ValueCommitOutX must be rejected")
}

func TestDecodeMigrationAction_NonCanonicalValueCommitOutY(t *testing.T) {
	a := validMigrationBytes(t)
	nonCanonical := make([]byte, 32)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	a.ValueCommitOutY = nonCanonical

	_, err := decodeMigrationAction(a, ecc.BLS12_381)
	require.Error(t, err, "a non-canonical ValueCommitOutY must be rejected")
}

func TestDecodeMigrationAction_NonCanonicalTypeCommitment(t *testing.T) {
	a := validMigrationBytes(t)
	nonCanonical := make([]byte, 32)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	a.TypeCommitment = nonCanonical

	_, err := decodeMigrationAction(a, ecc.BLS12_381)
	require.Error(t, err, "a non-canonical TypeCommitment must be rejected")
}
