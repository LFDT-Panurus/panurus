/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue_test

import (
	stdasn1 "encoding/asn1"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/asn1"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/stretchr/testify/require"
)

// asn1Values mirrors the private asn1.Values{Values [][]byte} wire struct so
// tests can truncate an already-serialized asn1.Marshal envelope without
// reaching into a proof type's unexported fields.
type asn1Values struct {
	Values [][]byte
}

// TestCSPVerifierRejectsTruncatedSameTypeProof is T-GAP-C8: verifies that a
// CSP-based issue proof whose SameType sub-proof was serialized with missing
// trailing ASN.1 elements is rejected by CSPVerifier.Verify with a clean
// error rather than a panic.
//
// This mirrors TestBulletProofVerifierRejectsTruncatedSameTypeProof
// (sametype_test.go, guarding issue/verifier.go's BulletProofVerifier) for the
// CSP-based sibling path: Deserialize leaves Challenge/CommitmentToType nil
// without returning an error when trailing ASN.1 elements are missing
// (asn1.NextZr/NextG1 return (nil, nil) past the end of the encoded values).
// Before this fix, CSPVerifier.Verify went straight from Deserialize to
// v.SameType.Verify(tp.SameType) with no Validate step, so those nil fields
// reached SameTypeVerifier.Verify's Mul/Sub calls and panicked instead of
// returning an error.
func TestCSPVerifierRejectsTruncatedSameTypeProof(t *testing.T) {
	curve := math.Curves[math.BN254]
	pp, err := v1.NewWith(v1.SetupParams{
		DriverName:    v1.DLogNoGHDriverName,
		DriverVersion: v1.ProtocolV1,
		BitLength:     32,
		CurveID:       math.BN254,
		ProofType:     rp.CSPRangeProofType,
	})
	require.NoError(t, err)

	tokens, tw, err := token.GetTokensWithWitness([]uint64{10}, "ABC", pp.PedersenGenerators, curve)
	require.NoError(t, err)
	prover, err := issue.NewCSPBasedProver(tw, tokens, pp)
	require.NoError(t, err)
	proofBytes, err := prover.Prove()
	require.NoError(t, err)

	cp := &issue.CSPProof{}
	require.NoError(t, cp.Deserialize(proofBytes))

	// Truncate the SameType sub-proof to only its first 2 of 4 elements.
	truncatedSameType, err := asn1.MarshalMath(cp.SameType.Type, cp.SameType.BlindingFactor)
	require.NoError(t, err)

	corrupted, err := asn1.Marshal[asn1.Serializer](
		&rawSerializer{truncatedSameType},
		cp.RangeCorrectness,
	)
	require.NoError(t, err)

	verifier := issue.NewCSPVerifier(tokens, pp)

	var verifyErr error
	require.NotPanics(t, func() {
		verifyErr = verifier.Verify(corrupted)
	}, "T-GAP-C8: Verify must not panic on a truncated SameType sub-proof")
	require.Error(t, verifyErr)
	require.ErrorIs(t, verifyErr, issue.ErrInvalidIssueProof)
}

// TestCSPVerifierRejectsTruncatedRangeCorrectnessProof is T-GAP-C18: verifies
// that a CSP-based issue proof whose RangeCorrectness sub-proof was serialized
// with a truncated RangeProof (missing every field past pokV.A) is rejected by
// CSPVerifier.Verify.
//
// csp.RangeProof.Validate used to be a no-op that only restored the transient
// Curve field lost during deserialization, so it reported a RangeProof with
// every other field nil as valid. The truncated proof was still rejected
// before this fix (rangeVerifier.Verify's own validateRangeProof call caught
// it one layer deeper, wrapped as "invalid range proof structure"), so this
// closes a defense-in-depth/diagnostics gap rather than a demonstrated
// silent-accept — the same category as Fix #5 (BulletProofVerifier). Validate
// now performs the same structural check at the outer boundary, one layer
// earlier and with a more specific error.
func TestCSPVerifierRejectsTruncatedRangeCorrectnessProof(t *testing.T) {
	curve := math.Curves[math.BN254]
	pp, err := v1.NewWith(v1.SetupParams{
		DriverName:    v1.DLogNoGHDriverName,
		DriverVersion: v1.ProtocolV1,
		BitLength:     32,
		CurveID:       math.BN254,
		ProofType:     rp.CSPRangeProofType,
	})
	require.NoError(t, err)

	tokens, tw, err := token.GetTokensWithWitness([]uint64{10}, "ABC", pp.PedersenGenerators, curve)
	require.NoError(t, err)
	prover, err := issue.NewCSPBasedProver(tw, tokens, pp)
	require.NoError(t, err)
	proofBytes, err := prover.Prove()
	require.NoError(t, err)

	cp := &issue.CSPProof{}
	require.NoError(t, cp.Deserialize(proofBytes))

	// Truncate: take the first range proof's genuine wire encoding and drop
	// everything past its first two elements (pComm, pokV.A), dropping pokV.Z,
	// u, sComm, sEval, and the inner cspProof arrays. csp.RangeProof's fields
	// are unexported, so the truncation is done at the raw ASN.1 Values level
	// instead of reaching into the struct.
	require.NotEmpty(t, cp.RangeCorrectness.Proofs)
	genuineRangeProof, err := cp.RangeCorrectness.Proofs[0].Serialize()
	require.NoError(t, err)

	v := &asn1Values{}
	_, err = stdasn1.Unmarshal(genuineRangeProof, v)
	require.NoError(t, err)
	require.Greater(t, len(v.Values), 2)
	v.Values = v.Values[:2]
	truncatedRangeProof, err := stdasn1.Marshal(*v)
	require.NoError(t, err)

	rangeProofsArray, err := asn1.NewArray([]asn1.Serializer{&rawSerializer{truncatedRangeProof}})
	require.NoError(t, err)
	truncatedRangeCorrectness, err := asn1.Marshal[asn1.Serializer](rangeProofsArray)
	require.NoError(t, err)

	corrupted, err := asn1.Marshal[asn1.Serializer](
		cp.SameType,
		&rawSerializer{truncatedRangeCorrectness},
	)
	require.NoError(t, err)

	verifier := issue.NewCSPVerifier(tokens, pp)

	var verifyErr error
	require.NotPanics(t, func() {
		verifyErr = verifier.Verify(corrupted)
	}, "T-GAP-C18: Verify must not panic on a truncated RangeCorrectness sub-proof")
	require.Error(t, verifyErr)
	require.ErrorIs(t, verifyErr, issue.ErrInvalidIssueProof)
}
