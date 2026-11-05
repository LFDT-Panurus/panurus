/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package nativefri

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash"
	"math/big"
	"math/bits"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/consensys/gnark-crypto/ecc/bn254/fr/fft"
	fiatshamir "github.com/consensys/gnark-crypto/fiat-shamir"
	"github.com/consensys/gnark-crypto/utils"
)

// This file implements FRI as a batched polynomial commitment scheme with
// openings at arbitrary field points (the DEEP method), as designed for fix B
// (PROJECT.md, "Fix B design (B1)"):
//
//   - Commit: several polynomials are evaluated on the coset a·D (a = the
//     multiplicative generator of Fr, |D| = rho·degreeBound) and committed in a
//     single Merkle tree whose leaf i holds all their values at point i.
//     a·D is disjoint from every power-of-two subgroup (in particular from the
//     PLONK domain H), so revealed codeword values never sit on H.
//   - Open: claims "fₜ(zₜ) = vₜ" with zₜ ∉ a·D are proven by one FRI on
//     q(X) = Σₜ λᵗ·(fₜ(X) − vₜ)/(X − zₜ). q is a polynomial of degree
//     < degreeBound iff every claim is true. The verifier never receives q: at
//     a query point x it computes q(x) from the opened fₜ(x) ("virtual" layer
//     0). Running FRI over the coset is the same as running the plain FRI
//     folding over D on q(a·X) (Haböck, resources/2022-1216 §3.7), so the
//     folding code of the subgroup version is reused unchanged.
//
// Sources: resources/2022-1216 §3.3 (batching), §4.1 (DEEP PCS), Protocol 3 and
// Thm 8 (DEEP-ALI); resources/2021-582 §3.5 (one Merkle leaf per row).

var (
	ErrPointInDomain = errors.New("opening point lies in the evaluation domain")
	ErrDegree        = errors.New("polynomial exceeds the degree bound")
	ErrClaim         = errors.New("malformed opening claim")
)

// Scheme is a DEEP-FRI batched polynomial commitment scheme for polynomials
// with fewer than DegreeBound coefficients.
type Scheme struct {
	newHash     func() hash.Hash
	degreeBound uint64
	nbSteps     int

	// domain D, |D| = rho·degreeBound. Codewords live on the coset
	// a·D, a = domain.FrMultiplicativeGen.
	domain *fft.Domain

	// points[i] is the point of a·D at sorted position i (see sort).
	points []fr.Element

	// firstClaimPower is the power of λ of the first claim in the batch:
	// q = R + Σₜ λ^(firstClaimPower+t)·(fₜ − vₜ)/(X − zₜ). It must be ≥ 1 so the
	// mask R (coefficient λ⁰ = 1) shares no coefficient with a claim; tests set
	// it to 0 to demonstrate the attack this prevents (PROJECT.md, fix C, C4).
	firstClaimPower int
}

// NewScheme returns a scheme for polynomials with < degreeBound coefficients;
// degreeBound must be a power of two ≥ 2.
func NewScheme(degreeBound uint64, newHash func() hash.Hash) (*Scheme, error) {
	if degreeBound < 2 || degreeBound&(degreeBound-1) != 0 {
		return nil, errors.Errorf("degree bound %d is not a power of two ≥ 2", degreeBound)
	}
	s := &Scheme{
		newHash:     newHash,
		degreeBound: degreeBound,
		nbSteps:     bits.TrailingZeros64(degreeBound),
		domain:      fft.NewDomain(rho * degreeBound),

		firstClaimPower: 1,
	}
	n := int(s.domain.Cardinality) //nolint:gosec // rho·degreeBound, a valid FFT domain size
	natural := make([]fr.Element, n)
	natural[0].Set(&s.domain.FrMultiplicativeGen)
	for i := 1; i < n; i++ {
		natural[i].Mul(&natural[i-1], &s.domain.Generator)
	}
	s.points = sort(natural)

	return s, nil
}

