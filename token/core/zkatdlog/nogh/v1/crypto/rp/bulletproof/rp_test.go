/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bulletproof_test

import (
	"context"
	"math/big"
	"math/bits"
	"math/rand"
	"strconv"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/common"
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

// TestOutofRange is a regression test for a Fiat-Shamir transcript binding
// issue: the challenge x must be derived from a transcript that also
// includes the earlier commitments C, D, V (and the challenges y, z derived
// from them), not from T1, T2 alone. This test builds a proof the way a
// prover exploiting that gap would: it fixes T1, T2 (and thus x) first, then
// solves for a single coefficient so the evaluated vectors look like bits
// everywhere except slot 0, which absorbs K = t1 x + t2 x^2, encoding a
// value far outside the claimed range. With the challenges properly bound
// to the full transcript, verification must reject this proof.
func TestOutofRange(t *testing.T) {
	for _, cid := range []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY} {
		c := math.Curves[cid]
		const n = 64
		const rounds = 6
		gen := func(s string) *math.G1 { return c.HashToG1([]byte("probe." + s)) }
		G, H, P, Q := gen("G"), gen("H"), gen("P"), gen("Q")
		g := make([]*math.G1, n)
		h := make([]*math.G1, n)
		for i := range n {
			g[i] = gen("L" + strconv.Itoa(i))
			h[i] = gen("R" + strconv.Itoa(i))
		}
		rng, err := c.Rand()
		require.NoError(t, err)
		q := new(big.Int).SetBytes(c.GroupOrder.Bytes())
		toZr := func(b *big.Int) *math.Zr {
			buf := make([]byte, c.ScalarByteSize)
			b.FillBytes(buf)

			return c.NewZrFromBytes(buf)
		}
		toBig := func(z *math.Zr) *big.Int { return new(big.Int).SetBytes(z.Bytes()) }

		// 1. commit to t1,t2 first, obtain x
		var T1, T2 *math.G1
		var x, alpha0, tau1, tau2 *math.Zr
		for {
			t1, t2 := c.NewRandomZr(rng), c.NewRandomZr(rng)
			tau1, tau2 = c.NewRandomZr(rng), c.NewRandomZr(rng)
			T1 = G.Mul2(t1, H, tau1)
			T2 = G.Mul2(t2, H, tau2)
			raw, err := common.GetG1Array([]*math.G1{T1, T2}).Bytes()
			require.NoError(t, err)
			x = c.HashToZr(raw)
			K := c.ModAdd(c.ModMul(t1, x, c.GroupOrder), c.ModMul(t2, c.ModMul(x, x, c.GroupOrder), c.GroupOrder), c.GroupOrder)
			// alpha0^2 - alpha0 - K = 0  ->  alpha0 = (1 + sqrt(1+4K))/2
			disc := new(big.Int).Add(big.NewInt(1), new(big.Int).Mul(big.NewInt(4), toBig(K)))
			disc.Mod(disc, q)
			s := new(big.Int).ModSqrt(disc, q)
			if s == nil {
				continue
			}

			a := new(big.Int).Add(big.NewInt(1), s)
			a.Mul(a, new(big.Int).ModInverse(big.NewInt(2), q))
			a.Mod(a, q)
			alpha0 = toZr(a)

			break
		}
		one := c.NewZrFromInt(1)
		// 2. alpha = (alpha0, 0, ..., 0), beta = alpha - 1; aL = alpha - x sL, aR = beta - x sR
		alpha := make([]*math.Zr, n)
		beta := make([]*math.Zr, n)
		aL := make([]*math.Zr, n)
		aR := make([]*math.Zr, n)
		sL := make([]*math.Zr, n)
		sR := make([]*math.Zr, n)
		for i := range n {
			if i == 0 {
				alpha[i] = alpha0
			} else {
				alpha[i] = c.NewZrFromInt(0)
			}
			beta[i] = c.ModSub(alpha[i], one, c.GroupOrder)
			sL[i], sR[i] = c.NewRandomZr(rng), c.NewRandomZr(rng)
			aL[i] = c.ModSub(alpha[i], c.ModMul(x, sL[i], c.GroupOrder), c.GroupOrder)
			aR[i] = c.ModSub(beta[i], c.ModMul(x, sR[i], c.GroupOrder), c.GroupOrder)
		}
		rho, eta := c.NewRandomZr(rng), c.NewRandomZr(rng)
		C := bulletproof.CommitVectorPlusOne(aL, aR, g, h, rho, P, c)
		D := bulletproof.CommitVectorPlusOne(sL, sR, g, h, eta, P, c)
		// 3. value v = sum 2^i alpha_i = alpha0 (a random field element, far outside [0,2^64))
		v := alpha0
		gamma := c.NewRandomZr(rng)
		V := G.Mul2(v, H, gamma)
		raw, err := common.GetG1Array([]*math.G1{C, D, V}).Bytes()
		require.NoError(t, err)
		y := c.HashToZr(raw)
		z := c.HashToZr(y.Bytes())
		z2 := c.ModMul(z, z, c.GroupOrder)
		// 4. l = alpha - z, r_i = y^i(beta_i + z) + z^2 2^i
		l := make([]*math.Zr, n)
		r := make([]*math.Zr, n)
		hp := make([]*math.G1, n)
		yInv := y.Copy()
		yInv.InvModOrder()
		yi, yInvi := c.NewZrFromInt(1), c.NewZrFromInt(1)
		two := big.NewInt(1)
		for i := range n {
			l[i] = c.ModSub(alpha[i], z, c.GroupOrder)
			r[i] = c.ModMul(yi, c.ModAdd(beta[i], z, c.GroupOrder), c.GroupOrder)
			r[i] = c.ModAdd(r[i], c.ModMul(z2, toZr(two), c.GroupOrder), c.GroupOrder)
			hp[i] = h[i].Mul(yInvi)
			yi = c.ModMul(yi, y, c.GroupOrder)
			yInvi = c.ModMul(yInvi, yInv, c.GroupOrder)
			two = new(big.Int).Lsh(two, 1)
		}
		tHat := c.NewZrFromInt(0)
		for i := range n {
			tHat = c.ModAdd(tHat, c.ModMul(l[i], r[i], c.GroupOrder), c.GroupOrder)
		}
		x2 := c.ModMul(x, x, c.GroupOrder)
		tau := c.ModAdd(c.ModAdd(c.ModMul(tau1, x, c.GroupOrder), c.ModMul(tau2, x2, c.GroupOrder), c.GroupOrder), c.ModMul(z2, gamma, c.GroupOrder), c.GroupOrder)
		delta := c.ModAdd(rho, c.ModMul(eta, x, c.GroupOrder), c.GroupOrder)
		com := bulletproof.CommitVector(l, r, g, hp, c)
		ipa, err := bulletproof.NewIPAProver(tHat, l, r, Q, g, hp, com, rounds, c, newStandaloneIPATranscript(c), nil).Prove()
		require.NoError(t, err)
		proof := &bulletproof.RangeProof{Data: &bulletproof.RangeProofData{T1: T1, T2: T2, C: C, D: D, Tau: tau, Delta: delta, InnerProduct: tHat}, IPA: ipa}

		vBig := toBig(v)
		t.Logf("curve %d: committed value v has %d bits (range is 64 bits)", cid, vBig.BitLen())
		err = bulletproof.NewRangeVerifier(V, []*math.G1{G, H}, g, h, P, Q, rounds, n, c, nil).Verify(proof)
		require.Error(t, err, "curve %d: range proof for out-of-range value (bitlen %d) must be rejected", cid, vBig.BitLen())
	}
}

func log2(x uint64) uint64 {
	if x == 0 {
		return 0
	}

	return uint64(bits.Len64(x)) - 1 //nolint:gosec
}
