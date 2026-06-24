/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	mathlib "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
)

// leftChild returns the index of the left child of node i in the tree array.
func leftChild(i int) int {
	return 2*i + 1
}

// rightChild returns the index of the right child of node i in the tree array.
func rightChild(i int) int {
	return 2*i + 2
}

// computeNumeratorsBinaryTree computes the Lagrange numerators — for each point i in [0,m)
// it returns ∏_{k≠i}(c-k) — via a binary tree over the leaves (c-k), backed by pooled.slab.
//
// Adjacent leaves are paired thus exploiting the fact that their integer points differ by 1:
//
//   - Leaves are placed so every sibling leaf pair holds (c-2i) and (c-(2i+1)).
//     for odd m the value c-(m-1) is the unpaired leftover and is placed as the first leaf at leafStart.
//
//   - Bottom-up: every node should hold the product of the leaves in its sub-tree.
//     This is computed from the leaves up so that every node computes the product of its two children.
//
//     Level 1 (just above the leaves) computes these products with only additions and 1 product, as follows:
//     The i's node should compute the product of the i'th pair: (c-2i)(c-(2i+1))
//     Start with the product of the first pair: N_0 = c·(c-1) [cost = 1 product]
//     then every subsequence product of pair  : N_i = (c-2i)(c-(2i+1)) = N_{i-1} - 4c + (8i-2)
//     So computing N_i from N{i-1} involves additions only.
//     Higher levels of the tree are computed with every node computing the product of its 2 children.
//
//   - Top-down leaf level: At this phase every node in the top-down tree should hold
//     the product of all the leaves in the entire tree, excluding the leaves of the node's sub-tree.
//     The root's value is 1. The going top down - consider visiting a node N with an exclude value E
//     and the values Nleft, Nright computed for its two child nodes in the bottom-up stage.
//     Now compute the exclusion values of the two child nodes of N as follows:
//     Eleft = E*Nright, and Eright = E*Nleft [cost = two products per parent node]
//
//     The exclusion values for the leaves are computed in the nodes of Level 1 (just above the leaves).
//     This is done with one product per parent node (instead of two) as follows:
//     For the i'th parent of the leaf-pair (c-2i)(c-(2i+1)) which has exclude value E
//     compute the exclude values of the two child leaves (i.e. the respective target lagrange numerators)
//     as follows:
//     numer_left  = E·(c-(2i+1))   [cost = one product] and
//     numer_right = numer_left + E [one addition, since (c-2i) = (c-(2i+1))+1].
//
// Slab layout (see lagrange_pool.go):
//
//	slab = [ tree+leaves (treeSize) | exclude+numers (treeSize) | scratch (numeratorScratch) ]
//
// The first half holds subtree products (indices [0,leafStart)) and leaves ([leafStart,treeSize));
// the second half mirrors it with exclude products / numerator outputs; the tail is scratch.
// Leaf-pair parents occupy [leafPairsStart, leafStart)
// A node is a parent of leaves iff leftChild(i) >= leafStart.
// Both even and odd m are supported.
//
// relevant is an optional mask (indexed by output integer v in [0,m)) marking which numerators
// the caller will actually use; nil means all of them. When set, the top-down pass is pruned to
// the ancestors of relevant leaves — a node's exclude value is computed only if its subtree
// contains a relevant leaf — so the products for discarded numerators are skipped (used by the
// partial path, which keeps only {0, n+1, ..., 2n}). Placement and both bottom-up phases are
// unaffected: a kept leaf's exclude still needs the aggregate products of the discarded subtrees.
func computeNumeratorsBinaryTree[T any, E math2.GnarkFr[T]](m int, c *mathlib.Zr, pooled *treeArrays[T], relevant []bool) []E {
	leafStart := m - 1
	treeSize := 2*m - 1
	m2 := m / 2
	sl := pooled.slab

	// Working field elements are taken from the pooled slab's scratch tail — so there are no allocations.
	// Every integer constant we need forms an arithmetic sequence, so it is produced by cheap
	// field additions from a Montgomery-encoded step instead of a per-use SetInt64 (each
	// SetInt64 costs ~a multiply). Costing only ~4 SetInt64 run per call.
	scratch := sl[2*treeSize:]
	base := leafStart + (m & 1)
	numPairedLeaves := 2 * m2 // paired leaves == m when m is even, or m-1 when m is odd

	// cE aliases the first leaf slot (integer 0 → c-0 = c), so placing c needs no separate
	// copy; leaf slots are never overwritten by the bottom-up sweep. (m==1 has no pairs; its
	// single leaf is the leftover at leafStart.)
	var cE E
	if numPairedLeaves > 0 {
		cE = E(&sl[base])
	} else {
		cE = E(&sl[leafStart])
	}
	math2.SetNativeFromZr[T, E](c, cE)

	oneE := E(&scratch[0])
	oneE.SetInt64(1)

	// Leaves: slot base+j holds integer j (value c-j). Chain each from its predecessor by -1,
	// thus avoiding a costly SetInt64 op.
	for j := 1; j < numPairedLeaves; j++ {
		E(&sl[base+j]).Sub(E(&sl[base+j-1]), oneE)
	}
	// Odd m: the value c-(m-1) is the unpaired leftover and is placed at slot leafStart.
	if m&1 == 1 && numPairedLeaves > 0 {
		tmp := E(&scratch[4])
		tmp.SetInt64(int64(m - 1))
		E(&sl[leafStart]).Sub(cE, tmp)
	}

	// Leaf-pair parents occupy [leafPairsStart, leafStart); pair i -> slot leafPairsStart+i.
	leafPairsStart := leafStart - m2

	// Phase 1: Bottom-up.
	// Level 1:
	// The i'th parent of the i;th pair should compute the product
	//   N_i = (c-2i)(c-(2i+1))
	// Start by compute N_0 with one product:
	//   N_0 = c·(c-1)
	// The compute the next pairs with only additions:
	//   N_i = N_{i-1} - 4c + (8i-2)   [= c^2 - (4i+1)c + 2i(2i+1)].
	// The added constant 8i-2 = 6,14,22,... steps by 8, so it is carried incrementally too.
	if m2 > 0 {
		c4 := E(&scratch[1])
		c4.Add(cE, cE) // 2c
		c4.Add(c4, c4) // 4c
		cm1 := E(&scratch[5])
		cm1.Sub(cE, oneE)                   // c-1
		E(&sl[leafPairsStart]).Mul(cE, cm1) // seed N_0 (the only level-1 multiplication)
		if m2 > 1 {
			delta := E(&scratch[2])
			eight := E(&scratch[3])
			delta.SetInt64(6) // 8·1 - 2
			eight.SetInt64(8)
			for i := 1; i < m2; i++ {
				cur := E(&sl[leafPairsStart+i])
				cur.Sub(E(&sl[leafPairsStart+i-1]), c4)
				cur.Add(cur, delta)
				delta.Add(delta, eight)
			}
		}
	}

	// Level 2+: each internal node is the product of its two children (children have higher
	// indices, so a descending sweep has them ready). Folds in the odd-m leftover leaf.
	for i := leafPairsStart - 1; i >= 0; i-- {
		E(&sl[i]).Mul(E(&sl[leftChild(i)]), E(&sl[rightChild(i)]))
	}

	// When this function is called in the context of "partial" nominators, some of the
	// nominators will not be required by the caller, and are deemed "irrelevant".
	// Optional relevance mask: nodeRel[i] is true iff node i's subtree holds a kept numerator,
	// so the top-down can skip everything else. nil ⇒ everything is relevant (full path).
	var nodeRel []bool
	if relevant != nil {
		nodeRel = make([]bool, treeSize)
		for j := range numPairedLeaves { // slot base+j holds integer j
			nodeRel[base+j] = relevant[j]
		}
		if m&1 == 1 { // leftover slot leafStart holds integer m-1
			nodeRel[leafStart] = relevant[m-1]
		}
		for i := leafStart - 1; i >= 0; i-- {
			nodeRel[i] = nodeRel[leftChild(i)] || nodeRel[rightChild(i)]
		}
	}

	// Phase 2: Top-down — exclude products; leaf slots receive the numerators.
	E(&sl[treeSize]).SetOne() // exclude[root] = 1
	for i := range leafStart {
		if nodeRel != nil && !nodeRel[i] {
			continue // no kept numerator under this node — its exclude is never read
		}
		l, r := leftChild(i), rightChild(i)
		exclI := E(&sl[treeSize+i]) // the already-computed exclusion value of node i
		relL := nodeRel == nil || nodeRel[l]
		relR := nodeRel == nil || nodeRel[r]
		if l >= leafStart {
			// => node i is a Leaf-pair parent with exclude=exclI
			// => its children are consecutive leaves (r=l+1) and hold consecutive values (sl[l] = sl[r] + 1).
			//   numer_left  = exclude · sl[r] (one product)
			//   numer_right = numer_left + exclude (one addition)
			switch {
			case relL && relR:
				exclL := E(&sl[treeSize+l])
				exclL.Mul(exclI, E(&sl[r]))
				E(&sl[treeSize+r]).Add(exclL, exclI)
			case relL:
				E(&sl[treeSize+l]).Mul(exclI, E(&sl[r]))
			case relR:
				E(&sl[treeSize+r]).Mul(exclI, E(&sl[l]))
			}
		} else {
			if relL {
				E(&sl[treeSize+l]).Mul(exclI, E(&sl[r]))
			}
			if relR {
				E(&sl[treeSize+r]).Mul(exclI, E(&sl[l]))
			}
		}
	}

	// Extraction: the leaf slot holding (c-v) carries numer[v].
	numersE := make([]E, m)
	if m&1 == 1 {
		numersE[m-1] = E(&sl[treeSize+leafStart])
	}
	for i := range m2 {
		numersE[2*i] = E(&sl[treeSize+base+2*i])
		numersE[2*i+1] = E(&sl[treeSize+base+2*i+1])
	}

	return numersE
}

