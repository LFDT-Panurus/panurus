/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package nativefri

import (
	"bytes"
	"crypto/sha256"
	"math/big"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
)

func randPoly(t *testing.T, n int) []fr.Element {
	t.Helper()
	p := make([]fr.Element, n)
	for i := range p {
		if _, err := p[i].SetRandom(); err != nil {
			t.Fatal(err)
		}
	}

	return p
}

func evalPoly(p []fr.Element, z fr.Element) fr.Element {
	var r fr.Element
	for i := len(p) - 1; i >= 0; i-- {
		r.Mul(&r, &z).Add(&r, &p[i])
	}

	return r
}

func randPoint(t *testing.T) fr.Element {
	t.Helper()
	var z fr.Element
	if _, err := z.SetRandom(); err != nil {
		t.Fatal(err)
	}

	return z
}

// batchFixture mimics the PLONK layout: 3 commitments of different widths,
// polynomials of different degrees, claims at z (all polys) and at ω·z (one).
type batchFixture struct {
	s           *Scheme
	polys       [][][]fr.Element
	committed   []*Committed
	commitments []Commitment
	claims      []Claim
	seed        []byte
}

func newBatchFixture(t *testing.T, degreeBound uint64) *batchFixture {
	t.Helper()
	s, err := NewScheme(degreeBound, sha256.New)
	if err != nil {
		t.Fatal(err)
	}
	k := int(degreeBound) //nolint:gosec // a small test degree bound
	f := &batchFixture{s: s, seed: []byte("caller transcript state")}
	f.polys = [][][]fr.Element{
		{randPoly(t, k), randPoly(t, k/2+1), randPoly(t, min(3, k))},
		{randPoly(t, k-1)},
		{randPoly(t, k/2), randPoly(t, k)},
	}
	for _, ps := range f.polys {
		c, err := s.Commit(ps...)
		if err != nil {
			t.Fatal(err)
		}
		f.committed = append(f.committed, c)
		f.commitments = append(f.commitments, c.Commitment)
	}
	z := randPoint(t)
	all := Claim{Point: z}
	for ci, ps := range f.polys {
		for pi, p := range ps {
			all.Polys = append(all.Polys, PolyRef{ci, pi})
			all.Values = append(all.Values, evalPoly(p, z))
		}
	}
	var wz fr.Element
	wz.Mul(&z, &s.domain.Generator)
	shifted := Claim{Point: wz, Polys: []PolyRef{{1, 0}}, Values: []fr.Element{evalPoly(f.polys[1][0], wz)}}
	f.claims = []Claim{all, shifted}

	return f
}

func (f *batchFixture) prove(t *testing.T) *BatchProof {
	t.Helper()
	proof, err := f.s.Open(f.seed, f.committed, f.claims)
	if err != nil {
		t.Fatal(err)
	}

	return proof
}

// TestCommitEvaluatesOnCoset checks the committed values are p(x) at the points
// of a·D, and that a·D shares no point with the subgroup D -- hence none with
// the PLONK domain H ⊂ D (the ZK leak of the old subgroup domain).
func TestCommitEvaluatesOnCoset(t *testing.T) {
	s, err := NewScheme(16, sha256.New)
	if err != nil {
		t.Fatal(err)
	}
	p := randPoly(t, 16)
	c, err := s.Commit(p)
	if err != nil {
		t.Fatal(err)
	}
	n := new(big.Int).SetUint64(s.domain.Cardinality)
	for i, x := range s.points {
		want := evalPoly(p, x)
		if !c.evals[0][i].Equal(&want) {
			t.Fatalf("sorted position %d: committed value is not p(x)", i)
		}
		var xn fr.Element
		xn.Exp(x, n)
		if xn.IsOne() {
			t.Fatalf("sorted position %d: point lies in the subgroup D", i)
		}
		if !s.InDomain(x) {
			t.Fatalf("sorted position %d: InDomain is false for a domain point", i)
		}
	}
	// sibling pairs must be {x, −x}, the fibers of x ↦ x²
	for i := 0; i < len(s.points); i += 2 {
		var sum fr.Element
		sum.Add(&s.points[i], &s.points[i+1])
		if !sum.IsZero() {
			t.Fatalf("positions %d,%d are not a fiber {x, −x}", i, i+1)
		}
	}
}

