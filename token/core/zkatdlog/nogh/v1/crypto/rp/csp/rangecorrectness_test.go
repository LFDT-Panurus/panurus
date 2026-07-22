/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"fmt"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/stretchr/testify/require"
)

// TestCSPRangeCorrectnessProveVerify verifies batch range proof generation and verification.
// Given a set of values and their corresponding blinding factors,
// When batch range proofs are generated and then verified,
// Then the verification should succeed for the entire batch.
func TestCSPRangeCorrectnessProveVerify(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			n := uint64(8)
			numCommitments := 3

			// Generate generators
			pedersenParams := []*math.G1{
				curve.HashToG1([]byte("ped-0")),
				curve.HashToG1([]byte("ped-1")),
			}
			leftGens := make([]*math.G1, n+1)
			rightGens := make([]*math.G1, n+1)
			for i := uint64(0); i <= n; i++ {
				leftGens[i] = curve.HashToG1([]byte{byte(i), 0})
				rightGens[i] = curve.HashToG1([]byte{byte(i), 1})
			}

			// Generate commitments and values
			commitments := make([]*math.G1, numCommitments)
			values := make([]uint64, numCommitments)
			blindingFactors := make([]*math.Zr, numCommitments)

			for i := range numCommitments {
				values[i] = uint64(i + 1)
				blindingFactors[i] = curve.NewRandomZr(rand)
				v := curve.NewZrFromUint64(values[i])
				commitments[i] = curve.MultiScalarMul(pedersenParams, []*math.Zr{v, blindingFactors[i]})
			}

			// Prove
			prover := NewRangeCorrectnessProver(
				commitments,
				values,
				blindingFactors,
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))

			rc, err := prover.Prove()
			require.NoError(t, err)
			require.NotNil(t, rc)
			require.Len(t, rc.Proofs, numCommitments)

			// Verify
			verifier := NewRangeCorrectnessVerifier(
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))
			verifier.Commitments = commitments

			err = verifier.Verify(rc)
			require.NoError(t, err)
		})
	}
}

// TestCSPRangeCorrectnessSingleCommitment verifies batch proof with single commitment.
// Given a single committed value,
// When a batch range proof (of size 1) is generated and verified,
// Then the verification should succeed.
func TestCSPRangeCorrectnessSingleCommitment(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			n := uint64(8)

			pedersenParams := []*math.G1{
				curve.HashToG1([]byte("ped-0")),
				curve.HashToG1([]byte("ped-1")),
			}
			leftGens := make([]*math.G1, n+1)
			rightGens := make([]*math.G1, n+1)
			for i := uint64(0); i <= n; i++ {
				leftGens[i] = curve.HashToG1([]byte{byte(i), 0})
				rightGens[i] = curve.HashToG1([]byte{byte(i), 1})
			}

			value := uint64(42)
			blindingFactor := curve.NewRandomZr(rand)
			v := curve.NewZrFromUint64(value)
			commitment := curve.MultiScalarMul(pedersenParams, []*math.Zr{v, blindingFactor})

			prover := NewRangeCorrectnessProver(
				[]*math.G1{commitment},
				[]uint64{value},
				[]*math.Zr{blindingFactor},
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))

			rc, err := prover.Prove()
			require.NoError(t, err)
			require.Len(t, rc.Proofs, 1)

			verifier := NewRangeCorrectnessVerifier(
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))
			verifier.Commitments = []*math.G1{commitment}

			err = verifier.Verify(rc)
			require.NoError(t, err)
		})
	}
}

// TestCSPRangeCorrectnessEmptyCommitments verifies behavior with empty commitment set.
// Given no commitments,
// When a batch range proof is requested,
// Then it should return an empty proof set that trivially verifies.
func TestCSPRangeCorrectnessEmptyCommitments(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			n := uint64(8)

			pedersenParams := []*math.G1{
				curve.HashToG1([]byte("ped-0")),
				curve.HashToG1([]byte("ped-1")),
			}
			leftGens := make([]*math.G1, n+1)
			rightGens := make([]*math.G1, n+1)
			for i := uint64(0); i <= n; i++ {
				leftGens[i] = curve.HashToG1([]byte{byte(i), 0})
				rightGens[i] = curve.HashToG1([]byte{byte(i), 1})
			}

			prover := NewRangeCorrectnessProver(
				[]*math.G1{},
				[]uint64{},
				[]*math.Zr{},
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))

			rc, err := prover.Prove()
			require.NoError(t, err)
			require.Empty(t, rc.Proofs)

			verifier := NewRangeCorrectnessVerifier(
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))

			verifier.Commitments = []*math.G1{}

			err = verifier.Verify(rc)
			require.NoError(t, err)
		})
	}
}

