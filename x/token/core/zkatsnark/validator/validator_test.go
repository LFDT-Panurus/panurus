/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	cryptorand "crypto/rand"
	"crypto/sha256"
	x509std "crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strconv"
	"sync"
	"testing"
	"time"

	mathlib "github.com/IBM/mathlib"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	x509crypto "github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/backend"
	snarkdriver "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/validator"
)

// e2eConfig is one (proof system, outer curve) combination under test.
type e2eConfig struct {
	proofSystem string
	curve       ecc.ID
}

func (c e2eConfig) name() string { return c.proofSystem + "/" + c.curve.String() }

// e2eConfigs lists every supported proof system x curve combination.
var e2eConfigs = []e2eConfig{
	{"groth16", ecc.BLS12_381},
	{"groth16", ecc.BN254},
	{"plonk", ecc.BLS12_381},
	{"plonk", ecc.BN254},
}

// e2eFixture holds a real Orchestrator (prover side) and Validator for one
// config, built against the SAME PublicParams — the production shape, with
// keys generated locally rather than distributed via channel configuration.
type e2eFixture struct {
	cfg  e2eConfig
	pp   *pp.PublicParams
	orch *prover.Orchestrator
	val  *validator.Validator

	migOnce sync.Once
	migErr  error
}

var (
	fixturesMu sync.Mutex
	fixtures   = map[string]*e2eFixture{}
	fixtureErr = map[string]error{}
)

// fixtureFor lazily builds (once per config) the Spend/Output setup, provers,
// orchestrator and validator. Accepts testing.TB so it can be shared between
// tests (*testing.T) and benchmarks (*testing.B).
func fixtureFor(tb testing.TB, cfg e2eConfig) *e2eFixture {
	tb.Helper()
	fixturesMu.Lock()
	defer fixturesMu.Unlock()

	if err := fixtureErr[cfg.name()]; err != nil {
		tb.Fatalf("fixture %s: %v", cfg.name(), err)
	}
	if f, ok := fixtures[cfg.name()]; ok {
		return f
	}
	f, err := buildFixture(cfg)
	if err != nil {
		fixtureErr[cfg.name()] = err
		tb.Fatalf("fixture %s: %v", cfg.name(), err)
	}
	fixtures[cfg.name()] = f

	return f
}

func buildFixture(cfg e2eConfig) (*e2eFixture, error) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = cfg.proofSystem
	p.Curve = cfg.curve

	p, err := setup.SetupAll(p)
	if err != nil {
		return nil, err
	}
	b, err := backend.NewProofSystemBackend(p.ProofSystem, p.Curve)
	if err != nil {
		return nil, err
	}

	spendCS, err := setup.CompileSpendCircuit(p)
	if err != nil {
		return nil, err
	}
	spendPK, err := setup.LoadProvingKeyBytes(p, setup.CircuitSpend)
	if err != nil {
		return nil, err
	}
	outputCS, err := setup.CompileOutputCircuit(p)
	if err != nil {
		return nil, err
	}
	outputPK, err := setup.LoadProvingKeyBytes(p, setup.CircuitOutput)
	if err != nil {
		return nil, err
	}

	orch := prover.NewOrchestrator(
		prover.NewSpendProver(spendCS, spendPK, b, p),
		prover.NewOutputProver(outputCS, outputPK, b, p),
		p,
	)
	val, err := validator.NewValidator(p, nil, driver.ResourceLimits{}.WithDefaults())
	if err != nil {
		return nil, err
	}

	return &e2eFixture{cfg: cfg, pp: p, orch: orch, val: val}, nil
}

// forEachConfig runs fn as a subtest for every supported proof system x curve.
func forEachConfig(t *testing.T, fn func(t *testing.T, f *e2eFixture)) {
	t.Helper()
	for _, cfg := range e2eConfigs {
		t.Run(cfg.name(), func(t *testing.T) {
			fn(t, fixtureFor(t, cfg))
		})
	}
}

