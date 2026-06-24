/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"testing"

	mathlib "github.com/IBM/mathlib"
	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// naiveNumeratorsBLS computes ∏_{j≠i} input[j] for each i using a straightforward
// O(n²) loop. Used as a reference oracle in tests.
func naiveNumeratorsBLS(input []*bls12381fr.Element, m int) []*bls12381fr.Element {
	out := make([]*bls12381fr.Element, m)
	for i := range m {
		out[i] = new(bls12381fr.Element).SetOne()
		for j := range m {
			if j != i {
				out[i].Mul(out[i], input[j])
			}
		}
	}

	return out
}

// naiveNumeratorsBN254 is the BN254 counterpart of naiveNumeratorsBLS.
func naiveNumeratorsBN254(input []*bn254fr.Element, m int) []*bn254fr.Element {
	out := make([]*bn254fr.Element, m)
	for i := range m {
		out[i] = new(bn254fr.Element).SetOne()
		for j := range m {
			if j != i {
				out[i].Mul(out[i], input[j])
			}
		}
	}

	return out
}

// blsNumeratorsBinaryTree runs the binary tree algorithm for the given c and m,
// and returns the numerators. Test-only helper.
func blsNumeratorsBinaryTree(c int64, m int) []*bls12381fr.Element {
	curve := mathlib.Curves[mathlib.BLS12_381]
	cZr := curve.NewZrFromInt(c)
	pooled := getTreeArrays[bls12381fr.Element](m)
	result := computeNumeratorsBinaryTree[bls12381fr.Element, *bls12381fr.Element](m, cZr, pooled, nil)
	putTreeArrays(pooled)

	return result
}

// bn254NumeratorsBinaryTree is the BN254 counterpart of blsNumeratorsBinaryTree.
func bn254NumeratorsBinaryTree(c int64, m int) []*bn254fr.Element {
	curve := mathlib.Curves[mathlib.BN254]
	cZr := curve.NewZrFromInt(c)
	pooled := getTreeArrays[bn254fr.Element](m)
	result := computeNumeratorsBinaryTree[bn254fr.Element, *bn254fr.Element](m, cZr, pooled, nil)
	putTreeArrays(pooled)

	return result
}

// buildCMinusJBLS builds cMinusJE[j] = c - j for j = 0..m-1.
func buildCMinusJBLS(c int64, m int) []*bls12381fr.Element {
	elems := make([]*bls12381fr.Element, m)
	for j := range m {
		e := new(bls12381fr.Element)
		e.SetInt64(c - int64(j)) // #nosec G115
		elems[j] = e
	}

	return elems
}

func buildCMinusJBN254(c int64, m int) []*bn254fr.Element {
	elems := make([]*bn254fr.Element, m)
	for j := range m {
		e := new(bn254fr.Element)
		e.SetInt64(c - int64(j)) // #nosec G115
		elems[j] = e
	}

	return elems
}

// equalsBLS reports whether two BLS12-381 elements are equal.
func equalsBLS(a, b *bls12381fr.Element) bool { return a.Equal(b) }

// equalsBN254 reports whether two BN254 elements are equal.
func equalsBN254(a, b *bn254fr.Element) bool { return a.Equal(b) }

// smallMValues are small sizes of both parities exercising different tree shapes
// (method0 supports even and odd m via consecutive left-to-right pairing).
var smallMValues = []int{2, 3, 4, 5, 7, 8, 9, 13, 16}

// productionMValues are the sizes used in production (full path n+1 and partial path 2n+1
// for n∈{8,16,32,64}), plus even neighbours {64,128} for extra coverage.
var productionMValues = []int{17, 33, 64, 65, 128, 129}

// TestComputeNumeratorsBinaryTreeVsNaiveBLS verifies the binary-tree implementation
// against a naive O(n²) reference over small sizes (both parities) for BLS12-381.
//
// Given cMinusJE[j] = c-j for various (c, m),
// When computeNumeratorsBinaryTree is called,
// Then each numers[i] must equal ∏_{j≠i} cMinusJE[j].
func TestComputeNumeratorsBinaryTreeVsNaiveBLS(t *testing.T) {
	cValues := []int64{100, 7, 42}

	for _, m := range smallMValues {
		for _, c := range cValues {
			t.Run("", func(t *testing.T) {
				t.Logf("m=%d c=%d", m, c)
				input := buildCMinusJBLS(c, m)

				got := blsNumeratorsBinaryTree(c, m)
				want := naiveNumeratorsBLS(input, m)

				require.Len(t, got, m, "result length must equal m")
				for i := range m {
					assert.True(t, equalsBLS(got[i], want[i]),
						"m=%d c=%d i=%d: binary-tree result differs from naive reference", m, c, i)
				}
			})
		}
	}
}

