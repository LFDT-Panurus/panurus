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

func TestNewProverErrors(t *testing.T) {
	curve := math.Curves[math.BN254]
	pp, err := v1.Setup(32, nil, math.BN254)
	require.NoError(t, err)
	randReader, _ := curve.Rand()

	// tw[i] is nil
	validMeta := &token.Metadata{Type: "ABC", BlindingFactor: curve.NewRandomZr(randReader), Value: curve.NewZrFromInt(100)}
	_, err = NewProver([]*token.Metadata{validMeta, nil}, []*math.G1{curve.GenG1, curve.GenG1}, pp)
	require.ErrorIs(t, err, ErrInvalidTokenWitness)

	// tw[i].BlindingFactor is nil
	_, err = NewProver([]*token.Metadata{validMeta, {Type: "ABC"}}, []*math.G1{curve.GenG1, curve.GenG1}, pp)
	require.ErrorIs(t, err, ErrInvalidTokenWitness)

	// tw[i].Value is nil or invalid for Uint()
	tw := &token.Metadata{
		Type:           "ABC",
		BlindingFactor: curve.NewRandomZr(randReader),
		Value:          curve.NewRandomZr(randReader), // Likely out of range for uint64
	}
	// Ensure it is out of range by setting a very large value if possible,
	// but NewRandomZr is usually large enough.
	// Actually, let's just use a value that we know will fail Uint() if we want to test that specific error.
	// But the previous run showed it already fails with NewRandomZr.

	_, err = NewProver([]*token.Metadata{validMeta, tw}, []*math.G1{curve.GenG1, curve.GenG1}, pp)
	require.ErrorIs(t, err, ErrInvalidTokenWitnessValues)
}

// TestNewBulletProofProver_EmptyTokenWitness is T-GAP-C17: NewBulletProofProver
// indexed tw[0] (to compute the commitment to the token type) without first
// checking that tw was non-empty. An empty token-witness slice caused an
// index-out-of-range panic instead of returning an error.
func TestNewBulletProofProver_EmptyTokenWitness(t *testing.T) {
	pp, err := v1.Setup(32, nil, math.BN254)
	require.NoError(t, err)

	var proverErr error
	require.NotPanics(t, func() {
		_, proverErr = NewBulletProofProver(nil, nil, pp)
	})
	require.Error(t, proverErr)
	require.ErrorIs(t, proverErr, ErrInvalidInputs)
}

// TestNewCSPBasedProver_EmptyTokenWitness is T-GAP-C17: NewCSPBasedProver
// indexed tw[0] (to compute the commitment to the token type) without first
// checking that tw was non-empty. An empty token-witness slice caused an
// index-out-of-range panic instead of returning an error.
func TestNewCSPBasedProver_EmptyTokenWitness(t *testing.T) {
	pp, err := v1.NewWith(v1.SetupParams{
		DriverName:    v1.DLogNoGHDriverName,
		DriverVersion: v1.ProtocolV1,
		BitLength:     32,
		CurveID:       math.BN254,
		ProofType:     rp.CSPRangeProofType,
	})
	require.NoError(t, err)

	var proverErr error
	require.NotPanics(t, func() {
		_, proverErr = NewCSPBasedProver(nil, nil, pp)
	})
	require.Error(t, proverErr)
	require.ErrorIs(t, proverErr, ErrInvalidInputs)
}

// TestBulletProofVerifier_TokenCountMismatch is T-GAP-C3: verifies that a
// BulletProofVerifier configured for N+1 tokens (commitments) rejects a proof
// that was generated for N tokens.
//
// The SameType proof does not encode the token count. The range-proof count
// check in BulletProofVerifier.Verify is the only enforcement point. This test
// confirms that passing an extra commitment to the verifier causes the
// RangeCorrectness verifier to reject with a count-mismatch error.
func TestBulletProofVerifier_TokenCountMismatch(t *testing.T) {
	curve := math.Curves[math.BN254]
	pp, err := v1.Setup(32, nil, math.BN254)
	require.NoError(t, err)

	randReader, err := curve.Rand()
	require.NoError(t, err)

	// Generate a valid 1-token issue proof using the witness returned by GetTokensWithWitness.
	tokens, tw, err := token.GetTokensWithWitness([]uint64{10}, "ABC", pp.PedersenGenerators, curve)
	require.NoError(t, err)

	prover, err := NewProver(tw, tokens, pp)
	require.NoError(t, err)
	proofBytes, err := prover.Prove()
	require.NoError(t, err)

	// Construct a verifier with N=1 tokens — must succeed.
	verifier := NewBulletProofVerifier(tokens, pp)
	require.NoError(t, verifier.Verify(proofBytes))

	// Now construct a verifier with N+1=2 tokens by appending a second commitment.
	// The range proof was generated for 1 token so the count check will fail.
	extraToken := curve.GenG1.Mul(curve.NewRandomZr(randReader))
	verifierWithExtra := NewBulletProofVerifier(append(tokens, extraToken), pp)
	err = verifierWithExtra.Verify(proofBytes)
	require.Error(t, err, "T-GAP-C3: N+1 token verifier must reject a proof generated for N tokens")
}

