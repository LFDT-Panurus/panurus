/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	math "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	"github.com/stretchr/testify/require"
)

// cspSetup holds a fully consistent CSP statement and witness.
type cspSetup struct {
	prover   *prover
	verifier *verifier
	curve    *math.Curve
}

// newCSPSetup builds a random CSP instance with 2^rounds generators.
// Commitment = MSM(generators, witness), Value = ⟨linearForm, witness⟩.
func newCSPSetup(t *testing.T, curve *math.Curve, rounds uint64) *cspSetup {
	t.Helper()
	n := int(uint64(1) << rounds) // #nosec G115

	rand, err := curve.Rand()
	require.NoError(t, err)

	generators := make([]*math.G1, n)
	witness := make([]*math.Zr, n)
	linearForm := make([]*math.Zr, n)

	for i := range n {
		generators[i] = curve.HashToG1([]byte("csp-gen-" + strconv.Itoa(i)))
		witness[i] = curve.NewRandomZr(rand)
		linearForm[i] = curve.NewRandomZr(rand)
	}

	// Commitment = MSM(generators, witness)
	com := curve.MultiScalarMul(generators, witness)

	// Value = ⟨linearForm, witness⟩  (scalar-field MSM)
	value := math2.InnerProduct(linearForm, witness, curve)

	transcriptHeader := []byte("transcriptHeader")

	p := &prover{
		Commitment:     com,
		Generators:     generators,
		LinearForm:     linearForm,
		Value:          value,
		NumberOfRounds: rounds,
		Curve:          curve,
		witness:        witness,
	}
	p.WithTranscriptHeader(transcriptHeader)

	v := &verifier{
		Commitment:     com,
		Generators:     generators,
		LinearForm:     linearForm,
		Value:          value,
		NumberOfRounds: rounds,
		Curve:          curve,
	}
	v.WithTranscriptHeader(transcriptHeader)

	return &cspSetup{prover: p, verifier: v, curve: curve}
}

// TestCSPProveVerify checks that an honest proof always verifies.
// Given a random CSP instance and an honest prover,
// When a proof is generated and verified,
// Then the verification should succeed.
func TestCSPProveVerify(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	rounds := []uint64{5, 6}

	for _, curveID := range curves {
		for _, r := range rounds {
			t.Run(fmt.Sprintf("curveID=%d/rounds=%d", curveID, r), func(t *testing.T) {
				setup := newCSPSetup(t, math.Curves[curveID], r)

				start := time.Now()
				proof, err := setup.prover.Prove()
				t.Logf("Time to prove nr %d = %d msec", r, time.Since(start).Milliseconds())
				require.NoError(t, err)
				require.NotNil(t, proof)
				require.Len(t, proof.Left, int(r))   // #nosec G115
				require.Len(t, proof.Right, int(r))  // #nosec G115
				require.Len(t, proof.VLeft, int(r))  // #nosec G115
				require.Len(t, proof.VRight, int(r)) // #nosec G115

				start = time.Now()
				err = setup.verifier.Verify(proof)
				t.Logf("Time to verify nr %d = %d msec", r, time.Since(start).Milliseconds())

				require.NoError(t, err)
			})
		}
	}
}

// TestCSPWrongCommitment checks that verification fails when the verifier's
// commitment does not match the one used by the prover.
// Given an honest proof,
// When the verifier's commitment is replaced with a random point,
// Then the verification should fail.
func TestCSPWrongCommitment(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup := newCSPSetup(t, curve, 2)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			// Give verifier a random (wrong) commitment.
			rand, err := curve.Rand()
			require.NoError(t, err)
			setup.verifier.Commitment = curve.GenG1.Mul(curve.NewRandomZr(rand))

			err = setup.verifier.Verify(proof)
			require.Error(t, err)
			require.Contains(t, err.Error(), "verification failed")
		})
	}
}

// TestCSPWrongValue checks that verification fails when the claimed value is wrong.
// Given an honest proof,
// When the verifier's value is replaced with a random value,
// Then the verification should fail.
func TestCSPWrongValue(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup := newCSPSetup(t, curve, 2)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			// Flip one bit in the value.
			rand, err := curve.Rand()
			require.NoError(t, err)
			setup.verifier.Value = curve.NewRandomZr(rand)

			err = setup.verifier.Verify(proof)
			require.Error(t, err)
			require.Contains(t, err.Error(), "verification failed")
		})
	}
}

