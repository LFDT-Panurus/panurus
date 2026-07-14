/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"fmt"
	"testing"

	math "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCSPWithZeroWitness documents the behavior when the CSP witness is all-zeros.
//
// An all-zero witness causes every cross-commitment Left[i] = MSM(gen_L, wit_R) and
// Right[i] = MSM(gen_R, wit_L) to equal the point at infinity, because scalar
// multiplication by zero always yields the identity element.  Since the verifier now
// rejects identity elements in Left/Right (fix for HIGH finding: CSP folding accepts
// Left/Right at infinity, weakening range-proof soundness), proofs produced from an
// all-zero witness are invalid and verification must fail.
//
// This is not a regression: in the range proof the CSP witness encodes bit-polynomial
// coefficients mixed with random blinding terms and is never legitimately all-zero.
func TestCSPWithZeroWitness(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rounds := uint64(2)
			n := int(1 << rounds)

			rand, err := curve.Rand()
			require.NoError(t, err)

			generators := make([]*math.G1, n)
			witness := make([]*math.Zr, n)
			linearForm := make([]*math.Zr, n)

			for i := range n {
				generators[i] = curve.HashToG1([]byte{byte(i)})
				witness[i] = curve.NewZrFromInt(0) // All zeros → Left/Right will be identity
				linearForm[i] = curve.NewRandomZr(rand)
			}

			com := curve.MultiScalarMul(generators, witness)
			value := math2.InnerProduct(linearForm, witness, curve)

			p := &prover{
				Commitment:     com,
				Generators:     generators,
				LinearForm:     linearForm,
				Value:          value,
				NumberOfRounds: rounds,
				Curve:          curve,
				witness:        witness,
			}
			p.WithTranscriptHeader([]byte("transcript-header"))

			proof, err := p.Prove()
			require.NoError(t, err)

			v := &verifier{
				Commitment:     com,
				Generators:     generators,
				LinearForm:     linearForm,
				Value:          value,
				NumberOfRounds: rounds,
				Curve:          curve,
			}
			v.WithTranscriptHeader([]byte("transcript-header"))

			// All-zero witness ⇒ Left[i] and Right[i] are the identity element.
			// The verifier must reject such proofs to close the soundness gap.
			err = v.Verify(proof)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "proof.Left validation failed")
		})
	}
}

// TestCSPWithMaxFieldElements verifies behavior with maximum field elements.
// Given a CSP instance where witness and linear form use maximum field elements,
// When a proof is generated and verified,
// Then the verification should succeed (modulo arithmetic handles max field elements).
func TestCSPWithMaxFieldElements(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rounds := uint64(2)
			n := int(1 << rounds)

			generators := make([]*math.G1, n)
			witness := make([]*math.Zr, n)
			linearForm := make([]*math.Zr, n)

			// Use maximum field element (order - 1)
			maxElem := curve.NewZrFromBytes(curve.GroupOrder.Bytes())
			maxElem = curve.ModSub(maxElem, curve.NewZrFromInt(1), curve.GroupOrder)

			for i := range n {
				generators[i] = curve.HashToG1([]byte{byte(i)})
				witness[i] = maxElem.Copy()
				linearForm[i] = maxElem.Copy()
			}

			com := curve.MultiScalarMul(generators, witness)
			value := math2.InnerProduct(linearForm, witness, curve)

			prover := &prover{
				Commitment:     com,
				Generators:     generators,
				LinearForm:     linearForm,
				Value:          value,
				NumberOfRounds: rounds,
				Curve:          curve,
				witness:        witness,
			}
			prover.WithTranscriptHeader([]byte("transcript-header"))

			proof, err := prover.Prove()
			require.NoError(t, err)

			verifier := &verifier{
				Commitment:     com,
				Generators:     generators,
				LinearForm:     linearForm,
				Value:          value,
				NumberOfRounds: rounds,
				Curve:          curve,
			}
			verifier.WithTranscriptHeader([]byte("transcript-header"))

			err = verifier.Verify(proof)
			require.NoError(t, err)
		})
	}
}

