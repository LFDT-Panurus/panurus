/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover_test

import (
	"context"
	"crypto/sha256"
	"math/big"
	"testing"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// onCurveG1Bytes returns the uncompressed byte encoding of the BLS12-381 G1
// generator, a well-known on-curve point accepted by gnark-crypto's SetBytes.
func onCurveG1Bytes(t *testing.T) []byte {
	t.Helper()
	_, _, g1, _ := bls12381.Generators()
	raw := g1.RawBytes()

	return raw[:]
}

// randomCanonicalFr returns a random canonical BLS12-381 scalar field element
// serialized to 32 bytes.
func randomCanonicalFr(t *testing.T) []byte {
	t.Helper()
	var e fr.Element
	_, err := e.SetRandom()
	require.NoError(t, err)
	b := e.Bytes()

	return b[:]
}

func computeValidPedersenCommit(t *testing.T, value uint64, tokenType string, blindingFactor []byte) []byte {
	t.Helper()
	gens := prover.DefaultPedersenGeneratorCoords()

	var g0, g1, g2 bls12381.G1Affine
	g0.X.SetBigInt(gens.G0X)
	g0.Y.SetBigInt(gens.G0Y)
	g1.X.SetBigInt(gens.G1X)
	g1.Y.SetBigInt(gens.G1Y)
	g2.X.SetBigInt(gens.G2X)
	g2.Y.SetBigInt(gens.G2Y)

	var vBig big.Int
	vBig.SetUint64(value)

	h := sha256.Sum256([]byte(tokenType))
	var tBig big.Int
	tBig.SetBytes(h[:])
	tBig.Mod(&tBig, fr.Modulus())

	var rBig big.Int
	rBig.SetBytes(blindingFactor)

	var p0, p1, p2 bls12381.G1Jac
	p0.FromAffine(&g0).ScalarMultiplication(&p0, &tBig)
	p1.FromAffine(&g1).ScalarMultiplication(&p1, &vBig)
	p2.FromAffine(&g2).ScalarMultiplication(&p2, &rBig)

	var result bls12381.G1Jac
	result.Set(&p0).AddAssign(&p1).AddAssign(&p2)

	var resAffine bls12381.G1Affine
	resAffine.FromJacobian(&result)

	raw := resAffine.RawBytes()

	return raw[:]
}

// ── BuildMigrationWitness ─────────────────────────────────────────────────────

func TestBuildMigrationWitness_Basic(t *testing.T) {
	opening := snarktoken.PedersenOpening{
		Value:           100,
		TokenType:       "USD",
		BlindingFactor:  randomCanonicalFr(t),
		CommitmentBytes: onCurveG1Bytes(t),
	}

	var typeRandomness fr.Element
	_, err := typeRandomness.SetRandom()
	require.NoError(t, err)

	p := pp.DefaultPublicParams()
	res, err := prover.BuildMigrationWitness(opening, p, typeRandomness)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.Note)
	require.Equal(t, opening.Value, res.Note.Value)
	require.Equal(t, opening.TokenType, res.Note.TokenType)
}

func TestBuildMigrationWitness_EmptyTokenType_Error(t *testing.T) {
	opening := snarktoken.PedersenOpening{
		Value:           100,
		TokenType:       "", // invalid
		BlindingFactor:  randomCanonicalFr(t),
		CommitmentBytes: onCurveG1Bytes(t),
	}

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()

	p := pp.DefaultPublicParams()
	_, err := prover.BuildMigrationWitness(opening, p, typeRandomness)
	require.Error(t, err, "empty TokenType must be rejected by PedersenOpening.Validate()")
}

func TestBuildMigrationWitness_EmptyBlindingFactor_Error(t *testing.T) {
	opening := snarktoken.PedersenOpening{
		Value:           100,
		TokenType:       "USD",
		BlindingFactor:  nil, // invalid
		CommitmentBytes: onCurveG1Bytes(t),
	}

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()

	p := pp.DefaultPublicParams()
	_, err := prover.BuildMigrationWitness(opening, p, typeRandomness)
	require.Error(t, err, "nil BlindingFactor must be rejected by PedersenOpening.Validate()")
}

func TestBuildMigrationWitness_NonCanonicalBlindingFactor_Error(t *testing.T) {
	// All-0xFF exceeds the BLS12-381 Fr modulus, SetBytesCanonical must reject it.
	badBF := make([]byte, 32)
	for i := range badBF {
		badBF[i] = 0xFF
	}

	opening := snarktoken.PedersenOpening{
		Value:           100,
		TokenType:       "USD",
		BlindingFactor:  badBF,
		CommitmentBytes: onCurveG1Bytes(t),
	}

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()

	p := pp.DefaultPublicParams()
	_, err := prover.BuildMigrationWitness(opening, p, typeRandomness)
	require.Error(t, err, "a non-canonical blinding factor must be rejected")
}

func TestBuildMigrationWitness_GarbageCommitmentBytes_Error(t *testing.T) {
	opening := snarktoken.PedersenOpening{
		Value:           100,
		TokenType:       "USD",
		BlindingFactor:  randomCanonicalFr(t),
		CommitmentBytes: []byte("not a valid G1 point at all"),
	}

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()

	p := pp.DefaultPublicParams()
	_, err := prover.BuildMigrationWitness(opening, p, typeRandomness)
	require.Error(t, err, "garbage commitment bytes must be rejected")
}

