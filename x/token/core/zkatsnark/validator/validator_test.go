/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"strconv"
	"sync"
	"testing"

	mathlib "github.com/IBM/mathlib"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	token2 "github.com/LFDT-Panurus/panurus/token/token"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark/frontend"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/backend"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/validator"
)

var (
	testPP           *pp.PublicParams
	testOrch         *prover.Orchestrator
	testVal          *validator.Validator
	e2eSetupOnce     sync.Once
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
		b, err := backend.NewProofSystemBackend(testPP.ProofSystem, testPP.Curve)
		if err != nil {
			panic(err)
		}
		spendPKBytes, err := setup.LoadProvingKeyBytes(testPP, setup.CircuitSpend)
		if err != nil {
			panic(err)
		}
		spendProver := prover.NewSpendProver(spendCS, spendPKBytes, b, testPP)

		outputCS, err := setup.CompileOutputCircuit(testPP)
		if err != nil {
			panic(err)
		}
		outputPKBytes, err := setup.LoadProvingKeyBytes(testPP, setup.CircuitOutput)
		if err != nil {
			panic(err)
		}
		outputProver := prover.NewOutputProver(outputCS, outputPKBytes, b, testPP)

		testOrch = prover.NewOrchestrator(spendProver, outputProver, testPP)

		testVal, err = validator.NewValidator(testPP, nil, driver.ResourceLimits{}.WithDefaults())
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
		migPKBytes, err := setup.LoadProvingKeyBytes(testPP, setup.CircuitMigration)
		if err != nil {
			panic(err)
		}
		b, err := backend.NewProofSystemBackend(testPP.ProofSystem, testPP.Curve)
		if err != nil {
			panic(err)
		}
		testOrch.SetMigrationProver(prover.NewMigrationProver(migCS, migPKBytes, b, testPP))

		// Rebuild the Validator so it picks up the now-populated
		// vkMigration — testVal was originally constructed by
		// setupEndToEnd before migration setup ever ran.
		testVal, err = validator.NewValidator(testPP, nil, driver.ResourceLimits{}.WithDefaults())
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

// TestValidateTransfer_RejectsWrongTypeCommitment exercises the homogeneity
// check independently of proof verification.
func TestValidateTransfer_RejectsWrongTypeCommitment(t *testing.T) {
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

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()
	tr := typeRandomness.Bytes()
	tcEUR, _ := snarktoken.ComputeTypeCommitment("EUR", tr[:], testPP.Curve)

	action.TypeCommitment = tcEUR // declared type commitment now disagrees with every description's actual encoded type

	err = testVal.ValidateTransfer(action)
	require.Error(t, err, "a declared type commitment mismatching every description's actual type must be rejected")
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
		TypeCommitment: make([]byte, 32),
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

	freshVal, err := validator.NewValidator(freshPP, nil, driver.ResourceLimits{}.WithDefaults())
	require.NoError(t, err)

	err = freshVal.ValidateMigration(&snarktoken.MigrationAction{})
	require.ErrorIs(t, err, validator.ErrMigrationNotConfigured)
}

func setupE2EWithParams(t *testing.T, p *pp.PublicParams) (*prover.Orchestrator, *validator.Validator) {
	t.Helper()
	var err error
	p, err = setup.SetupAll(p)
	require.NoError(t, err)

	spendCS, err := setup.CompileSpendCircuit(p)
	require.NoError(t, err)

	b, err := backend.NewProofSystemBackend(p.ProofSystem, p.Curve)
	require.NoError(t, err)

	spendPKBytes, err := setup.LoadProvingKeyBytes(p, setup.CircuitSpend)
	require.NoError(t, err)

	spendProver := prover.NewSpendProver(spendCS, spendPKBytes, b, p)

	outputCS, err := setup.CompileOutputCircuit(p)
	require.NoError(t, err)

	outputPKBytes, err := setup.LoadProvingKeyBytes(p, setup.CircuitOutput)
	require.NoError(t, err)

	outputProver := prover.NewOutputProver(outputCS, outputPKBytes, b, p)

	orch := prover.NewOrchestrator(spendProver, outputProver, p)
	val, err := validator.NewValidator(p, nil, driver.ResourceLimits{}.WithDefaults())
	require.NoError(t, err)

	return orch, val
}

// TestValidateTransfer_EndToEnd_Plonk tests the complete Orchestrator -> Validator
// pipeline under the non-default Plonk proof system on BLS12-381.
func TestValidateTransfer_EndToEnd_Plonk(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "plonk"

	orch, val := setupE2EWithParams(t, p)

	note1, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)
	note2, err := snarktoken.NewRandomNote(50, "USD")
	require.NoError(t, err)

	action, _, err := orch.BuildTransferAction(
		context.Background(),
		[]prover.SpendRequest{{Note: note1}, {Note: note2}},
		[]prover.OutputRequest{
			{Value: 80, TokenType: "USD", Recipient: []byte("bob")},
			{Value: 70, TokenType: "USD", Recipient: []byte("alice-change")},
		},
		"USD",
		p,
	)
	require.NoError(t, err)
	require.Len(t, action.Inputs, 2)
	require.Len(t, action.Outputs, 2)

	err = val.ValidateTransfer(action)
	require.NoError(t, err, "a genuinely valid Plonk TransferAction produced by the Orchestrator must validate")

	t.Logf("Plonk BLS12-381 SpendProof length: %d bytes", len(action.Inputs[0].SpendProof))
	t.Logf("Plonk BLS12-381 OutputProof length: %d bytes", len(action.Outputs[0].OutputProof))
}

