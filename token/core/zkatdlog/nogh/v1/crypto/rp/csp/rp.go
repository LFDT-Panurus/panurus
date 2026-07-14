/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"math/big"

	mathlib "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/asn1"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type pokCommitment struct {
	A *mathlib.G1   // first message A = s[0].g[0] + s[1].g[1]
	Z []*mathlib.Zr // last message
}

type RangeProof struct {
	// commitment to a_0,a_1,...,a_n,b_0,b_{n+1},...,b_{2n}
	pComm *mathlib.G1
	// proof of knowledge of VCommitment over VGenerators
	pokV pokCommitment
	// prover evaluation a(c) for checking multiplication of polynomials
	u *mathlib.Zr
	// Commitment of vector to blind the CSP witness: a_0,a_1,..,a_n,b_0,b_{n+1},...,b_{2n},v,r
	sComm *mathlib.G1 // commitment to blinding vector s_0...s_{2n+1}
	sEval *mathlib.Zr // linear form on the blinding vector
	// CSP proof for: Com(wit) = pExt + \epsilon\cdot sComm, L(wit) = L(x) + \epsilon\cdot sEval
	cspProof Proof
}

func (p *RangeProof) Serialize() ([]byte, error) {
	a1, err := asn1.NewElementArray[*mathlib.Zr](p.pokV.Z)
	if err != nil {
		return nil, err
	}
	left, err := asn1.NewElementArray(p.cspProof.Left)
	if err != nil {
		return nil, err
	}
	right, err := asn1.NewElementArray(p.cspProof.Right)
	if err != nil {
		return nil, err
	}
	vLeft, err := asn1.NewElementArray(p.cspProof.VLeft)
	if err != nil {
		return nil, err
	}
	vRight, err := asn1.NewElementArray(p.cspProof.VRight)
	if err != nil {
		return nil, err
	}

	return asn1.MarshalMath(
		p.pComm,
		p.pokV.A,
		a1,
		p.u,
		p.sComm,
		p.sEval,
		left,
		right,
		vLeft,
		vRight,
	)
}

func (p *RangeProof) Deserialize(raw []byte) error {
	unmarshaller, err := asn1.NewUnmarshaller(raw)
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize raw")
	}
	p.pComm, err = unmarshaller.NextG1()
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize Left")
	}
	p.pokV.A, err = unmarshaller.NextG1()
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize Right")
	}
	p.pokV.Z, err = unmarshaller.NextZrArray()
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize L")
	}
	p.u, err = unmarshaller.NextZr()
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize R")
	}
	p.sComm, err = unmarshaller.NextG1()
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize R")
	}
	p.sEval, err = unmarshaller.NextZr()
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize R")
	}
	p.cspProof.Left, err = unmarshaller.NextG1Array()
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize R")
	}
	p.cspProof.Right, err = unmarshaller.NextG1Array()
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize R")
	}
	p.cspProof.VLeft, err = unmarshaller.NextZrArray()
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize R")
	}
	p.cspProof.VRight, err = unmarshaller.NextZrArray()
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize R")
	}

	return nil
}

func (p *RangeProof) Validate(curve mathlib.CurveID) error {
	// Restore the Curve field after deserialization
	p.cspProof.Curve = mathlib.Curves[curve]

	return nil
}

type rangeProver struct {
	VCommitment *mathlib.G1 // commitment to value v
	v           *mathlib.Zr // value
	r           *mathlib.Zr // randomness to mask v

	VGenerators  []*mathlib.G1  // two generators to commit to v
	AGenerators  []*mathlib.G1  // generators to commit to bits encoded as polynomial a(X)
	BGenerators  []*mathlib.G1  // generators to commit to b(X) by committing to b(n+1),...b(2n)
	NumberOfBits uint64         // number of bits n; the value must lie in [0, 2^n - 1]
	Curve        *mathlib.Curve // curve

	TranscriptHeader []byte
}