// TestCSPNonPowerOfTwoSize verifies that non-power-of-2 sizes are rejected.
// Given a CSP instance with a non-power-of-two vector size,
// When proof generation is attempted,
// Then it should return an "invalid length" error.
func TestCSPNonPowerOfTwoSize(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rounds := uint64(2)
			n := 5 // Not a power of 2

			rand, err := curve.Rand()
			require.NoError(t, err)

			generators := make([]*math.G1, n)
			witness := make([]*math.Zr, n)
			linearForm := make([]*math.Zr, n)

			for i := range n {
				generators[i] = curve.HashToG1([]byte{byte(i)})
				witness[i] = curve.NewRandomZr(rand)
				linearForm[i] = curve.NewRandomZr(rand)
			}

			com := curve.MultiScalarMul(generators, witness)
			value := math2.InnerProduct(linearForm, witness, curve)

			prover := &prover{
				Commitment:     com,
				Generators:     generators,
				LinearForm:     linearForm,
				Value:          value,
				NumberOfRounds: rounds,
				Curve:          curve,
				witness:        witness,
			}

			_, err = prover.Prove()
			require.Error(t, err)
			require.Contains(t, err.Error(), "invalid length")
		})
	}
}

// TestCSPEmptyVectors verifies behavior with empty vectors.
// Given a CSP instance with empty vectors,
// When proof generation is attempted,
// Then it should return an error.
func TestCSPEmptyVectors(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]

			prover := &prover{
				Commitment:     curve.GenG1,
				Generators:     []*math.G1{},
				LinearForm:     []*math.Zr{},
				Value:          curve.NewZrFromInt(0),
				NumberOfRounds: 0,
				Curve:          curve,
				witness:        []*math.Zr{},
			}

			_, err := prover.Prove()
			require.Error(t, err)
		})
	}
}

// TestCSPMismatchedGeneratorsWitness verifies error when generators and witness sizes differ.
// Given a CSP instance with mismatched generators and witness lengths,
// When proof generation is attempted,
// Then it should return an error.
func TestCSPMismatchedGeneratorsWitness(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			generators := make([]*math.G1, 4)
			witness := make([]*math.Zr, 3) // Mismatch
			linearForm := make([]*math.Zr, 4)

			for i := range 4 {
				generators[i] = curve.HashToG1([]byte{byte(i)})
				linearForm[i] = curve.NewRandomZr(rand)
			}
			for i := range 3 {
				witness[i] = curve.NewRandomZr(rand)
			}

			prover := &prover{
				Commitment:     curve.GenG1,
				Generators:     generators,
				LinearForm:     linearForm,
				Value:          curve.NewZrFromInt(0),
				NumberOfRounds: 2,
				Curve:          curve,
				witness:        witness,
			}

			_, err = prover.Prove()
			require.Error(t, err)
		})
	}
}

// TestCSPTamperedMultipleRounds verifies that tampering any round invalidates the proof.
// Given an honest CSP proof across multiple rounds,
// When any single round of the proof is tampered with,
// Then the verification should fail for that round.
func TestCSPTamperedMultipleRounds(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup := newCSPSetup(t, curve, 3) // 8 elements, 3 rounds

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			// Test tampering each round
			for round := range 3 {
				t.Run("round_"+string(rune('0'+round)), func(t *testing.T) {
					tamperedProof := &Proof{
						Left:   make([]*math.G1, len(proof.Left)),
						Right:  make([]*math.G1, len(proof.Right)),
						VLeft:  make([]*math.Zr, len(proof.VLeft)),
						VRight: make([]*math.Zr, len(proof.VRight)),
						Curve:  proof.Curve,
					}

					// Copy all rounds
					for i := range proof.Left {
						tamperedProof.Left[i] = proof.Left[i].Copy()
						tamperedProof.Right[i] = proof.Right[i].Copy()
						tamperedProof.VLeft[i] = proof.VLeft[i].Copy()
						tamperedProof.VRight[i] = proof.VRight[i].Copy()
					}

					// Tamper the specific round
					rand, err := curve.Rand()
					require.NoError(t, err)
					tamperedProof.Left[round] = curve.GenG1.Mul(curve.NewRandomZr(rand))

					err = setup.verifier.Verify(tamperedProof)
					require.Error(t, err)
				})
			}
		})
	}
}

