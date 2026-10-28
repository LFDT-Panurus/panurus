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

// cspIssuePP returns public parameters configured for the CSP range-proof system.
func cspIssuePP(tb testing.TB) *v1.PublicParams {
	tb.Helper()
	pp, err := v1.NewWith(v1.SetupParams{
		DriverName:    v1.DLogNoGHDriverName,
		DriverVersion: v1.ProtocolV1,
		BitLength:     32,
		CurveID:       math.BN254,
		ProofType:     rp.CSPRangeProofType,
	})
	require.NoError(tb, err)

	return pp
}

// bulletProofIssuePP returns public parameters configured for the BulletProof
// range-proof system.
func bulletProofIssuePP(tb testing.TB) *v1.PublicParams {
	tb.Helper()
	pp, err := v1.Setup(32, nil, math.BN254)
	require.NoError(tb, err)

	return pp
}

// TestIssueVerifiersRejectInvalidTokenCommitments covers #2555: CSPVerifier.Verify
// validated the proof's own CommitmentToType against pp.Curve but not the token
// commitments it was constructed with, even though those come straight off the wire
// (utils.FromG1Proto accepts a G1 encoded on any valid curve, and Action.Validate ->
// Token.Validate(false) only nil-checks Data).
//
// So an issue action carrying an output commitment on a different valid curve passed
// every check and reached
//
//	coms[i] = v.SameType.Tokens[i].Copy(); coms[i].Sub(commitmentToType)
//
// where the foreign curve's Sub type-asserts its argument to its own backend type and
// panics ("interface conversion: driver.G1 is *gurvy.bn254G1, not *bls12381.G1"). The
// panic is unrecovered in IssueValidate, so it crashes the validation goroutine —
// an unauthenticated remote DoS.
//
// A nil and a point-at-infinity commitment are covered in the same table: the first
// panics on Copy(), and the second is not a commitment any honest issuer produces
// (g_0^H(T)·g_1^V·g_2^r is the identity only with negligible probability).
//
// Both proof systems are covered. The unguarded loop was identical in verifier.go
// (BulletProof) and cspissue.go (CSP), and which one runs is chosen by the attacker's
// own ProofType field, so fixing one and not the other would leave the crash fully
// reachable by flipping that field. The guard therefore lives on the shared
// SameTypeVerifier and both verifiers call it.
func TestIssueVerifiersRejectInvalidTokenCommitments(t *testing.T) {
	curve := math.Curves[math.BN254]

	for _, ps := range []struct {
		name      string
		proofType rp.ProofType
		pp        func(testing.TB) *v1.PublicParams
	}{
		{"CSP", rp.CSPRangeProofType, cspIssuePP},
		{"BulletProof", rp.RangeProofType, bulletProofIssuePP},
	} {
		t.Run(ps.name, func(t *testing.T) {
			pp := ps.pp(t)
			tokens, tw, err := token.GetTokensWithWitness([]uint64{10, 20}, "ABC", pp.PedersenGenerators, curve)
			require.NoError(t, err)
			prover, err := issue.NewProver(tw, tokens, pp)
			require.NoError(t, err)
			require.Equal(t, ps.proofType, prover.RangeProofType(), "NewProver must select the proof system under test")
			proofBytes, err := prover.Prove()
			require.NoError(t, err)

			runInvalidCommitmentCases(t, pp, ps.proofType, curve, tokens, proofBytes)
		})
	}
}

// runInvalidCommitmentCases feeds each malformed commitment shape to the issue
// verifier for proofType and asserts a clean typed error instead of a panic.
func runInvalidCommitmentCases(
	t *testing.T,
	pp *v1.PublicParams,
	proofType rp.ProofType,
	curve *math.Curve,
	tokens []*math.G1,
	proofBytes []byte,
) {
	t.Helper()
	for _, tc := range []struct {
		name   string
		tokens func() []*math.G1
	}{
		{
			name: "foreign curve commitment",
			tokens: func() []*math.G1 {
				return []*math.G1{math.Curves[math.BLS12_381].GenG1.Copy(), tokens[1]}
			},
		},
		{
			name: "nil commitment",
			tokens: func() []*math.G1 {
				return []*math.G1{nil, tokens[1]}
			},
		},
		{
			name: "infinity commitment",
			tokens: func() []*math.G1 {
				return []*math.G1{curve.NewG1(), tokens[1]}
			},
		},
		{
			name: "no commitments",
			tokens: func() []*math.G1 {
				return nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verifier, err := issue.NewVerifier(tc.tokens(), pp, proofType)
			require.NoError(t, err)

			var verifyErr error
			require.NotPanics(t, func() {
				verifyErr = verifier.Verify(proofBytes)
			}, "#2555: Verify must not panic on an invalid token commitment")
			require.Error(t, verifyErr)
			require.ErrorIs(t, verifyErr, issue.ErrInvalidIssueProof)
			require.ErrorIs(t, verifyErr, issue.ErrInvalidTokenCommitments)
		})
	}
}

// TestCSPIssueActionForeignCurveCommitmentRejectedEndToEnd walks the full wire path
// #2555 describes, rather than constructing the verifier directly: an attacker-built
// issue action is serialized, deserialized, and run through exactly the sequence
// IssueValidate uses (Deserialize -> Validate -> GetCommitments -> NewVerifier ->
// Verify).
//
// It pins the two facts that made the panic reachable without any authorization: the
// foreign-curve commitment survives the protobuf round-trip (utils.FromG1Proto honours
// the sender's curve id), and Action.Validate accepts it (Token.Validate(false) only
// nil-checks Data). Verification must then reject it rather than panic.
func TestCSPIssueActionForeignCurveCommitmentRejectedEndToEnd(t *testing.T) {
	curve := math.Curves[math.BN254]
	pp := cspIssuePP(t)

	tokens, tw, err := token.GetTokensWithWitness([]uint64{10}, "ABC", pp.PedersenGenerators, curve)
	require.NoError(t, err)
	prover, err := issue.NewCSPBasedProver(tw, tokens, pp)
	require.NoError(t, err)
	proofBytes, err := prover.Prove()
	require.NoError(t, err)

	raw, err := (&issue.Action{
		Issuer: []byte("issuer"),
		Outputs: []*token.Token{
			{Owner: []byte("owner-1"), Data: math.Curves[math.BLS12_381].GenG1.Copy()},
		},
		ProofType: rp.CSPRangeProofType,
		Proof:     proofBytes,
	}).Serialize()
	require.NoError(t, err)

	action := &issue.Action{}
	require.NoError(t, action.Deserialize(raw))
	require.NoError(t, action.Validate(), "Action.Validate only nil-checks the commitment")

	commitments, err := action.GetCommitments()
	require.NoError(t, err)
	require.NotEqual(t, pp.Curve, commitments[0].CurveID(),
		"the foreign curve id must survive the wire round-trip for this test to be meaningful")

	verifier, err := issue.NewVerifier(commitments, pp, action.ProofType)
	require.NoError(t, err)

	var verifyErr error
	require.NotPanics(t, func() {
		verifyErr = verifier.Verify(action.GetProof())
	}, "#2555: a foreign-curve output commitment must not panic the validator")
	require.ErrorIs(t, verifyErr, issue.ErrInvalidTokenCommitments)
}
