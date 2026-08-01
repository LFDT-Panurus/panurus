/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"context"
	"sync"
	"testing"
	"crypto/sha256"
	"math/big"
	"strconv"

	"github.com/stretchr/testify/require"
	mathlib "github.com/IBM/mathlib"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/validator"
)

var (
	testPP       *pp.PublicParams
	testOrch     *prover.Orchestrator
	testVal      *validator.Validator
	e2eSetupOnce sync.Once
 	migrationE2EOnce sync.Once
)

// setupEndToEnd compiles both circuits, runs a real (local) trusted setup,
// and constructs a real Orchestrator (prover side) and a real Validator
// (this layer) against the SAME PublicParams — exactly the shape a
// production deployment takes, just with keys generated locally rather
// than distributed via channel configuration.
func setupEndToEnd(t *testing.T) {
	t.Helper()
	e2eSetupOnce.Do(func() {
		testPP = pp.DefaultPublicParams()

		var err error
		testPP, err = setup.SetupAll(testPP)
		if err != nil {
			panic(err)
		}

		spendCS, err := setup.CompileSpendCircuit(testPP)
		if err != nil {
			panic(err)
		}
		spendPK, err := setup.LoadProvingKey(testPP, setup.CircuitSpend)
		if err != nil {
			panic(err)
		}
		spendProver := prover.NewSpendProver(spendCS, spendPK)

		outputCS, err := setup.CompileOutputCircuit(testPP)
		if err != nil {
			panic(err)
		}
		outputPK, err := setup.LoadProvingKey(testPP, setup.CircuitOutput)
		if err != nil {
			panic(err)
		}
		outputProver := prover.NewOutputProver(outputCS, outputPK)

		testOrch = prover.NewOrchestrator(spendProver, outputProver)

		testVal, err = validator.NewValidator(testPP)
		if err != nil {
			panic(err)
		}
	})
}

// computeTestPedersenCommitment computes a genuine zkatdlog-style Pedersen
// commitment matching what prover.DefaultPedersenGeneratorCoords derives
// internally (seeded on "zkatdlognogh", version 1) — so a PedersenOpening
// built from this function's output is exactly what BuildMigrationWitness
// expects to be able to open.
//
// This duplicates logic already present in circuit/migration_test.go's
// computePedersenCommitment. Worth extracting both (and any future copy in
// prover tests) into a small shared internal test-helper package rather
// than tripling this derivation across the codebase.
//
// SCOPE: proves Layer 3 and Layer 4 agree with EACH OTHER using whatever
// generator/hash derivation is currently wired in. Does NOT prove
// compatibility with real, already-deployed zkatdlog commitments — see the
// still-open generator-authenticity concern from the migration circuit
// review.
func computeTestPedersenCommitment(t *testing.T, value uint64, tokenType string, blindingFactor fr.Element) []byte {
	t.Helper()

	curve := mathlib.Curves[mathlib.BLS12_381]
	const driverName, driverVersion = "zkatdlognogh", 1

	gens := make([]*mathlib.G1, 3)
	for i := range 3 {
		seed := "lfdt-panurus." + driverName + "." + strconv.Itoa(driverVersion) + ".PedersenGenerators." + strconv.Itoa(i)
		gens[i] = curve.HashToG1([]byte(seed))
	}

	digest := sha256.Sum256([]byte(tokenType))
	digestBig := new(big.Int).SetBytes(digest[:])
	digestBig.Mod(digestBig, fr.Modulus())
	var tokenTypePed fr.Element
	tokenTypePed.SetBigInt(digestBig)
	typeScalar := curve.NewZrFromBytes(tokenTypePed.Marshal())

	var vField fr.Element
	vField.SetUint64(value)
	valueScalar := curve.NewZrFromBytes(vField.Marshal())

	bfScalar := curve.NewZrFromBytes(blindingFactor.Marshal())

	com := curve.NewG1()
	com.Add(gens[0].Mul(typeScalar))
	com.Add(gens[1].Mul(valueScalar))
	com.Add(gens[2].Mul(bfScalar))

	return com.Bytes()
}