// randomFieldBytes returns a random canonical 32-byte element of the
// config's scalar field.
func (f *e2eFixture) randomFieldBytes(t *testing.T) []byte {
	t.Helper()
	n, err := cryptorand.Int(cryptorand.Reader, f.cfg.curve.ScalarField())
	require.NoError(t, err)

	return n.FillBytes(make([]byte, 32))
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

// requireMigration extends the fixture with a real MigrationCircuit trusted
// setup and wires a MigrationProver into the Orchestrator.
//
// BN254 is skipped: although the circuit compiles and sets up there,
// prover.BuildMigrationWitness and DefaultPedersenGeneratorCoords are
// hardcoded to BLS12-381 field/G1 types (the legacy zkatdlog commitments
// being migrated are BLS12-381 points), so no witness can be built.
func (f *e2eFixture) requireMigration(t *testing.T) {
	t.Helper()
	if f.cfg.curve != ecc.BLS12_381 {
		t.Skipf("migration prover is BLS12-381-only; not supported on %s", f.cfg.name())
	}
	f.migOnce.Do(func() { f.migErr = f.setupMigration() })
	require.NoError(t, f.migErr, "migration setup must succeed on %s", f.cfg.name())
}

func (f *e2eFixture) setupMigration() error {
	gens := prover.DefaultPedersenGeneratorCoords()

	p, err := setup.SetupMigration(f.pp, gens)
	if err != nil {
		return err
	}
	migCS, err := setup.CompileMigrationCircuit(p, gens)
	if err != nil {
		return err
	}
	migPK, err := setup.LoadProvingKeyBytes(p, setup.CircuitMigration)
	if err != nil {
		return err
	}
	b, err := backend.NewProofSystemBackend(p.ProofSystem, p.Curve)
	if err != nil {
		return err
	}
	f.orch.SetMigrationProver(prover.NewMigrationProver(migCS, migPK, b, p))

	// Rebuild the Validator so it picks up the now-populated vkMigration.
	f.val, err = validator.NewValidator(p, nil, driver.ResourceLimits{}.WithDefaults())

	return err
}

// TestValidateTransfer_EndToEnd is the single most important test in this
// package. Layer 3's Orchestrator produces an action; this layer's
// Validator must accept it, using nothing but the wire-format bytes.
func TestValidateTransfer_EndToEnd(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		note1, err := snarktoken.NewRandomNote(100, "USD", f.cfg.curve)
		require.NoError(t, err)
		note2, err := snarktoken.NewRandomNote(50, "USD", f.cfg.curve)
		require.NoError(t, err)

		action, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note1}, {Note: note2}},
			[]prover.OutputRequest{
				{Value: 80, TokenType: "USD", Recipient: []byte("bob")},
				{Value: 70, TokenType: "USD", Recipient: []byte("alice-change")},
			},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		err = f.val.ValidateTransfer(action)
		require.NoError(t, err, "a genuinely valid TransferAction produced by the Orchestrator must validate")
	})
}

// TestValidateIssue_EndToEnd exercises the issuance path, including
// TotalValue round-tripping correctly through the wire format.
func TestValidateIssue_EndToEnd(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		action, _, err := f.orch.BuildIssueAction(
			t.Context(),
			[]byte("issuer-identity"),
			[]prover.OutputRequest{{Value: 500, TokenType: "USD", Recipient: []byte("alice")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		err = f.val.ValidateIssue(action)
		require.NoError(t, err, "a genuinely valid IssueAction produced by the Orchestrator must validate")
	})
}

// TestValidateTransfer_RejectsTamperedCommitment demonstrates that
// corrupting a single public byte after proof generation is caught: the
// public witness reconstructed from the corrupted bytes no longer matches
// what the proof was actually generated against.
func TestValidateTransfer_RejectsTamperedCommitment(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		note, err := snarktoken.NewRandomNote(100, "USD", f.cfg.curve)
		require.NoError(t, err)

		action, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note}},
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		action.Inputs[0].CommitmentIn[0] ^= 0xFF // well-formed but wrong 32-byte value

		err = f.val.ValidateTransfer(action)
		require.Error(t, err, "a tampered commitment must be rejected")
	})
}

// TestValidateTransfer_RejectsWrongTypeCommitment exercises the homogeneity
// check independently of proof verification.
func TestValidateTransfer_RejectsWrongTypeCommitment(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		note, err := snarktoken.NewRandomNote(100, "USD", f.cfg.curve)
		require.NoError(t, err)

		action, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note}},
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		tr := f.randomFieldBytes(t)
		tcEUR, _ := snarktoken.ComputeTypeCommitment("EUR", tr, f.pp.Curve)

		action.TypeCommitment = tcEUR // declared type commitment now disagrees with every description's actual encoded type

		err = f.val.ValidateTransfer(action)
		require.Error(t, err, "a declared type commitment mismatching every description's actual type must be rejected")
	})
}

// TestValidateTransfer_RejectsCorruptedBindingSignature confirms that
// tampering with the signature itself, as opposed to the commitments it
// covers, is caught independently of proof verification.
func TestValidateTransfer_RejectsCorruptedBindingSignature(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		note, err := snarktoken.NewRandomNote(100, "USD", f.cfg.curve)
		require.NoError(t, err)

		action, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note}},
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		action.BindingSignature[0] ^= 0xFF

		err = f.val.ValidateTransfer(action)
		require.Error(t, err, "a corrupted binding signature must be rejected")
	})
}

// TestValidateTransfer_MalformedShape confirms structural checks reject
// obviously wrong-length fields before any cryptography runs at all.
func TestValidateTransfer_MalformedShape(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		action := &snarktoken.TransferAction{
			TypeCommitment: make([]byte, 32),
			Inputs: []snarktoken.SpendDescription{
				{CommitmentIn: []byte("too short")},
			},
		}

		err := f.val.ValidateTransfer(action)
		require.Error(t, err, "structurally malformed action must be rejected before any decode/crypto step")
	})
}