// getLagrangeMultipliersNative is the native fr.Element implementation of
// getLagrangeMultipliers. Conversions between mathlib.Zr and fr.Element occur
// only once at the boundary (once for input c, n+1 times for the output slice),
// so the O(n²) arithmetic runs entirely in native Montgomery form.
//
// The denominator inverses d_i^{-1} = (∏_{j≠i}(i-j))^{-1} depend only on n,
// not on c, so they are retrieved from the cache (computed once per n).
func getLagrangeMultipliersNative[T any, E math2.GnarkFr[T]](n uint64, c *mathlib.Zr, curve *mathlib.Curve, denomInvs []E) ([]*mathlib.Zr, error) {
	m := int(n) + 1 // #nosec G115

	// Compute numerator for each Lagrange basis polynomial L_i(c).
	// Denominators come from the cache — no O(n²) recomputation.
	// The full path uses every numerator, so no relevance mask (nil ⇒ all).
	pooled := getTreeArrays[T](m)
	numersE := computeNumeratorsBinaryTree[T, E](m, c, pooled, nil)

	result := make([]*mathlib.Zr, m)
	for i := range m {
		var prod T
		E(&prod).Mul(numersE[i], denomInvs[i])
		result[i] = math2.NativeToZr[T, E](E(&prod), curve)
	}
	putTreeArrays(pooled)

	return result, nil
}