func NewRangeProver(
	VCommitment *mathlib.G1,
	v *mathlib.Zr,
	r *mathlib.Zr,
	VGenerators []*mathlib.G1,
	AGenerators []*mathlib.G1,
	BGenerators []*mathlib.G1,
	numberOfBits uint64,
	curve *mathlib.Curve,
) *rangeProver {
	return &rangeProver{
		VCommitment:  VCommitment,
		v:            v,
		r:            r,
		VGenerators:  VGenerators,
		AGenerators:  AGenerators,
		BGenerators:  BGenerators,
		NumberOfBits: numberOfBits,
		Curve:        curve,
	}
}

func (rp *rangeProver) WithTranscriptHeader(h []byte) *rangeProver {
	rp.TranscriptHeader = h

	return rp
}

func (rp *rangeProver) Prove() (*RangeProof, error) {
	// Validate all inputs
	if err := validateRangeProverInputs(rp.Curve, rp); err != nil {
		return nil, errors.Wrap(err, "invalid range prover inputs")
	}

	n := rp.NumberOfBits
	tr := Transcript{Curve: rp.Curve}
	if len(rp.TranscriptHeader) != 0 {
		tr.SetState(rp.TranscriptHeader)
	} else {
		tr.InitHasher()
		// Absorb the public statement: VGenerators || AGenerators || BGenerators || NumberOfBits.
		for _, g := range rp.VGenerators {
			tr.Absorb(g.Bytes())
		}
		for _, g := range rp.AGenerators {
			tr.Absorb(g.Bytes())
		}
		for _, g := range rp.BGenerators {
			tr.Absorb(g.Bytes())
		}
		tr.Absorb(new(big.Int).SetUint64(n).Bytes())
	}
	// Absorb VCommitment
	tr.Absorb(rp.VCommitment.Bytes())

	// Schnorr proof of knowledge for VCommitment = v·G_v + r·G_r.
	// Prover samples blinding scalars, commits, then responds to the FS challenge.
	rand, err := rp.Curve.Rand()
	if err != nil {
		return nil, errors.Wrap(err, "failed to initialize random number generator")
	}
	pokTv := rp.Curve.NewRandomZr(rand)
	pokTr := rp.Curve.NewRandomZr(rand)
	pokA := rp.Curve.MultiScalarMul(rp.VGenerators, []*mathlib.Zr{pokTv, pokTr})
	tr.Absorb(pokA.Bytes())
	pokE, err := tr.Squeeze()
	if err != nil {
		return nil, errors.New("unable to obtain PoK challenge")
	}
	// z_v = t_v + e·v,  z_r = t_r + e·r
	pokZv := rp.Curve.ModAddMul2(
		pokTv, math.One(rp.Curve),
		pokE, rp.v,
		rp.Curve.GroupOrder,
	)
	pokZr := rp.Curve.ModAddMul2(
		pokTr, math.One(rp.Curve),
		pokE, rp.r,
		rp.Curve.GroupOrder,
	)

	// Step 1: Compute witness p = aCoeffs || bCoeffs where
	//   aCoeffs = [a_0, a_1, ..., a_n]:  a_1..a_n are bits of v, a_0 is random
	//   bCoeffs = [b_0, b_{n+1}, ..., b_{2n}]:  b_i = a(i)*(a(i)-1)
	a0 := rp.Curve.NewRandomZr(rand)
	b0 := rp.Curve.ModSub(a0, math.One(rp.Curve), rp.Curve.GroupOrder)
	bitsOfV, err := toBits(rp.v, n, rp.Curve)
	if err != nil {
		return nil, err
	}

	aCoeffs := make([]*mathlib.Zr, n+1)
	bCoeffs := make([]*mathlib.Zr, n+1)
	aCoeffs[0] = a0
	copy(aCoeffs[1:], bitsOfV)

	// Extend a(X) to all of {0,...,2n} and compute b(i) = a(i)*(a(i)-1).
	aCoeffsExt, err := interpolate(n, aCoeffs, rp.Curve)
	if err != nil {
		return nil, errors.New("Error while extending a polynomial")
	}
	bCoeffs[0] = rp.Curve.ModMul(a0, b0, rp.Curve.GroupOrder)
	for i := uint64(1); i <= n; i++ {
		ai := aCoeffsExt[n+i]
		aiMinus1 := rp.Curve.ModSub(ai, math.One(rp.Curve), rp.Curve.GroupOrder)
		bCoeffs[i] = rp.Curve.ModMul(ai, aiMinus1, rp.Curve.GroupOrder)
	}

	p := make([]*mathlib.Zr, 2*n+2)
	g := make([]*mathlib.G1, 2*n+2)
	copy(p, aCoeffs)
	copy(p[n+1:], bCoeffs)
	copy(g, rp.AGenerators)
	copy(g[n+1:], rp.BGenerators)

	// First prover message: pComm = MSM(g, p). Absorb and squeeze eta, c.
	pComm := rp.Curve.MultiScalarMul(g, p)
	tr.Absorb(pComm.Bytes())
	eta, err := tr.Squeeze()
	if err != nil {
		return nil, errors.New("Unable to obtain challenge eta")
	}
	c, err := tr.Squeeze()
	if err != nil {
		return nil, errors.New("Unable to obtain challenge c")
	}

	// Compute u = a(c) via Lagrange interpolation, absorb it, then squeeze gamma.
	mu, err := getLagrangeMultipliers(n, c, rp.Curve)
	if err != nil {
		return nil, errors.New("Unable to obtain lagrange multipliers")
	}
	nu, err := getLagrangeMultipliersPartial(n, c, rp.Curve)
	if err != nil {
		return nil, errors.New("Unable to obtain partial lagrange multipliers")
	}
	// Compute u = <aCoeffs, mu> — dispatch to native for supported curves.
	isBLS, isBN254 := math.DispatchCurve(rp.Curve)
	var u *mathlib.Zr
	if isBLS {
		u = nativeRPInnerProduct[bls12381fr.Element, *bls12381fr.Element](aCoeffs, mu, rp.Curve)
	} else if isBN254 {
		u = nativeRPInnerProduct[bn254fr.Element, *bn254fr.Element](aCoeffs, mu, rp.Curve)
	} else {
		u = math.InnerProduct(aCoeffs, mu, rp.Curve)
	}

	tr.Absorb(u.Bytes())
	gamma, err := tr.Squeeze()
	if err != nil {
		return nil, errors.New("Unable to obtain challenge gamma")
	}

	// Compute paddedSize for lf/wit (next power of 2 >= 2n+4).
	witSize := 2*n + 4
	cspRounds := uint64(0)
	paddedSize := uint64(1)
	for paddedSize < witSize {
		paddedSize <<= 1
		cspRounds++
	}

	// Extended commitment: pCommExt = pComm + eta * VCommitment.
	pCommExt := pComm.Copy()
	pCommExt.Add(rp.VCommitment.Mul(eta))

	// Extended witness pExt = aCoeffs || bCoeffs || v || r
	// over generators  gExt = AGenerators || BGenerators || \eta * VGenerators.
	pExt := make([]*mathlib.Zr, 2*n+4)
	gExt := make([]*mathlib.G1, 2*n+4)
	copy(pExt, p)
	pExt[2*n+2] = rp.v.Copy()
	pExt[2*n+3] = rp.r.Copy()
	copy(gExt, g)
	gExt[2*n+2] = rp.VGenerators[0].Mul(eta)
	gExt[2*n+3] = rp.VGenerators[1].Mul(eta)

	// Build aggregated linear form, and compute lVal.
	// Dispatch to native gnark arithmetic for supported curves.
	var lf []*mathlib.Zr
	var lVal *mathlib.Zr
	if isBLS {
		lf, _, lVal = nativeRPBuildLF[bls12381fr.Element, *bls12381fr.Element](
			n, eta, gamma, mu, nu, aCoeffs, u, rp.Curve)
	} else if isBN254 {
		lf, _, lVal = nativeRPBuildLF[bn254fr.Element, *bn254fr.Element](
			n, eta, gamma, mu, nu, aCoeffs, u, rp.Curve)
	} else {
		gammaSquare := rp.Curve.ModMul(gamma, gamma, rp.Curve.GroupOrder)
		lf = make([]*mathlib.Zr, len(pExt))
		for i := range lf {
			lf[i] = math.Zero(rp.Curve)
		}
		// L1 contribution1s.
		for i := uint64(1); i <= n; i++ {
			lf[i] = rp.Curve.ModMul(eta, math.PowerOfTwo(rp.Curve, i-1), rp.Curve.GroupOrder)
		}
		negEta := eta.Copy()
		negEta.Neg()
		lf[2*n+2] = negEta
		// L2 contributions: add gamma*mu[i] at positions 0..n.
		for i := uint64(0); i <= n; i++ {
			lf[i] = rp.Curve.ModAddMul2(
				lf[i], math.One(rp.Curve),
				gamma, mu[i],
				rp.Curve.GroupOrder,
			)
		}
		// L3 contributions: gamma^2*nu[k] at positions n+1..2n+1.
		for k := uint64(0); k <= n; k++ {
			lf[n+1+k] = rp.Curve.ModMul(gammaSquare, nu[k], rp.Curve.GroupOrder)
		}

		// Claimed value: lVal = gamma*u + gamma^2*u*(u-1)  (L1(pExt)=0 for honest prover).
		uMinus1 := rp.Curve.ModSub(u, math.One(rp.Curve), rp.Curve.GroupOrder)
		lVal = rp.Curve.ModAddMul2(
			gamma, u,
			gammaSquare, rp.Curve.ModMul(u, uMinus1, rp.Curve.GroupOrder),
			rp.Curve.GroupOrder,
		)
	}

	// ZK blinding: random sBlind, commit it, evaluate L on it.
	sBlind := make([]*mathlib.Zr, len(pExt))
	for i := range sBlind {
		sBlind[i] = rp.Curve.NewRandomZr(rand)
	}
	sComm := rp.Curve.MultiScalarMul(gExt, sBlind)
	// Compute sVal = ⟨lf, sBlind⟩ — dispatch to native for supported curves.
	var sVal *mathlib.Zr
	if isBLS {
		sVal = nativeRPInnerProduct[bls12381fr.Element, *bls12381fr.Element](lf, sBlind, rp.Curve)
	} else if isBN254 {
		sVal = nativeRPInnerProduct[bn254fr.Element, *bn254fr.Element](lf, sBlind, rp.Curve)
	} else {
		sVal = math.InnerProduct(lf, sBlind, rp.Curve)
	}
	tr.Absorb(sComm.Bytes())
	tr.Absorb(sVal.Bytes())

	rho, err := tr.Squeeze()
	if err != nil {
		return nil, errors.New("Unable to obtain challenge rho")
	}

	// Compute wit and witVal — dispatch to native for supported curves.
	var wit []*mathlib.Zr
	var witVal *mathlib.Zr
	if isBLS {
		wit, witVal = nativeRPBlindWitness[bls12381fr.Element, *bls12381fr.Element](
			sBlind, pExt, rho, lVal, sVal, rp.Curve)
	} else if isBN254 {
		wit, witVal = nativeRPBlindWitness[bn254fr.Element, *bn254fr.Element](
			sBlind, pExt, rho, lVal, sVal, rp.Curve)
	} else {
		wit = make([]*mathlib.Zr, len(pExt))
		for i := range pExt {
			wit[i] = rp.Curve.ModAddMul2(
				pExt[i], math.One(rp.Curve),
				rho, sBlind[i],
				rp.Curve.GroupOrder,
			)
		}
		witVal = rp.Curve.ModAddMul2(
			lVal, math.One(rp.Curve),
			rho, sVal,
			rp.Curve.GroupOrder,
		)
	}
	witComm := pCommExt.Copy()
	witComm.Add(sComm.Mul(rho))

	// Pad witness / generators / linear form to the next power of 2 for CSP.
	for uint64(len(wit)) < paddedSize {
		wit = append(wit, math.Zero(rp.Curve))
		gExt = append(gExt, rp.Curve.GenG1)
		lf = append(lf, math.Zero(rp.Curve))
	}

	// Non-ZK CSP proof for the blinded statement.
	cspP := &prover{
		Commitment:     witComm,
		Generators:     gExt,
		LinearForm:     lf,
		Value:          witVal,
		NumberOfRounds: cspRounds,
		Curve:          rp.Curve,
		witness:        wit,
	}
	cspProof, err := cspP.WithTranscriptHeader(rp.TranscriptHeader).Prove()
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate CSP proof")
	}

	return &RangeProof{
		pComm:    pComm,
		pokV:     pokCommitment{A: pokA, Z: []*mathlib.Zr{pokZv, pokZr}},
		u:        u,
		sComm:    sComm,
		sEval:    sVal,
		cspProof: *cspProof,
	}, nil
}

