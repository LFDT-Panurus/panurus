/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"fmt"
	"io"
	"strconv"
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
)

// permutationItems builds n tokens for BenchmarkNewPermutation and the direct NewPermutation
// unit tests below: "uniform" puts every item in one bucket (the worst case for the shuffle,
// since rand.Shuffle sees the whole slice as a single run), "distinct" gives every item a
// different Quantity (the common case for a realistic wallet, where the shuffle is a no-op
// because every bucket has size 1 and NewPermutation's cost is dominated by the slice copy).
func permutationItems(n int, shape string) []*token2.UnspentTokenInWallet {
	items := make([]*token2.UnspentTokenInWallet, n)
	for i := range items {
		q := "1"
		if shape == "distinct" {
			q = strconv.Itoa(i)
		}
		items[i] = &token2.UnspentTokenInWallet{
			Id:       token2.ID{TxId: "tx-" + strconv.Itoa(i)},
			Quantity: q,
		}
	}

	return items
}

// BenchmarkNewPermutation measures the cost bucketedIterator.NewPermutation pays on every
// cache hit under the eager/mixed fetcher strategies: it rand.Shuffles the entire cached
// candidate list, not just the tokens a Select actually needs. See docs/services/selector.md
// for the measured scaling characteristic at realistic wallet sizes.
func BenchmarkNewPermutation(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000, 1_000_000} {
		for _, shape := range []string{"uniform", "distinct"} {
			b.Run(fmt.Sprintf("tokens=%d/%s", n, shape), func(b *testing.B) {
				items := permutationItems(n, shape)
				it := newBucketedIterator(items)
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					_ = it.NewPermutation()
				}
			})
		}
	}
}

// TestNewPermutation_PreservesAcrossBucketOrder checks the part of the contract that is
// independent of any shuffling: items of distinct amounts come back in the same ascending
// order NewPermutation was handed, whatever happens within each equal-amount run.
func TestNewPermutation_PreservesAcrossBucketOrder(t *testing.T) {
	items := []*token2.UnspentTokenInWallet{
		{Id: token2.ID{TxId: "a"}, Quantity: "1"},
		{Id: token2.ID{TxId: "b"}, Quantity: "2"},
		{Id: token2.ID{TxId: "c1"}, Quantity: "3"},
		{Id: token2.ID{TxId: "c2"}, Quantity: "3"},
		{Id: token2.ID{TxId: "d"}, Quantity: "4"},
	}
	perm := newBucketedIterator(items).NewPermutation()

	var gotQuantities []string
	for {
		item, err := perm.Next()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if item == nil {
			break
		}
		gotQuantities = append(gotQuantities, item.Quantity)
	}

	want := []string{"1", "2", "3", "3", "4"}
	if len(gotQuantities) != len(want) {
		t.Fatalf("got %d items, want %d: %v", len(gotQuantities), len(want), gotQuantities)
	}
	for i, q := range want {
		if gotQuantities[i] != q {
			t.Fatalf("position %d: got quantity %s, want %s (full: %v)", i, gotQuantities[i], q, gotQuantities)
		}
	}
}

// TestNewPermutation_FullDrainYieldsEveryElementExactlyOnce checks that shuffling a single
// bucket never drops or duplicates an element, run repeatedly since the shuffle is randomized.
func TestNewPermutation_FullDrainYieldsEveryElementExactlyOnce(t *testing.T) {
	const n = 1000
	items := permutationItems(n, "uniform")

	for trial := range 50 {
		perm := newBucketedIterator(items).NewPermutation()
		seen := make(map[string]int, n)
		for {
			item, err := perm.Next()
			if err != nil {
				t.Fatalf("trial %d: unexpected error: %v", trial, err)
			}
			if item == nil {
				break
			}
			seen[item.Id.TxId]++
		}
		if len(seen) != n {
			t.Fatalf("trial %d: got %d distinct elements, want %d", trial, len(seen), n)
		}
		for txID, count := range seen {
			if count != 1 {
				t.Fatalf("trial %d: element %s seen %d times, want exactly 1", trial, txID, count)
			}
		}
	}
}

// TestNewPermutation_ExhaustionNeverReturnsIOEOF pins the exhaustion contract documented on
// bucketedIterator.Next: sherdlock.Iterator signals exhaustion with (nil, nil), never io.EOF -
// a caller that treats io.EOF as a hard failure would break on every refetch cycle otherwise.
func TestNewPermutation_ExhaustionNeverReturnsIOEOF(t *testing.T) {
	perm := newBucketedIterator(permutationItems(3, "uniform")).NewPermutation()

	for range 3 {
		item, err := perm.Next()
		if err != nil {
			t.Fatalf("unexpected error before exhaustion: %v", err)
		}
		if item == nil {
			t.Fatalf("got nil item before exhaustion")
		}
	}

	for range 5 {
		item, err := perm.Next()
		if err != nil {
			t.Fatalf("got error %v after exhaustion, want nil (never io.EOF: %v)", err, io.EOF)
		}
		if item != nil {
			t.Fatalf("got non-nil item %v after exhaustion, want nil forever", item)
		}
	}
}

// TestNewPermutation_RandomizationSpreadsAcrossCandidates checks that the shuffle itself does
// something observable: across many trials on a single 32-element bucket, more than a handful
// of distinct elements appear first. A no-op permutation would return the same first element
// every single trial.
func TestNewPermutation_RandomizationSpreadsAcrossCandidates(t *testing.T) {
	const n = 32
	const numTrials = 200
	items := permutationItems(n, "uniform")

	firstSeen := make(map[string]int)
	for range numTrials {
		perm := newBucketedIterator(items).NewPermutation()
		item, err := perm.Next()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if item == nil {
			t.Fatalf("got nil item on a non-empty bucket")
		}
		firstSeen[item.Id.TxId]++
	}

	if len(firstSeen) <= 4 {
		t.Fatalf("expected randomization to spread the first-picked element across more than a "+
			"handful of the %d candidates over %d trials, got only %d distinct first picks: %v",
			n, numTrials, len(firstSeen), firstSeen)
	}
}