func TestValidateMigration_EndToEnd(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()
		f.requireMigration(t)

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

		actions, _, err := f.orch.BuildMigrationAction(
			t.Context(),
			[]prover.MigrationRequest{{Opening: opening, Recipient: []byte("alice")}},
			f.pp,
		)
		require.NoError(t, err)
		require.Len(t, actions, 1)

		err = f.val.ValidateMigration(&actions[0])
		require.NoError(t, err, "a genuine migration, proven by the Orchestrator, must validate")
	})
}

func TestValidateMigration_RejectsTamperedCommitment(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()
		f.requireMigration(t)

		var blindingFactor fr.Element
		_, err := blindingFactor.SetRandom()
		require.NoError(t, err)
		bfBytes := blindingFactor.Bytes()
		commitmentBytes := computeTestPedersenCommitment(t, 100, "USD", blindingFactor)

		opening := snarktoken.PedersenOpening{
			Value: 100, TokenType: "USD",
			BlindingFactor: bfBytes[:], CommitmentBytes: commitmentBytes,
		}
		actions, _, err := f.orch.BuildMigrationAction(
			t.Context(),
			[]prover.MigrationRequest{{Opening: opening, Recipient: []byte("bob")}},
			f.pp,
		)
		require.NoError(t, err)

		actions[0].CommitmentMiMC[0] ^= 0xFF

		err = f.val.ValidateMigration(&actions[0])
		require.Error(t, err, "a tampered MiMC commitment must be rejected")
	})
}

// TestValidateIssue_RejectsTamperedOutput demonstrates that corrupting an
// output commitment byte causes the proof to fail verification.
func TestValidateIssue_RejectsTamperedOutput(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		action, _, err := f.orch.BuildIssueAction(
			t.Context(),
			[]byte("issuer-identity"),
			[]prover.OutputRequest{{Value: 500, TokenType: "USD", Recipient: []byte("alice")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		action.Outputs[0].CommitmentOut[0] ^= 0xFF

		err = f.val.ValidateIssue(action)
		require.Error(t, err, "a tampered output commitment must be rejected")
	})
}

// TestValidateIssue_RejectsCorruptedBindingSignature confirms that
// tampering with the binding signature on an issue action is caught.
func TestValidateIssue_RejectsCorruptedBindingSignature(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		action, _, err := f.orch.BuildIssueAction(
			t.Context(),
			[]byte("issuer-identity"),
			[]prover.OutputRequest{{Value: 500, TokenType: "USD", Recipient: []byte("alice")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		action.BindingSignature[0] ^= 0xFF

		err = f.val.ValidateIssue(action)
		require.Error(t, err, "a corrupted binding signature on an issue action must be rejected")
	})
}

// TestValidateIssue_MalformedShape confirms that a structurally malformed
// issue action is rejected before any cryptographic validation.
func TestValidateIssue_MalformedShape(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		action := &snarktoken.IssueAction{
			TypeCommitment: make([]byte, 32),
			TotalValue:     make([]byte, 32),
			// No outputs — structurally invalid.
		}

		err := f.val.ValidateIssue(action)
		require.Error(t, err, "structurally malformed issue action must be rejected")
	})
}

// TestValidateIssue_RejectsNonCanonicalTotalValue confirms that a
// non-canonical TotalValue field is rejected.
func TestValidateIssue_RejectsNonCanonicalTotalValue(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		action, _, err := f.orch.BuildIssueAction(
			t.Context(),
			[]byte("issuer-identity"),
			[]prover.OutputRequest{{Value: 500, TokenType: "USD", Recipient: []byte("alice")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		// Set TotalValue to a non-canonical value (all 0xFF > field modulus)
		nonCanonical := make([]byte, 32)
		for i := range nonCanonical {
			nonCanonical[i] = 0xFF
		}
		action.TotalValue = nonCanonical

		err = f.val.ValidateIssue(action)
		require.Error(t, err, "a non-canonical TotalValue must be rejected")
	})
}

// TestValidateTransfer_RejectsNonCanonicalTypeCommitment confirms
// that non-canonical TypeCommitment bytes are caught.
func TestValidateTransfer_RejectsNonCanonicalTypeCommitment(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		note, err := snarktoken.NewRandomNote(100, "USD", f.cfg.curve)
		require.NoError(t, err)

		action, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note}},
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		// Set TypeCommitment to non-canonical
		nonCanonical := make([]byte, 32)
		for i := range nonCanonical {
			nonCanonical[i] = 0xFF
		}
		action.TypeCommitment = nonCanonical

		err = f.val.ValidateTransfer(action)
		require.Error(t, err, "non-canonical TypeCommitment bytes must be rejected")
	})
}