// TestValidateProof_BN254_Groth16AndPlonk tests that spend proofs
// compile, prove, and verify cleanly under the non-default BN254 curve with both
// Groth16 and Plonk backends.
func TestValidateProof_BN254_Groth16AndPlonk(t *testing.T) {
	for _, ps := range []string{"groth16", "plonk"} {
		t.Run(ps, func(t *testing.T) {
			p := pp.DefaultPublicParams()
			p.Curve = ecc.BN254
			p.ProofSystem = ps

			p, err := setup.SetupAll(p)
			require.NoError(t, err)

			b, err := backend.NewProofSystemBackend(p.ProofSystem, p.Curve)
			require.NoError(t, err)

			spendCS, err := setup.CompileSpendCircuit(p)
			require.NoError(t, err)

			spendPK, err := setup.LoadProvingKeyBytes(p, setup.CircuitSpend)
			require.NoError(t, err)

			spendProver := prover.NewSpendProver(spendCS, spendPK, b, p)

			note, err := snarktoken.NewRandomNote(100, "USD")
			require.NoError(t, err)

			tr := make([]byte, 32)
			tr[0] = 1

			wRes, err := prover.BuildSpendWitness(note, tr, p)
			require.NoError(t, err)

			proof, err := spendProver.Prove(wRes.Assignment)
			require.NoError(t, err)

			t.Logf("BN254 %s SpendProof length: %d bytes", ps, len(proof))

			pubW, err := frontend.NewWitness(wRes.Assignment, p.Curve.ScalarField(), frontend.PublicOnly())
			require.NoError(t, err)

			vkSpend, err := setup.LoadVerifyingKeyBytes(p, setup.CircuitSpend)
			require.NoError(t, err)

			err = b.Verify(vkSpend, proof, pubW)
			require.NoError(t, err, "BN254 %s spend proof must verify", ps)
		})
	}
}

