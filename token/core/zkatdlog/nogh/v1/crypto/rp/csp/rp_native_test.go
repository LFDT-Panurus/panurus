/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"fmt"
	"testing"

	mathlib "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nativeRPCtx holds the parameters for testing nativeRP* helper functions.
type nativeRPCtx struct {
	n       uint64
	curve   *mathlib.Curve
	eta     *mathlib.Zr
	gamma   *mathlib.Zr
	mu      []*mathlib.Zr
	nu      []*mathlib.Zr
	aCoeffs []*mathlib.Zr // prover-side polynomial coefficients
}

func newNativeRPCtx(t *testing.T, curveID mathlib.CurveID, n uint64) *nativeRPCtx {
	t.Helper()
	curve := mathlib.Curves[curveID]
	rand, err := curve.Rand()
	require.NoError(t, err)

	eta := curve.NewRandomZr(rand)
	gamma := curve.NewRandomZr(rand)

	mu := make([]*mathlib.Zr, n+1)
	nu := make([]*mathlib.Zr, n+1)
	aCoeffs := make([]*mathlib.Zr, n+1)
	for i := uint64(0); i <= n; i++ {
		mu[i] = curve.NewRandomZr(rand)
		nu[i] = curve.NewRandomZr(rand)
		aCoeffs[i] = curve.NewRandomZr(rand)
	}

	return &nativeRPCtx{
		n: n, curve: curve, eta: eta, gamma: gamma,
		mu: mu, nu: nu, aCoeffs: aCoeffs,
	}
}

// nativeCurve groups a curve label and ID for table-driven tests.
type nativeCurve struct {
	name string
	id   mathlib.CurveID
}

func nativeCurveIDs() []nativeCurve {
	return []nativeCurve{
		{"BLS12-381", mathlib.BLS12_381_BBS_GURVY},
		{"BN254", mathlib.BN254},
	}
}

// --------------------------------------------------------------------------
// nativeRPBuildLF tests
// --------------------------------------------------------------------------

// refBuildLF computes lf, u, and lVal using the mathlib (non-native) fallback path
// from rp.go, so we can compare it against the native result.
func refBuildLF(ctx *nativeRPCtx) ([]*mathlib.Zr, *mathlib.Zr, *mathlib.Zr) {
	n := ctx.n
	curve := ctx.curve
	eta := ctx.eta
	gamma := ctx.gamma
	mu := ctx.mu
	nu := ctx.nu
	aCoeffs := ctx.aCoeffs

	gammaSquare := curve.ModMul(gamma, gamma, curve.GroupOrder)

	lf := make([]*mathlib.Zr, 2*n+4)
	for i := range lf {
		lf[i] = math.Zero(curve)
	}

	for i := uint64(1); i <= n; i++ {
		lf[i] = curve.ModMul(eta, math.PowerOfTwo(curve, i-1), curve.GroupOrder)
	}

	negEta := eta.Copy()
	negEta.Neg()
	lf[2*n+2] = negEta

	for i := uint64(0); i <= n; i++ {
		lf[i] = curve.ModAddMul2(
			lf[i], math.One(curve),
			gamma, mu[i],
			curve.GroupOrder,
		)
	}

	for k := uint64(0); k <= n; k++ {
		lf[n+1+k] = curve.ModMul(gammaSquare, nu[k], curve.GroupOrder)
	}

	u := math.InnerProduct(aCoeffs, mu, curve)

	uMinus1 := curve.ModSub(u, math.One(curve), curve.GroupOrder)
	lVal := curve.ModAddMul2(
		gamma, u,
		gammaSquare, curve.ModMul(u, uMinus1, curve.GroupOrder),
		curve.GroupOrder,
	)

	return lf, u, lVal
}

// TestNativeRPBuildLFConsistency checks that nativeRPBuildLF produces the same
// lf, u, and lVal as the reference mathlib implementation.
func TestNativeRPBuildLFConsistency(t *testing.T) {
	for _, tc := range nativeCurveIDs() {
		t.Run(tc.name, func(t *testing.T) {
			for _, n := range []uint64{2, 4, 30} {
				t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
					ctx := newNativeRPCtx(t, tc.id, n)

					refLf, refU, refLVal := refBuildLF(ctx)

					var natLf []*mathlib.Zr
					var natU, natLVal *mathlib.Zr

					switch tc.id {
					case mathlib.BLS12_381_BBS_GURVY:
						natLf, natU, natLVal = nativeRPBuildLF[bls12381fr.Element, *bls12381fr.Element](
							ctx.n, ctx.eta, ctx.gamma, ctx.mu, ctx.nu, ctx.aCoeffs, nil, ctx.curve)
					case mathlib.BN254:
						natLf, natU, natLVal = nativeRPBuildLF[bn254fr.Element, *bn254fr.Element](
							ctx.n, ctx.eta, ctx.gamma, ctx.mu, ctx.nu, ctx.aCoeffs, nil, ctx.curve)
					}

					// Check lf
					require.Len(t, natLf, len(refLf), "lf length mismatch")
					for i := range refLf {
						assert.Equal(t, refLf[i].Bytes(), natLf[i].Bytes(),
							"lf[%d] mismatch", i)
					}

					// Check u
					assert.Equal(t, refU.Bytes(), natU.Bytes(), "u mismatch")

					// Check lVal
					assert.Equal(t, refLVal.Bytes(), natLVal.Bytes(), "lVal mismatch")
				})
			}
		})
	}
}

