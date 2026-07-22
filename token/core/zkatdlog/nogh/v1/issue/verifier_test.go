/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package issue_test

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/asn1"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/stretchr/testify/require"
)

// TestBulletProofVerifierRejectsInfinityInRangeCorrectnessProof is T-GAP-C10.
//
// BulletProofVerifier.Verify validated the SameType sub-proof structurally
// before use, but not the RangeCorrectness sub-proof: unlike the transfer
// path's Proof.Validate (bftransfer.go), it went straight from Deserialize
// into RangeCorrectness.Verify. Every point-at-infinity substitution tried
// against an unpatched verifier (single/all IPA.L, IPA.R, and RangeProofData's
// C/D/T1/T2) was still rejected, but only deep inside the cryptographic
// equation checks in rangeVerifier.Verify/ipaVerifier.Verify, with the generic
// "invalid IPA"/"invalid range proof" errors rather than the specific
// "element is infinity" diagnosis. Adding RangeCorrectness.Validate(curveID)
// right after SameType.Validate closes that gap for issue actions, mirroring
// 13a8bd89d's fix to IPA.Validate for the transfer path and giving issue the
// same defense-in-depth structural check transfer already has.
func TestBulletProofVerifierRejectsInfinityInRangeCorrectnessProof(t *testing.T) {
	curve := math.Curves[math.BN254]
	pp, err := v1.Setup(32, nil, math.BN254)
	require.NoError(t, err)

	tokens, tw, err := token.GetTokensWithWitness([]uint64{10}, "ABC", pp.PedersenGenerators, curve)
	require.NoError(t, err)
	prover, err := issue.NewProver(tw, tokens, pp)
	require.NoError(t, err)
	proofBytes, err := prover.Prove()
	require.NoError(t, err)

	bp := &issue.BulletProof{}
	require.NoError(t, bp.Deserialize(proofBytes))

	// Substitute the point at infinity for the first IPA.L element of the
	// first range proof.
	require.NotEmpty(t, bp.RangeCorrectness.Proofs)
	require.NotEmpty(t, bp.RangeCorrectness.Proofs[0].IPA.L)
	bp.RangeCorrectness.Proofs[0].IPA.L[0] = curve.NewG1()

	corrupted, err := bp.Serialize()
	require.NoError(t, err)

	verifier, err := issue.NewVerifier(tokens, pp, rp.RangeProofType)
	require.NoError(t, err)

	var verifyErr error
	require.NotPanics(t, func() {
		verifyErr = verifier.Verify(corrupted)
	})
	require.Error(t, verifyErr)
	require.ErrorIs(t, verifyErr, issue.ErrInvalidIssueProof)
	require.ErrorContains(t, verifyErr, "element is infinity")
}

// TestBulletProofVerifierRejectsTruncatedRangeCorrectnessProof guards against a
// BulletProof whose RangeCorrectness sub-proof was serialized with a truncated
// IPA (missing the L/R commitment arrays). In that case IPA.Deserialize leaves
// L and R nil without returning an error (asn1.NextG1Array returns (nil, nil)
// past the end of the encoded values). Before RangeCorrectness.Validate was
// added to BulletProofVerifier.Verify, this nil check happened deep inside
// ipaVerifier.Verify rather than at the structural-validation boundary; it was
// already caught (see the "invalid IPA proof" errors below), but only because
// every Verify method along the chain repeats its own defensive nil checks.
// This test locks in the more specific, earlier rejection path.
func TestBulletProofVerifierRejectsTruncatedRangeCorrectnessProof(t *testing.T) {
	curve := math.Curves[math.BN254]
	pp, err := v1.Setup(32, nil, math.BN254)
	require.NoError(t, err)

	tokens, tw, err := token.GetTokensWithWitness([]uint64{10}, "ABC", pp.PedersenGenerators, curve)
	require.NoError(t, err)
	prover, err := issue.NewProver(tw, tokens, pp)
	require.NoError(t, err)
	proofBytes, err := prover.Prove()
	require.NoError(t, err)

	bp := &issue.BulletProof{}
	require.NoError(t, bp.Deserialize(proofBytes))

	// Truncate: serialize the first range proof's IPA with only Left/Right,
	// dropping the L/R commitment arrays.
	ipa := bp.RangeCorrectness.Proofs[0].IPA
	truncatedIPA, err := asn1.MarshalMath(ipa.Left, ipa.Right)
	require.NoError(t, err)

	truncatedRangeProof, err := asn1.Marshal[asn1.Serializer](
		bp.RangeCorrectness.Proofs[0].Data,
		&rawSerializer{truncatedIPA},
	)
	require.NoError(t, err)

	rangeProofsArray, err := asn1.NewArray([]asn1.Serializer{&rawSerializer{truncatedRangeProof}})
	require.NoError(t, err)
	truncatedRangeCorrectness, err := asn1.Marshal[asn1.Serializer](rangeProofsArray)
	require.NoError(t, err)

	corrupted, err := asn1.Marshal[asn1.Serializer](
		bp.SameType,
		&rawSerializer{truncatedRangeCorrectness},
	)
	require.NoError(t, err)

	verifier, err := issue.NewVerifier(tokens, pp, rp.RangeProofType)
	require.NoError(t, err)

	var verifyErr error
	require.NotPanics(t, func() {
		verifyErr = verifier.Verify(corrupted)
	})
	require.Error(t, verifyErr)
	require.ErrorIs(t, verifyErr, issue.ErrInvalidIssueProof)
	require.ErrorContains(t, verifyErr, "nil L")
}
