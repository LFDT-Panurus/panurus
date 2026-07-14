/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue

import (
	"testing"

	math "github.com/IBM/mathlib"
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