// TestValidateMigration_MalformedShape confirms structural rejection
// before cryptographic work.
func TestValidateMigration_MalformedShape(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()
		f.requireMigration(t)

		action := &snarktoken.MigrationAction{
			CommitmentPedersenX: make([]byte, 32), // wrong length (should be 48)
		}

		err := f.val.ValidateMigration(action)
		require.Error(t, err, "malformed migration action must be rejected")
	})
}

// ── Callback entry-point tests ──────────────────────────────────────────────
//
// These test the TransferZKValidate / IssueZKValidate methods (the callback
// versions) by constructing a Context directly. This covers the 0%-coverage
// callback functions that are normally only invoked via common.Validator's
// VerifyTokenRequest path.

func TestTransferZKValidate_Valid(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		note1, err := snarktoken.NewRandomNote(100, "USD", f.cfg.curve)
		require.NoError(t, err)
		note2, err := snarktoken.NewRandomNote(50, "USD", f.cfg.curve)
		require.NoError(t, err)

		action, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note1}, {Note: note2}},
			[]prover.OutputRequest{
				{Value: 80, TokenType: "USD", Recipient: []byte("bob")},
				{Value: 70, TokenType: "USD", Recipient: []byte("alice")},
			},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		ctx := &validator.Context{
			PP:             f.pp,
			TransferAction: action,
		}

		err = f.val.TransferZKValidate(t.Context(), ctx)
		require.NoError(t, err, "TransferZKValidate callback must succeed for a valid action")
	})
}

func TestTransferZKValidate_TamperedProof(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		note, err := snarktoken.NewRandomNote(100, "USD", f.cfg.curve)
		require.NoError(t, err)

		action, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note}},
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		action.Inputs[0].SpendProof[0] ^= 0xFF

		ctx := &validator.Context{
			PP:             f.pp,
			TransferAction: action,
		}

		err = f.val.TransferZKValidate(t.Context(), ctx)
		require.Error(t, err, "TransferZKValidate must reject a tampered spend proof")
	})
}

func TestTransferZKValidate_MalformedShape(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		action := &snarktoken.TransferAction{
			TypeCommitment: make([]byte, 32),
			// No inputs or outputs — structurally invalid.
		}

		ctx := &validator.Context{
			PP:             f.pp,
			TransferAction: action,
		}

		err := f.val.TransferZKValidate(t.Context(), ctx)
		require.Error(t, err, "TransferZKValidate must reject a malformed action")
	})
}

func TestIssueZKValidate_Valid(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		action, _, err := f.orch.BuildIssueAction(
			t.Context(),
			[]byte("issuer"),
			[]prover.OutputRequest{{Value: 500, TokenType: "USD", Recipient: []byte("alice")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		ctx := &validator.Context{
			PP:          f.pp,
			IssueAction: action,
		}

		err = f.val.IssueZKValidate(t.Context(), ctx)
		require.NoError(t, err, "IssueZKValidate callback must succeed for a valid action")
	})
}

func TestIssueZKValidate_TamperedBinding(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		action, _, err := f.orch.BuildIssueAction(
			t.Context(),
			[]byte("issuer"),
			[]prover.OutputRequest{{Value: 500, TokenType: "USD", Recipient: []byte("alice")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		action.BindingSignature[0] ^= 0xFF

		ctx := &validator.Context{
			PP:          f.pp,
			IssueAction: action,
		}

		err = f.val.IssueZKValidate(t.Context(), ctx)
		require.Error(t, err, "IssueZKValidate must reject a tampered binding signature")
	})
}

func TestIssueZKValidate_MalformedShape(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		action := &snarktoken.IssueAction{
			TypeCommitment: make([]byte, 32),
			TotalValue:     make([]byte, 32),
			// No outputs — structurally invalid.
		}

		ctx := &validator.Context{
			PP:          f.pp,
			IssueAction: action,
		}

		err := f.val.IssueZKValidate(t.Context(), ctx)
		require.Error(t, err, "IssueZKValidate must reject a malformed action")
	})
}

// ── DeserializeActions ──────────────────────────────────────────────────────

func TestDeserializeActions_RoundTrip(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		issueAction, _, err := f.orch.BuildIssueAction(
			t.Context(),
			[]byte("issuer"),
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("alice")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)
		issueBytes, err := issueAction.Serialize()
		require.NoError(t, err)

		note, err := snarktoken.NewRandomNote(50, "USD", f.cfg.curve)
		require.NoError(t, err)
		transferAction, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note}},
			[]prover.OutputRequest{{Value: 50, TokenType: "USD", Recipient: []byte("bob")}},
			"USD",
			f.pp,
			nil,
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
	})
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
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()
		action := &snarktoken.TransferAction{
			// No inputs.
			TypeCommitment: make([]byte, 32),
		}

		ctx := &validator.Context{
			PP:             f.pp,
			TransferAction: action,
		}

		err := validator.TransferSignatureValidate(t.Context(), ctx)
		require.ErrorIs(t, err, validator.ErrInvalidInputs)
	})
}

