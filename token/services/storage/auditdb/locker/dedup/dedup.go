/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dedup

import (
	"slices"

	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
)

// AndSort returns the elements of source with duplicates removed and the result
// sorted in ascending order.
//
// Lockers call this before acquiring the per-enrollment-ID locks of a request.
// Removing duplicates avoids acquiring (and having to release) the same lock
// twice, and the canonical ascending order is what prevents deadlock: two
// concurrent acquisitions whose enrollment-ID sets intersect always take the
// shared locks in the same order, so they cannot block each other in a cycle.
func AndSort(source []string) []string {
	slice := collections.NewSet(source...).ToSlice()
	slices.Sort(slice)

	return slice
}

// Added returns the members of want that do not already appear in held, keeping
// want's order.
//
// Lockers use it to reconcile a re-acquisition against the set an anchor already
// holds. A non-empty result over a non-empty held set is a widening: the caller
// is asking to wait for new IDs while keeping the ones it holds, which is the
// hold-and-wait the sorted ordering above cannot protect against, because the
// held IDs were ordered against an earlier call's set rather than this one's.
func Added(want, held []string) []string {
	set := setOf(held)
	added := make([]string, 0, len(want))
	for _, id := range want {
		if _, ok := set[id]; !ok {
			added = append(added, id)
		}
	}

	return added
}

// Dropped returns the members of held that no longer appear in want, keeping
// held's order.
//
// These are the locks a narrowing re-acquisition must give up. Left in place they
// are unreachable, since the caller's only handle on them was the record the
// re-acquisition replaced.
func Dropped(held, want []string) []string {
	if len(held) == 0 {
		return nil
	}
	set := setOf(want)
	dropped := make([]string, 0, len(held))
	for _, id := range held {
		if _, ok := set[id]; !ok {
			dropped = append(dropped, id)
		}
	}

	return dropped
}

// setOf returns ids as a set for membership tests.
func setOf(ids []string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}

	return set
}
