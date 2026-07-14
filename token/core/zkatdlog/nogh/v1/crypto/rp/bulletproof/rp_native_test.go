/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bulletproof_test

import (
	"math/bits"
	"strconv"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp/bulletproof"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nativeTestCtx holds the common setup for native range proof tests.
type nativeTestCtx struct {
	com       *math.G1
	Q         *math.G1
	P         *math.G1
	H         *math.G1
	G         *math.G1
	bf        *math.Zr
	leftGens  []*math.G1
	rightGens []*math.G1
	nr        uint64
	bitLen    uint64
	curve     *math.Curve
}

func newNativeTestCtx(t *testing.T, curveID math.CurveID, bitLen uint64, value int64) *nativeTestCtx {
	t.Helper()
	curve := math.Curves[curveID]
	nr := uint64(bits.Len64(bitLen)) - 1 //nolint:gosec

	rand, err := curve.Rand()
	require.NoError(t, err)

	Q := curve.GenG1.Mul(curve.NewRandomZr(rand))
	P := curve.GenG1.Mul(curve.NewRandomZr(rand))
	H := curve.GenG1.Mul(curve.NewRandomZr(rand))
	G := curve.GenG1.Mul(curve.NewRandomZr(rand))

	leftGens := make([]*math.G1, bitLen)
	rightGens := make([]*math.G1, bitLen)
	for i := range leftGens {
		leftGens[i] = curve.HashToG1([]byte(strconv.Itoa(2 * i)))
		rightGens[i] = curve.HashToG1([]byte(strconv.Itoa(2*i + 1)))
	}

	bf := curve.NewRandomZr(rand)
	com := G.Mul(curve.NewZrFromInt(value))
	com.Add(H.Mul(bf))

	return &nativeTestCtx{
		com: com, Q: Q, P: P, H: H, G: G, bf: bf,
		leftGens: leftGens, rightGens: rightGens,
		nr: nr, bitLen: bitLen, curve: curve,
	}
}

func (c *nativeTestCtx) prove(t *testing.T, value uint64) *bulletproof.RangeProof {
	t.Helper()
	prover := bulletproof.NewRangeProver(
		c.com, value,
		[]*math.G1{c.G, c.H}, c.bf,
		c.leftGens, c.rightGens, c.P, c.Q,
		c.nr, c.bitLen, c.curve, nil,
	)
	proof, err := prover.Prove()
	require.NoError(t, err)
	require.NotNil(t, proof)

	return proof
}

func (c *nativeTestCtx) verify(t *testing.T, proof *bulletproof.RangeProof) error {
	t.Helper()
	verifier := bulletproof.NewRangeVerifier(
		c.com,
		[]*math.G1{c.G, c.H},
		c.leftGens, c.rightGens, c.P, c.Q,
		c.nr, c.bitLen, c.curve, nil,
	)

	return verifier.Verify(proof)
}

// nativeCurveCase describes a curve to test the native gnark dispatch.
type nativeCurveCase struct {
	name string
	id   math.CurveID
}

func nativeCurves() []nativeCurveCase {
	return []nativeCurveCase{
		{"BLS12-381", math.BLS12_381_BBS_GURVY},
		{"BN254", math.BN254},
	}
}

// TestNativeRPProveVerify exercises the full prove→verify cycle through
// nativeRPPreprocess and nativeRPVerify for both supported curves.
func TestNativeRPProveVerify(t *testing.T) {
	for _, tc := range nativeCurves() {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newNativeTestCtx(t, tc.id, 32, 115)
			proof := ctx.prove(t, 115)
			require.NoError(t, ctx.verify(t, proof))
		})
	}
}

// TestNativeRPMultipleValues verifies a range of different values
// to exercise various bit patterns through the native path.
func TestNativeRPMultipleValues(t *testing.T) {
	values := []struct {
		name  string
		value int64
	}{
		{"zero", 0},
		{"one", 1},
		{"small", 42},
		{"medium", 1000},
		{"max_32bit", 1<<32 - 1},
	}

	for _, tc := range nativeCurves() {
		t.Run(tc.name, func(t *testing.T) {
			for _, v := range values {
				t.Run(v.name, func(t *testing.T) {
					ctx := newNativeTestCtx(t, tc.id, 32, v.value)
					proof := ctx.prove(t, uint64(v.value)) //nolint:gosec
					require.NoError(t, ctx.verify(t, proof))
				})
			}
		})
	}
}

// TestNativeRPPowerOfTwoBitLengths verifies that the native path works
// correctly with different bit lengths (all must be powers of two).
func TestNativeRPPowerOfTwoBitLengths(t *testing.T) {
	for _, tc := range nativeCurves() {
		t.Run(tc.name, func(t *testing.T) {
			for _, bl := range []uint64{8, 16, 64} {
				t.Run(strconv.FormatUint(bl, 10)+"bits", func(t *testing.T) {
					ctx := newNativeTestCtx(t, tc.id, bl, 42)
					proof := ctx.prove(t, 42)
					require.NoError(t, ctx.verify(t, proof))
				})
			}
		})
	}
}

