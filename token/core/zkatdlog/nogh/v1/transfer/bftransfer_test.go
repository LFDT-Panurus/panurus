/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package transfer_test

import (
	"runtime"
	"testing"
	"time"

	math "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/common/crypto/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp/bulletproof"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	TestBits  = uint64(32)
	TestCurve = math2.BLS12_381_BBS_GURVY_FAST_RNG
)

func TestProof_Validate_ErrConditions(t *testing.T) {
	proof := &transfer.Proof{}
	err := proof.Validate(math.BN254)
	require.Error(t, err)
	require.ErrorIs(t, err, transfer.ErrMissingTypeAndSumProof)
	assert.Contains(t, err.Error(), "invalid transfer proof: missing type-and-sum proof")

	c := math.Curves[TestCurve]
	proof.TypeAndSum = &transfer.TypeAndSumProof{}
	err = proof.Validate(TestCurve)
	require.Error(t, err)
	require.ErrorIs(t, err, transfer.ErrInvalidCommitmentToType)
	require.ErrorIs(t, err, transfer.ErrInvalidTransferProof)

	// valid type and sum, nil range correctness
	proof.TypeAndSum = &transfer.TypeAndSumProof{
		CommitmentToType:     c.GenG1.Copy(),
		InputBlindingFactors: []*math.Zr{c.NewZrFromInt(1)},
		InputValues:          []*math.Zr{c.NewZrFromInt(1)},
		Type:                 c.NewZrFromInt(1),
		TypeBlindingFactor:   c.NewZrFromInt(1),
		EqualityOfSum:        c.NewZrFromInt(1),
		Challenge:            c.NewZrFromInt(1),
	}
	err = proof.Validate(TestCurve)
	require.NoError(t, err)

	// invalid range correctness
	proof.RangeCorrectness = &bulletproof.RangeCorrectness{
		Proofs: []*bulletproof.RangeProof{nil},
	}
	err = proof.Validate(TestCurve)
	require.Error(t, err)
	require.ErrorIs(t, err, transfer.ErrInvalidTransferProof)
}

func TestTransfer(t *testing.T) {
	proofTypes := []struct {
		name      string
		proofType rp.ProofType
	}{
		{"RangeProof", rp.RangeProofType},
		{"CSPRangeProof", rp.CSPRangeProofType},
	}

	for _, pt := range proofTypes {
		t.Run(pt.name, func(t *testing.T) {
			t.Run("parameters and witness are initialized correctly", func(t *testing.T) {
				env, err := newTransferEnvWithProofType(t, pt.proofType)
				require.NoError(t, err)
				proofRaw, err := env.prover.Prove()
				require.NoError(t, err)
				require.NotNil(t, proofRaw)

				if pt.proofType == rp.CSPRangeProofType {
					proof := &transfer.CSPProof{}
					err = proof.Deserialize(proofRaw)
					require.NoError(t, err)
					assert.NotNil(t, proof.TypeAndSum)
					assert.NotNil(t, proof.RangeCorrectness)
				} else {
					proof := &transfer.Proof{}
					err = proof.Deserialize(proofRaw)
					require.NoError(t, err)
					assert.NotNil(t, proof.TypeAndSum)
					assert.NotNil(t, proof.RangeCorrectness)
				}

				err = env.verifier.Verify(proofRaw)
				require.NoError(t, err)
			})
			t.Run("Output Values > Input Values", func(t *testing.T) {
				env, err := newTransferEnvWithWrongSumAndProofType(pt.proofType)
				require.NoError(t, err)

				proof, err := env.prover.Prove()
				require.NoError(t, err)
				require.NotNil(t, proof)
				err = env.verifier.Verify(proof)
				require.Error(t, err)
				require.Contains(t, err.Error(), "invalid transfer proof: invalid sum and type proof")
			})
			t.Run("Output Values out of range", func(t *testing.T) {
				env, err := newTransferEnvWithInvalidRangeAndProofType(pt.proofType)
				require.NoError(t, err)

				proof, err := env.prover.Prove()
				require.NoError(t, err)
				require.NotNil(t, proof)
				err = env.verifier.Verify(proof)
				require.Error(t, err)
				require.Contains(t, err.Error(), "invalid range proof at index 0")
			})
		})
	}
}