// DegreeBound returns the number of coefficients a committed polynomial may have.
func (s *Scheme) DegreeBound() uint64 { return s.degreeBound }

// InDomain reports whether z belongs to the evaluation coset a·D, i.e. whether
// (z/a)^|D| = 1. Opening points must not.
func (s *Scheme) InDomain(z fr.Element) bool {
	var t fr.Element
	t.Mul(&z, &s.domain.FrMultiplicativeGenInv)
	t.Exp(t, new(big.Int).SetUint64(s.domain.Cardinality))

	return t.IsOne()
}

// Commitment is what the verifier knows about a committed batch of polynomials.
type Commitment struct {
	Root    []byte
	NbPolys int
}

// Committed is the prover-side state of a commitment.
type Committed struct {
	Commitment
	evals [][]fr.Element // evals[j][i] = polynomial j at s.points[i]
	tree  merkleTree
}

// Commit commits to polys (canonical coefficients, each with at most
// DegreeBound coefficients) in a single Merkle tree.
func (s *Scheme) Commit(polys ...[]fr.Element) (*Committed, error) {
	for _, p := range polys {
		if uint64(len(p)) > s.degreeBound {
			return nil, ErrDegree
		}
	}

	return s.commit(polys)
}

// commit is Commit without the degree check; tests use it to play a prover
// that commits to a too-high-degree polynomial.
func (s *Scheme) commit(polys [][]fr.Element) (*Committed, error) {
	if len(polys) == 0 {
		return nil, ErrClaim
	}
	n := int(s.domain.Cardinality) //nolint:gosec // rho·degreeBound, a valid FFT domain size
	c := &Committed{evals: make([][]fr.Element, len(polys))}
	for j, p := range polys {
		if len(p) > n {
			return nil, ErrDegree
		}
		e := make([]fr.Element, n)
		copy(e, p)
		s.domain.FFT(e, fft.DIF, fft.OnCoset())
		utils.BitReverse(e)
		c.evals[j] = sort(e)
	}

	return s.commitEvals(c.evals), nil
}

// commitEvals commits to words given by their values at s.points (sorted
// order) -- not necessarily evaluations of low-degree polynomials; tests use
// it to play a cheating prover.
func (s *Scheme) commitEvals(evals [][]fr.Element) *Committed {
	n := len(s.points)
	c := &Committed{evals: evals}
	polys := evals
	leaves := make([][]byte, n)
	for i := range leaves {
		leaves[i] = make([]byte, 0, len(polys)*fr.Bytes)
		for j := range polys {
			leaves[i] = append(leaves[i], c.evals[j][i].Marshal()...)
		}
	}
	c.tree = newMerkleTree(s.newHash(), leaves)
	c.Root = c.tree.root()
	c.NbPolys = len(polys)

	return c
}

// PolyRef designates polynomial Poly of commitment Commitment.
type PolyRef struct{ Commitment, Poly int }

// Claim states that the polynomials Polys evaluate to Values at Point.
type Claim struct {
	Point  fr.Element
	Polys  []PolyRef
	Values []fr.Element
}

// PairOpening opens the two leaves of a Merkle tree at sorted positions 2p and
// 2p+1 -- the fiber {x, −x} of x ↦ x² -- with one authentication path.
type PairOpening struct {
	Rows [2][]byte // raw leaf data
	Path [][]byte  // sibling hashes from the level above the leaves up to the root (excluded)
}

// BatchQuery holds the answers to one query chain.
type BatchQuery struct {
	Mask        PairOpening   // layer 0: opening of the mask R
	Commitments []PairOpening // layer 0: one opening per commitment
	Layers      []PairOpening // committed FRI layers 1..nbSteps-1
}

// BatchProof proves a batch of claims.
type BatchProof struct {
	Mask       []byte   // Merkle root of the mask polynomial R (zero-knowledge)
	Roots      [][]byte // Merkle roots of the FRI layers 1..nbSteps-1 (layer 0 is virtual)
	Evaluation fr.Element
	Queries    []BatchQuery
}