func TestTransferSignatureValidate_MissingInputIDs(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		note, err := snarktoken.NewRandomNote(100, "USD", f.cfg.curve)
		require.NoError(t, err)

		action, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note}},
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		// Clear InputIDs so the count mismatches.
		action.InputIDs = nil

		ctx := &validator.Context{
			PP:             f.pp,
			TransferAction: action,
		}

		err = validator.TransferSignatureValidate(t.Context(), ctx)
		require.Error(t, err)
		require.ErrorIs(t, err, validator.ErrMissingInputIDs)
	})
}

func TestTransferSignatureValidate_NilInputID(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		note, err := snarktoken.NewRandomNote(100, "USD", f.cfg.curve)
		require.NoError(t, err)

		action, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note}},
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		action.InputIDs = []*token2.ID{nil}

		ctx := &validator.Context{
			PP:             f.pp,
			TransferAction: action,
		}

		err = validator.TransferSignatureValidate(t.Context(), ctx)
		require.Error(t, err)
		require.ErrorIs(t, err, validator.ErrNilInputID)
	})
}

func TestTransferSignatureValidate_LedgerError(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		note, err := snarktoken.NewRandomNote(100, "USD", f.cfg.curve)
		require.NoError(t, err)

		action, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note}},
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)
		action.InputIDs = []*token2.ID{{TxId: "tx1", Index: 0}}

		mockLedger := &drivermock.Ledger{}
		mockLedger.GetStateReturns(nil, errors.New("ledger error"))

		ctx := &validator.Context{
			PP:             f.pp,
			TransferAction: action,
			Ledger:         mockLedger,
		}

		err = validator.TransferSignatureValidate(t.Context(), ctx)
		require.Error(t, err)
	})
}

func TestTransferSignatureValidate_CommitmentMismatch(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		note, err := snarktoken.NewRandomNote(100, "USD", f.cfg.curve)
		require.NoError(t, err)

		action, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note}},
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
			"USD",
			f.pp,
			nil,
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
			PP:             f.pp,
			TransferAction: action,
			Ledger:         mockLedger,
		}

		err = validator.TransferSignatureValidate(t.Context(), ctx)
		require.Error(t, err)
		require.ErrorIs(t, err, validator.ErrCommitmentMismatch)
	})
}

func TestTransferSignatureValidate_NilSignatureProvider(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		note, err := snarktoken.NewRandomNote(100, "USD", f.cfg.curve)
		require.NoError(t, err)

		action, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note}},
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
			"USD",
			f.pp,
			nil,
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
			PP:                f.pp,
			TransferAction:    action,
			Ledger:            mockLedger,
			Deserializer:      mockDes,
			SignatureProvider: nil,
		}

		err = validator.TransferSignatureValidate(t.Context(), ctx)
		require.ErrorIs(t, err, common.ErrNilSignatureProvider)
	})
}

func TestTransferSignatureValidate_Success(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		note, err := snarktoken.NewRandomNote(100, "USD", f.cfg.curve)
		require.NoError(t, err)

		action, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note}},
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
			"USD",
			f.pp,
			nil,
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
			PP:                f.pp,
			TransferAction:    action,
			Ledger:            mockLedger,
			Deserializer:      mockDes,
			SignatureProvider: mockSigProv,
		}

		err = validator.TransferSignatureValidate(t.Context(), ctx)
		require.NoError(t, err)
		require.Len(t, ctx.InputTokens, 1)
		require.Len(t, ctx.Signatures, 1)
	})
}

func TestTransferSignatureValidate_Redeem_MissingIssuer(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		note, err := snarktoken.NewRandomNote(100, "USD", f.cfg.curve)
		require.NoError(t, err)

		action, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note}},
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
			"USD",
			f.pp,
			nil,
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

		ppWithIssuers := *f.pp
		ppWithIssuers.IssuerIdentities = []driver.Identity{driver.Identity("issuer1")}

		ctx := &validator.Context{
			PP:                &ppWithIssuers,
			TransferAction:    action,
			Ledger:            mockLedger,
			Deserializer:      mockDes,
			SignatureProvider: &drivermock.SignatureProvider{},
		}

		err = validator.TransferSignatureValidate(t.Context(), ctx)
		require.ErrorIs(t, err, validator.ErrMissingIssuer)
	})
}

func TestTransferSignatureValidate_Redeem_Success(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		note, err := snarktoken.NewRandomNote(100, "USD", f.cfg.curve)
		require.NoError(t, err)

		action, _, err := f.orch.BuildTransferAction(
			t.Context(),
			[]prover.SpendRequest{{Note: note}},
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("bob")}},
			"USD",
			f.pp,
			nil,
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

		ppWithIssuers := *f.pp
		ppWithIssuers.IssuerIdentities = []driver.Identity{driver.Identity("issuer1")}

		ctx := &validator.Context{
			PP:                &ppWithIssuers,
			TransferAction:    action,
			Ledger:            mockLedger,
			Deserializer:      mockDes,
			SignatureProvider: mockSigProv,
		}

		err = validator.TransferSignatureValidate(t.Context(), ctx)
		require.NoError(t, err)
		require.Len(t, ctx.Signatures, 2) // owner sig + issuer sig
	})
}

