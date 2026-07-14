/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bulletproof_test

import (
	"context"
	"math/bits"
	"math/rand"
	"strconv"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp/bulletproof"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bfSetup struct {
	com       *math.G1
	Q         *math.G1
	P         *math.G1
	H         *math.G1
	G         *math.G1
	bf        *math.Zr
	leftGens  []*math.G1
	rightGens []*math.G1
	nr        uint64
	l         uint64
	curve     *math.Curve
}

func newBfSetup(curveID math.CurveID, b uint64, value int64) (*bfSetup, error) {
	curve := math.Curves[curveID]
	l := b
	nr := log2(l)

	leftGens := make([]*math.G1, l)
	rightGens := make([]*math.G1, l)

	rand, err := curve.Rand()
	if err != nil {
		return nil, err
	}

	Q := curve.GenG1.Mul(curve.NewRandomZr(rand))
	P := curve.GenG1.Mul(curve.NewRandomZr(rand))
	H := curve.GenG1.Mul(curve.NewRandomZr(rand))
	G := curve.GenG1.Mul(curve.NewRandomZr(rand))
	for i := range leftGens {
		leftGens[i] = curve.HashToG1([]byte(strconv.Itoa(2 * i)))
		rightGens[i] = curve.HashToG1([]byte(strconv.Itoa(2*i + 1)))
	}
	bf := curve.NewRandomZr(rand)
	com := G.Mul(curve.NewZrFromInt(value))
	com.Add(H.Mul(bf))

	return &bfSetup{
		com:       com,
		Q:         Q,
		P:         P,
		H:         H,
		G:         G,
		bf:        bf,
		leftGens:  leftGens,
		rightGens: rightGens,
		nr:        nr,
		l:         l,
		curve:     curve,
	}, nil
}

func TestBFProofVerify(t *testing.T) {
	setup, err := newBfSetup(math.BLS12_381_BBS_GURVY, 32, 115)
	require.NoError(t, err)

	prover := bulletproof.NewRangeProver(
		setup.com,
		115,
		[]*math.G1{setup.G, setup.H},
		setup.bf,
		setup.leftGens,
		setup.rightGens,
		setup.P,
		setup.Q,
		setup.nr,
		setup.l,
		setup.curve,
		nil,
	)
	proof, err := prover.Prove()
	require.NoError(t, err)
	assert.NotNil(t, proof)

	verifier := bulletproof.NewRangeVerifier(
		setup.com,
		[]*math.G1{setup.G, setup.H},
		setup.leftGens,
		setup.rightGens,
		setup.P,
		setup.Q,
		setup.nr,
		setup.l,
		setup.curve,
		nil,
	)
	err = verifier.Verify(proof)
	require.NoError(t, err)
}

func BenchmarkBFProver(b *testing.B) {
	// pp, err := profile.New(profile.WithAll(), profile.WithPath("./profile"))
	// require.NoError(b, err)
	// require.NoError(b, pp.Start())
	// defer pp.Stop()
	envs := make([]*bfSetup, 0, 128)
	for range 128 {
		setup, err := newBfSetup(math.BLS12_381_BBS_GURVY, 64, 1_000_000_000_000_000)
		require.NoError(b, err)
		envs = append(envs, setup)
	}

	b.Run("bench", func(b *testing.B) {
		b.ResetTimer()
		for b.Loop() {
			setup := envs[rand.Intn(len(envs))]
			prover := bulletproof.NewRangeProver(
				setup.com,
				1_000_000_000_000_000,
				[]*math.G1{setup.G, setup.H},
				setup.bf,
				setup.leftGens,
				setup.rightGens,
				setup.P,
				setup.Q,
				setup.nr,
				setup.l,
				setup.curve,
				nil,
			)
			proof, err := prover.Prove()
			require.NoError(b, err)
			assert.NotNil(b, proof)
		}
	})
}

func BenchmarkBFVerifier(b *testing.B) {
	setup, err := newBfSetup(math.BLS12_381_BBS_GURVY, 32, 115)
	require.NoError(b, err)

	prover := bulletproof.NewRangeProver(
		setup.com,
		115,
		[]*math.G1{setup.G, setup.H},
		setup.bf,
		setup.leftGens,
		setup.rightGens,
		setup.P,
		setup.Q,
		setup.nr,
		setup.l,
		setup.curve,
		nil,
	)
	proof, err := prover.Prove()
	require.NoError(b, err)

	verifier := bulletproof.NewRangeVerifier(
		setup.com,
		[]*math.G1{setup.G, setup.H},
		setup.leftGens,
		setup.rightGens,
		setup.P,
		setup.Q,
		setup.nr,
		setup.l,
		setup.curve,
		nil,
	)

	b.Run("bench", func(b *testing.B) {
		for b.Loop() {
			err = verifier.Verify(proof)
			require.NoError(b, err)
		}
	})
}

func TestParallelBFProver(t *testing.T) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)

	test := benchmark2.NewTest[*bfSetup](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*bfSetup, error) {
			return newBfSetup(c.CurveID, c.Bits, 1_000_000_000_000_000)
		},
		func(ctx context.Context, setup *bfSetup) error {
			prover := bulletproof.NewRangeProver(
				setup.com,
				1_000_000_000_000_000,
				[]*math.G1{setup.G, setup.H},
				setup.bf,
				setup.leftGens,
				setup.rightGens,
				setup.P,
				setup.Q,
				setup.nr,
				setup.l,
				setup.curve,
				nil,
			)
			_, err := prover.Prove()

			return err
		},
	)
}

func log2(x uint64) uint64 {
	if x == 0 {
		return 0
	}

	return uint64(bits.Len64(x)) - 1 //nolint:gosec
}