// transcript names: "lambda", the folding challenges "x0".."x{nbSteps-1}", and
// the query seed "s0".
func (s *Scheme) transcript() (*fiatshamir.Transcript, []string) {
	names := make([]string, 0, s.nbSteps+2)
	names = append(names, "lambda")
	for i := range s.nbSteps {
		names = append(names, fmt.Sprintf("x%d", i))
	}
	names = append(names, "s0")

	return fiatshamir.NewTranscript(s.newHash(), names...), names
}

// bindStatement checks the claims and binds everything the proof is about --
// the caller's seed, every commitment and every claim -- before deriving λ, so
// none of it can be chosen after seeing a challenge (strong Fiat-Shamir,
// resources/2023-691 Def. 3).
func (s *Scheme) bindStatement(fs *fiatshamir.Transcript, name string, seed, mask []byte, commitments []Commitment, claims []Claim) (fr.Element, error) {
	var lambda fr.Element
	if len(commitments) == 0 || len(claims) == 0 {
		return lambda, ErrClaim
	}
	var buf [8]byte
	if err := fs.Bind(name, seed); err != nil {
		return lambda, err
	}
	// the mask is committed before λ: it cannot be chosen to cancel a claim
	if err := fs.Bind(name, mask); err != nil {
		return lambda, err
	}
	for _, c := range commitments {
		binary.BigEndian.PutUint64(buf[:], uint64(c.NbPolys)) //nolint:gosec // a polynomial count, never negative
		if err := fs.Bind(name, c.Root); err != nil {
			return lambda, err
		}
		if err := fs.Bind(name, buf[:]); err != nil {
			return lambda, err
		}
	}
	for _, cl := range claims {
		if s.InDomain(cl.Point) {
			return lambda, ErrPointInDomain
		}
		if len(cl.Polys) == 0 || len(cl.Polys) != len(cl.Values) {
			return lambda, ErrClaim
		}
		if err := fs.Bind(name, cl.Point.Marshal()); err != nil {
			return lambda, err
		}
		for t, ref := range cl.Polys {
			if ref.Commitment < 0 || ref.Commitment >= len(commitments) || ref.Poly < 0 || ref.Poly >= commitments[ref.Commitment].NbPolys {
				return lambda, ErrClaim
			}
			binary.BigEndian.PutUint64(buf[:], uint64(ref.Commitment)<<32|uint64(ref.Poly))
			if err := fs.Bind(name, buf[:]); err != nil {
				return lambda, err
			}
			if err := fs.Bind(name, cl.Values[t].Marshal()); err != nil {
				return lambda, err
			}
		}
	}
	b, err := fs.ComputeChallenge(name)
	if err != nil {
		return lambda, err
	}
	lambda.SetBytes(b)

	return lambda, nil
}

func challenge(fs *fiatshamir.Transcript, name string) (fr.Element, error) {
	var x fr.Element
	b, err := fs.ComputeChallenge(name)
	if err != nil {
		return x, err
	}
	x.SetBytes(b)

	return x, nil
}

// Open proves the claims about the committed polynomials. seed must bind
// everything the caller's protocol did before (e.g. its last challenge).
//
// For zero-knowledge, every opening includes a fresh random mask polynomial R
// with DegreeBound − 1 coefficients, committed before λ and added to the
// batch: then every FRI layer value is uniformly random, whatever the
// committed polynomials are (resources/2024-1037 Protocol 2, Lemma 2).
func (s *Scheme) Open(seed []byte, committed []*Committed, claims []Claim) (*BatchProof, error) {
	r := make([]fr.Element, s.degreeBound-1)
	for i := range r {
		if _, err := r[i].SetRandom(); err != nil {
			return nil, err
		}
	}
	mask, err := s.commit([][]fr.Element{r})
	if err != nil {
		return nil, err
	}

	return s.open(seed, committed, claims, mask)
}