// ── IssueSignatureValidate error paths ──────────────────────────────────────

func TestIssueSignatureValidate_NilDeserializer(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		action, _, err := f.orch.BuildIssueAction(
			t.Context(),
			[]byte("issuer"),
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("alice")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		ctx := &validator.Context{
			PP:           f.pp,
			IssueAction:  action,
			Deserializer: nil,
		}

		err = validator.IssueSignatureValidate(t.Context(), ctx)
		require.Error(t, err)
	})
}

func TestIssueSignatureValidate_GetVerifierError(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		action, _, err := f.orch.BuildIssueAction(
			t.Context(),
			[]byte("issuer"),
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("alice")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		mockDes := &drivermock.Deserializer{}
		mockDes.GetIssuerVerifierReturns(nil, errors.New("failed getting verifier"))

		ctx := &validator.Context{
			PP:           f.pp,
			IssueAction:  action,
			Deserializer: mockDes,
		}

		err = validator.IssueSignatureValidate(t.Context(), ctx)
		require.Error(t, err)
	})
}

func TestIssueSignatureValidate_NilSignatureProvider(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		action, _, err := f.orch.BuildIssueAction(
			t.Context(),
			[]byte("issuer"),
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("alice")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		mockDes := &drivermock.Deserializer{}
		mockDes.GetIssuerVerifierReturns(&drivermock.Verifier{}, nil)

		ctx := &validator.Context{
			PP:                f.pp,
			IssueAction:       action,
			Deserializer:      mockDes,
			SignatureProvider: nil,
		}

		err = validator.IssueSignatureValidate(t.Context(), ctx)
		require.ErrorIs(t, err, common.ErrNilSignatureProvider)
	})
}

func TestIssueSignatureValidate_Success(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		action, _, err := f.orch.BuildIssueAction(
			t.Context(),
			[]byte("issuer"),
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("alice")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		mockDes := &drivermock.Deserializer{}
		mockDes.GetIssuerVerifierReturns(&drivermock.Verifier{}, nil)

		mockSigProv := &drivermock.SignatureProvider{}
		mockSigProv.HasBeenSignedByReturns([]byte("valid-issuer-sig"), nil)

		ctx := &validator.Context{
			PP:                f.pp,
			IssueAction:       action,
			Deserializer:      mockDes,
			SignatureProvider: mockSigProv,
		}

		err = validator.IssueSignatureValidate(t.Context(), ctx)
		require.NoError(t, err)
	})
}

func TestIssueSignatureValidate_IssuerNotAuthorized(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()

		action, _, err := f.orch.BuildIssueAction(
			t.Context(),
			[]byte("issuer"),
			[]prover.OutputRequest{{Value: 100, TokenType: "USD", Recipient: []byte("alice")}},
			"USD",
			f.pp,
			nil,
		)
		require.NoError(t, err)

		ppRestricted := *f.pp
		ppRestricted.IssuerIdentities = []driver.Identity{driver.Identity("other-issuer")}

		ctx := &validator.Context{
			PP:          &ppRestricted,
			IssueAction: action,
		}

		err = validator.IssueSignatureValidate(t.Context(), ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "issuer not authorized")
	})
}

// TestValidateMigration_NotConfiguredWithoutSetup confirms the "migration
// never set up" path fails closed with a specific, distinguishable error,
// rather than silently skipping the check or panicking.
func TestValidateMigration_NotConfiguredWithoutSetup(t *testing.T) {
	forEachConfig(t, func(t *testing.T, f *e2eFixture) {
		t.Helper()
		// A fresh Validator over Spend/Output-only params — no migration key.
		freshPP := *f.pp
		freshPP.PKMigration, freshPP.VKMigration = nil, nil

		freshVal, err := validator.NewValidator(&freshPP, nil, driver.ResourceLimits{}.WithDefaults())
		require.NoError(t, err)

		err = freshVal.ValidateMigration(&snarktoken.MigrationAction{})
		require.ErrorIs(t, err, validator.ErrMigrationNotConfigured)
	})
}

// ── Benchmarks ───────────────────────────────────────────────────────────────
//
// These mirror the pattern in token/core/fabtoken/v1/validator/validator_test.go:
// BenchmarkValidatorTransfer / TestParallelBenchmarkValidatorTransfer build a
// Validator once per benchmark case and repeatedly call
// VerifyTokenRequestFromRaw against raw wire-format bytes, rather than going
// through the TransferService / Orchestrator on every iteration.