func TestBatchCompleteness(t *testing.T) {
	for _, k := range []uint64{2, 4, 64} {
		for i := range 5 {
			f := newBatchFixture(t, k)
			if err := f.s.Verify(f.seed, f.commitments, f.claims, f.prove(t)); err != nil {
				t.Fatalf("degreeBound=%d run %d: honest proof rejected: %v", k, i, err)
			}
		}
	}
}

// TestBatchRejectsWrongValue: a false claim f(z) = v' makes (f − v')/(X − z) a
// rational function, far from every low-degree polynomial; FRI must reject.
// This is exactly what the PLONK attack in attack_test.go needs to get past.
func TestBatchRejectsWrongValue(t *testing.T) {
	const runs = 100
	accepted := 0
	reasons := map[string]int{}
	for i := range runs {
		f := newBatchFixture(t, 32)
		var one fr.Element
		one.SetOne()
		f.claims[0].Values[i%len(f.claims[0].Values)].Add(&f.claims[0].Values[i%len(f.claims[0].Values)], &one)
		proof, err := f.s.Open(f.seed, f.committed, f.claims) // cheater runs the honest prover on the lie
		if err != nil {
			t.Fatal(err)
		}
		if err := f.s.Verify(f.seed, f.commitments, f.claims, proof); err == nil {
			accepted++
		} else {
			reasons[err.Error()]++
		}
	}
	t.Logf("false evaluation claims accepted: %d/%d; rejected by: %v", accepted, runs, reasons)
	if accepted != 0 {
		t.Fatalf("%d/%d false claims accepted", accepted, runs)
	}
}

// TestBatchRejectsHighDegree: committing to a polynomial above the degree bound
// (true values claimed) must be rejected by FRI.
func TestBatchRejectsHighDegree(t *testing.T) {
	const runs = 100
	accepted := 0
	reasons := map[string]int{}
	for range runs {
		f := newBatchFixture(t, 32)
		high := randPoly(t, 64) // 2× the degree bound
		c, err := f.s.commit([][]fr.Element{high})
		if err != nil {
			t.Fatal(err)
		}
		f.committed[1] = c
		f.commitments[1] = c.Commitment
		f.claims[0].Values[3] = evalPoly(high, f.claims[0].Point) // ref {1,0}
		f.claims[1].Values[0] = evalPoly(high, f.claims[1].Point)
		proof, err := f.s.Open(f.seed, f.committed, f.claims)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.s.Verify(f.seed, f.commitments, f.claims, proof); err == nil {
			accepted++
		} else {
			reasons[err.Error()]++
		}
	}
	t.Logf("too-high-degree commitments accepted: %d/%d; rejected by: %v", accepted, runs, reasons)
	if accepted != 0 {
		t.Fatalf("%d/%d high-degree commitments accepted", accepted, runs)
	}
}

func TestBatchRejectsPointInDomain(t *testing.T) {
	f := newBatchFixture(t, 16)
	f.claims[1].Point = f.s.points[5]
	f.claims[1].Values[0] = evalPoly(f.polys[1][0], f.claims[1].Point)
	if _, err := f.s.Open(f.seed, f.committed, f.claims); !errors.Is(err, ErrPointInDomain) {
		t.Fatalf("Open: got %v, want ErrPointInDomain", err)
	}
	g := newBatchFixture(t, 16)
	proof := g.prove(t)
	g.claims[1].Point = g.s.points[5]
	if err := g.s.Verify(g.seed, g.commitments, g.claims, proof); !errors.Is(err, ErrPointInDomain) {
		t.Fatalf("Verify: got %v, want ErrPointInDomain", err)
	}
}

