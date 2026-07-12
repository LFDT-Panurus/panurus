/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"testing"

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

// blsNumeratorsBinaryTree writes input into the pooled leaves, runs the binary
// tree algorithm, and returns the numerators. Test-only helper.
func blsNumeratorsBinaryTree(input []*bls12381fr.Element, m int) []*bls12381fr.Element {
	treeSize := binaryTreeSize(m)
	leafStart := treeSize - m
	pooled := getTreeArrays[bls12381fr.Element](leafStart, m)
	for j := range m {
		pooled.leaves[j] = *input[j]
	}
	result := computeNumeratorsBinaryTree[bls12381fr.Element, *bls12381fr.Element](m, pooled)
	putTreeArrays(pooled)
	return result
}

// bn254NumeratorsBinaryTree is the BN254 counterpart of blsNumeratorsBinaryTree.
func bn254NumeratorsBinaryTree(input []*bn254fr.Element, m int) []*bn254fr.Element {
	treeSize := binaryTreeSize(m)
	leafStart := treeSize - m
	pooled := getTreeArrays[bn254fr.Element](leafStart, m)
	for j := range m {
		pooled.leaves[j] = *input[j]
	}
	result := computeNumeratorsBinaryTree[bn254fr.Element, *bn254fr.Element](m, pooled)
	putTreeArrays(pooled)
	return result
}

// buildCMinusJ builds a cMinusJE slice for a given integer c over m elements:
// cMinusJE[j] = c - j  for j = 0..m-1.
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

// TestComputeNumeratorsBinaryTreeVsOriginalBLS verifies that the binary-tree
// implementation produces the same numerators as a naive O(n²) reference for
// the BLS12-381 field, across a range of m values that exercise different tree
// shapes (perfect and imperfect binary trees).
//
// Note: m=1 is not a valid input for computeNumeratorsBinaryTree (callers
// always pass m = n+1 with n ≥ 1, so m ≥ 2). The m=1 case is therefore
// excluded from this table.
//
// Given cMinusJE[j] = c-j for various (c, m) pairs,
// When computeNumeratorsBinaryTree is called,
// Then each numers[i] must equal ∏_{j≠i} cMinusJE[j].
func TestComputeNumeratorsBinaryTreeVsOriginalBLS(t *testing.T) {
	// m values: 2 (root-is-parent-of-two-leaves), 3 (first non-power-of-2),
	// 4 (perfect), 5 and 7 (imperfect), 8 (perfect), 13 (imperfect).
	mValues := []int{2, 3, 4, 5, 7, 8, 13}
	cValues := []int64{100, 7, 42}

	for _, m := range mValues {
		for _, c := range cValues {
			t.Run("", func(t *testing.T) {
				t.Logf("m=%d c=%d", m, c)
				input := buildCMinusJBLS(c, m)

				got := blsNumeratorsBinaryTree(input, m)
					want := naiveNumeratorsBLS(input, m)

				require.Len(t, got, m, "result length must equal m")
				for i := range m {
					assert.True(t, equalsBLS(got[i], want[i]),
						"m=%d c=%d i=%d: binary-tree result differs from original", m, c, i)
				}
			})
		}
	}
}

// TestComputeNumeratorsBinaryTreeVsOriginalBN254 verifies the binary-tree
// implementation against the naive reference for BN254.
func TestComputeNumeratorsBinaryTreeVsOriginalBN254(t *testing.T) {
	mValues := []int{2, 3, 4, 5, 7, 8, 13}
	cValues := []int64{100, 7, 42}

	for _, m := range mValues {
		for _, c := range cValues {
			t.Run("", func(t *testing.T) {
				t.Logf("m=%d c=%d", m, c)
				input := buildCMinusJBN254(c, m)

				got := bn254NumeratorsBinaryTree(input, m)
					want := naiveNumeratorsBN254(input, m)

				require.Len(t, got, m, "result length must equal m")
				for i := range m {
					assert.True(t, equalsBN254(got[i], want[i]),
						"m=%d c=%d i=%d: binary-tree result differs from original", m, c, i)
				}
			})
		}
	}
}

// TestComputeNumeratorsBinaryTreeTwoElements verifies the m=2 case.
//
// With c=10:
//   - cMinusJ[0] = 10, cMinusJ[1] = 9
//   - numers[0] = cMinusJ[1] = 9
//   - numers[1] = cMinusJ[0] = 10
func TestComputeNumeratorsBinaryTreeTwoElements(t *testing.T) {
	// c=10, m=2
	input := buildCMinusJBLS(10, 2)

	got := blsNumeratorsBinaryTree(input, 2)
	require.Len(t, got, 2)

	var nine, ten bls12381fr.Element
	nine.SetInt64(9)
	ten.SetInt64(10)

	assert.True(t, equalsBLS(got[0], &nine), "m=2: numers[0] should be c-1 = 9")
	assert.True(t, equalsBLS(got[1], &ten), "m=2: numers[1] should be c-0 = 10")
}