// BenchmarkValidatorTransfer benchmarks VerifyTokenRequestFromRaw on a
// zkatsnark transfer token request, for every configured benchmark case.
// Each case's curve comes from benchmarkCase.CurveID (driven by the shared
// -curves flag); the proof system comes from the -proof_system flag
// (default groth16).
func BenchmarkValidatorTransfer(b *testing.B) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)

	for _, tc := range cases {
		b.Run(tc.Name, func(b *testing.B) {
			cfg, err := benchmarkConfigFor(tc.BenchmarkCase)
			require.NoError(b, err)
			f := fixtureFor(b, cfg)

			n := int(benchmark2.SetupSamples()) // #nosec G115
			if n == 0 {
				n = min(b.N, 1000)
			}
			env, err := newBenchmarkValidatorEnv(b, n, tc.BenchmarkCase, f)
			require.NoError(b, err)

			b.ResetTimer()
			i := 0
			for b.Loop() {
				e := env.Envs[i%len(env.Envs)]
				_, _, err := e.v.VerifyTokenRequestFromRaw(
					b.Context(),
					e.getState,
					"an_anchor",
					e.raw,
				)
				require.NoError(b, err)
				i++
			}
		})
	}
}

// TestParallelBenchmarkValidatorTransfer runs the validator transfer
// benchmark in parallel. Each case's curve comes from
// benchmarkCase.CurveID (driven by the shared -curves flag); the proof
// system comes from the -proof_system flag (default groth16).
func TestParallelBenchmarkValidatorTransfer(t *testing.T) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)

	test := benchmark2.NewTest[*benchmarkValidatorEnv](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*benchmarkValidatorEnv, error) {
			cfg, err := benchmarkConfigFor(c)
			if err != nil {
				return nil, err
			}
			f := fixtureFor(t, cfg)

			return newBenchmarkValidatorEnv(t, 1, c, f)
		},
		func(ctx context.Context, env *benchmarkValidatorEnv) error {
			_, _, err := env.Envs[0].v.VerifyTokenRequestFromRaw(
				ctx,
				env.Envs[0].getState,
				"an_anchor",
				env.Envs[0].raw,
			)

			return err
		},
	)
}

type benchmarkValidatorSingleEnv struct {
	v        *validator.Validator
	raw      []byte
	getState driver.GetStateFnc
}

type benchmarkValidatorEnv struct {
	Envs []*benchmarkValidatorSingleEnv
}

// newBenchmarkValidatorEnv builds n independent transfer token requests
// (each with its own freshly proven TransferAction) against the shared
// fixture's Validator.
func newBenchmarkValidatorEnv(tb testing.TB, n int, benchmarkCase *benchmark2.Case, f *e2eFixture) (*benchmarkValidatorEnv, error) {
	tb.Helper()
	envs := make([]*benchmarkValidatorSingleEnv, n)
	for i := range n {
		env, err := newValidatorBenchmarkSingleEnv(tb, benchmarkCase, f)
		if err != nil {
			return nil, err
		}
		envs[i] = env
	}

	return &benchmarkValidatorEnv{Envs: envs}, nil
}

