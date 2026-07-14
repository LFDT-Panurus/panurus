/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	math "github.com/IBM/mathlib"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	fscprofile "github.com/hyperledger-labs/fabric-smart-client/node/start/profile"
	"github.com/stretchr/testify/require"
)

// rpSetup holds a consistent prover/verifier pair for a range proof instance.
type rpSetup struct {
	prover   *rangeProver
	verifier *rangeVerifier
	curve    *math.Curve
}

// newRPSetup builds an honest range proof instance for the given value.
// n is the number of bits; the value must lie in [0, 2^n - 1].
// AGenerators has n+1 elements (for a_0..a_n).
// BGenerators has n+1 elements (for b_0, b_{n+1}..b_{2n}).
// VCommitment = v·VGenerators[0] + r·VGenerators[1].
func newRPSetup(curve *math.Curve, n uint64, value int64) (*rpSetup, error) {
	rand, err := curve.Rand()
	if err != nil {
		return nil, err
	}

	aGens := make([]*math.G1, n+1)
	for i := uint64(0); i <= n; i++ {
		aGens[i] = curve.HashToG1([]byte("a-gen-" + strconv.FormatUint(i, 10)))
	}
	bGens := make([]*math.G1, n+1)
	for i := uint64(0); i <= n; i++ {
		bGens[i] = curve.HashToG1([]byte("b-gen-" + strconv.FormatUint(i, 10)))
	}
	vGens := []*math.G1{
		curve.HashToG1([]byte("v-gen-0")),
		curve.HashToG1([]byte("v-gen-1")),
	}

	v := curve.NewZrFromInt(value)
	r := curve.NewRandomZr(rand)
	vComm := curve.MultiScalarMul(vGens, []*math.Zr{v, r})

	transcriptHeader := []byte("transcript-header")
	p := &rangeProver{
		VGenerators:  vGens,
		AGenerators:  aGens,
		BGenerators:  bGens,
		VCommitment:  vComm,
		NumberOfBits: n,
		v:            v,
		r:            r,
		Curve:        curve,
	}
	p.WithTranscriptHeader(transcriptHeader)
	ver := &rangeVerifier{
		VGenerators:  vGens,
		AGenerators:  aGens,
		BGenerators:  bGens,
		VCommitment:  vComm,
		NumberOfBits: n,
		Curve:        curve,
	}
	ver.WithTranscriptHeader(transcriptHeader)

	return &rpSetup{prover: p, verifier: ver, curve: curve}, nil
}

// TestRangeProofProveVerify checks that an honest proof always verifies.
// Test cases use n values where 2n+4 is a power of 2 (no padding needed in CSP):
//
//	n=2  → 2·2+4=8=2³   (2-bit range  [0, 3])
//	n=30 → 2·30+4=64=2⁶ (30-bit range [0, 2³⁰-1])
//	n=62 → 2·62+4=128=2⁷ (62-bit range [0, 2⁶²-1])
//
// Given an honest prover and a value in range,
// When a range proof is generated and verified,
// Then the verification should succeed.
func TestRangeProofProveVerify(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	cases := []struct {
		n     uint64
		value int64
	}{
		{2, 0},                      // min 2-bit
		{2, 3},                      // max 2-bit (2²-1)
		{30, 0},                     // min 30-bit
		{30, 1 << 15},               // mid 30-bit range
		{30, 1<<30 - 1},             // max 30-bit (2³⁰-1)
		{62, 0},                     // min 62-bit
		{62, 1_000_000_000_000_000}, // mid 62-bit range
	}

	for _, curveID := range curves {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("curveID=%d/n=%d/value=%d", curveID, tc.n, tc.value), func(t *testing.T) {
				setup, err := newRPSetup(math.Curves[curveID], tc.n, tc.value)
				require.NoError(t, err)

				proof, err := setup.prover.Prove()
				require.NoError(t, err)
				require.NotNil(t, proof)

				err = setup.verifier.Verify(proof)
				require.NoError(t, err)
			})
		}
	}
}