// TestComputeNumeratorsBinaryTreeThreeElements verifies the m=3 case with
// hand-computed expected values.
//
// With c=10:
//   - cMinusJ = [10, 9, 8]
//   - numers[0] = 9*8 = 72
//   - numers[1] = 10*8 = 80
//   - numers[2] = 10*9 = 90
func TestComputeNumeratorsBinaryTreeThreeElements(t *testing.T) {
	input := buildCMinusJBLS(10, 3)

	got := blsNumeratorsBinaryTree(input, 3)
	require.Len(t, got, 3)

	expected := []int64{72, 80, 90}
	for i, exp := range expected {
		var e bls12381fr.Element
		e.SetInt64(exp)
		assert.True(t, equalsBLS(got[i], &e), "m=3: numers[%d] should be %d", i, exp)
	}
}

// TestComputeNumeratorsBinaryTreeFourElements verifies the m=4 perfect binary tree.
//
// With c=10:
//   - cMinusJ = [10, 9, 8, 7]
//   - numers[0] = 9*8*7 = 504
//   - numers[1] = 10*8*7 = 560
//   - numers[2] = 10*9*7 = 630
//   - numers[3] = 10*9*8 = 720
func TestComputeNumeratorsBinaryTreeFourElements(t *testing.T) {
	input := buildCMinusJBLS(10, 4)

	got := blsNumeratorsBinaryTree(input, 4)
	require.Len(t, got, 4)

	expected := []int64{504, 560, 630, 720}
	for i, exp := range expected {
		var e bls12381fr.Element
		e.SetInt64(exp)
		assert.True(t, equalsBLS(got[i], &e), "m=4: numers[%d] should be %d", i, exp)
	}
}

// TestComputeNumeratorsBinaryTreeOutputLength checks that the returned slice
// always has exactly m elements for various m values.
// m=1 is excluded (not a valid input; callers always pass m ≥ 2).
func TestComputeNumeratorsBinaryTreeOutputLength(t *testing.T) {
	for _, m := range []int{2, 3, 4, 5, 6, 7, 8, 15, 16, 17} {
		input := buildCMinusJBLS(1000, m)
		got := blsNumeratorsBinaryTree(input, m)
		assert.Len(t, got, m, "expected output length == m for m=%d", m)
	}
}

// TestComputeNumeratorsBinaryTreeAllDifferent verifies that distinct input
// elements produce distinct numerators (no accidental aliasing in the tree).
//
// For c far from 0..m-1, all (c-j) are distinct non-zero integers, so the
// products ∏_{j≠i} (c-j) are also distinct.
func TestComputeNumeratorsBinaryTreeAllDifferent(t *testing.T) {
	m := 5
	c := int64(1000) // far from 0..4 so all values are distinct

	input := buildCMinusJBLS(c, m)
	got := blsNumeratorsBinaryTree(input, m)
	require.Len(t, got, m)

	// Verify no two numerators are equal
	for i := range m {
		for j := i + 1; j < m; j++ {
			assert.False(t, equalsBLS(got[i], got[j]),
				"m=%d c=%d: numers[%d] and numers[%d] should be distinct", m, c, i, j)
		}
	}
}

// TestComputeNumeratorsBinaryTreeDoesNotMutateInput verifies that the input
// cMinusJE slice is not modified by the algorithm (no aliasing between input
// storage and the tree's internal scratch space).
func TestComputeNumeratorsBinaryTreeDoesNotMutateInput(t *testing.T) {
	m := 6
	c := int64(50)

	input := buildCMinusJBLS(c, m)

	// snapshot input values before the call
	snapshot := make([]bls12381fr.Element, m)
	for i, e := range input {
		snapshot[i] = *e
	}

	_ = blsNumeratorsBinaryTree(input, m)

	// verify input is unchanged
	for i, e := range input {
		assert.True(t, equalsBLS(e, &snapshot[i]),
			"input element %d was mutated by computeNumeratorsBinaryTree", i)
	}
}

// TestComputeNumeratorsBinaryTreeLargePerfectTree stress-tests a large perfect
// binary tree (m=32) against the original implementation for both curves.
func TestComputeNumeratorsBinaryTreeLargePerfectTree(t *testing.T) {
	m := 32
	c := int64(10000)

	t.Run("BLS12381", func(t *testing.T) {
		input := buildCMinusJBLS(c, m)
		got := blsNumeratorsBinaryTree(input, m)
		want := naiveNumeratorsBLS(input, m)
		require.Len(t, got, m)
		for i := range m {
			assert.True(t, equalsBLS(got[i], want[i]), "m=%d i=%d: mismatch", m, i)
		}
	})

	t.Run("BN254", func(t *testing.T) {
		input := buildCMinusJBN254(c, m)
		got := bn254NumeratorsBinaryTree(input, m)
		want := naiveNumeratorsBN254(input, m)
		require.Len(t, got, m)
		for i := range m {
			assert.True(t, equalsBN254(got[i], want[i]), "m=%d i=%d: mismatch", m, i)
		}
	})
}

// TestComputeNumeratorsBinaryTreeLargeImperfectTree stress-tests an imperfect
// binary tree (m=33) against the original implementation.
func TestComputeNumeratorsBinaryTreeLargeImperfectTree(t *testing.T) {
	m := 33
	c := int64(10000)

	input := buildCMinusJBLS(c, m)
	got := blsNumeratorsBinaryTree(input, m)
	want := naiveNumeratorsBLS(input, m)

	require.Len(t, got, m)
	for i := range m {
		assert.True(t, equalsBLS(got[i], want[i]), "m=%d i=%d: mismatch", m, i)
	}
}
