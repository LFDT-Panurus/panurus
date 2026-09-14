/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue

import (
	"testing"

	math "github.com/IBM/mathlib"
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