// setupMigrationEndToEnd extends setupEndToEnd with a real MigrationCircuit
// trusted setup and wires a MigrationProver into the shared Orchestrator.
func setupMigrationEndToEnd(t *testing.T) {
	t.Helper()
	migrationE2EOnce.Do(func() {
		setupEndToEnd(t)

		gens := prover.DefaultPedersenGeneratorCoords()

		var err error
		testPP, err = setup.SetupMigration(testPP, gens)
		if err != nil {
			panic(err)
		}

		migCS, err := setup.CompileMigrationCircuit(testPP, gens)
		if err != nil {
			panic(err)
		}
		migPK, err := setup.LoadProvingKey(testPP, setup.CircuitMigration)
		if err != nil {
			panic(err)
		}
		testOrch.SetMigrationProver(prover.NewMigrationProver(migCS, migPK))

		// Rebuild the Validator so it picks up the now-populated
		// vkMigration — testVal was originally constructed by
		// setupEndToEnd before migration setup ever ran.
		testVal, err = validator.NewValidator(testPP)
		if err != nil {
			panic(err)
		}
	})
}

// TestValidateTransfer_EndToEnd is the single most important test in this
// package. Layer 3's Orchestrator produces an action; this layer's
// Validator must accept it, using nothing but the wire-format bytes.
func TestValidateTransfer_EndToEnd(t *testing.T) {
	setupEndToEnd(t)

	note1, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)
	note2, err := snarktoken.NewRandomNote(50, "USD")
	require.NoError(t, err)

	action, _, err := testOrch.BuildTransferAction(
		context.Background(),
		[]prover.SpendRequest{{Note: note1}, {Note: note2}},
		[]prover.OutputRequest{
			{Value: 80, TokenType: "USD", Recipient: []byte("bob")},
			{Value: 70, TokenType: "USD", Recipient: []byte("alice-change")},
		},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	err = testVal.ValidateTransfer(action)
	require.NoError(t, err, "a genuinely valid TransferAction produced by the Orchestrator must validate")
}

