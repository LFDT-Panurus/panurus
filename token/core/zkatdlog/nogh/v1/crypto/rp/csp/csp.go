/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	mathlib "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Proof is the non-interactive compressed sigma-protocol proof for a linear
// form evaluation over a Pedersen commitment.
//
// At each of the NumberOfRounds folding steps the prover supplies:
//
//	Left[i]   = MSM(gen_L, wit_R)   — cross-commitment
//	Right[i]  = MSM(gen_R, wit_L)   — cross-commitment
//	VLeft[i]  = ⟨f_L, wit_R⟩       — cross scalar (ModAddMul)
//	VRight[i] = ⟨f_R, wit_L⟩       — cross scalar (ModAddMul)
//
// The verifier reproduces all Fiat-Shamir challenges c_0,…,c_{k-1} and tracks
//
//	C' = c·C + Left  + c²·Right
//	v' = c·v + VLeft + c²·VRight
//
// Then computes gen_f and f_f via a single final MSM / inner-product using the
// coefficient vector s (see cspSVector), and checks
//
//	com_f^{f_f} == gen_f^{val_f}
type Proof struct {
	Left   []*mathlib.G1  // cross commitment MSM(gen_L, wit_R)
	Right  []*mathlib.G1  // cross commitment MSM(gen_R, wit_L)
	VLeft  []*mathlib.Zr  // cross scalar <f_L, wit_R>
	VRight []*mathlib.Zr  // cross scalar <f_R, wit_L>
	Curve  *mathlib.Curve // elliptic curve
}

// prover instantiates a CSP prover
type prover struct {
	Generators     []*mathlib.G1  // Generators for pedersen commitment
	NumberOfRounds uint64         // log(length of vectors), assume power of 2
	Commitment     *mathlib.G1    // Commitment to the witness
	LinearForm     []*mathlib.Zr  // Linear function
	Value          *mathlib.Zr    // Claimed value of linear function over witness
	Curve          *mathlib.Curve // Curve identifier
	witness        []*mathlib.Zr  // opening for the commitment

	TranscriptHeader []byte
}

func (p *prover) WithTranscriptHeader(h []byte) *prover {
	p.TranscriptHeader = h

	return p
}

// Prove generates a CSP proof. Generators, LinearForm, and witness must all
// have length exactly 2^NumberOfRounds.
//
// Per-round folding rules (c is the Fiat-Shamir challenge):
//
//	gen'_j = gen_L[j] + c · gen_R[j]
//	f'_j   = f_L[j]   + c · f_R[j]
//	w'_j   = c · w_L[j] + w_R[j]
func (p *prover) Prove() (*Proof, error) {
	// Validate all inputs
	if err := validateCSPProverInputs(p.Curve, p); err != nil {
		return nil, errors.Wrap(err, "invalid CSP prover inputs")
	}

	// Initialize transcript.
	if len(p.TranscriptHeader) == 0 {
		return nil, errors.New("invalid transcript header")
	}

	tr := Transcript{Curve: p.Curve}
	tr.SetState(p.TranscriptHeader)

	tr.Absorb(p.Commitment.Bytes())
	for _, f := range p.LinearForm {
		tr.Absorb(f.Bytes())
	}
	tr.Absorb(p.Value.Bytes())

	left := make([]*mathlib.G1, p.NumberOfRounds)
	right := make([]*mathlib.G1, p.NumberOfRounds)
	vLeft := make([]*mathlib.Zr, p.NumberOfRounds)
	vRight := make([]*mathlib.Zr, p.NumberOfRounds)

	// Working copies — G1 Add mutates the receiver in-place, so deep-copy generators.
	// Zr values are replaced by ModAdd/ModMul (pointer swap), so shallow copy suffices.
	generators := make([]*mathlib.G1, len(p.Generators))
	for i, g := range p.Generators {
		generators[i] = g.Copy()
	}

	linearForm := make([]*mathlib.Zr, len(p.LinearForm))
	for i, f := range p.LinearForm {
		linearForm[i] = f.Copy()
	}

	witness := make([]*mathlib.Zr, len(p.witness))
	for i, w := range p.witness {
		witness[i] = w.Copy()
	}

	one := math.One(p.Curve)

	for i := range p.NumberOfRounds {
		n := len(generators) / 2

		// Cross-commitments via MSM:
		//   left[i]  = MSM(gen_L, wit_R)   gen_L = generators[:n], wit_R = witness[n:]
		//   right[i] = MSM(gen_R, wit_L)   gen_R = generators[n:], wit_L = witness[:n]
		left[i] = p.Curve.MultiScalarMul(generators[:n], witness[n:])
		right[i] = p.Curve.MultiScalarMul(generators[n:], witness[:n])

		// Cross scalar products via ModAddMul (scalar-field MSM):
		//   vLeft[i]  = ⟨f_L, wit_R⟩
		//   vRight[i] = ⟨f_R, wit_L⟩
		vLeft[i] = math.InnerProduct(linearForm[:n], witness[n:], p.Curve)
		vRight[i] = math.InnerProduct(linearForm[n:], witness[:n], p.Curve)

		// Absorb cross terms into transcript, then squeeze challenge.
		tr.Absorb(left[i].Bytes())
		tr.Absorb(right[i].Bytes())
		tr.Absorb(vLeft[i].Bytes())
		tr.Absorb(vRight[i].Bytes())

		c, err := tr.Squeeze()
		if err != nil {
			return nil, errors.New("unable to generate challenge")
		}

		// Fold generators:  gen'[j] = gen_L[j] + c · gen_R[j]
		// Fold linear form: f'[j]   = f_L[j]   + c · f_R[j]
		// Fold witness:     w'[j]   = c · w_L[j] + w_R[j]
		for j := range n {
			// gen'[j] = 1·gen_L[j] + c·gen_R[j], zero allocations
			generators[j].Mul2InPlace(one, generators[n+j], c)

			p.Curve.ModAddMul2InPlace(
				linearForm[j],
				one, linearForm[j],
				c, linearForm[n+j],
				p.Curve.GroupOrder,
			)

			p.Curve.ModAddMul2InPlace(
				witness[j],
				c, witness[j],
				witness[n+j], one,
				p.Curve.GroupOrder,
			)
		}

		generators = generators[:n]
		linearForm = linearForm[:n]
		witness = witness[:n]
	}

	return &Proof{
		Left:   left,
		Right:  right,
		VLeft:  vLeft,
		VRight: vRight,
		Curve:  p.Curve,
	}, nil
}