type rangeVerifier struct {
	VGenerators  []*mathlib.G1 // generators for the value commitment
	AGenerators  []*mathlib.G1 // generators for the bits polynomial a(X)
	BGenerators  []*mathlib.G1 // generators for b(X) at points {0, n+1, ..., 2n}
	VCommitment  *mathlib.G1   // commitment to value v
	NumberOfBits uint64        // number of bits n; the value must lie in [0, 2^n - 1]
	Curve        *mathlib.Curve

	TranscriptHeader []byte
}

func NewRangeVerifier(VGenerators []*mathlib.G1, AGenerators []*mathlib.G1, BGenerators []*mathlib.G1, VCommitment *mathlib.G1, numberOfBits uint64, curve *mathlib.Curve) *rangeVerifier {
	return &rangeVerifier{VGenerators: VGenerators, AGenerators: AGenerators, BGenerators: BGenerators, VCommitment: VCommitment, NumberOfBits: numberOfBits, Curve: curve}
}

func (rv *rangeVerifier) WithTranscriptHeader(h []byte) *rangeVerifier {
	rv.TranscriptHeader = h

	return rv
}

// Verify checks that proof is a valid CSP range proof against the public statement.
// It mirrors the prover transcript exactly, reconstructs all challenges, rebuilds
// the aggregated linear form, and delegates the final check to cspVerifier.
func (rv *rangeVerifier) Verify(proof *RangeProof) error {
	// Validate all inputs
	if err := validateRangeVerifierInputs(rv.Curve, rv); err != nil {
		return errors.Wrap(err, "invalid range verifier inputs")
	}

	// Validate proof structure
	if err := validateRangeProof(rv.Curve, proof); err != nil {
		return errors.Wrap(err, "invalid range proof structure")
	}

	n := rv.NumberOfBits
	tr := Transcript{Curve: rv.Curve}
	if len(rv.TranscriptHeader) != 0 {
		tr.SetState(rv.TranscriptHeader)
	} else {
		tr.InitHasher()

		// Replay transcript: absorb the same public statement as the prover.
		for _, g := range rv.VGenerators {
			tr.Absorb(g.Bytes())
		}
		for _, g := range rv.AGenerators {
			tr.Absorb(g.Bytes())
		}
		for _, g := range rv.BGenerators {
			tr.Absorb(g.Bytes())
		}
		tr.Absorb(new(big.Int).SetUint64(n).Bytes())
	}
	tr.Absorb(rv.VCommitment.Bytes())

	// Verify Schnorr PoK for VCommitment = v·G_v + r·G_r.
	// Check: z_v·G_v + z_r·G_r == pokA + e·V
	tr.Absorb(proof.pokV.A.Bytes())
	pokE, err := tr.Squeeze()
	if err != nil {
		return errors.New("unable to recompute PoK challenge")
	}
	pokLHS := rv.Curve.MultiScalarMul(rv.VGenerators, proof.pokV.Z)
	pokRHS := proof.pokV.A.Copy()
	pokRHS.Add(rv.VCommitment.Mul(pokE))
	if !pokLHS.Equals(pokRHS) {
		return errors.New("proof of knowledge for value commitment failed")
	}

	tr.Absorb(proof.pComm.Bytes())

	eta, err := tr.Squeeze()
	if err != nil {
		return errors.New("unable to recompute challenge eta")
	}
	c, err := tr.Squeeze()
	if err != nil {
		return errors.New("unable to recompute challenge c")
	}

	// start := time.Now()
	mu, err := getLagrangeMultipliers(n, c, rv.Curve)
	if err != nil {
		return errors.New("unable to obtain lagrange multipliers")
	}
	nu, err := getLagrangeMultipliersPartial(n, c, rv.Curve)
	if err != nil {
		return errors.New("unable to obtain partial lagrange multipliers")
	}
	// fmt.Printf("Tine taken for Lagrange multipliers: %d", time.Since(start).Milliseconds())

	tr.Absorb(proof.u.Bytes())
	gamma, err := tr.Squeeze()
	if err != nil {
		return errors.New("unable to recompute challenge gamma")
	}

	// pCommExt = pComm + eta * VCommitment
	pCommExt := proof.pComm.Copy()
	pCommExt.Add(rv.VCommitment.Mul(eta))

	// Rebuild gExt = AGenerators || BGenerators || VGenerators (size 2n+4).
	gExt := make([]*mathlib.G1, 2*n+4)
	copy(gExt, rv.AGenerators)
	copy(gExt[n+1:], rv.BGenerators)
	gExt[2*n+2] = rv.VGenerators[0].Mul(eta)
	gExt[2*n+3] = rv.VGenerators[1].Mul(eta)

	// Rebuild lf = L1 + gamma*L2 + gamma^2*L3 — identical to the prover.
	gammaSquare := rv.Curve.ModMul(gamma, gamma, rv.Curve.GroupOrder)
	lf := make([]*mathlib.Zr, 2*n+4)
	for i := range lf {
		lf[i] = math.Zero(rv.Curve)
	}
	for i := uint64(1); i <= n; i++ {
		lf[i] = rv.Curve.ModMul(eta, math.PowerOfTwo(rv.Curve, i-1), rv.Curve.GroupOrder)
	}
	negEta := eta.Copy()
	negEta.Neg()
	lf[2*n+2] = negEta
	for i := uint64(0); i <= n; i++ {
		lf[i] = rv.Curve.ModAddMul2(
			lf[i], math.One(rv.Curve),
			gamma, mu[i],
			rv.Curve.GroupOrder,
		)
	}
	for k := uint64(0); k <= n; k++ {
		lf[n+1+k] = rv.Curve.ModMul(gammaSquare, nu[k], rv.Curve.GroupOrder)
	}

	// lVal = gamma*u + gamma^2*u*(u-1)
	uMinus1 := rv.Curve.ModSub(proof.u, math.One(rv.Curve), rv.Curve.GroupOrder)
	lVal := rv.Curve.ModAddMul2(
		gamma, proof.u,
		gammaSquare, rv.Curve.ModMul(proof.u, uMinus1, rv.Curve.GroupOrder),
		rv.Curve.GroupOrder,
	)

	// Absorb sComm and sEval, squeeze rho.
	tr.Absorb(proof.sComm.Bytes())
	tr.Absorb(proof.sEval.Bytes())
	rho, err := tr.Squeeze()
	if err != nil {
		return errors.New("unable to recompute challenge rho")
	}

	// witComm = pCommExt + rho*sComm
	witComm := pCommExt.Copy()
	witComm.Add(proof.sComm.Mul(rho))

	// witVal = lVal + rho*sEval
	witVal := rv.Curve.ModAddMul2(
		lVal, math.One(rv.Curve),
		rho, proof.sEval,
		rv.Curve.GroupOrder,
	)

	// Pad gExt and lf to the next power of 2 (same logic as prover).
	extSize := uint64(len(gExt))
	cspRounds := uint64(0)
	paddedSize := uint64(1)
	for paddedSize < extSize {
		paddedSize <<= 1
		cspRounds++
	}
	for uint64(len(gExt)) < paddedSize {
		gExt = append(gExt, rv.Curve.GenG1)
		lf = append(lf, math.Zero(rv.Curve))
	}

	cspV := &verifier{
		Commitment:     witComm,
		Generators:     gExt,
		LinearForm:     lf,
		Value:          witVal,
		NumberOfRounds: cspRounds,
		Curve:          rv.Curve,
	}

	return cspV.WithTranscriptHeader(rv.TranscriptHeader).Verify(&proof.cspProof)
}

