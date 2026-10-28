/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package plonkfri

import (
	"math/big"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/consensys/gnark/backend"
	cs "github.com/consensys/gnark/constraint/bn254"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/scs"
)

// TestRevealedValuesAreUniform checks the zero-knowledge condition of fix C
// (PROJECT.md, "Fix C design"; resources/2024-1037 Lemma 1 and Lemma 4).
//
// A secret polynomial is committed as ŵ = w + B(X)·r(X), r with b uniformly
// random coefficients (B = Z_H for l, r, o, z; B = Xᵏ for the quotient pieces
// h1, h2). The verifier sees ŵ at a set of distinct points x₁..x_m. Those m
// values are uniformly random -- whatever the witness w -- iff the linear map
// r ↦ (B(xᵢ)·r(xᵢ))ᵢ is onto, i.e. the m×b matrix [B(xᵢ)·xᵢʲ] has rank m.
//
// This is a structural test on purpose: in a 254-bit field every revealed
// value *looks* random, leaking or not, so sampling proofs proves nothing.
//
// The test reads the revealed points from a real proof (ζ, ω·ζ for z, and the
// pair {x, −x} of every FRI query) and also checks that the prover's
// polynomials really carry n + b coefficients.
func TestRevealedValuesAreUniform(t *testing.T) {
	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), scs.NewBuilder, &powChain{})
	if err != nil {
		t.Fatal(err)
	}
	spr := ccs.(*cs.SparseR1CS)
	pk, vk, err := Setup(spr)
	if err != nil {
		t.Fatal(err)
	}
	var x, y fr.Element
	x.SetUint64(3)
	y.Set(&x)
	for range 60 {
		y.Square(&y)
	}
	w, err := frontend.NewWitness(&powChain{X: x, Y: y}, ecc.BN254.ScalarField())
	if err != nil {
		t.Fatal(err)
	}
	public := fr.Vector{y}
	proof, err := Prove(spr, pk, w)
	if err != nil {
		t.Fatal(err)
	}

	// the prover must really add b random coefficients: n + b coefficients
	n := int(vk.Size) //nolint:gosec // a small test domain
	vals := make([]fr.Element, n)
	for i := range vals {
		vals[i].SetUint64(uint64(i + 2))
	}
	l, _, _, err := computeBlindedLROCanonical(vals, vals, vals, &pk.Domain[0])
	if err != nil {
		t.Fatal(err)
	}
	z, err := computeBlindedZCanonical(vals, vals, vals, pk, vals[0], vals[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(l) != n+nbBlindLRO || len(z) != n+nbBlindZ {
		t.Fatalf("blinded l has %d coefficients (want n+%d = %d), z has %d (want %d)",
			len(l), nbBlindLRO, n+nbBlindLRO, len(z), n+nbBlindZ)
	}

	// revealed points
	cfg, _ := backend.NewVerifierConfig()
	fs := newTranscript(cfg.ChallengeHash, vk, public)
	if _, _, err := fs.afterLRO(proof.LRO); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.afterZ(proof.Z); err != nil {
		t.Fatal(err)
	}
	zeta, seed, err := fs.afterH(proof.H)
	if err != nil {
		t.Fatal(err)
	}
	queried, err := vk.Fri.QueriedPoints(seed, proof.commitments(vk), proof.claims(vk, zeta), proof.Opening)
	if err != nil {
		t.Fatal(err)
	}
	var zetaShifted fr.Element
	zetaShifted.Mul(&zeta, &vk.Generator)
	atZeta := distinct(append([]fr.Element{zeta}, queried...))
	atZetaAndShift := distinct(append([]fr.Element{zeta, zetaShifted}, queried...))

	var one fr.Element
	one.SetOne()
	zH := func(x fr.Element) fr.Element { // Z_H(x) = xⁿ − 1
		var r fr.Element
		r.Exp(x, big.NewInt(int64(n))).Sub(&r, &one)

		return r
	}
	xk := func(x fr.Element) fr.Element { // Xᵏ, the quotient-piece randomizer factor
		var r fr.Element
		r.Exp(x, new(big.Int).SetUint64(vk.pieceSize()))

		return r
	}

	cases := []struct {
		name   string
		points []fr.Element
		factor func(fr.Element) fr.Element
		b      int
	}{
		{"l, r, o", atZeta, zH, nbBlindLRO},
		{"z", atZetaAndShift, zH, nbBlindZ},
		{"h1, h2 (pieces)", atZeta, xk, nbBlindQuotient},
	}
	for _, c := range cases {
		rows := make([][]fr.Element, len(c.points))
		for i, p := range c.points {
			rows[i] = make([]fr.Element, c.b)
			f := c.factor(p)
			for j := range c.b {
				rows[i][j] = f
				f.Mul(&f, &p)
			}
		}
		r := rank(rows, c.b)
		t.Logf("%-16s revealed at %3d distinct points, %3d random coefficients, rank %3d", c.name, len(c.points), c.b, r)
		if r < len(c.points) {
			t.Errorf("%s: revealed values are not uniform (rank %d < %d revealed points): the proof leaks about the witness",
				c.name, r, len(c.points))
		}
	}
}

func distinct(points []fr.Element) []fr.Element {
	seen := map[fr.Element]bool{}
	var res []fr.Element
	for _, p := range points {
		if !seen[p] {
			seen[p] = true
			res = append(res, p)
		}
	}

	return res
}

// rank returns the rank of a matrix with nbCols columns (Gaussian elimination).
func rank(rows [][]fr.Element, nbCols int) int {
	m := make([][]fr.Element, len(rows))
	for i := range rows {
		m[i] = append([]fr.Element{}, rows[i]...)
	}
	r := 0
	for c := 0; c < nbCols && r < len(m); c++ {
		pivot := -1
		for i := r; i < len(m); i++ {
			if !m[i][c].IsZero() {
				pivot = i

				break
			}
		}
		if pivot < 0 {
			continue
		}
		m[r], m[pivot] = m[pivot], m[r]
		var inv fr.Element
		inv.Inverse(&m[r][c])
		for i := r + 1; i < len(m); i++ {
			if m[i][c].IsZero() {
				continue
			}
			var f, t fr.Element
			f.Mul(&m[i][c], &inv)
			for j := c; j < nbCols; j++ {
				t.Mul(&f, &m[r][j])
				m[i][j].Sub(&m[i][j], &t)
			}
		}
		r++
	}

	return r
}

// TestRank sanity-checks the rank helper on Vandermonde matrices.
func TestRank(t *testing.T) {
	points := make([]fr.Element, 6)
	for i := range points {
		points[i].SetUint64(uint64(i + 1))
	}
	vandermonde := func(nbCols int) [][]fr.Element {
		rows := make([][]fr.Element, len(points))
		for i, p := range points {
			rows[i] = make([]fr.Element, nbCols)
			var f fr.Element
			f.SetOne()
			for j := range rows[i] {
				rows[i][j] = f
				f.Mul(&f, &p)
			}
		}

		return rows
	}
	for _, c := range []struct{ cols, want int }{{0, 0}, {2, 2}, {6, 6}, {9, 6}} {
		if got := rank(vandermonde(c.cols), c.cols); got != c.want {
			t.Fatalf("6 points × %d columns: rank %d, want %d", c.cols, got, c.want)
		}
	}
	rows := vandermonde(4)
	rows[3] = append([]fr.Element{}, rows[1]...) // duplicate row
	if got := rank(rows, 4); got != 4 {
		t.Fatalf("with a duplicate row: rank %d, want 4", got)
	}
	if got := rank(rows[:4], 4); got != 3 {
		t.Fatalf("4 rows, one duplicated: rank %d, want 3", got)
	}
}