// TestCSPTamperedLeft checks that flipping Left[0] invalidates the proof.
// Given an honest proof,
// When a component of the proof (Left[0]) is tampered with,
// Then the verification should fail.
func TestCSPTamperedLeft(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup := newCSPSetup(t, curve, 2)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			// Replace Left[0] with the generator (almost certainly wrong).
			proof.Left[0] = curve.GenG1

			err = setup.verifier.Verify(proof)
			require.Error(t, err)
		})
	}
}

// TestCSPTamperedVRight checks that flipping VRight[0] invalidates the proof.
// Given an honest proof,
// When a component of the proof (VRight[0]) is tampered with,
// Then the verification should fail.
func TestCSPTamperedVRight(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup := newCSPSetup(t, curve, 2)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			rand, err := curve.Rand()
			require.NoError(t, err)
			proof.VRight[0] = curve.NewRandomZr(rand)

			err = setup.verifier.Verify(proof)
			require.Error(t, err)
		})
	}
}

// TestCSPWrongVectorLength checks that Prove() rejects mismatched vector sizes.
// Given a CSP prover,
// When the number of generators does not match 2^rounds,
// Then the proof generation should fail.
func TestCSPWrongVectorLength(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup := newCSPSetup(t, curve, 2) // expects 4 elements

			// Drop one generator to break the 2^rounds constraint.
			setup.prover.Generators = setup.prover.Generators[:3]

			_, err := setup.prover.Prove()
			require.Error(t, err)
			require.Contains(t, err.Error(), "invalid length")
		})
	}
}

// TestCSPMalformedProof checks that Verify() rejects a proof with the wrong
// number of rounds.
// Given an honest prover,
// When a proof with fewer rounds than expected is verified,
// Then the verification should fail.
func TestCSPMalformedProof(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup := newCSPSetup(t, curve, 2)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			// Drop one round from the proof.
			proof.Left = proof.Left[:1]
			proof.Right = proof.Right[:1]
			proof.VLeft = proof.VLeft[:1]
			proof.VRight = proof.VRight[:1]

			err = setup.verifier.Verify(proof)
			require.Error(t, err)
			require.Contains(t, err.Error(), "invalid length")
		})
	}
}

// TestCSPSVector validates the coefficient vector produced by cspSVector against
// a direct (naive) computation for a small instance.
// Given a set of Fiat-Shamir challenges,
// When the s-vector is computed via the recurrence relation,
// Then it should match the product representation s[i] = prod_{r=0}^{k-1} c_r^{bit(i, k-1-r)}.
func TestCSPSVector(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			k := 3
			n := 1 << k
			challenges := make([]*math.Zr, k)
			for i := range challenges {
				challenges[i] = curve.NewRandomZr(rand)
			}

			s := sVector(n, challenges, curve)
			require.Len(t, s, n)

			// Naive check: s[i] = prod_{r=0}^{k-1} c_r^{bit(i, k-1-r)}
			for i := range n {
				expected := curve.NewZrFromInt(1)
				for r := range k {
					bit := (i >> (k - 1 - r)) & 1
					if bit == 1 {
						expected = curve.ModMul(expected, challenges[r], curve.GroupOrder)
					}
				}
				require.True(t, s[i].Equals(expected),
					"s[%d] mismatch: got %s, want %s", i, s[i], expected)
			}
		})
	}
}

// TestCSP_TranscriptHeaderMismatch is T-GAP-C2: verifies that a CSP proof
// generated with transcript header "A" is rejected when verified with
// transcript header "B", and that no panic occurs.
//
// The CSP Fiat-Shamir transform uses the transcript header as a domain
// separator in the challenge computation. A single-byte difference produces
// a completely different challenge sequence, causing the IPA verification
// to fail. This test documents the availability-risk: if PP is stored and
// reloaded with a different header encoding, all existing proofs become
// unverifiable.
func TestCSP_TranscriptHeaderMismatch(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]

			// Build a setup using transcript header "headerA".
			setup := newCSPSetup(t, curve, 2)
			// The prover already has "transcriptHeader" set.
			// Override to use a custom header for the prover only.
			setup.prover.WithTranscriptHeader([]byte("headerA"))

			proof, err := setup.prover.Prove()
			require.NoError(t, err)
			require.NotNil(t, proof)

			// Verifier uses a different header — all challenges will differ.
			setup.verifier.WithTranscriptHeader([]byte("headerB"))

			require.NotPanics(t, func() {
				err = setup.verifier.Verify(proof)
			}, "T-GAP-C2: transcript header mismatch must not panic")
			require.Error(t, err, "T-GAP-C2: transcript header mismatch must cause verification failure")
		})
	}
}