// TestNativeRPTamperedProofRejected verifies that the native verifier
// rejects proofs where individual fields have been tampered with.
func TestNativeRPTamperedProofRejected(t *testing.T) {
	for _, tc := range nativeCurves() {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newNativeTestCtx(t, tc.id, 32, 100)
			proof := ctx.prove(t, 100)

			rand, err := ctx.curve.Rand()
			require.NoError(t, err)

			t.Run("tampered_tau", func(t *testing.T) {
				bad := copyRangeProof(t, proof)
				bad.Data.Tau = ctx.curve.NewRandomZr(rand)
				assert.Error(t, ctx.verify(t, bad))
			})

			t.Run("tampered_inner_product", func(t *testing.T) {
				bad := copyRangeProof(t, proof)
				bad.Data.InnerProduct = ctx.curve.NewRandomZr(rand)
				assert.Error(t, ctx.verify(t, bad))
			})

			t.Run("tampered_delta", func(t *testing.T) {
				bad := copyRangeProof(t, proof)
				bad.Data.Delta = ctx.curve.NewRandomZr(rand)
				assert.Error(t, ctx.verify(t, bad))
			})

			t.Run("tampered_T1", func(t *testing.T) {
				bad := copyRangeProof(t, proof)
				bad.Data.T1 = ctx.curve.GenG1.Mul(ctx.curve.NewRandomZr(rand))
				assert.Error(t, ctx.verify(t, bad))
			})

			t.Run("tampered_T2", func(t *testing.T) {
				bad := copyRangeProof(t, proof)
				bad.Data.T2 = ctx.curve.GenG1.Mul(ctx.curve.NewRandomZr(rand))
				assert.Error(t, ctx.verify(t, bad))
			})
		})
	}
}

// TestNativeRPWrongCommitmentRejected verifies that the native verifier
// rejects a valid proof when presented with a different commitment.
func TestNativeRPWrongCommitmentRejected(t *testing.T) {
	for _, tc := range nativeCurves() {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newNativeTestCtx(t, tc.id, 32, 50)
			proof := ctx.prove(t, 50)

			rand, err := ctx.curve.Rand()
			require.NoError(t, err)

			wrongCom := ctx.G.Mul(ctx.curve.NewZrFromInt(999))
			wrongCom.Add(ctx.H.Mul(ctx.curve.NewRandomZr(rand)))

			wrongVerifier := bulletproof.NewRangeVerifier(
				wrongCom,
				[]*math.G1{ctx.G, ctx.H},
				ctx.leftGens, ctx.rightGens, ctx.P, ctx.Q,
				ctx.nr, ctx.bitLen, ctx.curve, nil,
			)
			err = wrongVerifier.Verify(proof)
			assert.Error(t, err)
		})
	}
}

// TestNativeRPSerializeRoundTrip verifies that a proof generated through the
// native path survives serialization and deserialization.
func TestNativeRPSerializeRoundTrip(t *testing.T) {
	for _, tc := range nativeCurves() {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newNativeTestCtx(t, tc.id, 32, 77)
			proof := ctx.prove(t, 77)

			raw, err := proof.Serialize()
			require.NoError(t, err)
			require.NotEmpty(t, raw)

			proof2 := &bulletproof.RangeProof{}
			err = proof2.Deserialize(raw)
			require.NoError(t, err)

			require.NoError(t, ctx.verify(t, proof2))
		})
	}
}

// TestNativeRPVerifierNilElementErrors verifies that the verifier
// correctly rejects proofs with nil elements before reaching the native path.
func TestNativeRPVerifierNilElementErrors(t *testing.T) {
	for _, tc := range nativeCurves() {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newNativeTestCtx(t, tc.id, 32, 10)
			verifier := bulletproof.NewRangeVerifier(
				ctx.com,
				[]*math.G1{ctx.G, ctx.H},
				ctx.leftGens, ctx.rightGens, ctx.P, ctx.Q,
				ctx.nr, ctx.bitLen, ctx.curve, nil,
			)

			err := verifier.Verify(&bulletproof.RangeProof{
				Data: &bulletproof.RangeProofData{},
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), "nil elements")
		})
	}
}

// copyRangeProof creates an independent copy of a RangeProof via serialize/deserialize.
func copyRangeProof(t *testing.T, proof *bulletproof.RangeProof) *bulletproof.RangeProof {
	t.Helper()
	raw, err := proof.Serialize()
	require.NoError(t, err)
	cp := &bulletproof.RangeProof{}
	require.NoError(t, cp.Deserialize(raw))

	return cp
}