// TestCSPRangeCorrectnessMismatchedProofCount verifies error when proof count doesn't match.
// Given a verifier expecting two commitments but receiving only one proof,
// When verification is attempted,
// Then it should return an error.
func TestCSPRangeCorrectnessMismatchedProofCount(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			n := uint64(8)

			pedersenParams := []*math.G1{
				curve.HashToG1([]byte("ped-0")),
				curve.HashToG1([]byte("ped-1")),
			}
			leftGens := make([]*math.G1, n+1)
			rightGens := make([]*math.G1, n+1)
			for i := uint64(0); i <= n; i++ {
				leftGens[i] = curve.HashToG1([]byte{byte(i), 0})
				rightGens[i] = curve.HashToG1([]byte{byte(i), 1})
			}

			// Create 2 commitments
			commitments := make([]*math.G1, 2)
			for i := range 2 {
				v := curve.NewZrFromUint64(uint64(i + 1))
				r := curve.NewRandomZr(rand)
				commitments[i] = curve.MultiScalarMul(pedersenParams, []*math.Zr{v, r})
			}

			// Create proof with only 1 proof
			rc := &RangeCorrectness{
				Proofs: []*RangeProof{{}},
			}

			verifier := NewRangeCorrectnessVerifier(
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))
			verifier.Commitments = commitments

			err = verifier.Verify(rc)
			require.Error(t, err)
			require.Contains(t, err.Error(), "invalid range proof")
		})
	}
}

// TestCSPRangeCorrectnessNilProof verifies error when a proof is nil.
// Given a batch proof set containing a nil element,
// When verification is attempted,
// Then it should return an error.
func TestCSPRangeCorrectnessNilProof(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			n := uint64(8)

			pedersenParams := []*math.G1{
				curve.HashToG1([]byte("ped-0")),
				curve.HashToG1([]byte("ped-1")),
			}
			leftGens := make([]*math.G1, n+1)
			rightGens := make([]*math.G1, n+1)
			for i := uint64(0); i <= n; i++ {
				leftGens[i] = curve.HashToG1([]byte{byte(i), 0})
				rightGens[i] = curve.HashToG1([]byte{byte(i), 1})
			}

			commitment := curve.GenG1.Mul(curve.NewRandomZr(rand))

			rc := &RangeCorrectness{
				Proofs: []*RangeProof{nil},
			}

			verifier := NewRangeCorrectnessVerifier(
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))
			verifier.Commitments = []*math.G1{commitment}

			err = verifier.Verify(rc)
			require.Error(t, err)
			require.Contains(t, err.Error(), "nil proof")
		})
	}
}

// TestCSPRangeCorrectnessSerializationRoundTrip verifies serialization and deserialization.
// Given a set of range proofs,
// When they are serialized to bytes and then deserialized,
// Then the restored proofs should match the original and pass verification.
func TestCSPRangeCorrectnessSerializationRoundTrip(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			n := uint64(8)
			numCommitments := 2

			pedersenParams := []*math.G1{
				curve.HashToG1([]byte("ped-0")),
				curve.HashToG1([]byte("ped-1")),
			}
			leftGens := make([]*math.G1, n+1)
			rightGens := make([]*math.G1, n+1)
			for i := uint64(0); i <= n; i++ {
				leftGens[i] = curve.HashToG1([]byte{byte(i), 0})
				rightGens[i] = curve.HashToG1([]byte{byte(i), 1})
			}

			commitments := make([]*math.G1, numCommitments)
			values := make([]uint64, numCommitments)
			blindingFactors := make([]*math.Zr, numCommitments)

			for i := range numCommitments {
				values[i] = uint64(i + 1)
				blindingFactors[i] = curve.NewRandomZr(rand)
				v := curve.NewZrFromUint64(values[i])
				commitments[i] = curve.MultiScalarMul(pedersenParams, []*math.Zr{v, blindingFactors[i]})
			}

			prover := NewRangeCorrectnessProver(
				commitments,
				values,
				blindingFactors,
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))

			rc, err := prover.Prove()
			require.NoError(t, err)

			// Serialize
			serialized, err := rc.Serialize()
			require.NoError(t, err)
			require.NotEmpty(t, serialized)

			// Deserialize
			rc2 := &RangeCorrectness{}
			err = rc2.Deserialize(serialized)
			require.NoError(t, err)
			require.Len(t, rc2.Proofs, numCommitments)

			// Validate to restore Curve fields
			err = rc2.Validate(curveID)
			require.NoError(t, err)

			// Verify deserialized proof
			verifier := NewRangeCorrectnessVerifier(
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))

			verifier.Commitments = commitments

			err = verifier.Verify(rc2)
			require.NoError(t, err)
		})
	}
}