// TestBatchRejectsTampering changes one thing at a time in an honest proof or
// statement; every change must be rejected.
func TestBatchRejectsTampering(t *testing.T) {
	f := newBatchFixture(t, 16)
	honest := f.prove(t)
	if err := f.s.Verify(f.seed, f.commitments, f.claims, honest); err != nil {
		t.Fatal(err)
	}
	clone := func() *BatchProof {
		p := *honest
		p.Roots = append([][]byte{}, honest.Roots...)
		p.Queries = make([]BatchQuery, len(honest.Queries))
		for i, q := range honest.Queries {
			p.Queries[i].Mask = q.Mask
			p.Queries[i].Commitments = append([]PairOpening{}, q.Commitments...)
			p.Queries[i].Layers = append([]PairOpening{}, q.Layers...)
		}

		return &p
	}
	flip := func(b []byte) []byte {
		c := append([]byte{}, b...)
		c[len(c)-1] ^= 1

		return c
	}
	var one fr.Element
	one.SetOne()

	cases := []struct {
		name   string
		mutate func(p *BatchProof, f *batchFixture)
	}{
		{"commitment row", func(p *BatchProof, _ *batchFixture) {
			p.Queries[3].Commitments[2].Rows[1] = flip(p.Queries[3].Commitments[2].Rows[1])
		}},
		{"commitment path", func(p *BatchProof, _ *batchFixture) {
			p.Queries[0].Commitments[0].Path = append([][]byte{flip(p.Queries[0].Commitments[0].Path[0])}, p.Queries[0].Commitments[0].Path[1:]...)
		}},
		{"layer row", func(p *BatchProof, _ *batchFixture) {
			p.Queries[7].Layers[0].Rows[0] = flip(p.Queries[7].Layers[0].Rows[0])
		}},
		{"layer root", func(p *BatchProof, _ *batchFixture) { p.Roots[1] = flip(p.Roots[1]) }},
		{"mask root", func(p *BatchProof, _ *batchFixture) { p.Mask = flip(p.Mask) }},
		{"mask row", func(p *BatchProof, _ *batchFixture) { p.Queries[5].Mask.Rows[0] = flip(p.Queries[5].Mask.Rows[0]) }},
		{"missing mask", func(p *BatchProof, _ *batchFixture) { p.Mask = nil }},
		{"final evaluation", func(p *BatchProof, _ *batchFixture) { p.Evaluation.Add(&p.Evaluation, &one) }},
		{"missing query", func(p *BatchProof, _ *batchFixture) { p.Queries = p.Queries[1:] }},
		{"missing commitment opening", func(p *BatchProof, _ *batchFixture) {
			p.Queries[0].Commitments = p.Queries[0].Commitments[:2]
		}},
		{"seed (caller transcript)", func(_ *BatchProof, f *batchFixture) { f.seed = []byte("another statement") }},
		{"commitment root", func(_ *BatchProof, f *batchFixture) { f.commitments[0].Root = flip(f.commitments[0].Root) }},
		{"claimed value", func(_ *BatchProof, f *batchFixture) { f.claims[1].Values[0].Add(&f.claims[1].Values[0], &one) }},
		{"claim point", func(_ *BatchProof, f *batchFixture) { f.claims[0].Point.Add(&f.claims[0].Point, &one) }},
		{"bad poly ref", func(_ *BatchProof, f *batchFixture) { f.claims[1].Polys[0] = PolyRef{1, 1} }},
	}
	for _, tc := range cases {
		g := *f
		g.seed = f.seed
		g.commitments = append([]Commitment{}, f.commitments...)
		g.claims = make([]Claim, len(f.claims))
		for i, cl := range f.claims {
			g.claims[i] = Claim{Point: cl.Point, Polys: append([]PolyRef{}, cl.Polys...), Values: append([]fr.Element{}, cl.Values...)}
		}
		p := clone()
		tc.mutate(p, &g)
		if err := g.s.Verify(g.seed, g.commitments, g.claims, p); err == nil {
			t.Errorf("%s: tampered proof accepted", tc.name)
		} else {
			t.Logf("%-28s rejected: %v", tc.name, err)
		}
	}
	// the honest proof must still verify (mutations must not have aliased it)
	if err := f.s.Verify(f.seed, f.commitments, f.claims, honest); err != nil {
		t.Fatalf("honest proof altered by the test: %v", err)
	}
}