// toBits returns the n-bit little-endian representation of v as field elements,
// where bits[0] is the LSB and bits[n-1] is the MSB. Returns an error if v >= 2^n.
// Here, one can check if v can be encoded correctly. If not, the verification will fail.
//
//	limit := new(big.Int).Lsh(big.NewInt(1), uint(n))
//	if val.Cmp(limit) >= 0 {
//		return nil, errors.Errorf("value %s does not fit in %d bits", val, n)
//	}
func toBits(v *mathlib.Zr, n uint64, curve *mathlib.Curve) ([]*mathlib.Zr, error) {
	val := new(big.Int).SetBytes(v.Bytes())

	bits := make([]*mathlib.Zr, n)
	for i := range n {
		bits[i] = math.NewCachedZrFromInt(curve, uint64(val.Bit(int(i)))) // #nosec G115
	}

	return bits, nil
}

// fieldDiffInt returns (a - b) mod GroupOrder as a field element.
// Handles negative differences by negating the absolute value.
func fieldDiffInt(a, b int, curve *mathlib.Curve) *mathlib.Zr {
	if a >= b {
		return math.NewCachedZrFromInt(curve, uint64(a-b)) //nolint:gosec
	}

	return math.NewCachedNegZrFromInt(curve, uint64(b-a)) //nolint:gosec
}

