/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package blockscan_test

import (
	"math"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/blockscan"
	"github.com/stretchr/testify/assert"
)

func TestStartingBlock(t *testing.T) {
	for _, tc := range []struct {
		name      string
		lastBlock uint64
		expected  uint64
	}{
		{name: "empty chain", lastBlock: 0, expected: blockscan.FirstBlock},
		{name: "first block only", lastBlock: 1, expected: blockscan.FirstBlock},
		{name: "younger than the rewind window", lastBlock: 5, expected: blockscan.FirstBlock},
		{name: "rewind window not yet complete", lastBlock: 10, expected: blockscan.FirstBlock},
		{name: "exactly one full rewind window", lastBlock: 11, expected: blockscan.FirstBlock},
		{name: "just past the rewind window", lastBlock: 12, expected: 2},
		{name: "well past the rewind window", lastBlock: 100, expected: 90},
		{name: "max block", lastBlock: math.MaxUint64, expected: math.MaxUint64 - blockscan.NumberPastBlocks},
	} {
		t.Run(tc.name, func(t *testing.T) {
			startingBlock := blockscan.StartingBlock(tc.lastBlock)
			assert.Equal(t, tc.expected, startingBlock)
			assert.GreaterOrEqual(t, startingBlock, uint64(blockscan.FirstBlock),
				"a scan must never start before the first block")
			assert.LessOrEqual(t, startingBlock, max(tc.lastBlock, uint64(blockscan.FirstBlock)),
				"a scan must never start after the last known block")
		})
	}
}