// verifier verifies a Proof against a public statement.
type verifier struct {
	Commitment       *mathlib.G1   // Pedersen commitment C = MSM(gen, w)
	Generators       []*mathlib.G1 // Commitment generators
	LinearForm       []*mathlib.Zr // Coefficients of the linear form f
	Value            *mathlib.Zr   // Claimed evaluation v = ⟨f, w⟩
	NumberOfRounds   uint64        // log₂(vector length)
	Curve            *mathlib.Curve
	TranscriptHeader []byte
}

func (v *verifier) WithTranscriptHeader(h []byte) *verifier {
	v.TranscriptHeader = h

	return v
}

// Verify checks that proof is a valid CSP proof for the statement
// (Commitment, Generators, LinearForm, Value).
// Then it computes gen_final and f_final via a single final MSM using the coefficient
// vector s (see cspSVector), and checks:
//
//	com_f^{f_f} == gen_f^{val_f}
//
// This holds iff there exists w_f : com_f = MSM(gen_f, w_f) ∧ val_f = f_f · w_f.
// This is similar to the optimisation used in Bulletproof verifier:
// See: Page 17, Section 3, https://eprint.iacr.org/2017/1066.pdf
func (v *verifier) Verify(proof *Proof) error {
	// Validate verifier inputs
	if err := validateCSPVerifierInputs(v.Curve, v); err != nil {
		return errors.Wrap(err, "invalid CSP verifier inputs")
	}

	// Validate proof structure
	if err := validateCSPProof(v.Curve, proof, v.NumberOfRounds); err != nil {
		return errors.Wrap(err, "invalid CSP proof")
	}

	// Initialize transcript — must mirror Prove() exactly.
	if len(v.TranscriptHeader) == 0 {
		return errors.New("invalid transcript header")
	}

	tr := Transcript{Curve: v.Curve}
	tr.SetState(v.TranscriptHeader)

	tr.Absorb(v.Commitment.Bytes())
	for _, f := range v.LinearForm {
		tr.Absorb(f.Bytes())
	}
	tr.Absorb(v.Value.Bytes())

	// Replay transcript to collect all challenges and update the value
	// accumulator. The commitment folding is deferred to a single MSM below.
	challenges := make([]*mathlib.Zr, v.NumberOfRounds)
	val := v.Value.Copy()
	one := math.One(v.Curve)

	cSq := v.Curve.NewZrFromInt(0)

	for i := range v.NumberOfRounds {
		// Absorb cross terms (same order as Prove()).
		tr.Absorb(proof.Left[i].Bytes())
		tr.Absorb(proof.Right[i].Bytes())
		tr.Absorb(proof.VLeft[i].Bytes())
		tr.Absorb(proof.VRight[i].Bytes())

		c, err := tr.Squeeze()
		if err != nil {
			return errors.New("unable to recompute challenge")
		}
		challenges[i] = c

		// Update value: v' = c·v + VLeft[i] + c²·VRight[i]
		// (scalar-only; no G1 allocations)
		v.Curve.ModMulInPlace(cSq, c, c, v.Curve.GroupOrder)

		v.Curve.ModAddMul3InPlace(
			val,
			c, val,
			proof.VLeft[i], one,
			cSq, proof.VRight[i],
			v.Curve.GroupOrder,
		)
	}

	// Batch-compute the folded commitment via a single MSM, replacing the
	// sequential Mul+Add loop that allocated one G1 per round.
	k := int(v.NumberOfRounds) // #nosec G115

	suffProd := make([]*mathlib.Zr, k)
	suffProd[k-1] = one

	for i := k - 2; i >= 0; i-- {
		suffProd[i] = v.Curve.NewZrFromInt(0)
		v.Curve.ModMulInPlace(suffProd[i], suffProd[i+1], challenges[i+1], v.Curve.GroupOrder)
	}

	sC := v.Curve.NewZrFromInt(0)
	v.Curve.ModMulInPlace(sC, challenges[0], suffProd[0], v.Curve.GroupOrder)

	// Build flat (point, scalar) slices: C₀, then (L_i, R_i) for each round.
	comPoints := make([]*mathlib.G1, 0, 2*k+1)
	comScalars := make([]*mathlib.Zr, 0, 2*k+1)

	comPoints = append(comPoints, v.Commitment)
	comScalars = append(comScalars, sC)

	cSqScratch := v.Curve.NewZrFromInt(0)
	mulScratch := v.Curve.NewZrFromInt(0)

	for i := range k {
		v.Curve.ModMulInPlace(cSqScratch, challenges[i], challenges[i], v.Curve.GroupOrder)

		comPoints = append(comPoints, proof.Left[i])
		comScalars = append(comScalars, suffProd[i])

		v.Curve.ModMulInPlace(mulScratch, cSqScratch, suffProd[i], v.Curve.GroupOrder)

		comPoints = append(comPoints, proof.Right[i])
		comScalars = append(comScalars, mulScratch.Copy())
	}

	com := v.Curve.MultiScalarMul(comPoints, comScalars)

	// Compute the coefficient vector s such that
	//   gen_f = sum_i s[i] · gen[i]   and   f_f = sum_i s[i] · f[i]
	// then evaluate both via a single MSM and a single inner product.
	n := 1 << v.NumberOfRounds
	s := sVector(n, challenges, v.Curve)

	// gen_f = MSM(Generators, s)
	genF := v.Curve.MultiScalarMul(v.Generators, s)

	// f_f = ⟨s, LinearForm⟩  (scalar-field MSM via ModAddMul)
	fF := math.InnerProduct(s, v.LinearForm, v.Curve)

	// Final check: com_f^{f_f} == gen_f^{val_f}
	lhs := com.Mul(fF)
	rhs := genF.Mul(val)

	if !lhs.Equals(rhs) {
		return errors.New("CSP proof verification failed")
	}

	return nil
}

// sVector computes the length-n coefficient vector s such that the
// final folded generator gen_f = sum_i s[i] · gen[i].
//
// Under the folding rule gen'_j = gen_L[j] + c_r · gen_R[j], after k=log₂(n)
// rounds with challenges c_0,…,c_{k-1}:
//
//	s[i] = ∏_{r=0}^{k-1} c_r^{ bit(i, k-1-r) }
//
// where bit(i,m) is the m-th bit of i (0 = LSB). The vector is built in O(n)
// multiplications using the recurrence: s[i + 2^r] = s[i] · c_{k-1-r}.
func sVector(n int, challenges []*mathlib.Zr, curve *mathlib.Curve) []*mathlib.Zr {
	k := len(challenges)
	s := make([]*mathlib.Zr, n)

	s[0] = math.One(curve)

	for i := 1; i < n; i++ {
		s[i] = curve.NewZrFromInt(0)
	}

	for r := range k {
		halfLen := 1 << r      // number of entries already filled
		c := challenges[k-1-r] // reverse order matches bit(i, k-1-r)
		for i := range halfLen {
			curve.ModMulInPlace(s[i+halfLen], s[i], c, curve.GroupOrder)
		}
	}

	return s
}