// TestValidateIssue_EndToEnd exercises the issuance path, including
// TotalValue round-tripping correctly through the wire format.
func TestValidateIssue_EndToEnd(t *testing.T) {
	setupEndToEnd(t)

	action, _, err := testOrch.BuildIssueAction(
		context.Background(),
		[]byte("issuer-identity"),
		[]prover.OutputRequest{{Value: 500, TokenType: "USD", Recipient: []byte("alice")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	err = testVal.ValidateIssue(action)
	require.NoError(t, err, "a genuinely valid IssueAction produced by the Orchestrator must validate")
}

// TestValidateTransfer_RejectsTamperedCommitment demonstrates that
// corrupting a single public byte after proof generation is caught: the
// public witness reconstructed from the corrupted bytes no longer matches
// what the proof was actually generated against.
func TestValidateTransfer_RejectsTamperedCommitment(t *testing.T) {
	setupEndToEnd(t)

	note, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)

	action, _, err := testOrch.BuildTransferAction(
		context.Background(),
		[]prover.SpendRequest{{Note: note}},
		[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	action.Inputs[0].CommitmentIn[0] ^= 0xFF // well-formed but wrong 32-byte value

	err = testVal.ValidateTransfer(action)
	require.Error(t, err, "a tampered commitment must be rejected")
}

// TestValidateTransfer_RejectsWrongTokenType exercises the homogeneity
// check independently of proof verification.
func TestValidateTransfer_RejectsWrongTokenType(t *testing.T) {
	setupEndToEnd(t)

	note, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)

	action, _, err := testOrch.BuildTransferAction(
		context.Background(),
		[]prover.SpendRequest{{Note: note}},
		[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	action.TokenType = "EUR" // declared type now disagrees with every description's actual encoded type

	err = testVal.ValidateTransfer(action)
	require.Error(t, err, "a declared type mismatching every description's actual type must be rejected")
}

// TestValidateTransfer_RejectsCorruptedBindingSignature confirms that
// tampering with the signature itself, as opposed to the commitments it
// covers, is caught independently of proof verification.
func TestValidateTransfer_RejectsCorruptedBindingSignature(t *testing.T) {
	setupEndToEnd(t)

	note, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)

	action, _, err := testOrch.BuildTransferAction(
		context.Background(),
		[]prover.SpendRequest{{Note: note}},
		[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	action.BindingSignature[0] ^= 0xFF

	err = testVal.ValidateTransfer(action)
	require.Error(t, err, "a corrupted binding signature must be rejected")
}

// TestValidateTransfer_MalformedShape confirms structural checks reject
// obviously wrong-length fields before any cryptography runs at all.
func TestValidateTransfer_MalformedShape(t *testing.T) {
	setupEndToEnd(t)

	action := &snarktoken.TransferAction{
		TokenType: "USD",
		Inputs: []snarktoken.SpendDescription{
			{CommitmentIn: []byte("too short")},
		},
	}

	err := testVal.ValidateTransfer(action)
	require.Error(t, err, "structurally malformed action must be rejected before any decode/crypto step")
}

func TestValidateMigration_EndToEnd(t *testing.T) {
	setupMigrationEndToEnd(t)

	var blindingFactor fr.Element
	_, err := blindingFactor.SetRandom()
	require.NoError(t, err)
	bfBytes := blindingFactor.Bytes()

	commitmentBytes := computeTestPedersenCommitment(t, 250, "USD", blindingFactor)

	opening := snarktoken.PedersenOpening{
		Value:           250,
		TokenType:       "USD",
		BlindingFactor:  bfBytes[:],
		CommitmentBytes: commitmentBytes,
	}

	actions, _, err := testOrch.BuildMigrationAction(
		context.Background(),
		[]prover.MigrationRequest{{Opening: opening, Recipient: []byte("alice")}},
		testPP,
	)
	require.NoError(t, err)
	require.Len(t, actions, 1)

	err = testVal.ValidateMigration(&actions[0])
	require.NoError(t, err, "a genuine migration, proven by the Orchestrator, must validate")
}

func TestValidateMigration_RejectsTamperedCommitment(t *testing.T) {
	setupMigrationEndToEnd(t)

	var blindingFactor fr.Element
	_, err := blindingFactor.SetRandom()
	require.NoError(t, err)
	bfBytes := blindingFactor.Bytes()
	commitmentBytes := computeTestPedersenCommitment(t, 100, "USD", blindingFactor)

	opening := snarktoken.PedersenOpening{
		Value: 100, TokenType: "USD",
		BlindingFactor: bfBytes[:], CommitmentBytes: commitmentBytes,
	}
	actions, _, err := testOrch.BuildMigrationAction(
		context.Background(),
		[]prover.MigrationRequest{{Opening: opening, Recipient: []byte("bob")}},
		testPP,
	)
	require.NoError(t, err)

	actions[0].CommitmentMiMC[0] ^= 0xFF

	err = testVal.ValidateMigration(&actions[0])
	require.Error(t, err, "a tampered MiMC commitment must be rejected")
}

// TestValidateMigration_NotConfiguredWithoutSetup confirms the "migration
// never set up" path fails closed with a specific, distinguishable error,
// rather than silently skipping the check or panicking.
func TestValidateMigration_NotConfiguredWithoutSetup(t *testing.T) {
	freshPP := pp.DefaultPublicParams()
	freshPP, err := setup.SetupAll(freshPP) // Spend/Output only — no migration
	require.NoError(t, err)

	freshVal, err := validator.NewValidator(freshPP)
	require.NoError(t, err)

	err = freshVal.ValidateMigration(&snarktoken.MigrationAction{})
	require.ErrorIs(t, err, validator.ErrMigrationNotConfigured)
}