// TestNativeRPBuildLFVerifierMode checks nativeRPBuildLF when called with
// a non-nil proofU (verifier mode) instead of aCoeffs (prover mode).
func TestNativeRPBuildLFVerifierMode(t *testing.T) {
	for _, tc := range nativeCurveIDs() {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newNativeRPCtx(t, tc.id, 4)

			// Compute u from prover path first
			refU := math.InnerProduct(ctx.aCoeffs, ctx.mu, ctx.curve)

			// Verifier mode: pass proofU instead of aCoeffs
			var natLf []*mathlib.Zr
			var natU, natLVal *mathlib.Zr

			switch tc.id {
			case mathlib.BLS12_381_BBS_GURVY:
				natLf, natU, natLVal = nativeRPBuildLF[bls12381fr.Element, *bls12381fr.Element](
					ctx.n, ctx.eta, ctx.gamma, ctx.mu, ctx.nu, nil, refU, ctx.curve)
			case mathlib.BN254:
				natLf, natU, natLVal = nativeRPBuildLF[bn254fr.Element, *bn254fr.Element](
					ctx.n, ctx.eta, ctx.gamma, ctx.mu, ctx.nu, nil, refU, ctx.curve)
			}

			require.NotNil(t, natLf)
			assert.Equal(t, refU.Bytes(), natU.Bytes(), "u should match when provided as proofU")
			assert.NotNil(t, natLVal)
		})
	}
}

// TestNativeRPBuildLFSize checks the output lf vector has the correct size 2n+4.
func TestNativeRPBuildLFSize(t *testing.T) {
	for _, tc := range nativeCurveIDs() {
		t.Run(tc.name, func(t *testing.T) {
			for _, n := range []uint64{2, 8, 30} {
				t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
					ctx := newNativeRPCtx(t, tc.id, n)

					var lf []*mathlib.Zr

					switch tc.id {
					case mathlib.BLS12_381_BBS_GURVY:
						lf, _, _ = nativeRPBuildLF[bls12381fr.Element, *bls12381fr.Element](
							ctx.n, ctx.eta, ctx.gamma, ctx.mu, ctx.nu, ctx.aCoeffs, nil, ctx.curve)
					case mathlib.BN254:
						lf, _, _ = nativeRPBuildLF[bn254fr.Element, *bn254fr.Element](
							ctx.n, ctx.eta, ctx.gamma, ctx.mu, ctx.nu, ctx.aCoeffs, nil, ctx.curve)
					}

					assert.Len(t, lf, int(2*n+4), //nolint:gosec
						"lf should have length 2n+4")
				})
			}
		})
	}
}

// --------------------------------------------------------------------------
// nativeRPInnerProduct tests
// --------------------------------------------------------------------------

// TestNativeRPInnerProductConsistency checks that the native inner product
// matches the mathlib InnerProduct.
func TestNativeRPInnerProductConsistency(t *testing.T) {
	for _, tc := range nativeCurveIDs() {
		t.Run(tc.name, func(t *testing.T) {
			for _, m := range []int{4, 8, 16} {
				t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
					curve := mathlib.Curves[tc.id]
					rand, err := curve.Rand()
					require.NoError(t, err)

					a := make([]*mathlib.Zr, m)
					b := make([]*mathlib.Zr, m)
					for i := range m {
						a[i] = curve.NewRandomZr(rand)
						b[i] = curve.NewRandomZr(rand)
					}

					refIP := math.InnerProduct(a, b, curve)

					var natIP *mathlib.Zr

					switch tc.id {
					case mathlib.BLS12_381_BBS_GURVY:
						natIP = nativeRPInnerProduct[bls12381fr.Element, *bls12381fr.Element](a, b, curve)
					case mathlib.BN254:
						natIP = nativeRPInnerProduct[bn254fr.Element, *bn254fr.Element](a, b, curve)
					}

					assert.Equal(t, refIP.Bytes(), natIP.Bytes(), "inner product mismatch")
				})
			}
		})
	}
}