// BenchmarkTransferProofGeneration benchmarks the ZK proof generation for a transfer operation
func BenchmarkTransferProofGeneration(b *testing.B) {
	bits, err := benchmark2.Bits(32, 64)
	require.NoError(b, err)
	curves := benchmark2.Curves(math.BN254, math.BLS12_381_BBS_GURVY, math2.BLS12_381_BBS_GURVY_FAST_RNG)
	inputs, err := benchmark2.NumInputs(1, 2, 3)
	require.NoError(b, err)
	outputs, err := benchmark2.NumOutputs(1, 2, 3)
	require.NoError(b, err)
	testCases := benchmark2.GenerateCases(bits, curves, inputs, outputs, []int{1})
	proofType := benchmark.ProofType()

	for _, tc := range testCases {
		b.Run(tc.Name, func(b *testing.B) {
			// prepare env with specified proof type
			env, err := newBenchmarkTransferEnvWithProofType(b.N, tc.BenchmarkCase, proofType)
			require.NoError(b, err)

			b.ResetTimer()

			i := 0
			for b.Loop() {
				e := env.ProverEnvs[i%len(env.ProverEnvs)]
				prover, err := transfer.NewProver(
					e.a,
					e.b,
					e.c,
					e.d,
					env.pp,
				)
				require.NoError(b, err)
				_, err = prover.Prove()
				require.NoError(b, err)
				i++
			}
		})
	}
}

// TestParallelBenchmarkTransferProofGeneration benchmarks ZK proof generation for a transfer operation when multiple go routines are doing the same thing.
func TestParallelBenchmarkTransferProofGeneration(t *testing.T) {
	bits, err := benchmark2.Bits(32)
	require.NoError(t, err)
	curves := benchmark2.Curves(math.BN254)
	inputs, err := benchmark2.NumInputs(2)
	require.NoError(t, err)
	outputs, err := benchmark2.NumOutputs(2)
	require.NoError(t, err)
	workers, err := benchmark2.Workers(runtime.NumCPU())
	require.NoError(t, err)
	testCases := benchmark2.GenerateCases(bits, curves, inputs, outputs, workers)
	// proofType := benchmark.ProofType()
	proofType := rp.CSPRangeProofType

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			r := benchmark2.RunBenchmark(
				benchmark2.NewConfig(tc.BenchmarkCase.Workers,
					benchmark2.Duration(),
					3*time.Second),
				func() *benchmarkTransferEnv {
					env, err := newBenchmarkTransferEnvWithProofType(1, tc.BenchmarkCase, proofType)
					require.NoError(t, err)

					return env
				},
				func(env *benchmarkTransferEnv) error {
					prover, err := transfer.NewProver(
						env.ProverEnvs[0].a,
						env.ProverEnvs[0].b,
						env.ProverEnvs[0].c,
						env.ProverEnvs[0].d,
						env.pp,
					)
					if err != nil {
						return err
					}
					_, err = prover.Prove()

					return err
				},
			)
			r.Print()
		})
	}
}

func setupWithProofType(bits uint64, curveID math.CurveID, proofType rp.ProofType) (*v1.PublicParams, error) {
	pp, err := v1.NewWith(v1.SetupParams{
		DriverName:     v1.DLogNoGHDriverName,
		DriverVersion:  v1.ProtocolV1,
		BitLength:      bits,
		IdemixIssuerPK: nil,
		CurveID:        curveID,
		ProofType:      proofType,
	})
	if err != nil {
		return nil, err
	}

	return pp, nil
}

func prepareZKTransferWithProofType(proofType rp.ProofType) (transfer.Prover, transfer.Verifier, error) {
	pp, err := setupWithProofType(TestBits, TestCurve, proofType)
	if err != nil {
		return nil, nil, err
	}

	intw, outtw, in, out, err := prepareInputsForZKTransfer(pp, 2, 2)
	if err != nil {
		return nil, nil, err
	}

	prover, err := transfer.NewProver(intw, outtw, in, out, pp)
	if err != nil {
		return nil, nil, err
	}
	verifier, err := transfer.NewVerifier(in, out, pp, proofType)
	if err != nil {
		return nil, nil, err
	}

	return prover, verifier, nil
}