// TestValidateIssue_RejectsTamperedOutput demonstrates that corrupting an
// output commitment byte causes the proof to fail verification.
func TestValidateIssue_RejectsTamperedOutput(t *testing.T) {
	setupEndToEnd(t)

	action, _, err := testOrch.BuildIssueAction(
		context.Background(),
		[]byte("issuer-identity"),
		[]prover.OutputRequest{{Value: 500, TokenType: "USD", Recipient: []byte("alice")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	action.Outputs[0].CommitmentOut[0] ^= 0xFF

	err = testVal.ValidateIssue(action)
	require.Error(t, err, "a tampered output commitment must be rejected")
}

// TestValidateIssue_RejectsCorruptedBindingSignature confirms that
// tampering with the binding signature on an issue action is caught.
func TestValidateIssue_RejectsCorruptedBindingSignature(t *testing.T) {
	setupEndToEnd(t)

	action, _, err := testOrch.BuildIssueAction(
		context.Background(),
		[]byte("issuer-identity"),
		[]prover.OutputRequest{{Value: 500, TokenType: "USD", Recipient: []byte("alice")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	action.BindingSignature[0] ^= 0xFF

	err = testVal.ValidateIssue(action)
	require.Error(t, err, "a corrupted binding signature on an issue action must be rejected")
}

// TestValidateIssue_MalformedShape confirms that a structurally malformed
// issue action is rejected before any cryptographic validation.
func TestValidateIssue_MalformedShape(t *testing.T) {
	setupEndToEnd(t)

	action := &snarktoken.IssueAction{
		TypeCommitment: make([]byte, 32),
		TotalValue:     make([]byte, 32),
		// No outputs — structurally invalid.
	}

	err := testVal.ValidateIssue(action)
	require.Error(t, err, "structurally malformed issue action must be rejected")
}

// TestValidateIssue_RejectsNonCanonicalTotalValue confirms that a
// non-canonical TotalValue field is rejected.
func TestValidateIssue_RejectsNonCanonicalTotalValue(t *testing.T) {
	setupEndToEnd(t)

	action, _, err := testOrch.BuildIssueAction(
		context.Background(),
		[]byte("issuer-identity"),
		[]prover.OutputRequest{{Value: 500, TokenType: "USD", Recipient: []byte("alice")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	// Set TotalValue to a non-canonical value (all 0xFF > field modulus)
	nonCanonical := make([]byte, 32)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	action.TotalValue = nonCanonical

	err = testVal.ValidateIssue(action)
	require.Error(t, err, "a non-canonical TotalValue must be rejected")
}

// TestValidateTransfer_RejectsNonCanonicalTypeCommitment confirms
// that non-canonical TypeCommitment bytes are caught.
func TestValidateTransfer_RejectsNonCanonicalTypeCommitment(t *testing.T) {
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

	// Set TypeCommitment to non-canonical
	nonCanonical := make([]byte, 32)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	action.TypeCommitment = nonCanonical

	err = testVal.ValidateTransfer(action)
	require.Error(t, err, "non-canonical TypeCommitment bytes must be rejected")
}

// TestValidateMigration_MalformedShape confirms structural rejection
// before cryptographic work.
func TestValidateMigration_MalformedShape(t *testing.T) {
	setupMigrationEndToEnd(t)

	action := &snarktoken.MigrationAction{
		CommitmentPedersenX: make([]byte, 32), // wrong length (should be 48)
	}

	err := testVal.ValidateMigration(action)
	require.Error(t, err, "malformed migration action must be rejected")
}

// ── Callback entry-point tests ──────────────────────────────────────────────
//
// These test the TransferZKValidate / IssueZKValidate methods (the callback
// versions) by constructing a Context directly. This covers the 0%-coverage
// callback functions that are normally only invoked via common.Validator's
// VerifyTokenRequest path.

func TestTransferZKValidate_Valid(t *testing.T) {
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
			{Value: 70, TokenType: "USD", Recipient: []byte("alice")},
		},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	ctx := &validator.Context{
		PP:             testPP,
		TransferAction: action,
	}

	err = testVal.TransferZKValidate(context.Background(), ctx)
	require.NoError(t, err, "TransferZKValidate callback must succeed for a valid action")
}

func TestTransferZKValidate_TamperedProof(t *testing.T) {
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

	action.Inputs[0].SpendProof[0] ^= 0xFF

	ctx := &validator.Context{
		PP:             testPP,
		TransferAction: action,
	}

	err = testVal.TransferZKValidate(context.Background(), ctx)
	require.Error(t, err, "TransferZKValidate must reject a tampered spend proof")
}

func TestTransferZKValidate_MalformedShape(t *testing.T) {
	setupEndToEnd(t)

	action := &snarktoken.TransferAction{
		TypeCommitment: make([]byte, 32),
		// No inputs or outputs — structurally invalid.
	}

	ctx := &validator.Context{
		PP:             testPP,
		TransferAction: action,
	}

	err := testVal.TransferZKValidate(context.Background(), ctx)
	require.Error(t, err, "TransferZKValidate must reject a malformed action")
}

func TestIssueZKValidate_Valid(t *testing.T) {
	setupEndToEnd(t)

	action, _, err := testOrch.BuildIssueAction(
		context.Background(),
		[]byte("issuer"),
		[]prover.OutputRequest{{Value: 500, TokenType: "USD", Recipient: []byte("alice")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	ctx := &validator.Context{
		PP:          testPP,
		IssueAction: action,
	}

	err = testVal.IssueZKValidate(context.Background(), ctx)
	require.NoError(t, err, "IssueZKValidate callback must succeed for a valid action")
}

func TestIssueZKValidate_TamperedBinding(t *testing.T) {
	setupEndToEnd(t)

	action, _, err := testOrch.BuildIssueAction(
		context.Background(),
		[]byte("issuer"),
		[]prover.OutputRequest{{Value: 500, TokenType: "USD", Recipient: []byte("alice")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	action.BindingSignature[0] ^= 0xFF

	ctx := &validator.Context{
		PP:          testPP,
		IssueAction: action,
	}

	err = testVal.IssueZKValidate(context.Background(), ctx)
	require.Error(t, err, "IssueZKValidate must reject a tampered binding signature")
}

func TestIssueZKValidate_MalformedShape(t *testing.T) {
	setupEndToEnd(t)

	action := &snarktoken.IssueAction{
		TypeCommitment: make([]byte, 32),
		TotalValue:     make([]byte, 32),
		// No outputs — structurally invalid.
	}

	ctx := &validator.Context{
		PP:          testPP,
		IssueAction: action,
	}

	err := testVal.IssueZKValidate(context.Background(), ctx)
	require.Error(t, err, "IssueZKValidate must reject a malformed action")
}

// ── DeserializeActions ──────────────────────────────────────────────────────

func TestDeserializeActions_RoundTrip(t *testing.T) {
	setupEndToEnd(t)

	issueAction, _, err := testOrch.BuildIssueAction(
		context.Background(),
		[]byte("issuer"),
		[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("alice")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)
	issueBytes, err := issueAction.Serialize()
	require.NoError(t, err)

	note, err := snarktoken.NewRandomNote(50, "USD")
	require.NoError(t, err)
	transferAction, _, err := testOrch.BuildTransferAction(
		context.Background(),
		[]prover.SpendRequest{{Note: note}},
		[]prover.OutputRequest{{Value: 50, TokenType: "USD", Recipient: []byte("bob")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)
	transferBytes, err := transferAction.Serialize()
	require.NoError(t, err)

	tr := &driver.TokenRequest{
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: issueBytes},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: transferBytes},
		},
	}

	ad := &validator.ActionDeserializer{}
	issues, transfers, err := ad.DeserializeActions(tr)
	require.NoError(t, err)
	require.Len(t, issues, 1)
	require.Len(t, transfers, 1)

	require.Equal(t, issueAction.TokenType, issues[0].TokenType)
	require.Len(t, transfers[0].Inputs, 1)
}

func TestDeserializeActions_CorruptIssue(t *testing.T) {
	tr := &driver.TokenRequest{
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("garbage")},
		},
	}

	ad := &validator.ActionDeserializer{}
	_, _, err := ad.DeserializeActions(tr)
	require.Error(t, err)
}

func TestDeserializeActions_CorruptTransfer(t *testing.T) {
	tr := &driver.TokenRequest{
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("garbage")},
		},
	}

	ad := &validator.ActionDeserializer{}
	_, _, err := ad.DeserializeActions(tr)
	require.Error(t, err)
}

func TestDeserializeActions_Empty(t *testing.T) {
	tr := &driver.TokenRequest{}

	ad := &validator.ActionDeserializer{}
	issues, transfers, err := ad.DeserializeActions(tr)
	require.NoError(t, err)
	require.Empty(t, issues)
	require.Empty(t, transfers)
}

// ── TransferSignatureValidate error paths ───────────────────────────────────

func TestTransferSignatureValidate_NoInputs(t *testing.T) {
	action := &snarktoken.TransferAction{
		// No inputs.
		TypeCommitment: make([]byte, 32),
	}

	ctx := &validator.Context{
		PP:             testPP,
		TransferAction: action,
	}

	err := validator.TransferSignatureValidate(context.Background(), ctx)
	require.ErrorIs(t, err, validator.ErrInvalidInputs)
}

func TestTransferSignatureValidate_MissingInputIDs(t *testing.T) {
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

	// Clear InputIDs so the count mismatches.
	action.InputIDs = nil

	ctx := &validator.Context{
		PP:             testPP,
		TransferAction: action,
	}

	err = validator.TransferSignatureValidate(context.Background(), ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, validator.ErrMissingInputIDs)
}

func TestTransferSignatureValidate_NilInputID(t *testing.T) {
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

	action.InputIDs = []*token2.ID{nil}

	ctx := &validator.Context{
		PP:             testPP,
		TransferAction: action,
	}

	err = validator.TransferSignatureValidate(context.Background(), ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, validator.ErrNilInputID)
}

func TestTransferSignatureValidate_LedgerError(t *testing.T) {
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
	action.InputIDs = []*token2.ID{{TxId: "tx1", Index: 0}}

	mockLedger := &drivermock.Ledger{}
	mockLedger.GetStateReturns(nil, errors.New("ledger error"))

	ctx := &validator.Context{
		PP:             testPP,
		TransferAction: action,
		Ledger:         mockLedger,
	}

	err = validator.TransferSignatureValidate(context.Background(), ctx)
	require.Error(t, err)
}

func TestTransferSignatureValidate_CommitmentMismatch(t *testing.T) {
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
	action.InputIDs = []*token2.ID{{TxId: "tx1", Index: 0}}

	badDesc := snarktoken.OutputDescription{
		Recipient:     []byte("bob"),
		CommitmentOut: make([]byte, 32),
	}
	raw, err := json.Marshal(badDesc)
	require.NoError(t, err)

	mockLedger := &drivermock.Ledger{}
	mockLedger.GetStateReturns(raw, nil)

	ctx := &validator.Context{
		PP:             testPP,
		TransferAction: action,
		Ledger:         mockLedger,
	}

	err = validator.TransferSignatureValidate(context.Background(), ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, validator.ErrCommitmentMismatch)
}

func TestTransferSignatureValidate_NilSignatureProvider(t *testing.T) {
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
	action.InputIDs = []*token2.ID{{TxId: "tx1", Index: 0}}

	desc := snarktoken.OutputDescription{
		Recipient:     []byte("bob"),
		CommitmentOut: action.Inputs[0].CommitmentIn,
	}
	raw, err := json.Marshal(desc)
	require.NoError(t, err)

	mockLedger := &drivermock.Ledger{}
	mockLedger.GetStateReturns(raw, nil)

	mockDes := &drivermock.Deserializer{}
	mockDes.GetOwnerVerifierReturns(&drivermock.Verifier{}, nil)

	ctx := &validator.Context{
		PP:                testPP,
		TransferAction:    action,
		Ledger:            mockLedger,
		Deserializer:      mockDes,
		SignatureProvider: nil,
	}

	err = validator.TransferSignatureValidate(context.Background(), ctx)
	require.ErrorIs(t, err, common.ErrNilSignatureProvider)
}

func TestTransferSignatureValidate_Success(t *testing.T) {
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
	action.InputIDs = []*token2.ID{{TxId: "tx1", Index: 0}}

	desc := snarktoken.OutputDescription{
		Recipient:     []byte("bob"),
		CommitmentOut: action.Inputs[0].CommitmentIn,
	}
	raw, err := json.Marshal(desc)
	require.NoError(t, err)

	mockLedger := &drivermock.Ledger{}
	mockLedger.GetStateReturns(raw, nil)

	mockDes := &drivermock.Deserializer{}
	mockDes.GetOwnerVerifierReturns(&drivermock.Verifier{}, nil)

	mockSigProv := &drivermock.SignatureProvider{}
	mockSigProv.HasBeenSignedByReturns([]byte("valid-sig"), nil)

	ctx := &validator.Context{
		PP:                testPP,
		TransferAction:    action,
		Ledger:            mockLedger,
		Deserializer:      mockDes,
		SignatureProvider: mockSigProv,
	}

	err = validator.TransferSignatureValidate(context.Background(), ctx)
	require.NoError(t, err)
	require.Len(t, ctx.InputTokens, 1)
	require.Len(t, ctx.Signatures, 1)
}

func TestTransferSignatureValidate_Redeem_MissingIssuer(t *testing.T) {
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
	action.InputIDs = []*token2.ID{{TxId: "tx1", Index: 0}}
	action.Outputs[0].Recipient = nil // Make it a redeem output

	desc := snarktoken.OutputDescription{
		Recipient:     []byte("bob"),
		CommitmentOut: action.Inputs[0].CommitmentIn,
	}
	raw, err := json.Marshal(desc)
	require.NoError(t, err)

	mockLedger := &drivermock.Ledger{}
	mockLedger.GetStateReturns(raw, nil)

	mockDes := &drivermock.Deserializer{}
	mockDes.GetOwnerVerifierReturns(&drivermock.Verifier{}, nil)

	ppWithIssuers := *testPP
	ppWithIssuers.IssuerIdentities = []driver.Identity{driver.Identity("issuer1")}

	ctx := &validator.Context{
		PP:                &ppWithIssuers,
		TransferAction:    action,
		Ledger:            mockLedger,
		Deserializer:      mockDes,
		SignatureProvider: &drivermock.SignatureProvider{},
	}

	err = validator.TransferSignatureValidate(context.Background(), ctx)
	require.ErrorIs(t, err, validator.ErrMissingIssuer)
}

func TestTransferSignatureValidate_Redeem_Success(t *testing.T) {
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
	action.InputIDs = []*token2.ID{{TxId: "tx1", Index: 0}}
	action.Outputs[0].Recipient = nil // Make it a redeem output
	action.Issuer = []byte("issuer1")

	desc := snarktoken.OutputDescription{
		Recipient:     []byte("bob"),
		CommitmentOut: action.Inputs[0].CommitmentIn,
	}
	raw, err := json.Marshal(desc)
	require.NoError(t, err)

	mockLedger := &drivermock.Ledger{}
	mockLedger.GetStateReturns(raw, nil)

	mockDes := &drivermock.Deserializer{}
	mockDes.GetOwnerVerifierReturns(&drivermock.Verifier{}, nil)
	mockDes.GetIssuerVerifierReturns(&drivermock.Verifier{}, nil)

	mockSigProv := &drivermock.SignatureProvider{}
	mockSigProv.HasBeenSignedByReturns([]byte("sig"), nil)

	ppWithIssuers := *testPP
	ppWithIssuers.IssuerIdentities = []driver.Identity{driver.Identity("issuer1")}

	ctx := &validator.Context{
		PP:                &ppWithIssuers,
		TransferAction:    action,
		Ledger:            mockLedger,
		Deserializer:      mockDes,
		SignatureProvider: mockSigProv,
	}

	err = validator.TransferSignatureValidate(context.Background(), ctx)
	require.NoError(t, err)
	require.Len(t, ctx.Signatures, 2) // owner sig + issuer sig
}

// ── IssueSignatureValidate error paths ──────────────────────────────────────

func TestIssueSignatureValidate_NilDeserializer(t *testing.T) {
	setupEndToEnd(t)

	action, _, err := testOrch.BuildIssueAction(
		context.Background(),
		[]byte("issuer"),
		[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("alice")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	ctx := &validator.Context{
		PP:           testPP,
		IssueAction:  action,
		Deserializer: nil,
	}

	err = validator.IssueSignatureValidate(context.Background(), ctx)
	require.Error(t, err)
}

func TestIssueSignatureValidate_GetVerifierError(t *testing.T) {
	setupEndToEnd(t)

	action, _, err := testOrch.BuildIssueAction(
		context.Background(),
		[]byte("issuer"),
		[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("alice")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	mockDes := &drivermock.Deserializer{}
	mockDes.GetIssuerVerifierReturns(nil, errors.New("failed getting verifier"))

	ctx := &validator.Context{
		PP:           testPP,
		IssueAction:  action,
		Deserializer: mockDes,
	}

	err = validator.IssueSignatureValidate(context.Background(), ctx)
	require.Error(t, err)
}

func TestIssueSignatureValidate_NilSignatureProvider(t *testing.T) {
	setupEndToEnd(t)

	action, _, err := testOrch.BuildIssueAction(
		context.Background(),
		[]byte("issuer"),
		[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("alice")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	mockDes := &drivermock.Deserializer{}
	mockDes.GetIssuerVerifierReturns(&drivermock.Verifier{}, nil)

	ctx := &validator.Context{
		PP:                testPP,
		IssueAction:       action,
		Deserializer:      mockDes,
		SignatureProvider: nil,
	}

	err = validator.IssueSignatureValidate(context.Background(), ctx)
	require.ErrorIs(t, err, common.ErrNilSignatureProvider)
}

func TestIssueSignatureValidate_Success(t *testing.T) {
	setupEndToEnd(t)

	action, _, err := testOrch.BuildIssueAction(
		context.Background(),
		[]byte("issuer"),
		[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("alice")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	mockDes := &drivermock.Deserializer{}
	mockDes.GetIssuerVerifierReturns(&drivermock.Verifier{}, nil)

	mockSigProv := &drivermock.SignatureProvider{}
	mockSigProv.HasBeenSignedByReturns([]byte("valid-issuer-sig"), nil)

	ctx := &validator.Context{
		PP:                testPP,
		IssueAction:       action,
		Deserializer:      mockDes,
		SignatureProvider: mockSigProv,
	}

	err = validator.IssueSignatureValidate(context.Background(), ctx)
	require.NoError(t, err)
}

func TestIssueSignatureValidate_IssuerNotAuthorized(t *testing.T) {
	setupEndToEnd(t)

	action, _, err := testOrch.BuildIssueAction(
		context.Background(),
		[]byte("issuer"),
		[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("alice")}},
		"USD",
		testPP,
	)
	require.NoError(t, err)

	ppRestricted := *testPP
	ppRestricted.IssuerIdentities = []driver.Identity{driver.Identity("other-issuer")}

	ctx := &validator.Context{
		PP:          &ppRestricted,
		IssueAction: action,
	}

	err = validator.IssueSignatureValidate(context.Background(), ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "issuer not authorized")
}