// open is Open with a given mask word (tests use it to play a cheater who
// crafts the mask).
func (s *Scheme) open(seed []byte, committed []*Committed, claims []Claim, mask *Committed) (*BatchProof, error) {
	commitments := make([]Commitment, len(committed))
	for i, c := range committed {
		commitments[i] = c.Commitment
	}
	if mask == nil || mask.NbPolys != 1 {
		return nil, ErrClaim
	}
	proof := &BatchProof{Mask: mask.Root, Roots: make([][]byte, 0, s.nbSteps-1)}
	fs, names := s.transcript()
	lambda, err := s.bindStatement(fs, names[0], seed, proof.Mask, commitments, claims)
	if err != nil {
		return nil, err
	}

	// layer 0 (virtual): q = R + Σₜ λ^(firstClaimPower+t)·(fₜ − vₜ)/(X − zₜ) on
	// a·D, in sorted order.
	n := len(s.points)
	q := make([]fr.Element, n)
	copy(q, mask.evals[0])
	den := make([]fr.Element, n)
	var t fr.Element
	coef := s.claimCoefficient(lambda)
	for _, cl := range claims {
		for i := range den {
			den[i].Sub(&s.points[i], &cl.Point)
		}
		den = fr.BatchInvert(den)
		for k, ref := range cl.Polys {
			f := committed[ref.Commitment].evals[ref.Poly]
			for i := range q {
				t.Sub(&f[i], &cl.Values[k]).Mul(&t, &den[i]).Mul(&t, &coef)
				q[i].Add(&q[i], &t)
			}
			coef.Mul(&coef, &lambda)
		}
	}

	// COMMIT phase: fold layer 0, commit to layers 1..nbSteps-1.
	trees := make([]merkleTree, s.nbSteps)
	sorted := q
	var gInv fr.Element
	gInv.Set(&s.domain.GeneratorInv)
	for i := range s.nbSteps {
		if i > 0 {
			leaves := make([][]byte, len(sorted))
			for k := range sorted {
				leaves[k] = sorted[k].Marshal()
			}
			trees[i] = newMerkleTree(s.newHash(), leaves)
			proof.Roots = append(proof.Roots, trees[i].root())
			if err := fs.Bind(names[1+i], trees[i].root()); err != nil {
				return nil, err
			}
		}
		xi, err := challenge(fs, names[1+i])
		if err != nil {
			return nil, err
		}
		folded := foldPolynomialLagrangeBasis(sorted, gInv, xi)
		gInv.Square(&gInv)
		if i < s.nbSteps-1 {
			sorted = sort(folded)
		} else {
			proof.Evaluation.Set(&folded[0])
		}
	}

	// QUERY phase.
	positions, err := queryPositions(s.newHash(), fs, names[len(names)-1], proof.Evaluation, uint64(n))
	if err != nil {
		return nil, err
	}
	proof.Queries = make([]BatchQuery, nbQueries)
	for k, pos := range positions {
		si := queryChain(pos, n, s.nbSteps)
		bq := &proof.Queries[k]
		bq.Mask = mask.tree.pairProof(si[0] / 2)
		bq.Commitments = make([]PairOpening, len(committed))
		for c := range committed {
			bq.Commitments[c] = committed[c].tree.pairProof(si[0] / 2)
		}
		bq.Layers = make([]PairOpening, s.nbSteps-1)
		for i := 1; i < s.nbSteps; i++ {
			bq.Layers[i-1] = trees[i].pairProof(si[i] / 2)
		}
	}

	return proof, nil
}

// Verify checks a proof of the claims against the commitments.
func (s *Scheme) Verify(seed []byte, commitments []Commitment, claims []Claim, proof *BatchProof) error {
	lambda, xi, positions, err := s.replay(seed, commitments, claims, proof)
	if err != nil {
		return err
	}
	for k, pos := range positions {
		if err := s.verifyBatchQuery(commitments, claims, lambda, xi, proof, &proof.Queries[k], pos); err != nil {
			return err
		}
	}

	return nil
}