func prepareZKTransferWithWrongSumAndProofType(proofType rp.ProofType) (transfer.Prover, transfer.Verifier, error) {
	pp, err := setupWithProofType(TestBits, TestCurve, proofType)
	if err != nil {
		return nil, nil, err
	}

	intw, outtw, in, out, err := prepareInvalidInputsForZKTransfer(pp)
	if err != nil {
		return nil, nil, err
	}

	prover, err := transfer.NewProver(intw, outtw, in, out, pp)
	if err != nil {
		return nil, nil, err
	}
	verifier, err := transfer.NewVerifier(in, out, pp, proofType)
	if err != nil {
		return nil, nil, err
	}

	return prover, verifier, nil
}

func prepareZKTransferWithInvalidRangeAndProofType(proofType rp.ProofType) (transfer.Prover, transfer.Verifier, error) {
	pp, err := setupWithProofType(8, TestCurve, proofType)
	if err != nil {
		return nil, nil, err
	}

	intw, outtw, in, out, err := prepareInputsForZKTransfer(pp, 2, 2)
	if err != nil {
		return nil, nil, err
	}

	prover, err := transfer.NewProver(intw, outtw, in, out, pp)
	if err != nil {
		return nil, nil, err
	}
	verifier, err := transfer.NewVerifier(in, out, pp, proofType)
	if err != nil {
		return nil, nil, err
	}

	return prover, verifier, nil
}

func prepareInputsForZKTransfer(pp *v1.PublicParams, numInputs uint64, numOutputs uint64) ([]*token.Metadata, []*token.Metadata, []*math.G1, []*math.G1, error) {
	c := math.Curves[pp.Curve]
	rand, err := c.Rand()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	inBF := make([]*math.Zr, numInputs)
	outBF := make([]*math.Zr, numOutputs)
	inValues := make([]uint64, numInputs)
	outValues := make([]uint64, numOutputs)
	for i := range numInputs {
		inBF[i] = c.NewRandomZr(rand)
	}
	for i := range numOutputs {
		outBF[i] = c.NewRandomZr(rand)
	}
	ttype := token2.Type("ABC")

	// prepare inputs
	sumInputs := uint64(0)
	for i := range numInputs {
		v := i*10 + 500
		sumInputs += v
		inValues[i] = v
	}

	outputValue := sumInputs / numOutputs
	sumOutputs := uint64(0)
	for i := range numOutputs {
		outValues[i] = outputValue
		sumOutputs += outputValue
	}
	// add any adjustment to the last output
	delta := sumInputs - sumOutputs
	if delta > 0 {
		outValues[0] += delta
	}

	in, out := prepareInputsOutputs(inValues, outValues, inBF, outBF, ttype, pp.PedersenGenerators, c)
	intw := make([]*token.Metadata, len(inValues))
	for i := range intw {
		intw[i] = &token.Metadata{BlindingFactor: inBF[i], Value: c.NewZrFromUint64(inValues[i]), Type: ttype}
	}

	outtw := make([]*token.Metadata, len(outValues))
	for i := range outtw {
		outtw[i] = &token.Metadata{BlindingFactor: outBF[i], Value: c.NewZrFromUint64(outValues[i]), Type: ttype}
	}

	return intw, outtw, in, out, nil
}