// getLagrangeMultipliersPartialNative is the native fr.Element implementation of
// getLagrangeMultipliersPartial. Same boundary-only conversion strategy.
// Denominator inverses are retrieved from the cache.
func getLagrangeMultipliersPartialNative[T any, E math2.GnarkFr[T]](n uint64, c *mathlib.Zr, curve *mathlib.Curve, denomInvs []E) ([]*mathlib.Zr, error) {
	total := 2*int(n) + 1 // #nosec G115 // all evaluation points: 0..2n

	// Only numerators at the relevant indices {0, n+1, n+2, ..., 2n} are used below; the rest
	// are discarded. Passing that mask lets computeNumeratorsBinaryTree prune the top-down pass
	// (skip the exclude products of any subtree with no relevant leaf).
	relevant := make([]bool, total)
	relevant[0] = true
	for k := 1; k <= int(n); k++ { // #nosec G115
		relevant[int(n)+k] = true
	}
	pooled := getTreeArrays[T](total)
	allNumersE := computeNumeratorsBinaryTree[T, E](total, c, pooled, relevant)

	result := make([]*mathlib.Zr, int(n)+1) // #nosec G115

	// k=0: relevant index is 0
	var prod0 T
	E(&prod0).Mul(allNumersE[0], denomInvs[0])
	result[0] = math2.NativeToZr[T, E](E(&prod0), curve)

	// k=1..n: relevant index is n+k
	for k := 1; k <= int(n); k++ { // #nosec G115
		var prod T
		E(&prod).Mul(allNumersE[int(n)+k], denomInvs[k]) // #nosec G115
		result[k] = math2.NativeToZr[T, E](E(&prod), curve)
	}
	putTreeArrays(pooled)

	return result, nil
}