// TestComputeNumeratorsBinaryTreeVsNaiveBN254 is the BN254 counterpart.
func TestComputeNumeratorsBinaryTreeVsNaiveBN254(t *testing.T) {
	cValues := []int64{100, 7, 42}

	for _, m := range smallMValues {
		for _, c := range cValues {
			t.Run("", func(t *testing.T) {
				t.Logf("m=%d c=%d", m, c)
				input := buildCMinusJBN254(c, m)

				got := bn254NumeratorsBinaryTree(c, m)
				want := naiveNumeratorsBN254(input, m)

				require.Len(t, got, m, "result length must equal m")
				for i := range m {
					assert.True(t, equalsBN254(got[i], want[i]),
						"m=%d c=%d i=%d: binary-tree result differs from naive reference", m, c, i)
				}
			})
		}
	}
}

// TestComputeNumeratorsBinaryTreeProductionSizes verifies method0 against the naive
// reference for the production sizes {17,33,65,129} (plus even {64,128}), which exercise the
// top-down leaf trick at scale. c=10000 keeps every (c-j) distinct and non-zero.
func TestComputeNumeratorsBinaryTreeProductionSizes(t *testing.T) {
	const c = int64(10000)

	for _, m := range productionMValues {
		t.Run("BLS12381", func(t *testing.T) {
			input := buildCMinusJBLS(c, m)
			got := blsNumeratorsBinaryTree(c, m)
			want := naiveNumeratorsBLS(input, m)
			require.Len(t, got, m)
			for i := range m {
				assert.True(t, equalsBLS(got[i], want[i]), "m=%d i=%d: mismatch", m, i)
			}
		})

		t.Run("BN254", func(t *testing.T) {
			input := buildCMinusJBN254(c, m)
			got := bn254NumeratorsBinaryTree(c, m)
			want := naiveNumeratorsBN254(input, m)
			require.Len(t, got, m)
			for i := range m {
				assert.True(t, equalsBN254(got[i], want[i]), "m=%d i=%d: mismatch", m, i)
			}
		})
	}
}

// TestComputeNumeratorsBinaryTreeEvenM verifies method0 handles even m (which has no leftover
// leaf — all leaves pair up) correctly against the naive reference, on both curves.
func TestComputeNumeratorsBinaryTreeEvenM(t *testing.T) {
	const c = int64(10000)
	for _, m := range []int{2, 4, 6, 8, 10, 16, 32, 64, 128} {
		input := buildCMinusJBLS(c, m)
		got := blsNumeratorsBinaryTree(c, m)
		want := naiveNumeratorsBLS(input, m)
		require.Len(t, got, m)
		for i := range m {
			assert.True(t, equalsBLS(got[i], want[i]), "BLS m=%d i=%d: mismatch", m, i)
		}

		inputBN := buildCMinusJBN254(c, m)
		gotBN := bn254NumeratorsBinaryTree(c, m)
		wantBN := naiveNumeratorsBN254(inputBN, m)
		for i := range m {
			assert.True(t, equalsBN254(gotBN[i], wantBN[i]), "BN254 m=%d i=%d: mismatch", m, i)
		}
	}
}

// TestComputeNumeratorsBinaryTreeThreeElements verifies the m=3 case with
// hand-computed expected values (independent of the naive oracle).
//
// With c=10:
//   - cMinusJ = [10, 9, 8]
//   - numers[0] = 9*8 = 72
//   - numers[1] = 10*8 = 80
//   - numers[2] = 10*9 = 90
func TestComputeNumeratorsBinaryTreeThreeElements(t *testing.T) {
	got := blsNumeratorsBinaryTree(10, 3)
	require.Len(t, got, 3)

	expected := []int64{72, 80, 90}
	for i, exp := range expected {
		var e bls12381fr.Element
		e.SetInt64(exp)
		assert.True(t, equalsBLS(got[i], &e), "m=3: numers[%d] should be %d", i, exp)
	}
}

// TestComputeNumeratorsBinaryTreeNineElements verifies an odd size with a leftover leaf
// (m=9: four consecutive pairs + one unpaired leaf) against hand-computed expected values.
//
// With c=10, cMinusJ = [10,9,8,7,6,5,4,3,2]; numers[i] = ∏_{j≠i} (10-j).
func TestComputeNumeratorsBinaryTreeNineElements(t *testing.T) {
	const c, m = int64(10), 9
	got := blsNumeratorsBinaryTree(c, m)
	require.Len(t, got, m)

	// numers[i] = 9! / ((10-i)) scaled: full product P = 10*9*...*2 = 3628800.
	full := int64(1)
	for j := range m {
		full *= c - int64(j)
	}
	for i := range m {
		exp := full / (c - int64(i))
		var e bls12381fr.Element
		e.SetInt64(exp)
		assert.True(t, equalsBLS(got[i], &e), "m=9: numers[%d] should be %d", i, exp)
	}
}