// TestCSPRangeCorrectnessValidate verifies the Validate method.
// Given various batch proof sets (empty, valid, zero-value, or containing nil
// elements), when the Validate method is called, then it should correctly
// identify invalid proof sets.
//
// T-GAP-C18: the "zero_value_single" case used to be named "valid_single" and
// asserted no error for a RangeProof with every field nil, because
// RangeProof.Validate was a no-op that only restored the transient Curve
// field. Now that Validate performs real structural checks, a zero-value
// RangeProof is correctly rejected; a genuinely constructed proof is added as
// the new "valid_single" case to keep a true-positive case in the suite.
func TestCSPRangeCorrectnessValidate(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup, err := newRPSetup(curve, 4, 10)
			require.NoError(t, err)
			genuineProof, err := setup.prover.Prove()
			require.NoError(t, err)

			testCases := []struct {
				name      string
				rc        *RangeCorrectness
				curveID   math.CurveID
				expectErr bool
			}{
				{
					name: "valid_empty",
					rc: &RangeCorrectness{
						Proofs: []*RangeProof{},
					},
					curveID:   curveID,
					expectErr: false,
				},
				{
					name: "valid_single",
					rc: &RangeCorrectness{
						Proofs: []*RangeProof{genuineProof},
					},
					curveID:   curveID,
					expectErr: false,
				},
				{
					name: "zero_value_single",
					rc: &RangeCorrectness{
						Proofs: []*RangeProof{{}},
					},
					curveID:   curveID,
					expectErr: true,
				},
				{
					name: "nil_proof",
					rc: &RangeCorrectness{
						Proofs: []*RangeProof{nil},
					},
					curveID:   curveID,
					expectErr: true,
				},
				{
					name: "mixed_nil",
					rc: &RangeCorrectness{
						Proofs: []*RangeProof{{}, nil, {}},
					},
					curveID:   curveID,
					expectErr: true,
				},
			}

			for _, tc := range testCases {
				t.Run(tc.name, func(t *testing.T) {
					err := tc.rc.Validate(tc.curveID)
					if tc.expectErr {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
					}
				})
			}
		})
	}
}

// TestCSPRangeCorrectnessLargeSet verifies batch proof with many commitments.
// Given a large number of commitments (e.g., 10),
// When batch range proofs are generated and verified,
// Then the verification should succeed for all.
func TestCSPRangeCorrectnessLargeSet(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large test in short mode")
	}

	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			n := uint64(8)
			numCommitments := 10

			pedersenParams := []*math.G1{
				curve.HashToG1([]byte("ped-0")),
				curve.HashToG1([]byte("ped-1")),
			}
			leftGens := make([]*math.G1, n+1)
			rightGens := make([]*math.G1, n+1)
			for i := uint64(0); i <= n; i++ {
				leftGens[i] = curve.HashToG1([]byte{byte(i), 0})
				rightGens[i] = curve.HashToG1([]byte{byte(i), 1})
			}

			commitments := make([]*math.G1, numCommitments)
			values := make([]uint64, numCommitments)
			blindingFactors := make([]*math.Zr, numCommitments)

			for i := range numCommitments {
				values[i] = uint64(i * 10)
				blindingFactors[i] = curve.NewRandomZr(rand)
				v := curve.NewZrFromUint64(values[i])
				commitments[i] = curve.MultiScalarMul(pedersenParams, []*math.Zr{v, blindingFactors[i]})
			}

			prover := NewRangeCorrectnessProver(
				commitments,
				values,
				blindingFactors,
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))

			rc, err := prover.Prove()
			require.NoError(t, err)
			require.Len(t, rc.Proofs, numCommitments)

			verifier := NewRangeCorrectnessVerifier(
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))
			verifier.Commitments = commitments

			err = verifier.Verify(rc)
			require.NoError(t, err)
		})
	}
}

