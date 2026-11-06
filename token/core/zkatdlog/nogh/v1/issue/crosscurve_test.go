/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue_test

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/stretchr/testify/require"
)

// foreignCurveID is any valid math.Curves index other than the one the params use.
const foreignCurveID = math.BLS12_377_GURVY

// Issue path of #2554/#2555: a wrong-curve commitment panicked in Sub, unrecovered and
// before any authorization check — an unauthenticated remote DoS.
func TestVerifierRejectsCrossCurveTokenCommitment(t *testing.T) {
	for _, tc := range []struct {
		name      string
		proofType rp.ProofType
	}{
		{"BulletProof", rp.RangeProofType},
		{"CSP", rp.CSPRangeProofType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pp, err := v1.NewWith(v1.SetupParams{
				DriverName:    v1.DLogNoGHDriverName,
				DriverVersion: v1.ProtocolV1,
				BitLength:     32,
				CurveID:       math.BN254,
				ProofType:     tc.proofType,
			})
			require.NoError(t, err)
			curve := math.Curves[pp.Curve]

			// Genuine proof and tokens: only the commitment is swapped, so the test cannot
			// pass merely because the proof is malformed.
			tokens, tw, err := token.GetTokensWithWitness([]uint64{10}, "ABC", pp.PedersenGenerators, curve)
			require.NoError(t, err)
			prover, err := issue.NewProver(tw, tokens, pp)
			require.NoError(t, err)
			proofBytes, err := prover.Prove()
			require.NoError(t, err)

			foreign := math.Curves[foreignCurveID].GenG1.Copy()

			var (
				verifier  issue.Verifier
				verifyErr error
			)
			require.NotPanics(t, func() {
				verifier, verifyErr = issue.NewVerifier([]*math.G1{foreign}, pp, tc.proofType)
			})
			require.Error(t, verifyErr)
			require.Nil(t, verifier)
			require.ErrorIs(t, verifyErr, issue.ErrInvalidTokenCommitment)
			require.ErrorContains(t, verifyErr, "element curve must equal curve ID")

			// The honest commitment still verifies.
			honest, err := issue.NewVerifier(tokens, pp, tc.proofType)
			require.NoError(t, err)
			require.NoError(t, honest.Verify(proofBytes))
		})
	}
}

// The other two shapes the guard rejects. Nil is reachable on the wire: a nil Data
// encodes as an empty Raw that decodes back to nil.
func TestVerifierRejectsDegenerateTokenCommitment(t *testing.T) {
	pp, err := v1.Setup(32, nil, math.BN254)
	require.NoError(t, err)
	curve := math.Curves[pp.Curve]

	for _, tc := range []struct {
		name string
		com  *math.G1
		want string
	}{
		{"nil", nil, "element is nil"},
		{"infinity", curve.NewG1(), "element is infinity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				verifier  issue.Verifier
				verifyErr error
			)
			require.NotPanics(t, func() {
				verifier, verifyErr = issue.NewVerifier([]*math.G1{tc.com}, pp, rp.RangeProofType)
			})
			require.Error(t, verifyErr)
			require.Nil(t, verifier)
			require.ErrorIs(t, verifyErr, issue.ErrInvalidTokenCommitment)
			require.ErrorContains(t, verifyErr, tc.want)
		})
	}
}

// With no commitments the range proof is vacuously valid. Action.Validate rejects this
// earlier on the real path; this pins the verifier's own guard for direct callers.
func TestVerifierRejectsEmptyTokenCommitments(t *testing.T) {
	for _, proofType := range []rp.ProofType{rp.RangeProofType, rp.CSPRangeProofType} {
		pp, err := v1.NewWith(v1.SetupParams{
			DriverName:    v1.DLogNoGHDriverName,
			DriverVersion: v1.ProtocolV1,
			BitLength:     32,
			CurveID:       math.BN254,
			ProofType:     proofType,
		})
		require.NoError(t, err)

		var (
			verifier  issue.Verifier
			verifyErr error
		)
		require.NotPanics(t, func() {
			verifier, verifyErr = issue.NewVerifier([]*math.G1{}, pp, proofType)
		})
		require.Error(t, verifyErr)
		require.Nil(t, verifier)
		require.ErrorIs(t, verifyErr, issue.ErrNoTokenCommitments)
	}
}
