/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package blockscan holds the parameters shared by the Fabric services that fall back to
// scanning past blocks when a lookup misses on the local view of the ledger.
package blockscan

const (
	// NumberPastBlocks is how far back a fallback block scan rewinds from the last known block.
	NumberPastBlocks = 10
	// FirstBlock is the earliest block a fallback scan may start from.
	FirstBlock = 1
)

// StartingBlock returns the block a fallback scan should start from, given the last known block.
// The subtraction is underflow-safe: on a chain shorter than NumberPastBlocks the result is clamped
// to FirstBlock instead of wrapping around to a block number near MaxUint64, which would make the
// scan silently find nothing.
func StartingBlock(lastBlock uint64) uint64 {
	if lastBlock < FirstBlock+NumberPastBlocks {
		return FirstBlock
	}

	return lastBlock - NumberPastBlocks
}