// TestComputeNumeratorsBinaryTreeOutputLength checks that the returned slice always has
// exactly m elements, for both even and odd m.
func TestComputeNumeratorsBinaryTreeOutputLength(t *testing.T) {
	for _, m := range []int{2, 3, 4, 5, 6, 7, 8, 9, 15, 16, 17, 32, 33} {
		got := blsNumeratorsBinaryTree(1000, m)
		assert.Len(t, got, m, "expected output length == m for m=%d", m)
	}
}

// TestComputeNumeratorsBinaryTreeAllDifferent verifies that distinct input elements
// produce distinct numerators (no accidental aliasing in the tree).
func TestComputeNumeratorsBinaryTreeAllDifferent(t *testing.T) {
	m := 5
	c := int64(1000) // far from 0..4 so all values are distinct

	got := blsNumeratorsBinaryTree(c, m)
	require.Len(t, got, m)

	for i := range m {
		for j := i + 1; j < m; j++ {
			assert.False(t, equalsBLS(got[i], got[j]),
				"m=%d c=%d: numers[%d] and numers[%d] should be distinct", m, c, i, j)
		}
	}
}

// TestComputeNumeratorsBinaryTreeDeterministic verifies repeated calls with the same c
// return identical results (the pooled slab is not carried over between calls).
func TestComputeNumeratorsBinaryTreeDeterministic(t *testing.T) {
	m := 7
	c := int64(50)

	got1 := blsNumeratorsBinaryTree(c, m)
	got2 := blsNumeratorsBinaryTree(c, m)

	for i := range m {
		assert.True(t, equalsBLS(got1[i], got2[i]),
			"repeated call with same c=%d produced different result at index %d", c, i)
	}
}

// blsNumeratorsBinaryTreeMasked runs the binary tree with a relevance mask (partial pruning).
func blsNumeratorsBinaryTreeMasked(c int64, m int, relevant []bool) []*bls12381fr.Element {
	curve := mathlib.Curves[mathlib.BLS12_381]
	cZr := curve.NewZrFromInt(c)
	pooled := getTreeArrays[bls12381fr.Element](m)
	result := computeNumeratorsBinaryTree[bls12381fr.Element, *bls12381fr.Element](m, cZr, pooled, relevant)
	putTreeArrays(pooled)

	return result
}

func bn254NumeratorsBinaryTreeMasked(c int64, m int, relevant []bool) []*bn254fr.Element {
	curve := mathlib.Curves[mathlib.BN254]
	cZr := curve.NewZrFromInt(c)
	pooled := getTreeArrays[bn254fr.Element](m)
	result := computeNumeratorsBinaryTree[bn254fr.Element, *bn254fr.Element](m, cZr, pooled, relevant)
	putTreeArrays(pooled)

	return result
}

// partialRelevant builds the partial-path relevance mask over total=2n+1 points: {0, n+1, ..., 2n}.
func partialRelevant(n int) []bool {
	total := 2*n + 1
	rel := make([]bool, total)
	rel[0] = true
	for k := 1; k <= n; k++ {
		rel[n+k] = true
	}

	return rel
}

// TestComputeNumeratorsBinaryTreePartialPruning verifies that pruning the top-down to the partial
// relevant set {0, n+1, ..., 2n} yields exactly the same numerators at those indices as the full
// (unpruned) computation, and that both match the naive reference. This is the pruning used by
// getLagrangeMultipliersPartialNative.
func TestComputeNumeratorsBinaryTreePartialPruning(t *testing.T) {
	const c = int64(10007)
	for _, n := range []int{4, 8, 16, 32, 64} {
		total := 2*n + 1
		rel := partialRelevant(n)

		fullBLS := blsNumeratorsBinaryTree(c, total)
		prunedBLS := blsNumeratorsBinaryTreeMasked(c, total, rel)
		naiveBLS := naiveNumeratorsBLS(buildCMinusJBLS(c, total), total)

		fullBN := bn254NumeratorsBinaryTree(c, total)
		prunedBN := bn254NumeratorsBinaryTreeMasked(c, total, rel)
		naiveBN := naiveNumeratorsBN254(buildCMinusJBN254(c, total), total)

		for v := range total {
			if !rel[v] {
				continue
			}
			assert.True(t, equalsBLS(prunedBLS[v], fullBLS[v]), "BLS n=%d v=%d: pruned != full", n, v)
			assert.True(t, equalsBLS(prunedBLS[v], naiveBLS[v]), "BLS n=%d v=%d: pruned != naive", n, v)
			assert.True(t, equalsBN254(prunedBN[v], fullBN[v]), "BN254 n=%d v=%d: pruned != full", n, v)
			assert.True(t, equalsBN254(prunedBN[v], naiveBN[v]), "BN254 n=%d v=%d: pruned != naive", n, v)
		}
	}
}
