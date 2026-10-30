/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package plonkfri

import (
	"math/big"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/consensys/gnark/backend"
	cs "github.com/consensys/gnark/constraint/bn254"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/scs"
)

// squareCircuit proves knowledge of X such that X² = Y, Y public.
type squareCircuit struct {
	X frontend.Variable
	Y frontend.Variable `gnark:",public"`
}

func (c *squareCircuit) Define(api frontend.API) error {
	api.AssertIsEqual(api.Mul(c.X, c.X), c.Y)

	return nil
}

// TestForgeryZetaInDomain is the red-first test for concern 2 (fix B).
//
// The cheater proves the FALSE statement "I know X with X² = 5" (5 is a
// non-square in bn254 fr, so no such X exists). It computes every polynomial
// from a real witness (X=3, Y=9) but binds Y=5 into Fiat-Shamir, exactly as the
// verifier will. The two statements differ only in the public-input term
// Y·L₀(X), and L₀ vanishes on every row of H except row 0. So whenever zeta
// lands on a row ωʲ (j≠0) the verifier sees a consistent proof and accepts.
// Before fix B, zeta was drawn from the FRI domain, 1/16 of which is H:
// measured 36/800 accepted (≈ 1/16·3/4 predicted for n=4).
//
// After fix B (zeta ∈ F \ (D ∪ H)) the forgery rate must be 0.
func TestForgeryZetaInDomain(t *testing.T) {
	const attempts = 800

	var fake fr.Element
	fake.SetUint64(5)
	if fake.Legendre() != -1 {
		t.Fatal("5 must be a non-square for the statement to be false")
	}

	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), scs.NewBuilder, &squareCircuit{})
	if err != nil {
		t.Fatal(err)
	}
	spr := ccs.(*cs.SparseR1CS)
	pk, vk, err := Setup(spr)
	if err != nil {
		t.Fatal(err)
	}
	w, err := frontend.NewWitness(&squareCircuit{X: 3, Y: 9}, ecc.BN254.ScalarField())
	if err != nil {
		t.Fatal(err)
	}
	fakePublic := fr.Vector{fake}

	accepted, acceptedInH := 0, 0
	reasons := map[string]int{}
	for range attempts {
		// each attempt draws fresh blinding → new commitments → new zeta
		proof, err := prove(spr, pk, w, fakePublic)
		if err != nil {
			reasons["prover: "+err.Error()]++ // zeta ∈ H ∪ D is refused by the prover too

			continue
		}
		if err := Verify(proof, vk, fakePublic); err != nil {
			reasons[err.Error()]++

			continue
		}
		accepted++
		if zetaInH(t, proof, vk, fakePublic) {
			acceptedInH++
		}
	}
	t.Logf("false statement X²=5: %d/%d forged proofs accepted (%.1f%%; was ≈ 1/16·(n−1)/n before fix B); %d of them had zeta ∈ H; rejected by: %v",
		accepted, attempts, 100*float64(accepted)/attempts, acceptedInH, reasons)
	if accepted > 0 {
		t.Errorf("verifier accepted %d proofs of a false statement (concern 2: zeta sampled from the FRI domain)", accepted)
	}
}

// zetaInH recomputes zeta exactly as Verify does and reports whether it lies in
// the circuit domain H (zetaⁿ = 1).
func zetaInH(t *testing.T, proof *Proof, vk *VerifyingKey, public fr.Vector) bool {
	t.Helper()
	cfg, _ := backend.NewVerifierConfig()
	fs := newTranscript(cfg.ChallengeHash, vk, public)
	if _, _, err := fs.afterLRO(proof.LRO); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.afterZ(proof.Z); err != nil {
		t.Fatal(err)
	}
	zeta, _, err := fs.afterH(proof.H)
	if errors.Is(err, ErrZetaInDomain) {
		return true
	}
	if err != nil {
		t.Fatal(err)
	}
	var zn fr.Element
	zn.Exp(zeta, new(big.Int).SetUint64(vk.Size))

	return zn.IsOne()
}

