/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package transfer_test

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/stretchr/testify/require"
)

// foreignCurveID is any valid math.Curves index other than the one the params use.
const foreignCurveID = math.BLS12_377_GURVY

// Transfer path of #2554: a wrong-curve input or output commitment panicked in Sub from
// TransferZKProofValidate, in the type-and-sum stage every transfer shape runs.
func TestVerifierRejectsCrossCurveCommitments(t *testing.T) {
	foreign := math.Curves[foreignCurveID].GenG1.Copy()

	for _, pt := range []struct {
		name      string
		proofType rp.ProofType
	}{
		{"BulletProof", rp.RangeProofType},
		{"CSP", rp.CSPRangeProofType},
	} {
		t.Run(pt.name, func(t *testing.T) {
			pp, err := setupWithProofType(TestBits, TestCurve, pt.proofType)
			require.NoError(t, err)

			// Genuine 2-in/2-out proof (so the range-proof stage is populated too): only
			// the commitments are swapped, so the test cannot pass on a malformed proof.
			intw, outtw, in, out, err := prepareInputsForZKTransfer(pp, 2, 2)
			require.NoError(t, err)
			prover, err := transfer.NewProver(intw, outtw, in, out, pp)
			require.NoError(t, err)
			proofBytes, err := prover.Prove()
			require.NoError(t, err)

			for _, tc := range []struct {
				name     string
				in, out  []*math.G1
				wantErrs []error
			}{
				{
					name: "foreign input",
					in:   []*math.G1{in[0], foreign}, out: out,
					wantErrs: []error{transfer.ErrInvalidInputCommitment, transfer.ErrInvalidTransferProof},
				},
				{
					name: "foreign output",
					in:   in, out: []*math.G1{out[0], foreign},
					wantErrs: []error{transfer.ErrInvalidOutputCommitment, transfer.ErrInvalidTransferProof},
				},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var (
						verifier transfer.Verifier
						err      error
					)
					require.NotPanics(t, func() {
						verifier, err = transfer.NewVerifier(tc.in, tc.out, pp, pt.proofType)
					})
					require.Error(t, err)
					require.Nil(t, verifier)
					for _, want := range tc.wantErrs {
						require.ErrorIs(t, err, want)
					}
					require.ErrorContains(t, err, "element curve must equal curve ID")
				})
			}

			// The honest commitments still verify.
			honest, err := transfer.NewVerifier(in, out, pp, pt.proofType)
			require.NoError(t, err)
			require.NoError(t, honest.Verify(proofBytes))
		})
	}
}

// The 1-in/1-out case skips the range proof entirely, but still goes through
// NewTypeAndSumVerifier.
func TestVerifierRejectsCrossCurveCommitmentOnOwnershipTransfer(t *testing.T) {
	pp, err := setupWithProofType(TestBits, TestCurve, rp.RangeProofType)
	require.NoError(t, err)

	_, _, in, _, err := prepareInputsForZKTransfer(pp, 1, 1)
	require.NoError(t, err)

	foreign := math.Curves[foreignCurveID].GenG1.Copy()

	var verifier transfer.Verifier
	require.NotPanics(t, func() {
		verifier, err = transfer.NewVerifier(in, []*math.G1{foreign}, pp, rp.RangeProofType)
	})
	require.Error(t, err)
	require.Nil(t, verifier)
	require.ErrorIs(t, err, transfer.ErrInvalidOutputCommitment)
}

// The other two shapes the guard rejects. Nil is reachable on the wire: a nil Data
// encodes as an empty Raw that decodes back to nil.
func TestVerifierRejectsDegenerateCommitments(t *testing.T) {
	pp, err := setupWithProofType(TestBits, TestCurve, rp.RangeProofType)
	require.NoError(t, err)
	curve := math.Curves[pp.Curve]

	_, _, in, out, err := prepareInputsForZKTransfer(pp, 2, 2)
	require.NoError(t, err)

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
				verifier transfer.Verifier
				err      error
			)
			require.NotPanics(t, func() {
				verifier, err = transfer.NewVerifier(in, []*math.G1{out[0], tc.com}, pp, rp.RangeProofType)
			})
			require.Error(t, err)
			require.Nil(t, verifier)
			require.ErrorIs(t, err, transfer.ErrInvalidOutputCommitment)
			require.ErrorContains(t, err, tc.want)
		})
	}
}