// TestBulletProofVerifier_HeterogeneousTokenTypesRejected is F-06 (zkatdlog security
// report): "SameType Proof Does Not Verify Tokens Encode the Proven Type Commitment"
// claims that SameTypeVerifier.Verify only recomputes the Fiat-Shamir challenge over
// CommitmentToType and the proof's own responses, never inspecting v.Tokens, so a
// rogue issuer could commit tokens of different types while a self-consistent
// SameType proof claims a single type — the two are, on paper, unlinked.
//
// This is refuted end-to-end. NewBulletProofProver derives CommitmentToType from
// tw[0].Type alone (bfissue.go) and, for every token, subtracts that single
// commitment to obtain coms[i] := tokens[i] - CommitmentToType before handing coms
// to the range prover as the value/blinding-factor commitment. Because
// tokens[i] = G_type^Hash(tw[i].Type) * G_val^tw[i].Value * G_bf^tw[i].BlindingFactor
// (token.computeTokens), any tw[i].Type != tw[0].Type leaves a nonzero residual
// along G_type inside coms[i]. The range prover then has to supply a
// discrete-log-consistent opening of coms[i] under (G_val, G_bf) alone to satisfy the
// Bulletproof polynomial identity checked by rangeVerifier.Verify — it cannot, because
// doing so would require knowing a relation between G_type and (G_val, G_bf), which is
// intractable under the discrete-log assumption. So the range proof for the
// mismatched-type token fails verification, even though the SameType sub-proof itself
// verifies fine in isolation.
func TestBulletProofVerifier_HeterogeneousTokenTypesRejected(t *testing.T) {
	curve := math.Curves[math.BN254]
	pp, err := v1.Setup(32, nil, math.BN254)
	require.NoError(t, err)

	randReader, err := curve.Rand()
	require.NoError(t, err)
	bf0 := curve.NewRandomZr(randReader)
	bf1 := curve.NewRandomZr(randReader)

	tokens0, tw0, err := token.GetTokensWithWitnessAndBF([]uint64{10}, []*math.Zr{bf0}, "USD", pp.PedersenGenerators, curve)
	require.NoError(t, err)
	tokens1, tw1, err := token.GetTokensWithWitnessAndBF([]uint64{20}, []*math.Zr{bf1}, "EUR", pp.PedersenGenerators, curve)
	require.NoError(t, err)

	// A rogue prover assembling a batch with heterogeneous types, bypassing the
	// standard Issuer.GenerateZKIssue single-type API.
	tokens := []*math.G1{tokens0[0], tokens1[0]}
	tw := []*token.Metadata{tw0[0], tw1[0]}

	prover, err := NewBulletProofProver(tw, tokens, pp)
	require.NoError(t, err)
	proofBytes, err := prover.Prove()
	require.NoError(t, err)

	verifier := NewBulletProofVerifier(tokens, pp)
	err = verifier.Verify(proofBytes)
	require.Error(t, err, "F-06: heterogeneous-type issue proof (USD, EUR) must be rejected")
	require.ErrorIs(t, err, ErrInvalidIssueProof)
}

// TestCSPVerifier_HeterogeneousTokenTypesRejected mirrors
// TestBulletProofVerifier_HeterogeneousTokenTypesRejected (F-06) for the CSP-based
// range-proof path, which shares the same SameType component and the same
// coms[i] := tokens[i] - CommitmentToType construction (cspissue.go).
func TestCSPVerifier_HeterogeneousTokenTypesRejected(t *testing.T) {
	pp, err := v1.NewWith(v1.SetupParams{
		DriverName:    v1.DLogNoGHDriverName,
		DriverVersion: v1.ProtocolV1,
		BitLength:     32,
		CurveID:       math.BN254,
		ProofType:     rp.CSPRangeProofType,
	})
	require.NoError(t, err)
	curve := math.Curves[pp.Curve]

	randReader, err := curve.Rand()
	require.NoError(t, err)
	bf0 := curve.NewRandomZr(randReader)
	bf1 := curve.NewRandomZr(randReader)

	tokens0, tw0, err := token.GetTokensWithWitnessAndBF([]uint64{10}, []*math.Zr{bf0}, "USD", pp.PedersenGenerators, curve)
	require.NoError(t, err)
	tokens1, tw1, err := token.GetTokensWithWitnessAndBF([]uint64{20}, []*math.Zr{bf1}, "EUR", pp.PedersenGenerators, curve)
	require.NoError(t, err)

	tokens := []*math.G1{tokens0[0], tokens1[0]}
	tw := []*token.Metadata{tw0[0], tw1[0]}

	prover, err := NewProver(tw, tokens, pp)
	require.NoError(t, err)
	proofBytes, err := prover.Prove()
	require.NoError(t, err)

	verifier, err := NewVerifier(tokens, pp, rp.CSPRangeProofType)
	require.NoError(t, err)
	err = verifier.Verify(proofBytes)
	require.Error(t, err, "F-06: heterogeneous-type CSP issue proof (USD, EUR) must be rejected")
	require.ErrorIs(t, err, ErrInvalidIssueProof)
}