// getLagrangeMultipliers returns Lagrange coefficients [x_0, x_1, ..., x_n] such that
// for any degree-n polynomial p(X):
//
//	p(c) = x_0·p(0) + x_1·p(1) + ... + x_n·p(n)
//
// The evaluation points are the integers {0, 1, ..., n}. The i-th coefficient is
// the i-th Lagrange basis polynomial evaluated at c:
//
//	x_i = ∏_{j=0, j≠i}^{n} (c-j) / (i-j)

// Note that this is O(n^2) algorithm but field operations are significantly faster.
// We can revisit it if it ever becomes a bottleneck to have FFT based implementation.
func getLagrangeMultipliers(n uint64, c *mathlib.Zr, curve *mathlib.Curve) ([]*mathlib.Zr, error) {
	// For BN254 and BLS12 curves, we perform arithmetic over gnark crypto native type
	// instead of mathlib wrapper, which uses slower big.Int conversion.
	if r, ok, err := nativeLagrangeMultipliers(n, c, curve); ok {
		return r, err
	}
	m := int(n) + 1 // #nosec G115 // number of evaluation points: 0, 1, ..., n

	// Precompute (c - j) for j = 0..n.
	cMinusJ := make([]*mathlib.Zr, m)
	for j := range m {
		neg := math.NewCachedNegZrFromInt(curve, uint64(j))
		cMinusJ[j] = curve.ModAdd(c, neg, curve.GroupOrder)
	}

	numers := make([]*mathlib.Zr, m)
	denoms := make([]*mathlib.Zr, m)
	for i := range m {
		numer := math.One(curve)
		denom := math.One(curve)
		for j := range m {
			if j == i {
				continue
			}
			numer = curve.ModMul(numer, cMinusJ[j], curve.GroupOrder)
			denom = curve.ModMul(denom, fieldDiffInt(i, j, curve), curve.GroupOrder)
		}
		numers[i] = numer
		denoms[i] = denom
	}

	denomInvs := math.BatchInverse(denoms, curve)
	result := make([]*mathlib.Zr, m)
	for i := range m {
		result[i] = curve.ModMul(numers[i], denomInvs[i], curve.GroupOrder)
	}

	return result, nil
}