func prepareInvalidInputsForZKTransfer(pp *v1.PublicParams) ([]*token.Metadata, []*token.Metadata, []*math.G1, []*math.G1, error) {
	c := math.Curves[pp.Curve]
	rand, err := c.Rand()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	inBF := make([]*math.Zr, 2)
	outBF := make([]*math.Zr, 2)
	inValues := make([]uint64, 2)
	outValues := make([]uint64, 2)
	for i := range 2 {
		inBF[i] = c.NewRandomZr(rand)
	}
	for i := range 2 {
		outBF[i] = c.NewRandomZr(rand)
	}
	ttype := token2.Type("ABC")
	inValues[0] = 90
	inValues[1] = 60
	outValues[0] = 110
	outValues[1] = 45

	in, out := prepareInputsOutputs(inValues, outValues, inBF, outBF, ttype, pp.PedersenGenerators, c)
	intw := make([]*token.Metadata, len(inValues))
	for i := range intw {
		intw[i] = &token.Metadata{BlindingFactor: inBF[i], Value: c.NewZrFromUint64(inValues[i]), Type: ttype}
	}

	outtw := make([]*token.Metadata, len(outValues))
	for i := range outtw {
		outtw[i] = &token.Metadata{BlindingFactor: outBF[i], Value: c.NewZrFromUint64(outValues[i]), Type: ttype}
	}

	return intw, outtw, in, out, nil
}

type transferEnv struct {
	prover   transfer.Prover
	verifier transfer.Verifier
}

func newTransferEnvWithProofType(tb testing.TB, proofType rp.ProofType) (*transferEnv, error) {
	tb.Helper()
	prover, verifier, err := prepareZKTransferWithProofType(proofType)
	if err != nil {
		return nil, err
	}

	return &transferEnv{
		prover:   prover,
		verifier: verifier,
	}, nil
}

func newTransferEnvWithWrongSumAndProofType(proofType rp.ProofType) (*transferEnv, error) {
	prover, verifier, err := prepareZKTransferWithWrongSumAndProofType(proofType)
	if err != nil {
		return nil, err
	}

	return &transferEnv{
		prover:   prover,
		verifier: verifier,
	}, nil
}

func newTransferEnvWithInvalidRangeAndProofType(proofType rp.ProofType) (*transferEnv, error) {
	prover, verifier, err := prepareZKTransferWithInvalidRangeAndProofType(proofType)
	if err != nil {
		return nil, err
	}

	return &transferEnv{
		prover:   prover,
		verifier: verifier,
	}, nil
}

type singleProverEnv struct {
	a []*token.Metadata
	b []*token.Metadata
	c []*math.G1
	d []*math.G1
}

type benchmarkTransferEnv struct {
	ProverEnvs []singleProverEnv
	pp         *v1.PublicParams
}

func newBenchmarkTransferEnvWithProofType(n int, benchmarkCase *benchmark2.Case, proofType rp.ProofType) (*benchmarkTransferEnv, error) {
	pp, err := setupWithProofType(benchmarkCase.Bits, benchmarkCase.CurveID, proofType)
	if err != nil {
		return nil, err
	}

	entries := make([]singleProverEnv, n)
	for i := range n {
		intw, outtw, in, out, err := prepareInputsForZKTransfer(pp, uint64(benchmarkCase.NumInputs), uint64(benchmarkCase.NumOutputs)) //nolint:gosec
		if err != nil {
			return nil, err
		}
		entries[i] = singleProverEnv{
			a: intw,
			b: outtw,
			c: in,
			d: out,
		}
	}

	return &benchmarkTransferEnv{ProverEnvs: entries, pp: pp}, nil
}