// ── DefaultPedersenGeneratorCoords / PedersenGeneratorCoordsFrom ──────────────

func TestDefaultPedersenGeneratorCoords_Deterministic(t *testing.T) {
	g1 := prover.DefaultPedersenGeneratorCoords()
	g2 := prover.DefaultPedersenGeneratorCoords()

	require.Equal(t, 0, g1.G0X.Cmp(g2.G0X), "G0X must be deterministic")
	require.Equal(t, 0, g1.G0Y.Cmp(g2.G0Y), "G0Y must be deterministic")
	require.Equal(t, 0, g1.G1X.Cmp(g2.G1X), "G1X must be deterministic")
	require.Equal(t, 0, g1.G1Y.Cmp(g2.G1Y), "G1Y must be deterministic")
	require.Equal(t, 0, g1.G2X.Cmp(g2.G2X), "G2X must be deterministic")
	require.Equal(t, 0, g1.G2Y.Cmp(g2.G2Y), "G2Y must be deterministic")
}

func TestPedersenGeneratorCoordsFrom_DifferentDrivers_DifferentCoords(t *testing.T) {
	g1 := prover.PedersenGeneratorCoordsFrom("driver-alpha", 1)
	g2 := prover.PedersenGeneratorCoordsFrom("driver-beta", 1)

	// At least one coordinate pair must differ.
	require.NotEqual(t, 0, g1.G0X.Cmp(g2.G0X),
		"different driver names must produce different Pedersen generators")
}

func TestPedersenGeneratorCoordsFrom_DifferentVersions_DifferentCoords(t *testing.T) {
	g1 := prover.PedersenGeneratorCoordsFrom("zkatdlognogh", 1)
	g2 := prover.PedersenGeneratorCoordsFrom("zkatdlognogh", 2)

	require.NotEqual(t, 0, g1.G0X.Cmp(g2.G0X),
		"different driver versions must produce different Pedersen generators")
}

// ── Orchestrator error paths ──────────────────────────────────────────────────

func TestOrchestrator_BuildTransferAction_NoInputsNoOutputs(t *testing.T) {
	setupProvers(t)
	o := prover.NewOrchestrator(spendProver, outputProver)

	_, _, err := o.BuildTransferAction(
		context.Background(),
		nil,
		nil,
		"USD",
		testPP,
	)
	require.Error(t, err, "empty inputs and outputs must be rejected")
}

func TestOrchestrator_BuildIssueAction_NoOutputs(t *testing.T) {
	setupProvers(t)
	o := prover.NewOrchestrator(spendProver, outputProver)

	_, _, err := o.BuildIssueAction(
		context.Background(),
		[]byte("issuer"),
		nil,
		"USD",
		testPP,
	)
	require.Error(t, err, "zero outputs must be rejected")
}

func TestOrchestrator_BuildMigrationAction_NoProver(t *testing.T) {
	setupProvers(t)
	// Create an orchestrator WITHOUT a migration prover.
	o := prover.NewOrchestrator(spendProver, outputProver)

	_, _, err := o.BuildMigrationAction(
		context.Background(),
		[]prover.MigrationRequest{{
			Opening: snarktoken.PedersenOpening{
				Value:           100,
				TokenType:       "USD",
				BlindingFactor:  randomCanonicalFr(t),
				CommitmentBytes: onCurveG1Bytes(t),
			},
			Recipient: []byte("alice"),
		}},
		testPP,
	)
	require.Error(t, err, "orchestrator without migration prover must return error")
}

func TestOrchestrator_BuildMigrationAction_NoRequests(t *testing.T) {
	setupProvers(t)
	o := prover.NewOrchestrator(spendProver, outputProver)

	_, _, err := o.BuildMigrationAction(context.Background(), nil, testPP)
	require.Error(t, err, "empty migration request list must be rejected")
}

func TestOrchestrator_BuildMigrationAction_Valid(t *testing.T) {
	p := pp.DefaultPublicParams()
	gens := prover.DefaultPedersenGeneratorCoords()
	cs, err := setup.CompileMigrationCircuit(p, gens)
	require.NoError(t, err)

	pk, _, err := groth16.Setup(cs)
	require.NoError(t, err)

	migProver := prover.NewMigrationProver(cs, pk)

	o := prover.NewOrchestrator(nil, nil)
	o.SetMigrationProver(migProver)

	bf := randomCanonicalFr(t)
	commitBytes := computeValidPedersenCommit(t, 100, "USD", bf)

	opening := snarktoken.PedersenOpening{
		Value:           100,
		TokenType:       "USD",
		BlindingFactor:  bf,
		CommitmentBytes: commitBytes,
	}

	actions, notes, err := o.BuildMigrationAction(
		context.Background(),
		[]prover.MigrationRequest{{
			Opening:   opening,
			Recipient: []byte("alice"),
		}},
		p,
	)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Len(t, notes, 1)
}