// replay re-derives the verifier's challenges from the transcript: λ, the
// folding challenges, and the initial (sorted) query positions.
func (s *Scheme) replay(seed []byte, commitments []Commitment, claims []Claim, proof *BatchProof) (lambda fr.Element, xi []fr.Element, positions []int, err error) {
	if proof == nil || len(proof.Mask) == 0 || len(proof.Roots) != s.nbSteps-1 || len(proof.Queries) != nbQueries {
		err = ErrMalformedProof

		return
	}
	fs, names := s.transcript()
	if lambda, err = s.bindStatement(fs, names[0], seed, proof.Mask, commitments, claims); err != nil {
		return
	}
	xi = make([]fr.Element, s.nbSteps)
	for i := range s.nbSteps {
		if i > 0 {
			if err = fs.Bind(names[1+i], proof.Roots[i-1]); err != nil {
				return
			}
		}
		if xi[i], err = challenge(fs, names[1+i]); err != nil {
			return
		}
	}
	positions, err = queryPositions(s.newHash(), fs, names[len(names)-1], proof.Evaluation, uint64(len(s.points)))

	return
}

// QueriedPoints returns the points of the evaluation domain at which a proof
// reveals the committed polynomials: for each query, the pair {x, −x} of
// layer 0. It is an analysis helper (zero-knowledge tests count how many
// values of each polynomial a verifier sees); it does not verify the proof.
func (s *Scheme) QueriedPoints(seed []byte, commitments []Commitment, claims []Claim, proof *BatchProof) ([]fr.Element, error) {
	_, _, positions, err := s.replay(seed, commitments, claims, proof)
	if err != nil {
		return nil, err
	}
	res := make([]fr.Element, 0, 2*len(positions))
	for _, pos := range positions {
		p := pos / 2
		res = append(res, s.points[2*p], s.points[2*p+1])
	}

	return res, nil
}

func (s *Scheme) verifyBatchQuery(commitments []Commitment, claims []Claim, lambda fr.Element, xi []fr.Element, proof *BatchProof, bq *BatchQuery, pos int) error {
	n := len(s.points)
	if len(bq.Commitments) != len(commitments) || len(bq.Layers) != s.nbSteps-1 {
		return ErrMalformedProof
	}
	si := queryChain(pos, n, s.nbSteps)

	// layer 0: authenticate every commitment's two rows, then compute q(x), q(−x).
	p := si[0] / 2
	rows := make([][2][]fr.Element, len(commitments))
	for c, com := range commitments {
		op := bq.Commitments[c]
		if !verifyPair(s.newHash(), com.Root, op, p, n) {
			return ErrMerklePath
		}
		for side := range 2 {
			v, err := parseRow(op.Rows[side], com.NbPolys)
			if err != nil {
				return err
			}
			rows[c][side] = v
		}
	}
	// q starts with the mask R
	var cur [2]fr.Element
	if !verifyPair(s.newHash(), proof.Mask, bq.Mask, p, n) {
		return ErrMerklePath
	}
	for side := range 2 {
		v, err := parseRow(bq.Mask.Rows[side], 1)
		if err != nil {
			return err
		}
		cur[side] = v[0]
	}
	for side := range 2 {
		x := s.points[2*p+side]
		var t, d fr.Element
		coef := s.claimCoefficient(lambda)
		for _, cl := range claims {
			d.Sub(&x, &cl.Point).Inverse(&d)
			for k, ref := range cl.Polys {
				t.Sub(&rows[ref.Commitment][side][ref.Poly], &cl.Values[k]).Mul(&t, &d).Mul(&t, &coef)
				cur[side].Add(&cur[side], &t)
				coef.Mul(&coef, &lambda)
			}
		}
	}

	// folding chain, as in verifyQuery (plain FRI over D on q(a·X)).
	return s.foldChain(bq, proof, cur, xi, si)
}