// TestNativeRPInnerProductZero checks that the inner product with a zero vector is zero.
func TestNativeRPInnerProductZero(t *testing.T) {
	for _, tc := range nativeCurveIDs() {
		t.Run(tc.name, func(t *testing.T) {
			curve := mathlib.Curves[tc.id]
			rand, err := curve.Rand()
			require.NoError(t, err)

			m := 8
			a := make([]*mathlib.Zr, m)
			b := make([]*mathlib.Zr, m)
			for i := range m {
				a[i] = curve.NewRandomZr(rand)
				b[i] = math.Zero(curve)
			}

			var natIP *mathlib.Zr

			switch tc.id {
			case mathlib.BLS12_381_BBS_GURVY:
				natIP = nativeRPInnerProduct[bls12381fr.Element, *bls12381fr.Element](a, b, curve)
			case mathlib.BN254:
				natIP = nativeRPInnerProduct[bn254fr.Element, *bn254fr.Element](a, b, curve)
			}

			assert.Equal(t, math.Zero(curve).Bytes(), natIP.Bytes(), "inner product with zero vector should be zero")
		})
	}
}

// --------------------------------------------------------------------------
// nativeRPBlindWitness tests
// --------------------------------------------------------------------------

// TestNativeRPBlindWitnessConsistency checks that nativeRPBlindWitness produces
// the same wit and witVal as the reference mathlib computation.
func TestNativeRPBlindWitnessConsistency(t *testing.T) {
	for _, tc := range nativeCurveIDs() {
		t.Run(tc.name, func(t *testing.T) {
			curve := mathlib.Curves[tc.id]
			rand, err := curve.Rand()
			require.NoError(t, err)

			m := 8
			sBlind := make([]*mathlib.Zr, m)
			pExt := make([]*mathlib.Zr, m)
			for i := range m {
				sBlind[i] = curve.NewRandomZr(rand)
				pExt[i] = curve.NewRandomZr(rand)
			}

			rho := curve.NewRandomZr(rand)
			lVal := curve.NewRandomZr(rand)
			sVal := curve.NewRandomZr(rand)

			// Reference: wit[i] = pExt[i] + rho * sBlind[i]
			refWit := make([]*mathlib.Zr, m)
			for i := range m {
				refWit[i] = curve.ModAddMul2(
					pExt[i], math.One(curve),
					rho, sBlind[i],
					curve.GroupOrder,
				)
			}

			// Reference: witVal = lVal + rho * sVal
			refWitVal := curve.ModAddMul2(
				lVal, math.One(curve),
				rho, sVal,
				curve.GroupOrder,
			)

			var natWit []*mathlib.Zr
			var natWitVal *mathlib.Zr

			switch tc.id {
			case mathlib.BLS12_381_BBS_GURVY:
				natWit, natWitVal = nativeRPBlindWitness[bls12381fr.Element, *bls12381fr.Element](
					sBlind, pExt, rho, lVal, sVal, curve)
			case mathlib.BN254:
				natWit, natWitVal = nativeRPBlindWitness[bn254fr.Element, *bn254fr.Element](
					sBlind, pExt, rho, lVal, sVal, curve)
			}

			require.Len(t, natWit, len(refWit), "wit length mismatch")
			for i := range refWit {
				assert.Equal(t, refWit[i].Bytes(), natWit[i].Bytes(),
					"wit[%d] mismatch", i)
			}

			assert.Equal(t, refWitVal.Bytes(), natWitVal.Bytes(), "witVal mismatch")
		})
	}
}

// TestNativeRPBlindWitnessVerifierMode checks that when sBlind and pExt are nil
// (verifier mode), wit is nil and only witVal is returned.
func TestNativeRPBlindWitnessVerifierMode(t *testing.T) {
	for _, tc := range nativeCurveIDs() {
		t.Run(tc.name, func(t *testing.T) {
			curve := mathlib.Curves[tc.id]
			rand, err := curve.Rand()
			require.NoError(t, err)

			rho := curve.NewRandomZr(rand)
			lVal := curve.NewRandomZr(rand)
			sVal := curve.NewRandomZr(rand)

			var natWit []*mathlib.Zr
			var natWitVal *mathlib.Zr

			switch tc.id {
			case mathlib.BLS12_381_BBS_GURVY:
				natWit, natWitVal = nativeRPBlindWitness[bls12381fr.Element, *bls12381fr.Element](
					nil, nil, rho, lVal, sVal, curve)
			case mathlib.BN254:
				natWit, natWitVal = nativeRPBlindWitness[bn254fr.Element, *bn254fr.Element](
					nil, nil, rho, lVal, sVal, curve)
			}

			assert.Nil(t, natWit, "wit should be nil in verifier mode")
			assert.NotNil(t, natWitVal, "witVal should be non-nil")

			// witVal = lVal + rho * sVal
			refWitVal := curve.ModAddMul2(
				lVal, math.One(curve),
				rho, sVal,
				curve.GroupOrder,
			)

			assert.Equal(t, refWitVal.Bytes(), natWitVal.Bytes(), "witVal mismatch in verifier mode")
		})
	}
}

