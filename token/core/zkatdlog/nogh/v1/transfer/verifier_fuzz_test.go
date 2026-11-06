/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package transfer_test

import (
	"encoding/json"
	math2 "math"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/utils"
	protomath "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/stretchr/testify/require"
)

const maxFuzzTransferProofBytes = 256 << 10

// Fuzzes the exact call TransferValidate makes on unauthenticated wire bytes. Uses
// 2-in/2-out so RangeCorrectness is populated.
func FuzzBulletProofVerifierNoPanic(f *testing.F) {
	pp, err := setupWithProofType(TestBits, TestCurve, rp.RangeProofType)
	require.NoError(f, err)

	intw, outtw, in, out, err := prepareInputsForZKTransfer(pp, 2, 2)
	require.NoError(f, err)
	prover, err := transfer.NewBulletProofProver(intw, outtw, in, out, pp)
	require.NoError(f, err)
	proofBytes, err := prover.Prove()
	require.NoError(f, err)

	f.Add(proofBytes)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add(proofBytes[:len(proofBytes)/2])

	verifier, err := transfer.NewBulletProofVerifier(in, out, pp)
	require.NoError(f, err)
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzTransferProofBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_ = verifier.Verify(raw)
		})
	})
}

// FuzzCSPVerifierNoPanic mirrors FuzzBulletProofVerifierNoPanic for the CSP-based
// transfer proof verifier.
func FuzzCSPVerifierNoPanic(f *testing.F) {
	pp, err := setupWithProofType(TestBits, TestCurve, rp.CSPRangeProofType)
	require.NoError(f, err)

	intw, outtw, in, out, err := prepareInputsForZKTransfer(pp, 2, 2)
	require.NoError(f, err)
	prover, err := transfer.NewCSPBasedProver(intw, outtw, in, out, pp)
	require.NoError(f, err)
	proofBytes, err := prover.Prove()
	require.NoError(f, err)

	f.Add(proofBytes)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add(proofBytes[:len(proofBytes)/2])

	verifier, err := transfer.NewCSPVerifier(in, out, pp)
	require.NoError(f, err)
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzTransferProofBytes {
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

// curveSeed converts a curve id into the int32 the fuzz corpus carries, bounds-checked
// rather than asserted in case the curve table ever grows.
func curveSeed(id int) int32 {
	if id > math2.MaxInt32 || id < math2.MinInt32 {
		return 0
	}

	return int32(id)
}

// Fuzzes the commitment side (issue #2554), which the targets above cannot reach. Inputs
// and outputs both feed the type-and-sum stage, so the fuzzed commitment goes into each.
func FuzzTransferVerifierCommitmentNoPanic(f *testing.F) {
	bpPP, err := setupWithProofType(TestBits, TestCurve, rp.RangeProofType)
	require.NoError(f, err)
	cspPP, err := setupWithProofType(TestBits, TestCurve, rp.CSPRangeProofType)
	require.NoError(f, err)

	bpIntw, bpOuttw, bpIn, bpOut, err := prepareInputsForZKTransfer(bpPP, 2, 2)
	require.NoError(f, err)
	bpProver, err := transfer.NewBulletProofProver(bpIntw, bpOuttw, bpIn, bpOut, bpPP)
	require.NoError(f, err)
	bpProof, err := bpProver.Prove()
	require.NoError(f, err)

	cspIntw, cspOuttw, cspIn, cspOut, err := prepareInputsForZKTransfer(cspPP, 2, 2)
	require.NoError(f, err)
	cspProver, err := transfer.NewCSPBasedProver(cspIntw, cspOuttw, cspIn, cspOut, cspPP)
	require.NoError(f, err)
	cspProof, err := cspProver.Prove()
	require.NoError(f, err)

	curve := math.Curves[bpPP.Curve]
	// Seeds: the honest commitment, the cross-curve commitment that panicked before the
	// fix, the point at infinity, an out-of-range curve id, and truncated/empty elements.
	f.Add(curveSeed(int(bpPP.Curve)), bpOut[0].Bytes())
	f.Add(curveSeed(int(math.BLS12_377_GURVY)), math.Curves[math.BLS12_377_GURVY].GenG1.Bytes())
	f.Add(curveSeed(int(bpPP.Curve)), curve.NewG1().Bytes())
	f.Add(curveSeed(len(math.Curves)), bpOut[0].Bytes())
	f.Add(curveSeed(-1), bpOut[0].Bytes())
	f.Add(curveSeed(int(bpPP.Curve)), []byte{})
	f.Add(curveSeed(int(bpPP.Curve)), bpOut[0].Bytes()[:len(bpOut[0].Bytes())/2])

	f.Fuzz(func(t *testing.T, curveID int32, element []byte) {
		if len(element) > maxFuzzTransferProofBytes {
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
			v, err := transfer.NewBulletProofVerifier([]*math.G1{bpIn[0], com}, bpOut, bpPP)
			if err != nil {
				return
			}
			_ = v.Verify(bpProof)
		})
		require.NotPanics(t, func() {
			v, err := transfer.NewBulletProofVerifier(bpIn, []*math.G1{bpOut[0], com}, bpPP)
			if err != nil {
				return
			}
			_ = v.Verify(bpProof)
		})
		require.NotPanics(t, func() {
			v, err := transfer.NewCSPVerifier([]*math.G1{cspIn[0], com}, cspOut, cspPP)
			if err != nil {
				return
			}
			_ = v.Verify(cspProof)
		})
		require.NotPanics(t, func() {
			v, err := transfer.NewCSPVerifier(cspIn, []*math.G1{cspOut[0], com}, cspPP)
			if err != nil {
				return
			}
			_ = v.Verify(cspProof)
		})
	})
}