// getLagrangeMultipliersPartial returns coefficients [x_0, x_{n+1}, ..., x_{2n}] such that
// for a degree-2n polynomial p(X) satisfying p(1) = p(2) = ... = p(n) = 0:
//
//	p(c) = x_0·p(0) + x_{n+1}·p(n+1) + ... + x_{2n}·p(2n)
//
// The multipliers are the standard Lagrange basis values over the full set of 2n+1
// evaluation points {0, 1, ..., 2n}; only those for {0, n+1, ..., 2n} are returned
// because the remaining evaluations at {1, ..., n} are assumed to be zero.
func getLagrangeMultipliersPartial(n uint64, c *mathlib.Zr, curve *mathlib.Curve) ([]*mathlib.Zr, error) {
	// Use native gnark-crypto FrElement for faster field arithmetic.
	if r, ok, err := nativeLagrangeMultipliersPartial(n, c, curve); ok {
		return r, err
	}
	total := 2*int(n) + 1 // #nosec G115 // all evaluation points: 0, 1, ..., 2n

	// Precompute (c - j) for j = 0..2n.
	cMinusJ := make([]*mathlib.Zr, total)
	for j := range total {
		neg := math.NewCachedNegZrFromInt(curve, uint64(j))
		cMinusJ[j] = curve.ModAdd(c, neg, curve.GroupOrder)
	}

	// Indices of interest: {0, n+1, n+2, ..., 2n} — n+1 values in total.
	relevant := make([]int, int(n)+1) // #nosec G115
	relevant[0] = 0
	for k := 1; k <= int(n); k++ { // #nosec G115
		relevant[k] = int(n) + k // #nosec G115
	}

	numers := make([]*mathlib.Zr, len(relevant))
	denoms := make([]*mathlib.Zr, len(relevant))
	for k, i := range relevant {
		numer := math.One(curve)
		denom := math.One(curve)
		for j := range total {
			if j == i {
				continue
			}
			numer = curve.ModMul(numer, cMinusJ[j], curve.GroupOrder)
			denom = curve.ModMul(denom, fieldDiffInt(i, j, curve), curve.GroupOrder)
		}
		numers[k] = numer
		denoms[k] = denom
	}

	denomInvs := math.BatchInverse(denoms, curve)
	result := make([]*mathlib.Zr, len(relevant))
	for k := range relevant {
		result[k] = curve.ModMul(numers[k], denomInvs[k], curve.GroupOrder)
	}

	return result, nil
}

