// Copyright 2020 Consensys Software Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nativefri

import (
	"encoding/binary"
	"hash"
	"math/big"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	fiatshamir "github.com/consensys/gnark-crypto/fiat-shamir"
)

// This file holds the FRI building blocks shared by the batched DEEP-FRI
// scheme of batch.go: parameters, the folding step, query positions and the
// Merkle tree. (The former per-polynomial API -- one proof of proximity per
// polynomial, openings only at domain points -- was removed with fix B.)

var (
	ErrProximityTestFolding = errors.New("one round of interaction failed")
	ErrMerklePath           = errors.New("merkle path proof is wrong")
	ErrMalformedProof       = errors.New("malformed proof")
)

const rho = 8

// nbQueries is the number of independent query chains the verifier checks.
//
// A word that is δ-far from the Reed-Solomon code survives one query with
// probability at most (1-δ), so s queries give soundness error (1-δ)^s.
// For the bn254 scalar field (q ≫ n²), FRI is proven sound up to the Johnson
// bound δ = 1-√ρ (Proximity Gaps for Reed-Solomon Codes, BCIKS20, Thm 8.3),
// i.e. a per-query error of √ρ. With ρ = 1/rho = 1/8 that is 1.5 bits per
// query, so 128 bits of security need s = 2·128/log2(rho) ≈ 86 queries.
// (Under the commonly assumed list-decoding conjecture the per-query error is
// ρ, and 43 queries would suffice.) See PROJECT.md and resources/.
const nbQueries = 86

// NbQueries is the number of FRI query rounds of a proof. Each reveals the
// committed polynomials at two points (x, −x), which zero-knowledge blinding
// must account for.
const NbQueries = nbQueries

// 2^{-1}, used several times
var twoInv fr.Element

func init() {
	twoInv.SetUint64(2).Inverse(&twoInv)
}

// convertCanonicalSorted convert the index i, an entry in a
// sorted polynomial, to the corresponding entry in canonical
// representation. n is the size of the polynomial.
func convertCanonicalSorted(i, n int) int {
	if i < n/2 {
		return 2 * i
	} else {
		l := n - (i + 1)
		l = 2 * l

		return n - l - 1
	}
}

// queryChain returns, for an initial position pos in the sorted first layer of
// size size, the sorted position of the same query chain in each of the
// nbSteps layers.
func queryChain(pos, size, nbSteps int) []int {
	_s := size / 2
	res := make([]int, nbSteps)
	res[0] = pos
	for i := 1; i < nbSteps; i++ {
		t := (res[i-1] - (res[i-1] % 2)) / 2
		res[i] = convertCanonicalSorted(t, _s)
		_s = _s / 2
	}

	return res
}

// sort orders the evaluation of a polynomial on a domain
// such that contiguous entries are in the same fiber:
// {q(g⁰), q(g^{n/2}), q(g¹), q(g^{1+n/2}),...,q(g^{n/2-1}), q(gⁿ⁻¹)}
func sort(evaluations []fr.Element) []fr.Element {
	q := make([]fr.Element, len(evaluations))
	n := len(evaluations) / 2
	for i := range n {
		q[2*i].Set(&evaluations[i])
		q[2*i+1].Set(&evaluations[i+n])
	}

	return q
}

// foldPolynomialLagrangeBasis folds a polynomial p, expressed in Lagrange basis.
//
// Fᵣ[X]/(Xⁿ-1) is a free module of rank 2 on Fᵣ[Y]/(Y^{n/2}-1). If
// p∈ Fᵣ[X]/(Xⁿ-1), expressed in Lagrange basis, the function finds the coordinates
// p₁, p₂ of p in Fᵣ[Y]/(Y^{n/2}-1), expressed in Lagrange basis. Finally, it computes
// p₁ + x*p₂ and returns it.
//
// * p is the polynomial to fold, in Lagrange basis, sorted like this: p = [p(1),p(-1),p(g),p(-g),p(g²),p(-g²),...]
// * g is a generator of the subgroup of Fᵣ^{*} of size len(p)
// * x is the folding challenge x, used to return p₁+x*p₂
func foldPolynomialLagrangeBasis(pSorted []fr.Element, gInv, x fr.Element) []fr.Element {
	// we have the following system
	// p₁(g²ⁱ)+gⁱp₂(g²ⁱ) = p(gⁱ)
	// p₁(g²ⁱ)-gⁱp₂(g²ⁱ) = p(-gⁱ)
	// we solve the system for p₁(g²ⁱ),p₂(g²ⁱ)
	s := len(pSorted)
	res := make([]fr.Element, s/2)

	var p1, p2, acc fr.Element
	acc.SetOne()

	for i := range s / 2 {
		p1.Add(&pSorted[2*i], &pSorted[2*i+1])
		p2.Sub(&pSorted[2*i], &pSorted[2*i+1]).Mul(&p2, &acc)
		res[i].Mul(&p2, &x).Add(&res[i], &p1).Mul(&res[i], &twoInv)

		acc.Mul(&acc, &gInv)
	}

	return res
}

// queryPositions binds the final evaluation, draws one seed from the
// transcript and returns the nbQueries positions H(seed ∥ j) mod cardinality.
func queryPositions(h hash.Hash, fs *fiatshamir.Transcript, name string, evaluation fr.Element, cardinality uint64) ([]int, error) {
	if err := fs.Bind(name, evaluation.Marshal()); err != nil {
		return nil, err
	}
	seed, err := fs.ComputeChallenge(name)
	if err != nil {
		return nil, err
	}
	var bPos, bCardinality big.Int
	bCardinality.SetUint64(cardinality)
	var j [8]byte
	res := make([]int, nbQueries)
	for i := range res {
		binary.BigEndian.PutUint64(j[:], uint64(i))
		bPos.SetBytes(hashOf(h, seed, j[:]))
		bPos.Mod(&bPos, &bCardinality)
		res[i] = int(bPos.Uint64()) //nolint:gosec // bPos < cardinality, reduced above
	}

	return res, nil
}

// hashOf returns h(data[0] ∥ data[1] ∥ ...).
func hashOf(h hash.Hash, data ...[]byte) []byte {
	h.Reset()
	for _, d := range data {
		h.Write(d) // hash.Hash.Write never returns an error
	}

	return h.Sum(nil)
}

// merkleTree keeps every node of a Merkle tree, so that it is built once and
// many authentication paths can then be read from it. It uses the same
// conventions as gnark-crypto's accumulator/merkletree (leaf = H(data),
// node = H(left ∥ right), proof = [leaf data, sibling hashes bottom-up]), so
// its proofs verify with merkletree.VerifyProof. The number of leaves must be
// a power of two, which is always the case for FRI layers.
type merkleTree struct {
	leaves [][]byte   // raw leaf data
	levels [][][]byte // levels[0] = leaf hashes, ..., levels[len-1] = [root]
}

func newMerkleTree(h hash.Hash, leaves [][]byte) merkleTree {
	t := merkleTree{leaves: leaves}
	level := make([][]byte, len(leaves))
	for i := range leaves {
		level[i] = hashOf(h, leaves[i])
	}
	t.levels = append(t.levels, level)
	for len(level) > 1 {
		next := make([][]byte, len(level)/2)
		for i := range next {
			next[i] = hashOf(h, level[2*i], level[2*i+1])
		}
		t.levels = append(t.levels, next)
		level = next
	}

	return t
}

func (t merkleTree) root() []byte {
	return t.levels[len(t.levels)-1][0]
}
