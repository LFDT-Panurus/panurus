/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package transfer_test

import (
	stdasn1 "encoding/asn1"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/asn1"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// asn1Values mirrors the private asn1.Values{Values [][]byte} wire struct so
// tests can truncate an already-serialized asn1.Marshal envelope without
// reaching into a proof type's unexported fields.
type asn1Values struct {
	Values [][]byte
}

// rawSerializer wraps a pre-encoded byte slice so it can be injected verbatim
// into an asn1.Marshal[asn1.Serializer](...) envelope in place of a real
// asn1.Serializer.
type rawSerializer struct {
	raw []byte
}

func (r *rawSerializer) Serialize() ([]byte, error) { return r.raw, nil }
func (r *rawSerializer) Deserialize([]byte) error   { return nil }

// TestNewCSPBasedProver_EmptyInputWitness is T-GAP-C16: NewCSPBasedProver indexed
// inputWitness[0] (to compute the commitment to the token type) without first checking
// that inputWitness was non-empty. An empty inputWitness slice caused an
// index-out-of-range panic instead of returning an error.
func TestNewCSPBasedProver_EmptyInputWitness(t *testing.T) {
	pp, err := setupWithProofType(TestBits, TestCurve, rp.CSPRangeProofType)
	require.NoError(t, err)

	_, outtw, _, out, err := prepareInputsForZKTransfer(pp, 1, 1)
	require.NoError(t, err)

	var proverErr error
	require.NotPanics(t, func() {
		_, proverErr = transfer.NewCSPBasedProver(nil, outtw, nil, out, pp)
	})
	require.Error(t, proverErr)
	assert.Contains(t, proverErr.Error(), "invalid number of token inputs")
}

// TestCSPVerifierRejectsTruncatedRangeCorrectnessProof is the transfer-side
// mirror of issue.TestCSPVerifierRejectsTruncatedRangeCorrectnessProof
// (T-GAP-C18): verifies that a CSP-based transfer proof whose RangeCorrectness
// sub-proof was serialized with a truncated RangeProof (missing every field
// past pokV.A) is rejected by CSPVerifier.Verify.
//
// A 1-input/1-output ownership transfer skips RangeCorrectness entirely (see
// NewCSPBasedProver/NewCSPVerifier), so this uses a 2-input/2-output transfer
// to ensure RangeCorrectness is actually populated and exercised.
//
// csp.RangeProof.Validate used to be a no-op that only restored the transient
// Curve field lost during deserialization, so it reported a RangeProof with
// every other field nil as valid. As with the issue-side path, the truncated
// proof here was already rejected before that fix (rangeVerifier.Verify's own
// validateRangeProof call caught it one layer deeper), so this closes a
// defense-in-depth/diagnostics gap rather than a demonstrated silent-accept.
// CSPProof.Validate now performs the same structural check one layer earlier.
func TestCSPVerifierRejectsTruncatedRangeCorrectnessProof(t *testing.T) {
	pp, err := setupWithProofType(TestBits, TestCurve, rp.CSPRangeProofType)
	require.NoError(t, err)

	intw, outtw, in, out, err := prepareInputsForZKTransfer(pp, 2, 2)
	require.NoError(t, err)

	prover, err := transfer.NewCSPBasedProver(intw, outtw, in, out, pp)
	require.NoError(t, err)
	proofBytes, err := prover.Prove()
	require.NoError(t, err)

	cp := &transfer.CSPProof{}
	require.NoError(t, cp.Deserialize(proofBytes))
	require.NotEmpty(t, cp.RangeCorrectness.Proofs)

	// Truncate the first range proof's genuine wire encoding to only its
	// first 2 elements (pComm, pokV.A), dropping pokV.Z, u, sComm, sEval, and
	// the inner cspProof arrays. csp.RangeProof's fields are unexported, so
	// the truncation is done at the raw ASN.1 Values level instead of
	// reaching into the struct.
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
		cp.TypeAndSum,
		&rawSerializer{truncatedRangeCorrectness},
	)
	require.NoError(t, err)

	verifier := transfer.NewCSPVerifier(in, out, pp)

	var verifyErr error
	require.NotPanics(t, func() {
		verifyErr = verifier.Verify(corrupted)
	}, "T-GAP-C18: Verify must not panic on a truncated RangeCorrectness sub-proof")
	require.Error(t, verifyErr)
}