// foldChain checks the folding from layer 0 (values cur at sorted positions
// 2·(si[0]/2), 2·(si[0]/2)+1) down to proof.Evaluation.
func (s *Scheme) foldChain(bq *BatchQuery, proof *BatchProof, cur [2]fr.Element, xi []fr.Element, si []int) error {
	n := len(s.points)
	var accGInv fr.Element
	accGInv.Set(&s.domain.GeneratorInv)
	for i := range s.nbSteps {
		f := foldStep(cur, accGInv, xi[i], si[i]/2)
		accGInv.Square(&accGInv)
		if i == s.nbSteps-1 {
			if !f.Equal(&proof.Evaluation) {
				return ErrProximityTestFolding
			}

			return nil
		}
		op := bq.Layers[i]
		if !verifyPair(s.newHash(), proof.Roots[i], op, si[i+1]/2, n>>uint(i+1)) {
			return ErrMerklePath
		}
		for side := range 2 {
			v, err := parseRow(op.Rows[side], 1)
			if err != nil {
				return err
			}
			cur[side] = v[0]
		}
		if !f.Equal(&cur[si[i+1]%2]) {
			return ErrProximityTestFolding
		}
	}

	return nil
}

// claimCoefficient returns λ^firstClaimPower, the batching coefficient of the
// first claim.
func (s *Scheme) claimCoefficient(lambda fr.Element) fr.Element {
	var c fr.Element
	c.SetOne()
	for range s.firstClaimPower {
		c.Mul(&c, &lambda)
	}

	return c
}

// foldStep folds the pair (l, r) = (P(g^p), P(−g^p)) with challenge x:
// (P₀ + x·P₁)(g^{2p}) where P(X) = P₀(X²) + X·P₁(X²).
func foldStep(pair [2]fr.Element, gInv, x fr.Element, p int) fr.Element {
	var ginv, fe, fo fr.Element
	ginv.Exp(gInv, big.NewInt(int64(p)))
	fe.Add(&pair[0], &pair[1])
	fo.Sub(&pair[0], &pair[1]).Mul(&fo, &ginv)
	fo.Mul(&fo, &x).Add(&fo, &fe).Mul(&fo, &twoInv)

	return fo
}

// parseRow decodes a leaf holding nb canonical field elements.
func parseRow(row []byte, nb int) ([]fr.Element, error) {
	if len(row) != nb*fr.Bytes {
		return nil, ErrMalformedProof
	}
	res := make([]fr.Element, nb)
	for j := range res {
		if err := res[j].SetBytesCanonical(row[j*fr.Bytes : (j+1)*fr.Bytes]); err != nil {
			return nil, ErrMalformedProof
		}
	}

	return res, nil
}

// pairProof returns the opening of leaves 2p and 2p+1.
func (t merkleTree) pairProof(p int) PairOpening {
	op := PairOpening{Rows: [2][]byte{t.leaves[2*p], t.leaves[2*p+1]}}
	i := p
	for l := 1; l < len(t.levels)-1; l++ {
		op.Path = append(op.Path, t.levels[l][i^1])
		i >>= 1
	}

	return op
}

// verifyPair checks a PairOpening of leaves 2p, 2p+1 of a tree with
// nbLeaves leaves against root.
func verifyPair(h hash.Hash, root []byte, op PairOpening, p, nbLeaves int) bool {
	if nbLeaves < 2 || p < 0 || 2*p+1 >= nbLeaves || len(op.Path) != bits.TrailingZeros(uint(nbLeaves))-1 {
		return false
	}
	node := hashOf(h, hashOf(h, op.Rows[0]), hashOf(h, op.Rows[1]))
	i := p
	for _, sib := range op.Path {
		if i%2 == 0 {
			node = hashOf(h, node, sib)
		} else {
			node = hashOf(h, sib, node)
		}
		i >>= 1
	}

	return bytes.Equal(node, root)
}