// newValidatorBenchmarkSingleEnv builds one transfer token request using the
// REAL production stack: snarkdriver.NewDeserializer for identity
// verification, a real x509 owner identity/signer producing a genuine ECDSA
// signature, and zkatsnark.TransferService.Transfer (rather than calling
// f.orch.BuildTransferAction directly) to generate the TransferAction. This
// exercises the same code path a live node runs, including real signature
// verification inside TransferSignatureValidate.
func newValidatorBenchmarkSingleEnv(tb testing.TB, benchmarkCase *benchmark2.Case, f *e2eFixture) (*benchmarkValidatorSingleEnv, error) {
	tb.Helper()

	certPEM, sk, err := generateBenchmarkCert()
	if err != nil {
		return nil, err
	}
	owner, err := identity.WrapWithType(x509.IdentityType, certPEM)
	if err != nil {
		return nil, err
	}
	signer := x509crypto.NewEcdsaSigner(sk)

	numInputs := benchmarkCase.NumInputs
	if numInputs == 0 {
		numInputs = 1
	}
	numOutputs := benchmarkCase.NumOutputs
	if numOutputs == 0 {
		numOutputs = 1
	}

	ids := make([]*token2.ID, numInputs)
	loadedTokens := make([]common.LoadedToken[[]byte, []byte], numInputs)
	ledger := make(map[token2.ID][]byte, numInputs)
	for i := range numInputs {
		ids[i] = &token2.ID{TxId: strconv.Itoa(i), Index: 0}

		note, err := snarktoken.NewRandomNote(100, "ABC", f.cfg.curve)
		if err != nil {
			return nil, err
		}
		noteBytes, err := note.Serialize()
		if err != nil {
			return nil, err
		}

		cm, err := note.Commitment(f.cfg.curve)
		if err != nil {
			return nil, err
		}
		outputDesc := &snarktoken.OutputDescription{
			CommitmentOut: cm,
			Recipient:     owner,
		}
		rawOutputDesc, err := outputDesc.Serialize()
		if err != nil {
			return nil, err
		}
		ledger[*ids[i]] = rawOutputDesc

		loadedTokens[i] = common.LoadedToken[[]byte, []byte]{
			Token:    rawOutputDesc,
			Metadata: noteBytes,
		}
	}

	outputTokens := make([]*token2.Token, numOutputs)
	for i := range numOutputs {
		outputTokens[i] = &token2.Token{
			Owner:    owner,
			Type:     "ABC",
			Quantity: token2.NewQuantityFromUInt64(100).Hex(),
		}
	}

	// DefaultPublicParams seeds a placeholder, non-parseable idemix issuer
	// public key ("dummy-idemix-pk"): fine for Groth16/Plonk proof
	// verification, which never touches it, but snarkdriver.NewDeserializer
	// eagerly builds an idemix deserializer for every configured key and
	// fails on it. This benchmark only exercises x509 owner identities, so
	// strip it before constructing the real Deserializer.
	ppForDeserializer := *f.pp
	ppForDeserializer.IdemixIssuerPublicKeys = nil
	des, err := snarkdriver.NewDeserializer(&ppForDeserializer)
	if err != nil {
		return nil, err
	}

	ws := &drivermock.WalletService{}
	ws.GetAuditInfoReturns([]byte("audit"), nil)

	tl := &validatorBenchTokenLoader{tokens: loadedTokens}

	ppm, err := common.NewPublicParamsManagerFromParams(f.pp)
	if err != nil {
		return nil, err
	}

	ts := zkatsnark.NewTransferService(
		logging.MustGetLogger(),
		ppm,
		ws,
		tl,
		des,
		noop.NewTracerProvider(),
		nil,
		nil,
		f.orch,
	)

	const anchor = driver.TokenRequestAnchor("an_anchor")
	transferAction, _, err := ts.Transfer(tb.Context(), anchor, nil, ids, outputTokens, nil)
	if err != nil {
		return nil, err
	}

	action, ok := transferAction.(*snarktoken.TransferAction)
	if !ok {
		return nil, errors.New("expected *snarktoken.TransferAction")
	}

	rawTA, err := action.Serialize()
	if err != nil {
		return nil, err
	}

	tr := &driver.TokenRequest{
		Version: driver.ProtocolV1,
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: rawTA},
		},
	}

	toSign, err := tr.MarshalToMessageToSign([]byte(anchor))
	if err != nil {
		return nil, err
	}
	sigma, err := signer.Sign(toSign)
	if err != nil {
		return nil, err
	}
	for range numInputs {
		tr.Signatures = append(tr.Signatures, &driver.RequestSignature{
			Action: &driver.ActionSignature{Signature: sigma},
		})
	}

	raw, err := tr.Bytes()
	if err != nil {
		return nil, err
	}

	v, err := validator.NewValidator(f.pp, des, driver.DefaultResourceLimits())
	if err != nil {
		return nil, err
	}

	getState := func(id token2.ID) ([]byte, error) {
		raw, ok := ledger[id]
		if !ok {
			return nil, errors.Errorf("no such token [%s]", id)
		}

		return raw, nil
	}

	return &benchmarkValidatorSingleEnv{v: v, raw: raw, getState: getState}, nil
}

// validatorBenchTokenLoader is a trivial in-memory TokenLoader backing
// newValidatorBenchmarkSingleEnv's call into zkatsnark.TransferService.Transfer.
type validatorBenchTokenLoader struct {
	tokens []common.LoadedToken[[]byte, []byte]
}

func (t *validatorBenchTokenLoader) LoadTokens(_ context.Context, ids []*token2.ID) ([]common.LoadedToken[[]byte, []byte], error) {
	if len(ids) != len(t.tokens) {
		return nil, errors.Errorf("expected [%d] token ids, got [%d]", len(t.tokens), len(ids))
	}

	return t.tokens, nil
}

// generateBenchmarkCert generates an ephemeral self-signed ECDSA certificate
// usable as a real x509 owner identity, mirroring
// x509/crypto.GenerateTestCert without requiring a *testing.T (needed here
// since this helper is shared by both *testing.T and *testing.B callers).
func generateBenchmarkCert() ([]byte, *ecdsa.PrivateKey, error) {
	sk, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	if err != nil {
		return nil, nil, err
	}

	template := x509std.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "validator-benchmark"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509std.KeyUsageKeyEncipherment | x509std.KeyUsageDigitalSignature,
	}

	derBytes, err := x509std.CreateCertificate(cryptorand.Reader, &template, &template, &sk.PublicKey, sk)
	if err != nil {
		return nil, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	if certPEM == nil {
		return nil, nil, errors.New("failed to encode certificate to PEM")
	}

	return certPEM, sk, nil
}