// TestCSPSVectorProperties verifies mathematical properties of the s-vector.
// Given a set of Fiat-Shamir challenges,
// When the s-vector is computed,
// Then it should satisfy s[0]=1 and s[i + 2^r] = s[i] * c_{k-1-r}.
func TestCSPSVectorProperties(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			k := 4
			n := 1 << k
			challenges := make([]*math.Zr, k)
			for i := range challenges {
				challenges[i] = curve.NewRandomZr(rand)
			}

			s := sVector(n, challenges, curve)

			// Property 1: s[0] should be 1
			assert.True(t, s[0].Equals(curve.NewZrFromInt(1)), "s[0] should be 1")

			// Property 2: s[i + 2^r] = s[i] * c_{k-1-r} for all valid i, r
			for r := range k {
				halfLen := 1 << r
				c := challenges[k-1-r]
				for i := range halfLen {
					expected := curve.ModMul(s[i], c, curve.GroupOrder)
					assert.True(t, s[i+halfLen].Equals(expected),
						"s[%d] should equal s[%d] * c[%d]", i+halfLen, i, k-1-r)
				}
			}
		})
	}
}

// TestCSPSVectorDifferentChallenges verifies s-vector changes with different challenges.
// Given two different sets of challenges,
// When the corresponding s-vectors are computed,
// Then the vectors should be distinct.
func TestCSPSVectorDifferentChallenges(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			k := 3
			n := 1 << k

			challenges1 := make([]*math.Zr, k)
			challenges2 := make([]*math.Zr, k)
			for i := range k {
				challenges1[i] = curve.NewRandomZr(rand)
				challenges2[i] = curve.NewRandomZr(rand)
			}

			s1 := sVector(n, challenges1, curve)
			s2 := sVector(n, challenges2, curve)

			// Vectors should be different (except s[0] which is always 1)
			differentCount := 0
			for i := 1; i < n; i++ {
				if !s1[i].Equals(s2[i]) {
					differentCount++
				}
			}
			assert.Positive(t, differentCount, "s-vectors with different challenges should differ")
		})
	}
}

// TestCSPVerifierRejectsWrongNumberOfRounds verifies various malformed proof structures.
// Given a valid CSP proof,
// When the proof arrays (Left, Right, etc.) are malformed or have incorrect lengths,
// Then the verifier should reject the proof.
func TestCSPVerifierRejectsWrongNumberOfRounds(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup := newCSPSetup(t, curve, 2)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			testCases := []struct {
				name        string
				modifyProof func(*Proof)
			}{
				{
					name: "extra_left",
					modifyProof: func(p *Proof) {
						p.Left = append(p.Left, curve.GenG1)
					},
				},
				{
					name: "missing_right",
					modifyProof: func(p *Proof) {
						p.Right = p.Right[:len(p.Right)-1]
					},
				},
				{
					name: "extra_vleft",
					modifyProof: func(p *Proof) {
						p.VLeft = append(p.VLeft, curve.NewZrFromInt(0))
					},
				},
				{
					name: "missing_vright",
					modifyProof: func(p *Proof) {
						p.VRight = p.VRight[:len(p.VRight)-1]
					},
				},
			}

			for _, tc := range testCases {
				t.Run(tc.name, func(t *testing.T) {
					tamperedProof := &Proof{
						Left:   append([]*math.G1{}, proof.Left...),
						Right:  append([]*math.G1{}, proof.Right...),
						VLeft:  append([]*math.Zr{}, proof.VLeft...),
						VRight: append([]*math.Zr{}, proof.VRight...),
						Curve:  proof.Curve,
					}
					tc.modifyProof(tamperedProof)

					err := setup.verifier.Verify(tamperedProof)
					require.Error(t, err)
					require.Contains(t, err.Error(), "invalid length")
				})
			}
		})
	}
}

