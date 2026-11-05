/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package nativefri

import (
	"bytes"
	"crypto/sha256"
	"testing"

	"github.com/consensys/gnark-crypto/accumulator/merkletree"
)

// TestMerkleTreeMatchesGnarkCrypto checks that our Merkle tree uses the same
// conventions as gnark-crypto's accumulator/merkletree (leaf = H(data),
// node = H(left ∥ right)): same root, same authentication paths. Pair openings
// (batch.go) are the same path with the two leaves given instead of the
// first sibling hash.
func TestMerkleTreeMatchesGnarkCrypto(t *testing.T) {
	h := sha256.New()
	for _, size := range []int{2, 16, 128} {
		leaves := make([][]byte, size)
		for i, e := range randPoly(t, size) {
			leaves[i] = e.Marshal()
		}
		ours := newMerkleTree(h, leaves)
		for i := range size {
			ref := merkletree.New(h)
			if err := ref.SetIndex(uint64(i)); err != nil {
				t.Fatal(err)
			}
			for _, l := range leaves {
				ref.Push(l)
			}
			root, path, _, _ := ref.Prove()
			if !bytes.Equal(root, ours.root()) {
				t.Fatalf("size %d: root mismatch", size)
			}
			got := ours.proof(i)
			if len(got) != len(path) {
				t.Fatalf("size %d leaf %d: path length %d, want %d", size, i, len(got), len(path))
			}
			for k := range path {
				if !bytes.Equal(got[k], path[k]) {
					t.Fatalf("size %d leaf %d: path entry %d differs", size, i, k)
				}
			}
			if i%2 == 0 {
				pair := ours.pairProof(i / 2)
				if !bytes.Equal(pair.Rows[0], leaves[i]) || !bytes.Equal(pair.Rows[1], leaves[i+1]) {
					t.Fatalf("size %d pair %d: wrong rows", size, i/2)
				}
				if len(pair.Path) != len(path)-2 {
					t.Fatalf("size %d pair %d: path length %d, want %d", size, i/2, len(pair.Path), len(path)-2)
				}
				for k := range pair.Path {
					if !bytes.Equal(pair.Path[k], path[k+2]) {
						t.Fatalf("size %d pair %d: path entry %d differs", size, i/2, k)
					}
				}
			}
		}
	}
}

// proof returns the authentication path of leaf i. Production code builds
// paths for leaf pairs instead; this one is only used to check the tree
// against gnark-crypto's reference implementation.
func (t merkleTree) proof(i int) [][]byte {
	res := make([][]byte, 0, len(t.levels))
	res = append(res, t.leaves[i])
	for l := range len(t.levels) - 1 {
		res = append(res, t.levels[l][i^1])
		i >>= 1
	}

	return res
}
