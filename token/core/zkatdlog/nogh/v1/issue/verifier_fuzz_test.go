/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue

import (
	"encoding/json"
	math2 "math"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/utils"
	protomath "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/stretchr/testify/require"
)

const maxFuzzIssueProofBytes = 256 << 10

// Deterministic so a committed crash seed reproduces: the PoK check binds to the token
// commitments, and random blinding factors would make the seed fail PoK instead.
const fuzzBlindingFactor = 0xC0FFEE

func fuzzTokens(tb testing.TB, curve *math.Curve, pedersen []*math.G1) ([]*math.G1, []*token.Metadata) {
	tb.Helper()
	bfs := []*math.Zr{curve.NewZrFromInt(fuzzBlindingFactor)}
	tokens, tw, err := token.GetTokensWithWitnessAndBF([]uint64{10}, bfs, "ABC", pedersen, curve)
	require.NoError(tb, err)

	return tokens, tw
}

// Fuzzes the exact call IssueValidate makes on unauthenticated wire bytes. The seed
// corpus includes a genuine proof so valid structure is mutated, not just garbage.
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

	verifier, err := NewBulletProofVerifier(tokens, pp)
	require.NoError(f, err)
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

	verifier, err := NewCSPVerifier(tokens, pp)
	require.NoError(f, err)
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzIssueProofBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_ = verifier.Verify(raw)
		})
	})
}

// The wire form of a commitment: FromG1Proto decodes this shape and only bounds-checks
// the curve id.
func fuzzCommitmentProto(curveID int32, element []byte) *protomath.G1 {
	raw, err := json.Marshal(struct {
		CurveID int32  `json:"curve"`
		Element []byte `json:"element"`
	}{CurveID: curveID, Element: element})
	if err != nil {
		return nil
	}

	return &protomath.G1{Raw: raw}
}

// Bounds-checked rather than asserted in case the curve table ever grows.
func curveSeed(id int) int32 {
	if id > math2.MaxInt32 || id < math2.MinInt32 {
		return 0
	}

	return int32(id)
}

// Fuzzes the commitment side (issue #2554), which the targets above cannot reach: they
// hold the commitments fixed, so no proof mutation produces a cross-curve one.
func FuzzIssueVerifierCommitmentNoPanic(f *testing.F) {
	bpPP, err := v1.Setup(32, nil, math.BN254)
	require.NoError(f, err)
	cspPP, err := v1.NewWith(v1.SetupParams{
		DriverName:    v1.DLogNoGHDriverName,
		DriverVersion: v1.ProtocolV1,
		BitLength:     32,
		CurveID:       math.BN254,
		ProofType:     rp.CSPRangeProofType,
	})
	require.NoError(f, err)

	curve := math.Curves[math.BN254]
	tokens, tw := fuzzTokens(f, curve, bpPP.PedersenGenerators)

	bpProver, err := NewBulletProofProver(tw, tokens, bpPP)
	require.NoError(f, err)
	bpProof, err := bpProver.Prove()
	require.NoError(f, err)

	cspTokens, cspTW := fuzzTokens(f, curve, cspPP.PedersenGenerators)
	cspProver, err := NewCSPBasedProver(cspTW, cspTokens, cspPP)
	require.NoError(f, err)
	cspProof, err := cspProver.Prove()
	require.NoError(f, err)

	// Seeds: the honest commitment, the cross-curve commitment that panicked before the
	// fix, the point at infinity, an out-of-range curve id, and truncated/empty elements.
	f.Add(curveSeed(int(math.BN254)), tokens[0].Bytes())
	f.Add(curveSeed(int(math.BLS12_377_GURVY)), math.Curves[math.BLS12_377_GURVY].GenG1.Bytes())
	f.Add(curveSeed(int(math.BN254)), curve.NewG1().Bytes())
	f.Add(curveSeed(len(math.Curves)), tokens[0].Bytes())
	f.Add(curveSeed(-1), tokens[0].Bytes())
	f.Add(curveSeed(int(math.BN254)), []byte{})
	f.Add(curveSeed(int(math.BN254)), tokens[0].Bytes()[:len(tokens[0].Bytes())/2])

	f.Fuzz(func(t *testing.T, curveID int32, element []byte) {
		if len(element) > maxFuzzIssueProofBytes {
			t.Skip()
		}
		p := fuzzCommitmentProto(curveID, element)
		if p == nil {
			t.Skip()
		}

		// Only a commitment that decodes can reach the proof arithmetic.
		com, err := utils.FromG1Proto(p)
		if err != nil || com == nil {
			t.Skip()
		}

		// The check is at construction, so the constructor is inside the NotPanics too.
		require.NotPanics(t, func() {
			v, err := NewBulletProofVerifier([]*math.G1{com}, bpPP)
			if err != nil {
				return
			}
			_ = v.Verify(bpProof)
		})
		require.NotPanics(t, func() {
			v, err := NewCSPVerifier([]*math.G1{com}, cspPP)
			if err != nil {
				return
			}
			_ = v.Verify(cspProof)
		})
	})
}