// TestCSPWithIdentityGenerator documents that a generator equal to the identity element
// is now rejected at the prover input-validation stage.
//
// An identity generator means the corresponding witness component contributes nothing to
// the commitment (scalar·identity = identity for any scalar), making the commitment
// scheme unsound: two different witnesses produce the same commitment.  The prover must
// refuse such inputs, and does so since validateG1Slice now calls math.CheckElements
// which rejects points at infinity.
func TestCSPWithIdentityGenerator(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rounds := uint64(2)
			n := int(1 << rounds)

			rand, err := curve.Rand()
			require.NoError(t, err)

			generators := make([]*math.G1, n)
			witness := make([]*math.Zr, n)
			linearForm := make([]*math.Zr, n)

			for i := range n {
				if i == 0 {
					generators[i] = curve.NewG1() // identity element — invalid generator
				} else {
					generators[i] = curve.HashToG1([]byte{byte(i)})
				}
				witness[i] = curve.NewRandomZr(rand)
				linearForm[i] = curve.NewRandomZr(rand)
			}

			com := curve.MultiScalarMul(generators, witness)
			value := math2.InnerProduct(linearForm, witness, curve)

			p := &prover{
				Commitment:     com,
				Generators:     generators,
				LinearForm:     linearForm,
				Value:          value,
				NumberOfRounds: rounds,
				Curve:          curve,
				witness:        witness,
			}
			p.WithTranscriptHeader([]byte("transcript-header"))

			// Identity generator must be rejected by the prover before any proof is made.
			_, err = p.Prove()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "generators validation failed")
		})
	}
}

// TestCSPLargeNumberOfRounds verifies CSP works with larger vector sizes.
// Given a CSP instance with a large number of rounds (e.g., 8 rounds for 256 elements),
// When a proof is generated and verified,
// Then the verification should succeed.
func TestCSPLargeNumberOfRounds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large test in short mode")
	}

	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rounds := uint64(8) // 256 elements
			setup := newCSPSetup(t, curve, rounds)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			err = setup.verifier.Verify(proof)
			require.NoError(t, err)
		})
	}
}

// TestCSPCommitmentMismatch verifies that commitment mismatch is detected.
// Given a valid proof,
// When the verifier's commitment is slightly changed,
// Then the verification should fail.
func TestCSPCommitmentMismatch(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup := newCSPSetup(t, curve, 2)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			// Change commitment by adding a random point
			rand, err := curve.Rand()
			require.NoError(t, err)
			setup.verifier.Commitment = setup.verifier.Commitment.Copy()
			setup.verifier.Commitment.Add(curve.GenG1.Mul(curve.NewRandomZr(rand)))

			err = setup.verifier.Verify(proof)
			require.Error(t, err)
			require.Contains(t, err.Error(), "verification failed")
		})
	}
}

// TestCSPLinearFormMismatch verifies that linear form mismatch is detected.
// Given a valid proof,
// When the verifier's linear form is changed,
// Then the verification should fail.
func TestCSPLinearFormMismatch(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup := newCSPSetup(t, curve, 2)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			// Change one coefficient in the linear form
			rand, err := curve.Rand()
			require.NoError(t, err)
			setup.verifier.LinearForm[0] = curve.NewRandomZr(rand)

			err = setup.verifier.Verify(proof)
			require.Error(t, err)
		})
	}
}

// TestCSPGeneratorMismatch verifies that generator mismatch is detected.
// Given a valid proof,
// When the verifier's generators are changed,
// Then the verification should fail.
func TestCSPGeneratorMismatch(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup := newCSPSetup(t, curve, 2)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			// Change one generator
			rand, err := curve.Rand()
			require.NoError(t, err)
			setup.verifier.Generators[0] = curve.GenG1.Mul(curve.NewRandomZr(rand))

			err = setup.verifier.Verify(proof)
			require.Error(t, err)
		})
	}
}