// interpolate takes the n+1 values of a degree-n polynomial at {0, 1, ..., n}
// and returns the 2n+1 values at {0, 1, ..., 2n} by Lagrange extension.
//
// The n+1 input values are copied directly; the n new values at {n+1, ..., 2n}
// are computed as p(x) = Σ_{i=0}^{n} L_i(x)·p(i).
//
// We achieve O(n^2) efficiency as follows:
// - The n+1 denominator factors d_i = ∏_{j≠i}(i-j) are computed upfront in O(n^2) time.
// - For each x \in [n+1,2n] numerator P(x) = ∏_j(x-j) takes O(n) time
// - For each x \in [n+1,2n] the 1/(x-j) for all j\in [0,n] takes O(n) time, where we use batch inversion for efficiency.
// - L_i(x) = P(x)/(d_i (x-i))
func interpolate(n uint64, valuesOverN []*mathlib.Zr, curve *mathlib.Curve) ([]*mathlib.Zr, error) {
	if r, ok, err := nativeInterpolate(n, valuesOverN, curve); ok {
		return r, err
	}
	m := int(n) + 1 // #nosec G115 // number of known points (indices 0..n)

	// Step 1: precompute denominator d_i = ∏_{j≠i}(i-j) for i=0..n. O(n²).
	denoms := make([]*mathlib.Zr, m)
	for i := range m {
		d := math.One(curve)
		for j := range m {
			if j == i {
				continue
			}
			d = curve.ModMul(d, fieldDiffInt(i, j, curve), curve.GroupOrder)
		}
		denoms[i] = d
	}
	denomInvs := math.BatchInverse(denoms, curve)

	// Result: first n+1 entries are the inputs, followed by n new values.
	result := make([]*mathlib.Zr, 2*int(n)+1) // #nosec G115
	copy(result, valuesOverN)

	// Step 2: for each x in {n+1, ..., 2n} evaluate p(x). O(n) per point.
	for x := int(n) + 1; x <= 2*int(n); x++ { // #nosec G115
		// xMinusJ[j] = x - j, and P(x) = ∏_j xMinusJ[j].
		xMinusJ := make([]*mathlib.Zr, m)
		px := math.One(curve)
		for j := range m {
			xMinusJ[j] = fieldDiffInt(x, j, curve)
			px = curve.ModMul(px, xMinusJ[j], curve.GroupOrder)
		}

		// Batch-invert xMinusJ so L_i(x) = px * xMinusJ[i]^{-1} * denomInvs[i].
		xMinusJInvs := math.BatchInverse(xMinusJ, curve)

		val := math.Zero(curve)
		for i := range m {
			li := curve.ModMul(px, xMinusJInvs[i], curve.GroupOrder)
			li = curve.ModMul(li, denomInvs[i], curve.GroupOrder)

			val = curve.ModAdd(val, curve.ModMul(li, valuesOverN[i], curve.GroupOrder), curve.GroupOrder)
		}
		result[x] = val
	}

	return result, nil
}