// TestCSPRangeCorrectnessBoundaryValues verifies proofs for boundary values.
// Given values at the extreme ends of the range (0 and 2^n - 1),
// When batch range proofs are generated and verified,
// Then the verification should succeed.
func TestCSPRangeCorrectnessBoundaryValues(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			n := uint64(8) // Range [0, 255]

			pedersenParams := []*math.G1{
				curve.HashToG1([]byte("ped-0")),
				curve.HashToG1([]byte("ped-1")),
			}
			leftGens := make([]*math.G1, n+1)
			rightGens := make([]*math.G1, n+1)
			for i := uint64(0); i <= n; i++ {
				leftGens[i] = curve.HashToG1([]byte{byte(i), 0})
				rightGens[i] = curve.HashToG1([]byte{byte(i), 1})
			}

			boundaryValues := []uint64{0, 1, 127, 128, 254, 255}
			commitments := make([]*math.G1, len(boundaryValues))
			blindingFactors := make([]*math.Zr, len(boundaryValues))

			for i, val := range boundaryValues {
				blindingFactors[i] = curve.NewRandomZr(rand)
				v := curve.NewZrFromUint64(val)
				commitments[i] = curve.MultiScalarMul(pedersenParams, []*math.Zr{v, blindingFactors[i]})
			}

			prover := NewRangeCorrectnessProver(
				commitments,
				boundaryValues,
				blindingFactors,
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))

			rc, err := prover.Prove()
			require.NoError(t, err)

			verifier := NewRangeCorrectnessVerifier(
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))
			verifier.Commitments = commitments

			err = verifier.Verify(rc)
			require.NoError(t, err)
		})
	}
}

// TestCSPRangeCorrectnessWrongCommitment verifies detection of wrong commitment.
// Given a batch range proof set,
// When the verifier uses a wrong commitment for one of the proofs,
// Then the verification should fail.
func TestCSPRangeCorrectnessWrongCommitment(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			n := uint64(8)

			pedersenParams := []*math.G1{
				curve.HashToG1([]byte("ped-0")),
				curve.HashToG1([]byte("ped-1")),
			}
			leftGens := make([]*math.G1, n+1)
			rightGens := make([]*math.G1, n+1)
			for i := uint64(0); i <= n; i++ {
				leftGens[i] = curve.HashToG1([]byte{byte(i), 0})
				rightGens[i] = curve.HashToG1([]byte{byte(i), 1})
			}

			value := uint64(42)
			blindingFactor := curve.NewRandomZr(rand)
			v := curve.NewZrFromUint64(value)
			commitment := curve.MultiScalarMul(pedersenParams, []*math.Zr{v, blindingFactor})

			prover := NewRangeCorrectnessProver(
				[]*math.G1{commitment},
				[]uint64{value},
				[]*math.Zr{blindingFactor},
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))

			rc, err := prover.Prove()
			require.NoError(t, err)

			// Use wrong commitment for verification
			wrongCommitment := curve.GenG1.Mul(curve.NewRandomZr(rand))

			verifier := NewRangeCorrectnessVerifier(
				pedersenParams,
				leftGens,
				rightGens,
				n,
				curve,
				nil,
			).WithTranscriptHeader([]byte("a_transcript_header"))
			verifier.Commitments = []*math.G1{wrongCommitment}

			err = verifier.Verify(rc)
			require.Error(t, err)
		})
	}
}

// TestCSPRangeCorrectnessDeserializeInvalid verifies error handling for invalid serialization.
// Given malformed or truncated byte slices,
// When deserialization is attempted for a batch proof set,
// Then it should return an error.
func TestCSPRangeCorrectnessDeserializeInvalid(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			rc := &RangeCorrectness{}

			testCases := []struct {
				name string
				data []byte
			}{
				{
					name: "empty",
					data: []byte{},
				},
				{
					name: "invalid_asn1",
					data: []byte{0xFF, 0xFF, 0xFF},
				},
				{
					name: "truncated",
					data: []byte{0x30, 0x10, 0x00},
				},
			}

			for _, tc := range testCases {
				t.Run(tc.name, func(t *testing.T) {
					err := rc.Deserialize(tc.data)
					require.Error(t, err)
				})
			}
		})
	}
}