// TestPairProofMatchesTree checks pair openings against the tree for every pair.
func TestPairProofMatchesTree(t *testing.T) {
	h := sha256.New()
	leaves := make([][]byte, 16)
	for i := range leaves {
		leaves[i] = []byte{byte(i), 0xAB}
	}
	tree := newMerkleTree(h, leaves)
	for p := range 8 {
		if !verifyPair(h, tree.root(), tree.pairProof(p), p, 16) {
			t.Fatalf("pair %d: valid opening rejected", p)
		}
		if verifyPair(h, tree.root(), tree.pairProof(p), (p+1)%8, 16) {
			t.Fatalf("pair %d: opening accepted at the wrong index", p)
		}
	}
}

// TestMaskRandomizesOpening: with the mask R (fix C), opening the same
// statement twice must give unrelated FRI transcripts -- fresh randomness
// reaches every folded layer and the final value. Without R the opening is a
// deterministic function of the committed words, so its folded layers carry
// information about them (resources/2024-1037 §2).
func TestMaskRandomizesOpening(t *testing.T) {
	f := newBatchFixture(t, 32)
	a, b := f.prove(t), f.prove(t)
	if len(a.Mask) == 0 || len(b.Mask) == 0 {
		t.Fatal("proof has no mask commitment")
	}
	if bytes.Equal(a.Mask, b.Mask) {
		t.Fatal("same mask in two openings")
	}
	for i := range a.Roots {
		if bytes.Equal(a.Roots[i], b.Roots[i]) {
			t.Fatalf("FRI layer %d identical in two openings of the same statement", i+1)
		}
	}
	if a.Evaluation.Equal(&b.Evaluation) {
		t.Fatal("final FRI value identical in two openings of the same statement")
	}
	for _, p := range []*BatchProof{a, b} {
		if err := f.s.Verify(f.seed, f.commitments, f.claims, p); err != nil {
			t.Fatalf("masked proof rejected: %v", err)
		}
	}
}

// TestMaskCannotCancelFalseClaim demonstrates design decision C4. FRI only sees
// the sum q = R + Σ coefₜ·Qₜ, and R is an arbitrary committed word (nobody
// checks it alone). A cheater with a false claim v' ≠ f(z) has a quotient
// Q₀ = Q₀_true + E with E(x) = (f(z) − v')/(x − z), not a polynomial. If R had
// the same coefficient as Q₀, committing "R" = P − E (P any polynomial) would
// cancel E and FRI would accept. Giving the claims λ¹, λ², … (R keeps λ⁰ = 1)
// leaves (λ − 1)·E in q, which FRI rejects.
func TestMaskCannotCancelFalseClaim(t *testing.T) {
	const runs = 50
	accepted := map[int]int{}
	for _, power := range []int{0, 1} {
		for range runs {
			f := newBatchFixture(t, 32)
			f.s.firstClaimPower = power
			var one fr.Element
			one.SetOne()
			// false claim: v' = f(z) + 1, so E(x) = −1/(x − z)
			f.claims[0].Values[0].Add(&f.claims[0].Values[0], &one)
			z := f.claims[0].Point
			p := randPoly(t, 16)
			mask := make([]fr.Element, len(f.s.points))
			for k, x := range f.s.points {
				var d fr.Element
				d.Sub(&x, &z).Inverse(&d) // −E(x) = 1/(x − z)
				mask[k] = evalPoly(p, x)
				mask[k].Add(&mask[k], &d)
			}
			proof, err := f.s.open(f.seed, f.committed, f.claims, f.s.commitEvals([][]fr.Element{mask}))
			if err != nil {
				t.Fatal(err)
			}
			if f.s.Verify(f.seed, f.commitments, f.claims, proof) == nil {
				accepted[power]++
			}
		}
	}
	t.Logf("false claim cancelled by a crafted mask: accepted %d/%d if R shares λ⁰ with the first claim (unsafe), %d/%d with claims at λ¹… (our design)",
		accepted[0], runs, accepted[1], runs)
	if accepted[0] == 0 {
		t.Fatal("test bug: the attack should succeed when the coefficient is shared")
	}
	if accepted[1] != 0 {
		t.Fatalf("crafted mask accepted %d/%d times with claims at λ¹…", accepted[1], runs)
	}
}
