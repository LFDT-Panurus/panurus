/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/utils"
	mathproto "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/stretchr/testify/require"
)

const maxFuzzIssueProofBytes = 256 << 10

// fuzzBlindingFactor is a fixed, deterministic blinding factor used to build the
// harness tokens. Token blinding factors must be deterministic across runs:
// several panics (e.g. the cross-curve proof.u panic in the CSP range verifier)
// are only reached after the proof's proof-of-knowledge check passes, and that
// check binds to the token commitments. With random per-run blinding factors a
// committed crash seed's proof fails the PoK check against freshly-randomised
// tokens and returns an error instead of panicking, so no regression seed could
// ever reproduce. A fixed factor makes crashes reproducible from the corpus.
const fuzzBlindingFactor = 0xC0FFEE

func fuzzTokens(tb testing.TB, curve *math.Curve, pedersen []*math.G1) ([]*math.G1, []*token.Metadata) {
	tb.Helper()
	bfs := []*math.Zr{curve.NewZrFromInt(fuzzBlindingFactor)}
	tokens, tw, err := token.GetTokensWithWitnessAndBF([]uint64{10}, bfs, "ABC", pedersen, curve)
	require.NoError(tb, err)

	return tokens, tw
}

// FuzzBulletProofVerifierNoPanic fuzzes BulletProofVerifier.Verify with arbitrary
// proof bytes. This is the exact call IssueValidate makes on unauthenticated
// wire bytes (validator_issue.go's zkVerifier.Verify(action.GetProof())), so any
// panic anywhere in Deserialize/Validate/Verify's chain is an unauthenticated
// validator DoS. The seed corpus includes a genuine proof so mutations of valid
// structure are explored, not just garbage bytes.
func FuzzBulletProofVerifierNoPanic(f *testing.F) {
	curve := math.Curves[math.BN254]
	pp, err := v1.Setup(32, nil, math.BN254)
	require.NoError(f, err)

	tokens, tw := fuzzTokens(f, curve, pp.PedersenGenerators)
	prover, err := NewBulletProofProver(tw, tokens, pp)
	require.NoError(f, err)
	proofBytes, err := prover.Prove()
	require.NoError(f, err)

	f.Add(proofBytes)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add(proofBytes[:len(proofBytes)/2])

	verifier := NewBulletProofVerifier(tokens, pp)
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzIssueProofBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_ = verifier.Verify(raw)
		})
	})
}

// FuzzCSPVerifierNoPanic mirrors FuzzBulletProofVerifierNoPanic for the CSP-based
// issue proof verifier.
func FuzzCSPVerifierNoPanic(f *testing.F) {
	curve := math.Curves[math.BN254]
	pp, err := v1.NewWith(v1.SetupParams{
		DriverName:    v1.DLogNoGHDriverName,
		DriverVersion: v1.ProtocolV1,
		BitLength:     32,
		CurveID:       math.BN254,
		ProofType:     rp.CSPRangeProofType,
	})
	require.NoError(f, err)

	tokens, tw := fuzzTokens(f, curve, pp.PedersenGenerators)
	prover, err := NewCSPBasedProver(tw, tokens, pp)
	require.NoError(f, err)
	proofBytes, err := prover.Prove()
	require.NoError(f, err)

	f.Add(proofBytes)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add(proofBytes[:len(proofBytes)/2])

	verifier := NewCSPVerifier(tokens, pp)
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzIssueProofBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_ = verifier.Verify(raw)
		})
	})
}

// g1Raw returns the wire encoding of a G1 point as it travels inside an issue
// action's output (actions.Token.Data.Raw, produced by utils.ToProtoG1).
func g1Raw(tb testing.TB, g1 *math.G1) []byte {
	tb.Helper()
	p, err := utils.ToProtoG1(g1)
	require.NoError(tb, err)

	return p.Raw
}

// FuzzCSPVerifierTokenCommitmentNoPanic fuzzes the *token commitment* half of the CSP
// issue verifier's input, which FuzzCSPVerifierNoPanic cannot reach: that target holds
// the commitments fixed and only fuzzes the proof bytes, but the commitments are
// attacker-controlled too. They arrive as actions.Token.Data.Raw, are decoded by
// utils.FromG1Proto — which accepts a G1 encoded on any valid curve — and are only
// nil-checked by Action.Validate before IssueValidate hands them to issue.NewVerifier.
//
// Both halves are fuzzed together because the panic this guards (#2555) needs a
// structurally valid proof to get as far as the commitment arithmetic. The seed corpus
// pins the original crash: a BLS12-381 commitment against BN254 public parameters,
// which used to panic in coms[i].Sub(commitmentToType).
func FuzzCSPVerifierTokenCommitmentNoPanic(f *testing.F) {
	curve := math.Curves[math.BN254]
	pp, err := v1.NewWith(v1.SetupParams{
		DriverName:    v1.DLogNoGHDriverName,
		DriverVersion: v1.ProtocolV1,
		BitLength:     32,
		CurveID:       math.BN254,
		ProofType:     rp.CSPRangeProofType,
	})
	require.NoError(f, err)

	tokens, tw := fuzzTokens(f, curve, pp.PedersenGenerators)
	prover, err := NewCSPBasedProver(tw, tokens, pp)
	require.NoError(f, err)
	proofBytes, err := prover.Prove()
	require.NoError(f, err)

	f.Add(g1Raw(f, tokens[0]), proofBytes)
	// The #2555 crash: a commitment on a different valid curve than pp.Curve.
	f.Add(g1Raw(f, math.Curves[math.BLS12_381].GenG1), proofBytes)
	f.Add(g1Raw(f, curve.NewG1()), proofBytes)
	f.Add([]byte{}, proofBytes)
	f.Add([]byte("malformed"), proofBytes)
	f.Add(g1Raw(f, tokens[0]), []byte{})
	f.Add(g1Raw(f, tokens[0]), []byte("malformed"))

	f.Fuzz(func(t *testing.T, commitmentRaw, proof []byte) {
		if len(commitmentRaw) > maxFuzzIssueProofBytes || len(proof) > maxFuzzIssueProofBytes {
			t.Skip()
		}

		require.NotPanics(t, func() {
			commitment, err := utils.FromG1Proto(&mathproto.G1{Raw: commitmentRaw})
			if err != nil {
				return
			}
			_ = NewCSPVerifier([]*math.G1{commitment}, pp).Verify(proof)
		})
	})
}