// --------------------------------------------------------------------------
// End-to-end range proof tests through the native path
// --------------------------------------------------------------------------

// TestNativeRPEndToEnd verifies that the full range proof (prover→verifier)
// works end-to-end for both native curves. The prover internally dispatches
// to the native gnark path for supported curves.
func TestNativeRPEndToEnd(t *testing.T) {
	cases := []struct {
		n     uint64
		value int64
	}{
		{2, 0},
		{2, 3},
		{4, 10},
		{30, 1 << 15},
		{30, 1<<30 - 1},
	}

	for _, tc := range nativeCurveIDs() {
		t.Run(tc.name, func(t *testing.T) {
			for _, c := range cases {
				t.Run(fmt.Sprintf("n=%d/v=%d", c.n, c.value), func(t *testing.T) {
					setup, err := newRPSetup(mathlib.Curves[tc.id], c.n, c.value)
					require.NoError(t, err)

					proof, err := setup.prover.Prove()
					require.NoError(t, err)
					require.NotNil(t, proof)

					err = setup.verifier.Verify(proof)
					require.NoError(t, err)
				})
			}
		})
	}
}

// TestNativeRPSerializeRoundTrip ensures proofs generated via the native path
// survive serialization and deserialization.
func TestNativeRPSerializeRoundTrip(t *testing.T) {
	for _, tc := range nativeCurveIDs() {
		t.Run(tc.name, func(t *testing.T) {
			setup, err := newRPSetup(mathlib.Curves[tc.id], 4, 10)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			raw, err := proof.Serialize()
			require.NoError(t, err)
			require.NotEmpty(t, raw)

			proof2 := &RangeProof{}
			err = proof2.Deserialize(raw)
			require.NoError(t, err)

			err = proof2.Validate(tc.id)
			require.NoError(t, err)

			err = setup.verifier.Verify(proof2)
			require.NoError(t, err)
		})
	}
}

// TestNativeRPTamperedProofRejected verifies that a tampered proof is rejected
// when going through the native path.
func TestNativeRPTamperedProofRejected(t *testing.T) {
	for _, tc := range nativeCurveIDs() {
		t.Run(tc.name, func(t *testing.T) {
			curve := mathlib.Curves[tc.id]
			setup, err := newRPSetup(curve, 4, 5)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			rand, err := curve.Rand()
			require.NoError(t, err)

			t.Run("tampered_u", func(t *testing.T) {
				bad := copyCSPRangeProof(t, proof, tc.id)
				bad.u = curve.NewRandomZr(rand)
				assert.Error(t, setup.verifier.Verify(bad))
			})

			t.Run("tampered_sEval", func(t *testing.T) {
				bad := copyCSPRangeProof(t, proof, tc.id)
				bad.sEval = curve.NewRandomZr(rand)
				assert.Error(t, setup.verifier.Verify(bad))
			})

			t.Run("tampered_pComm", func(t *testing.T) {
				bad := copyCSPRangeProof(t, proof, tc.id)
				bad.pComm = curve.GenG1.Mul(curve.NewRandomZr(rand))
				assert.Error(t, setup.verifier.Verify(bad))
			})

			t.Run("tampered_sComm", func(t *testing.T) {
				bad := copyCSPRangeProof(t, proof, tc.id)
				bad.sComm = curve.GenG1.Mul(curve.NewRandomZr(rand))
				assert.Error(t, setup.verifier.Verify(bad))
			})
		})
	}
}

// TestNativeRPWrongCommitmentRejected verifies the native verifier rejects
// a valid proof when a different VCommitment is used.
func TestNativeRPWrongCommitmentRejected(t *testing.T) {
	for _, tc := range nativeCurveIDs() {
		t.Run(tc.name, func(t *testing.T) {
			curve := mathlib.Curves[tc.id]
			setup, err := newRPSetup(curve, 2, 1)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			rand, err := curve.Rand()
			require.NoError(t, err)
			setup.verifier.VCommitment = curve.GenG1.Mul(curve.NewRandomZr(rand))

			err = setup.verifier.Verify(proof)
			assert.Error(t, err)
		})
	}
}

// copyCSPRangeProof creates an independent copy of a CSP RangeProof via serialize/deserialize.
func copyCSPRangeProof(t *testing.T, proof *RangeProof, curveID mathlib.CurveID) *RangeProof {
	t.Helper()
	raw, err := proof.Serialize()
	require.NoError(t, err)
	cp := &RangeProof{}
	require.NoError(t, cp.Deserialize(raw))
	require.NoError(t, cp.Validate(curveID))

	return cp
}