// interpolateNative is the native fr.Element implementation of interpolate.
// Denominator inverses are retrieved from the cache.
func interpolateNative[T any, E math2.GnarkFr[T]](n uint64, valuesOverN []*mathlib.Zr, curve *mathlib.Curve, denomInvs []E) ([]*mathlib.Zr, error) {
	m := int(n) + 1 // #nosec G115

	// Convert all input values to native elements once.
	vals := make([]T, m)
	valsE := make([]E, m)
	for i := range m {
		valsE[i] = E(&vals[i])

		v := valuesOverN[i]
		switch {
		case v.IsZero():
			valsE[i].SetZero()
		case v.IsOne():
			valsE[i].SetOne()
		default:
			valsE[i].SetBigInt(valuesOverN[i].BigInt())
		}
	}

	// First m entries are the inputs verbatim.
	result := make([]*mathlib.Zr, 2*int(n)+1) // #nosec G115
	copy(result, valuesOverN)

	// Evaluate at each x in {n+1, ..., 2n} via Lagrange interpolation.
	for x := int(n) + 1; x <= 2*int(n); x++ { // #nosec G115
		// xMinusJ[j] = x - j, and px = ∏_j xMinusJ[j]
		xMinusJ := make([]T, m)
		xMinusJE := make([]E, m)
		var px T
		pxE := E(&px)
		pxE.SetOne()
		for j := range m {
			xMinusJE[j] = E(&xMinusJ[j])
			xMinusJE[j].SetInt64(int64(x - j)) // #nosec G115
			pxE.Mul(pxE, xMinusJE[j])
		}

		xMinusJInvs := math2.NativeBatchInverse[T, E](xMinusJE)

		var val T
		valE := E(&val)
		for i := range m {
			var li T
			liE := E(&li)
			liE.Mul(pxE, xMinusJInvs[i])
			liE.Mul(liE, denomInvs[i])
			liE.Mul(liE, valsE[i])
			valE.Add(valE, liE)
		}
		result[x] = math2.NativeToZr[T, E](valE, curve)
	}

	return result, nil
}

// nativeLagrangeMultipliers dispatches getLagrangeMultipliers to the native
// fr.Element implementation for supported curves, using cached denominator inverses.
func nativeLagrangeMultipliers(n uint64, c *mathlib.Zr, curve *mathlib.Curve) ([]*mathlib.Zr, bool, error) {
	switch curve.GroupOrder.CurveID() {
	case mathlib.BLS12_381, mathlib.BLS12_381_GURVY, mathlib.BLS12_381_BBS, mathlib.BLS12_381_BBS_GURVY:
		denomInvs := getOrComputeDenomInvsBLS(n, false)
		r, err := getLagrangeMultipliersNative[bls12381fr.Element, *bls12381fr.Element](n, c, curve, denomInvs)

		return r, true, err
	case mathlib.BN254:
		denomInvs := getOrComputeDenomInvsBN254(n, false)
		r, err := getLagrangeMultipliersNative[bn254fr.Element, *bn254fr.Element](n, c, curve, denomInvs)

		return r, true, err
	}

	return nil, false, nil
}

// nativeLagrangeMultipliersPartial dispatches getLagrangeMultipliersPartial to
// the native fr.Element implementation for supported curves, using cached denominator inverses.
func nativeLagrangeMultipliersPartial(n uint64, c *mathlib.Zr, curve *mathlib.Curve) ([]*mathlib.Zr, bool, error) {
	switch curve.GroupOrder.CurveID() {
	case mathlib.BLS12_381, mathlib.BLS12_381_GURVY, mathlib.BLS12_381_BBS, mathlib.BLS12_381_BBS_GURVY:
		denomInvs := getOrComputeDenomInvsBLS(n, true)
		r, err := getLagrangeMultipliersPartialNative[bls12381fr.Element, *bls12381fr.Element](n, c, curve, denomInvs)

		return r, true, err
	case mathlib.BN254:
		denomInvs := getOrComputeDenomInvsBN254(n, true)
		r, err := getLagrangeMultipliersPartialNative[bn254fr.Element, *bn254fr.Element](n, c, curve, denomInvs)

		return r, true, err
	}

	return nil, false, nil
}

// nativeInterpolate dispatches interpolate to the native fr.Element
// implementation for supported curves, using cached denominator inverses.
func nativeInterpolate(n uint64, vals []*mathlib.Zr, curve *mathlib.Curve) ([]*mathlib.Zr, bool, error) {
	switch curve.GroupOrder.CurveID() {
	case mathlib.BLS12_381, mathlib.BLS12_381_GURVY, mathlib.BLS12_381_BBS, mathlib.BLS12_381_BBS_GURVY:
		denomInvs := getOrComputeDenomInvsBLS(n, false)
		r, err := interpolateNative[bls12381fr.Element, *bls12381fr.Element](n, vals, curve, denomInvs)

		return r, true, err
	case mathlib.BN254:
		denomInvs := getOrComputeDenomInvsBN254(n, false)
		r, err := interpolateNative[bn254fr.Element, *bn254fr.Element](n, vals, curve, denomInvs)

		return r, true, err
	}

	return nil, false, nil
}
