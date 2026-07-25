/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"fmt"
	"strconv"
	"testing"

	math "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
)

// paddedInstance mirrors rp.go's CSP input layout: the first m entries are
// "real" (random), and the tail [m,N) is padded exactly as rp.go does —
// witness=0, linearForm=0, generators=GenG1 (a single duplicated generator).
type paddedInstance struct {
	curve      *math.Curve
	rounds     uint64
	N          int
	m          int // real length = 2n+4
	generators []*math.G1
	witness    []*math.Zr
	linearForm []*math.Zr
}

func buildPaddedInstance(curve *math.Curve, rounds uint64, m int) *paddedInstance {
	N := 1 << rounds
	rand, _ := curve.Rand()
	gen := make([]*math.G1, N)
	wit := make([]*math.Zr, N)
	lf := make([]*math.Zr, N)
	for i := range N {
		if i < m {
			gen[i] = curve.HashToG1([]byte("g-" + strconv.Itoa(i)))
			wit[i] = curve.NewRandomZr(rand)
			lf[i] = curve.NewRandomZr(rand)
		} else {
			gen[i] = curve.GenG1 // duplicated non-zero generator (as in rp.go)
			wit[i] = curve.NewZrFromInt(0)
			lf[i] = curve.NewZrFromInt(0)
		}
	}
	return &paddedInstance{curve: curve, rounds: rounds, N: N, m: m, generators: gen, witness: wit, linearForm: lf}
}

func (pi *paddedInstance) prover() *prover { return pi.proverRealLen(0) }

func (pi *paddedInstance) proverHinted() *prover { return pi.proverRealLen(uint64(pi.m)) }

// proverRealLen builds a CSP prover for this instance; realLen=0 disables the
// round-0 fast path (baseline), realLen=m enables it.
func (pi *paddedInstance) proverRealLen(realLen uint64) *prover {
	com := pi.curve.MultiScalarMul(pi.generators, pi.witness)
	value := math2.InnerProduct(pi.linearForm, pi.witness, pi.curve)
	p := &prover{
		Commitment:     com,
		Generators:     pi.generators,
		LinearForm:     pi.linearForm,
		Value:          value,
		NumberOfRounds: pi.rounds,
		Curve:          pi.curve,
		witness:        pi.witness,
		realLen:        realLen,
	}
	return p.WithTranscriptHeader([]byte("bench"))
}

func (pi *paddedInstance) verifier() *verifier {
	com := pi.curve.MultiScalarMul(pi.generators, pi.witness)
	value := math2.InnerProduct(pi.linearForm, pi.witness, pi.curve)
	v := &verifier{
		Commitment:     com,
		Generators:     pi.generators,
		LinearForm:     pi.linearForm,
		Value:          value,
		NumberOfRounds: pi.rounds,
		Curve:          pi.curve,
	}
	return v.WithTranscriptHeader([]byte("bench"))
}

// bitCases maps a range bit-length n to (rounds, m) where m=2n+4 and
// N=2^rounds is the next power of two >= m.
type bitCase struct {
	name   string
	rounds uint64
	m      int
}

func bitCases() []bitCase {
	return []bitCase{
		{"n32", 7, 68},  // 2*32+4=68  -> N=128
		{"n64", 8, 132}, // 2*64+4=132 -> N=256
	}
}

var benchCurves = []struct {
	name string
	id   math.CurveID
}{
	{"BN254", math.BN254},
	{"BLS12_381", math.BLS12_381_BBS_GURVY},
}

// BenchmarkCSPProve measures the CSP prover on a realistic padded input (the
// path production takes: the round-0 fast path enabled via the realLen hint),
// per curve and bit-length. To re-derive the optimization's speedup, swap
// pi.proverHinted() for pi.prover() (realLen=0 disables the fast path).
func BenchmarkCSPProve(b *testing.B) {
	for _, c := range benchCurves {
		curve := math.Curves[c.id]
		for _, bc := range bitCases() {
			pi := buildPaddedInstance(curve, bc.rounds, bc.m)
			b.Run(c.name+"/"+bc.name, func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					if _, err := pi.proverHinted().Prove(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkRangeProofProve measures the full CSP range-proof generation (Lagrange
// linear-form construction + the CSP engine, which carries the round-0 fast path
// via rp.go's realLen hint). This is the "one level up" prover harness — the
// counterpart to BenchmarkRangeProofVerify. Toggle rp.go's `realLen` line to get
// a before/after at this level.
func BenchmarkRangeProofProve(b *testing.B) {
	cases := []struct {
		n     uint64
		value int64
	}{
		{32, 1 << 15},
		{64, 1_000_000_000_000_000},
	}
	for _, c := range benchCurves {
		curve := math.Curves[c.id]
		for _, tc := range cases {
			setup, err := newRPSetup(curve, tc.n, tc.value)
			if err != nil {
				b.Fatal(err)
			}
			b.Run(fmt.Sprintf("%s/n=%d", c.name, tc.n), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					if _, err := setup.prover.Prove(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