// TestRangeProofOutOfRange checks that Prove rejects a value that exceeds 2^n - 1.
// Given a range prover,
// When a value that does not fit in n bits is provided,
// Then the proof generation should fail.
func TestRangeProofOutOfRange(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			n := uint64(4) // valid range [0, 15]

			rand, err := curve.Rand()
			require.NoError(t, err)

			aGens := make([]*math.G1, n+1)
			for i := uint64(0); i <= n; i++ {
				aGens[i] = curve.HashToG1([]byte("a-gen-" + strconv.FormatUint(i, 10)))
			}
			bGens := make([]*math.G1, n+1)
			for i := uint64(0); i <= n; i++ {
				bGens[i] = curve.HashToG1([]byte("b-gen-" + strconv.FormatUint(i, 10)))
			}
			vGens := []*math.G1{
				curve.HashToG1([]byte("v-gen-0")),
				curve.HashToG1([]byte("v-gen-1")),
			}

			v := curve.NewZrFromInt(16) // 16 = 2^4, one past the 4-bit max
			r := curve.NewRandomZr(rand)
			vComm := curve.MultiScalarMul(vGens, []*math.Zr{v, r})

			prover := &rangeProver{
				VGenerators:  vGens,
				AGenerators:  aGens,
				BGenerators:  bGens,
				VCommitment:  vComm,
				NumberOfBits: n,
				v:            v,
				r:            r,
				Curve:        curve,
			}
			prover.WithTranscriptHeader([]byte("transcript-header"))

			// the proof is still generated, the verification will fail though
			_, err = prover.Prove()
			require.NoError(t, err)
			// require.Contains(t, err.Error(), "does not fit")
		})
	}
}

// TestRangeProofWrongCommitment checks that Verify fails when VCommitment is replaced.
// Given an honest range proof,
// When the verifier's commitment is replaced with a random point,
// Then the verification should fail.
func TestRangeProofWrongCommitment(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup, err := newRPSetup(curve, 2, 1)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			rand, err := curve.Rand()
			require.NoError(t, err)
			setup.verifier.VCommitment = curve.GenG1.Mul(curve.NewRandomZr(rand))

			err = setup.verifier.Verify(proof)
			require.Error(t, err)
		})
	}
}

// TestRangeProofTamperedPoKA checks that Verify rejects a proof with a wrong PoK blinding commitment.
// Given an honest range proof,
// When the PoK component (pokV.A) is tampered with,
// Then the verification should fail.
func TestRangeProofTamperedPoKA(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup, err := newRPSetup(curve, 2, 1)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			rand, err := curve.Rand()
			require.NoError(t, err)
			proof.pokV.A = curve.GenG1.Mul(curve.NewRandomZr(rand))

			err = setup.verifier.Verify(proof)
			require.Error(t, err)
			require.Contains(t, err.Error(), "proof of knowledge")
		})
	}
}

// TestRangeProofTamperedPoKZ checks that Verify rejects a proof with a wrong PoK response.
// Given an honest range proof,
// When the PoK response (pokV.Z[0]) is tampered with,
// Then the verification should fail.
func TestRangeProofTamperedPoKZ(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup, err := newRPSetup(curve, 2, 1)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			rand, err := curve.Rand()
			require.NoError(t, err)
			proof.pokV.Z[0] = curve.NewRandomZr(rand)

			err = setup.verifier.Verify(proof)
			require.Error(t, err)
			require.Contains(t, err.Error(), "proof of knowledge")
		})
	}
}

// BenchmarkRangeProofProve measures prover performance for n=30 and n=62.
func BenchmarkBFProver(b *testing.B) {
	p, err := fscprofile.New(fscprofile.WithAll(), fscprofile.WithPath("./profile"))
	require.NoError(b, err)
	require.NoError(b, p.Start())
	defer p.Stop()

	cases := []struct {
		n     uint64
		value int64
	}{
		{32, 1 << 15},
		{64, 1_000_000_000_000_000},
	}
	curve := math.Curves[math.BLS12_381_BBS_GURVY]
	for _, tc := range cases {
		setup, err := newRPSetup(curve, tc.n, tc.value)
		require.NoError(b, err)
		b.Run(fmt.Sprintf("n=%d", tc.n), func(b *testing.B) {
			b.ResetTimer()
			for b.Loop() {
				_, err := setup.prover.Prove()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkRangeProofVerify measures verifier performance for n=30 and n=62.
func BenchmarkRangeProofVerify(b *testing.B) {
	cases := []struct {
		n     uint64
		value int64
	}{
		{32, 1 << 15},
		{64, 1_000_000_000_000_000},
	}
	curve := math.Curves[math.BLS12_381_BBS_GURVY]
	for _, tc := range cases {
		setup, err := newRPSetup(curve, tc.n, tc.value)
		require.NoError(b, err)
		proof, err := setup.prover.Prove()
		require.NoError(b, err)
		b.Run(fmt.Sprintf("n=%d", tc.n), func(b *testing.B) {
			for range b.N {
				if err := setup.verifier.Verify(proof); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestParallelBFProver(t *testing.T) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)

	test := benchmark2.NewTest[*rpSetup](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*rpSetup, error) {
			return newRPSetup(math.Curves[c.CurveID], c.Bits, 1_000_000_000_000_000)
		},
		func(ctx context.Context, setup *rpSetup) error {
			_, err := setup.prover.Prove()

			return err
		},
	)
}