// TestBulletProofVerifier_1to1WithAttachedRangeProof is T-GAP-C1: verifies that
// a 1-input/1-output transfer (ownership transfer) that has a non-nil
// RangeCorrectness field attached is still accepted.
//
// The BulletProofVerifier skips range-proof verification when len(inputs) == 1
// and len(outputs) == 1. A proof carrying a RangeCorrectness payload (e.g.,
// produced by a prover with different skip logic) must be silently ignored by
// the verifier and must not cause a failure.
func TestBulletProofVerifier_1to1WithAttachedRangeProof(t *testing.T) {
	pp, err := setupWithProofType(TestBits, TestCurve, rp.RangeProofType)
	require.NoError(t, err)

	// Build a 1-to-1 transfer: prover and verifier both use 1 input / 1 output.
	intw, outtw, in, out, err := prepareInputsForZKTransfer(pp, 1, 1)
	require.NoError(t, err)

	prover, err := transfer.NewProver(intw, outtw, in, out, pp)
	require.NoError(t, err)
	proofRaw, err := prover.Prove()
	require.NoError(t, err)

	// Deserialize the proof and manually attach a non-nil (but empty)
	// RangeCorrectness field to simulate a prover that always generates one.
	// An empty-but-non-nil RangeCorrectness is sufficient to show the verifier
	// ignores it for 1-to-1 transfers.
	proof := &transfer.Proof{}
	require.NoError(t, proof.Deserialize(proofRaw))

	// Attach a non-nil but empty range correctness structure.
	proof.RangeCorrectness = &bulletproof.RangeCorrectness{
		Proofs: []*bulletproof.RangeProof{},
	}
	tamperedRaw, err := proof.Serialize()
	require.NoError(t, err)

	// The verifier must accept the proof: for 1-to-1 transfers RangeCorrectness
	// is ignored entirely, so an incorrect (or even random) payload causes no failure.
	verifier := transfer.NewBulletProofVerifier(in, out, pp)
	err = verifier.Verify(tamperedRaw)
	require.NoError(t, err, "T-GAP-C1: 1-to-1 transfer with attached range proof must still be accepted")
}

// TestNewVerifier_ProofTypeUnavailable is T-SEC-1: verifies that NewVerifier returns
// ErrProofTypeMismatch when the action's ProofType refers to a range-proof algorithm
// whose params sub-struct is not populated in PublicParams, preventing an attacker
// from selecting a verifier whose params sub-struct is nil (algorithm confusion /
// nil-deref).
//
// Scenario A: only BulletProof params populated, action claims CSP  → error.
// Scenario B: only CSP params populated, action claims BulletProof  → error.
// Scenario C: both params populated (migration), each type is accepted → no error.
func TestNewVerifier_ProofTypeUnavailable(t *testing.T) {
	pp, err := setupWithProofType(TestBits, TestCurve, rp.RangeProofType)
	require.NoError(t, err)

	_, _, in, out, err := prepareInputsForZKTransfer(pp, 2, 2)
	require.NoError(t, err)

	t.Run("BulletProofPP_CSPActionType", func(t *testing.T) {
		pp, err := setupWithProofType(TestBits, TestCurve, rp.RangeProofType)
		require.NoError(t, err)

		_, err = transfer.NewVerifier(in, out, pp, rp.CSPRangeProofType)
		require.ErrorIs(t, err, transfer.ErrProofTypeMismatch,
			"T-SEC-1A: CSP proof type against BulletProof-only pp must return ErrProofTypeMismatch")
	})

	t.Run("CSPParamsPP_BulletProofActionType", func(t *testing.T) {
		pp, err := setupWithProofType(TestBits, TestCurve, rp.CSPRangeProofType)
		require.NoError(t, err)

		_, err = transfer.NewVerifier(in, out, pp, rp.RangeProofType)
		require.ErrorIs(t, err, transfer.ErrProofTypeMismatch,
			"T-SEC-1B: BulletProof proof type against CSP-only pp must return ErrProofTypeMismatch")
	})

	t.Run("BothParamsPP_BulletProofActionType", func(t *testing.T) {
		// Simulate a migration: both sub-structs are populated.
		pp, err := setupWithProofType(TestBits, TestCurve, rp.RangeProofType)
		require.NoError(t, err)
		require.NoError(t, pp.GenerateCSPRangeProofParameters(TestBits))

		_, err = transfer.NewVerifier(in, out, pp, rp.RangeProofType)
		require.NoError(t, err,
			"T-SEC-1C: BulletProof proof type against dual pp must be accepted")
	})

	t.Run("BothParamsPP_CSPActionType", func(t *testing.T) {
		// Simulate a migration: both sub-structs are populated.
		pp, err := setupWithProofType(TestBits, TestCurve, rp.RangeProofType)
		require.NoError(t, err)
		require.NoError(t, pp.GenerateCSPRangeProofParameters(TestBits))

		_, err = transfer.NewVerifier(in, out, pp, rp.CSPRangeProofType)
		require.NoError(t, err,
			"T-SEC-1D: CSP proof type against dual pp must be accepted")
	})
}
