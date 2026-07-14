/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue_test

import (
	"strconv"
	"testing"

	math "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/common/crypto/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	issue2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue/mock"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProverVerifier exercises a full prover -> verifier round-trip for
// a generated ZK-issue proof using the given curve and output count.
func TestProverVerifier(t *testing.T) {
	proofTypes := []struct {
		name      string
		setupFunc func(testing.TB, uint64, math.CurveID) *v1.PublicParams
	}{
		{"BulletProof", setup},
		{"CSPProof", setupCSP},
	}

	for _, pt := range proofTypes {
		t.Run(pt.name, func(t *testing.T) {
			prover, verifier := prepareZKIssueWithSetup(t, 32, math.BLS12_381_BBS_GURVY, 2, pt.setupFunc)
			proof, err := prover.Prove()
			require.NoError(t, err)
			assert.NotNil(t, proof)
			err = verifier.Verify(proof)
			require.NoError(t, err)
		})
	}
}

// TestIssuer tests the high-level issuer API: generating a ZK issue
// action and verifying the resulting proof.
func TestIssuer(t *testing.T) {
	proofTypes := []struct {
		name      string
		setupFunc func(testing.TB, uint64, math.CurveID) *v1.PublicParams
		proofType string
	}{
		{"BulletProof", setup, "RangeProofType"},
		{"CSPProof", setupCSP, "CSPRangeProofType"},
	}

	for _, pt := range proofTypes {
		t.Run(pt.name, func(t *testing.T) {
			pp := pt.setupFunc(t, 32, math.BLS12_381_BBS_GURVY)
			issuer := issue2.NewIssuer("ABC", &mock.SigningIdentity{}, pp)
			action, _, err := issuer.GenerateZKIssue([]uint64{10, 20}, [][]byte{[]byte("alice"), []byte("bob")})
			require.NoError(t, err)

			// check the proof type is set correctly
			if pt.proofType == "RangeProofType" {
				assert.Equal(t, rp.RangeProofType, action.ProofType)
			} else {
				assert.Equal(t, rp.CSPRangeProofType, action.ProofType)
			}

			// check the proof
			coms := make([]*math.G1, len(action.Outputs))
			for i := range len(action.Outputs) {
				coms[i] = action.Outputs[i].Data
			}
			verifier, err := issue2.NewVerifier(coms, pp, action.ProofType)
			require.NoError(t, err)
			require.NoError(t, verifier.Verify(action.GetProof()))
		})
	}
}

// BenchmarkIssuer measures the cost of creating ZK-issue actions under
// different parameter sets provided by the benchmark helper.
func BenchmarkIssuer(b *testing.B) {
	bits, err := benchmark2.Bits(32, 64)
	require.NoError(b, err)
	curves := benchmark2.Curves(math.BN254, math.BLS12_381_BBS_GURVY, math2.BLS12_381_BBS_GURVY_FAST_RNG)
	outputs, err := benchmark2.NumOutputs(1, 2, 3)
	require.NoError(b, err)
	testCases := benchmark2.GenerateCases(bits, curves, nil, outputs, nil)

	for _, tc := range testCases {
		b.Run(tc.Name, func(b *testing.B) {
			env := newBenchmarkIssuerEnv(b, b.N, tc.BenchmarkCase)

			// Optional: Reset timer if you had expensive setup code above
			b.ResetTimer()

			i := 0
			for b.Loop() {
				e := env.IssuerEnvs[i%len(env.IssuerEnvs)]
				issuer := issue2.NewIssuer("ABC", &mock.SigningIdentity{}, e.pp)
				action, _, err := issuer.GenerateZKIssue(
					e.outputValues,
					e.outputOwners,
				)
				require.NoError(b, err)
				_, err = action.Serialize()
				require.NoError(b, err)
				i++
			}
		})
	}
}

// BenchmarkProofVerificationIssuer measures the cost of verifying a previously
// serialized ZK-issue action under different benchmark parameter sets.
func BenchmarkProofVerificationIssuer(b *testing.B) {
	bits, err := benchmark2.Bits(32, 64)
	require.NoError(b, err)
	curves := benchmark2.Curves(math.BN254, math.BLS12_381_BBS_GURVY, math2.BLS12_381_BBS_GURVY_FAST_RNG)
	outputs, err := benchmark2.NumOutputs(1, 2, 3)
	require.NoError(b, err)
	testCases := benchmark2.GenerateCases(bits, curves, nil, outputs, nil)

	for _, tc := range testCases {
		b.Run(tc.Name, func(b *testing.B) {
			env := newBenchmarkIssuerProofVerificationEnv(b, b.N, tc.BenchmarkCase)

			// Optional: Reset timer if you had expensive setup code above
			b.ResetTimer()

			i := 0
			for b.Loop() {
				e := env.IssuerEnvs[i%len(env.IssuerEnvs)]
				// deserialize action
				action := &issue2.Action{}
				require.NoError(b, action.Deserialize(e.actionRaw))

				// verify
				coms := make([]*math.G1, len(action.Outputs))
				for i := range len(action.Outputs) {
					coms[i] = action.Outputs[i].Data
				}
				v, err := issue2.NewVerifier(coms, e.pp, action.ProofType)
				require.NoError(b, err)
				require.NoError(b, v.Verify(action.GetProof()))
				i++
			}
		})
	}
}

// issuerEnv holds a prepared public parameters set together with a set of
// output values and owners used by the issuer benchmarks and tests.
type issuerEnv struct {
	pp           *v1.PublicParams
	outputValues []uint64
	outputOwners [][]byte
	actionRaw    []byte
}

// newIssuerEnv constructs an issuerEnv populated with deterministic values
// for the given number of outputs and the provided public parameters.
func newIssuerEnv(pp *v1.PublicParams, numOutputs int) *issuerEnv {
	outputValues := make([]uint64, numOutputs)
	outputOwners := make([][]byte, numOutputs)
	for i := range outputValues {
		outputValues[i] = uint64(i*10 + 10) // #nosec G115
		outputOwners[i] = []byte("alice_" + strconv.Itoa(i))
	}

	return &issuerEnv{
		pp:           pp,
		outputValues: outputValues,
		outputOwners: outputOwners,
	}
}

// newIssuerProofVerificationEnv prepares an issuerEnv containing a serialized
// action that can be used to measure verification cost.
func newIssuerProofVerificationEnv(tb testing.TB, pp *v1.PublicParams, numOutputs int) *issuerEnv {
	tb.Helper()
	outputValues := make([]uint64, numOutputs)
	outputOwners := make([][]byte, numOutputs)
	for i := range outputValues {
		outputValues[i] = uint64(i*10 + 10) // #nosec G115
		outputOwners[i] = []byte("alice_" + strconv.Itoa(i))
	}

	issuer := issue2.NewIssuer("ABC", &mock.SigningIdentity{}, pp)
	action, _, err := issuer.GenerateZKIssue(
		outputValues,
		outputOwners,
	)
	require.NoError(tb, err)
	actionRaw, err := action.Serialize()
	require.NoError(tb, err)

	return &issuerEnv{
		pp:        pp,
		actionRaw: actionRaw,
	}
}

// benchmarkIssuerEnv groups a slice of prepared issuerEnv used by the
// benchmark harness.
type benchmarkIssuerEnv struct {
	IssuerEnvs []*issuerEnv
}

// newBenchmarkIssuerEnv creates n prepared issuer environments using the
// provided benchmark case parameters.
func newBenchmarkIssuerEnv(b *testing.B, n int, benchmarkCase *benchmark2.Case) *benchmarkIssuerEnv {
	b.Helper()
	envs := make([]*issuerEnv, n)
	pp := setup(b, benchmarkCase.Bits, benchmarkCase.CurveID)
	for i := range envs {
		envs[i] = newIssuerEnv(pp, benchmarkCase.NumOutputs)
	}

	return &benchmarkIssuerEnv{IssuerEnvs: envs}
}

// newBenchmarkIssuerProofVerificationEnv creates n prepared issuer
// environments where each entry contains a serialized action to be
// verified in benchmarks.
func newBenchmarkIssuerProofVerificationEnv(b *testing.B, n int, benchmarkCase *benchmark2.Case) *benchmarkIssuerEnv {
	b.Helper()
	envs := make([]*issuerEnv, n)
	pp := setup(b, benchmarkCase.Bits, benchmarkCase.CurveID)
	for i := range envs {
		envs[i] = newIssuerProofVerificationEnv(b, pp, benchmarkCase.NumOutputs)
	}

	return &benchmarkIssuerEnv{IssuerEnvs: envs}
}

// setup initializes public parameters for tests and benchmarks using the
// provided bit-size and curve identifier.
func setup(tb testing.TB, bits uint64, curveID math.CurveID) *v1.PublicParams {
	tb.Helper()
	pp, err := v1.Setup(bits, nil, curveID)
	require.NoError(tb, err)

	return pp
}

// prepareZKIssueWithSetup prepares a prover and verifier pair using a custom setup function
func prepareZKIssueWithSetup(t *testing.T, bits uint64, curveID math.CurveID, numOutputs int, setupFunc func(testing.TB, uint64, math.CurveID) *v1.PublicParams) (issue2.Prover, issue2.Verifier) {
	t.Helper()
	pp := setupFunc(t, bits, curveID)
	tw, tokens := prepareInputsForZKIssue(pp, numOutputs)
	prover, err := issue2.NewProver(tw, tokens, pp)
	require.NoError(t, err)
	verifier, err := issue2.NewVerifier(tokens, pp, prover.RangeProofType())
	require.NoError(t, err)

	return prover, verifier
}

// setupCSP initializes public parameters with CSP range proofs for tests and benchmarks
func setupCSP(tb testing.TB, bits uint64, curveID math.CurveID) *v1.PublicParams {
	tb.Helper()
	pp, err := v1.NewWith(v1.SetupParams{
		DriverName:     v1.DLogNoGHDriverName,
		DriverVersion:  v1.ProtocolV1,
		BitLength:      bits,
		IdemixIssuerPK: nil,
		CurveID:        curveID,
		ProofType:      rp.CSPRangeProofType,
	})
	require.NoError(tb, err)

	return pp
}

// prepareInputsForZKIssue creates deterministic token metadata and token
// commitments that serve as inputs to the prover/verifier in tests.
func prepareInputsForZKIssue(pp *v1.PublicParams, numOutputs int) ([]*token.Metadata, []*math.G1) {
	values := make([]uint64, numOutputs)
	for i := range values {
		values[i] = uint64(i)*10 + 10
	}
	curve := math.Curves[pp.Curve]
	rand, _ := curve.Rand()
	bf := make([]*math.Zr, len(values))
	for i := range values {
		bf[i] = math.Curves[pp.Curve].NewRandomZr(rand)
	}

	tokens := make([]*math.G1, len(values))
	for i := range values {
		tokens[i] = NewToken(curve.NewZrFromInt(int64(values[i])), bf[i], "ABC", pp.PedersenGenerators, curve) // #nosec G115
	}

	return token.NewMetadata(pp.Curve, "ABC", values, bf), tokens
}

func TestIssuerSignTokenActions(t *testing.T) {
	pp := setup(t, 32, math.BLS12_381_BBS_GURVY)
	signer := &mock.SigningIdentity{}
	issuer := issue2.NewIssuer("ABC", signer, pp)

	raw := []byte("hello")
	signer.SignReturns([]byte("signature"), nil)

	sig, err := issuer.SignTokenActions(raw)
	require.NoError(t, err)
	assert.Equal(t, []byte("signature"), sig)
	assert.Equal(t, 1, signer.SignCallCount())
	assert.Equal(t, raw, signer.SignArgsForCall(0))

	// Signer nil
	issuer.Signer = nil
	_, err = issuer.SignTokenActions(raw)
	require.ErrorIs(t, err, issue2.ErrSignTokenActionsNilSigner)
}

// TestIssuerGenerateZKIssueErrors tests error conditions for GenerateZKIssue.
func TestIssuerGenerateZKIssueErrors(t *testing.T) {
	pp := setup(t, 32, math.BLS12_381_BBS_GURVY)
	issuer := issue2.NewIssuer("ABC", &mock.SigningIdentity{}, pp)

	// PublicParams nil
	issuer.PublicParams = nil
	_, _, err := issuer.GenerateZKIssue([]uint64{10}, [][]byte{[]byte("alice")})
	require.ErrorIs(t, err, issue2.ErrNilPublicParameters)
	issuer.PublicParams = pp

	// Inadmissible curve
	oldCurve := issuer.PublicParams.Curve
	issuer.PublicParams.Curve = math.CurveID(len(math.Curves) + 1)
	_, _, err = issuer.GenerateZKIssue([]uint64{10}, [][]byte{[]byte("alice")})
	require.ErrorIs(t, err, issue2.ErrInvalidPublicParameters)
	issuer.PublicParams.Curve = oldCurve

	// Signer nil
	issuer.Signer = nil
	_, _, err = issuer.GenerateZKIssue([]uint64{10}, [][]byte{[]byte("alice")})
	require.ErrorIs(t, err, issue2.ErrNilSigner)
}

// TestNewVerifier_ProofTypeUnavailable is T-SEC-2: verifies that issue.NewVerifier
// returns ErrProofTypeMismatch when the action's ProofType refers to a range-proof
// algorithm whose params sub-struct is not populated in PublicParams, preventing an
// attacker from selecting a verifier whose params sub-struct is nil.
//
// Scenario A: only BulletProof params populated, action claims CSP  → error.
// Scenario B: only CSP params populated, action claims BulletProof  → error.
// Scenario C: both params populated (migration), each type is accepted → no error.
func TestNewVerifier_ProofTypeUnavailable(t *testing.T) {
	pp := setup(t, 32, math.BLS12_381_BBS_GURVY)
	_, tokens := prepareInputsForZKIssue(pp, 2)

	t.Run("BulletProofPP_CSPActionType", func(t *testing.T) {
		pp := setup(t, 32, math.BLS12_381_BBS_GURVY)
		_, err := issue2.NewVerifier(tokens, pp, rp.CSPRangeProofType)
		require.ErrorIs(t, err, issue2.ErrProofTypeMismatch,
			"T-SEC-2A: CSP proof type against BulletProof-only pp must return ErrProofTypeMismatch")
	})

	t.Run("CSPParamsPP_BulletProofActionType", func(t *testing.T) {
		pp := setupCSP(t, 32, math.BLS12_381_BBS_GURVY)
		_, err := issue2.NewVerifier(tokens, pp, rp.RangeProofType)
		require.ErrorIs(t, err, issue2.ErrProofTypeMismatch,
			"T-SEC-2B: BulletProof proof type against CSP-only pp must return ErrProofTypeMismatch")
	})

	t.Run("BothParamsPP_BulletProofActionType", func(t *testing.T) {
		pp := setup(t, 32, math.BLS12_381_BBS_GURVY)
		require.NoError(t, pp.GenerateCSPRangeProofParameters(32))
		_, err := issue2.NewVerifier(tokens, pp, rp.RangeProofType)
		require.NoError(t, err,
			"T-SEC-2C: BulletProof proof type against dual pp must be accepted")
	})

	t.Run("BothParamsPP_CSPActionType", func(t *testing.T) {
		pp := setup(t, 32, math.BLS12_381_BBS_GURVY)
		require.NoError(t, pp.GenerateCSPRangeProofParameters(32))
		_, err := issue2.NewVerifier(tokens, pp, rp.CSPRangeProofType)
		require.NoError(t, err,
			"T-SEC-2D: CSP proof type against dual pp must be accepted")
	})
}