// TestForgeryLieAtZeta: the same false statement, but now the cheater does not
// need zeta to be special. It solves the identity for h1(zeta) -- the one value
// that makes the verifier's equation hold at the random zeta -- and opens that
// lie with the batched DEEP-FRI. The identity check passes by construction,
// so only the opening can catch it: (h1(X) − h1')/(X − zeta) is not a
// polynomial. (Before fix A, this exact lie was accepted with probability 1:
// ClaimedValue was not bound to the Merkle leaf.)
func TestForgeryLieAtZeta(t *testing.T) {
	const attempts = 200

	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), scs.NewBuilder, &squareCircuit{})
	if err != nil {
		t.Fatal(err)
	}
	spr := ccs.(*cs.SparseR1CS)
	pk, vk, err := Setup(spr)
	if err != nil {
		t.Fatal(err)
	}
	w, err := frontend.NewWitness(&squareCircuit{X: 3, Y: 9}, ecc.BN254.ScalarField())
	if err != nil {
		t.Fatal(err)
	}
	var fake fr.Element
	fake.SetUint64(5)
	fakePublic := fr.Vector{fake}

	testHookBeforeOpen = func(proof *Proof, public fr.Vector, beta, gamma, alpha, zeta fr.Element) {
		// h1' = LHS/Z_H(zeta) − zeta^{n+2}·h2 − zeta^{2(n+2)}·h3
		lhs := identityLHS(vk, &proof.Evals, proof.ZShifted, public, beta, gamma, alpha, zeta)
		var zh, zn2, t, one fr.Element
		one.SetOne()
		zh.Exp(zeta, new(big.Int).SetUint64(vk.Size)).Sub(&zh, &one)
		zn2.Exp(zeta, new(big.Int).SetUint64(vk.Size+2))
		h1 := lhs
		h1.Div(&h1, &zh)
		t.Mul(&zn2, &proof.Evals.H2)
		h1.Sub(&h1, &t)
		t.Mul(&zn2, &zn2).Mul(&t, &proof.Evals.H3)
		h1.Sub(&h1, &t)
		proof.Evals.H1 = h1
	}
	defer func() { testHookBeforeOpen = nil }()

	accepted := 0
	reasons := map[string]int{}
	for i := range attempts {
		proof, err := prove(spr, pk, w, fakePublic)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			// sanity: the lie does satisfy the identity, so only the opening can object
			cfg, _ := backend.NewVerifierConfig()
			fs := newTranscript(cfg.ChallengeHash, vk, fakePublic)
			beta, gamma, _ := fs.afterLRO(proof.LRO)
			alpha, _ := fs.afterZ(proof.Z)
			zeta, _, _ := fs.afterH(proof.H)
			lhs := identityLHS(vk, &proof.Evals, proof.ZShifted, fakePublic, beta, gamma, alpha, zeta)
			var rhs, zh, zn2, one fr.Element
			one.SetOne()
			zh.Exp(zeta, new(big.Int).SetUint64(vk.Size)).Sub(&zh, &one)
			zn2.Exp(zeta, new(big.Int).SetUint64(vk.Size+2))
			rhs.Mul(&proof.Evals.H3, &zn2).Add(&rhs, &proof.Evals.H2).Mul(&rhs, &zn2).Add(&rhs, &proof.Evals.H1).Mul(&rhs, &zh)
			if !lhs.Equal(&rhs) {
				t.Fatal("test bug: the forged h1(zeta) does not satisfy the identity")
			}
		}
		if err := Verify(proof, vk, fakePublic); err != nil {
			reasons[err.Error()]++

			continue
		}
		accepted++
	}
	t.Logf("lie h1(zeta) solved from the identity: %d/%d accepted; rejected by: %v", accepted, attempts, reasons)
	if accepted > 0 {
		t.Errorf("verifier accepted %d proofs opening a false h1(zeta)", accepted)
	}
}

type powChain struct {
	X frontend.Variable
	Y frontend.Variable `gnark:",public"`
}

func (c *powChain) Define(api frontend.API) error {
	x := c.X
	for range 60 {
		x = api.Mul(x, x)
	}
	api.AssertIsEqual(x, c.Y)

	return nil
}

// TestNoWitnessValueRevealed: before fix B the FRI domain contained H, where
// the blinding vanishes, so ~1/16 of the revealed codeword values were raw
// secret wire values (measured 5–11 per polynomial per proof). On the coset
// domain no revealed value may equal a secret wire value. (Full zero-knowledge
// -- blinding degree vs. number of revealed values -- is fix C.)
func TestNoWitnessValueRevealed(t *testing.T) {
	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), scs.NewBuilder, &powChain{})
	if err != nil {
		t.Fatal(err)
	}
	spr := ccs.(*cs.SparseR1CS)
	pk, _, err := Setup(spr)
	if err != nil {
		t.Fatal(err)
	}
	var x, y fr.Element
	if _, err := x.SetRandom(); err != nil {
		t.Fatal(err)
	}
	y.Set(&x)
	for range 60 {
		y.Square(&y)
	}
	w, err := frontend.NewWitness(&powChain{X: x, Y: y}, ecc.BN254.ScalarField())
	if err != nil {
		t.Fatal(err)
	}
	sol, err := spr.Solve(w)
	if err != nil {
		t.Fatal(err)
	}
	s := sol.(*cs.SparseR1CSSolution)
	secret := map[fr.Element]bool{}
	for _, v := range [][]fr.Element{s.L, s.R, s.O} {
		for _, e := range v {
			secret[e] = true
		}
	}
	var one fr.Element
	one.SetOne()
	delete(secret, y) // public
	delete(secret, fr.Element{})
	delete(secret, one)

	proof, err := Prove(spr, pk, w)
	if err != nil {
		t.Fatal(err)
	}
	revealed, leaked := 0, 0
	for _, q := range proof.Opening.Queries {
		for _, row := range q.Commitments[comLRO].Rows {
			for j := range 3 {
				var v fr.Element
				v.SetBytes(row[j*fr.Bytes : (j+1)*fr.Bytes])
				revealed++
				if secret[v] {
					leaked++
				}
			}
		}
	}
	t.Logf("%d secret wire values; %d l/r/o codeword values revealed, %d of them raw secret wire values", len(secret), revealed, leaked)
	if leaked != 0 {
		t.Fatalf("%d raw secret wire values revealed", leaked)
	}
}
