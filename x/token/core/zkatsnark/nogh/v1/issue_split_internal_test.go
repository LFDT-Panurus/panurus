/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSplitUpgradeTotal checks that an upgrade total is split into
// ceil(total/maxValue) values in [1, maxValue] that sum to it.
func TestSplitUpgradeTotal(t *testing.T) {
	for _, tc := range []struct {
		total, maxValue uint64
		want            []uint64
	}{
		{total: 1, maxValue: 255, want: []uint64{1}},
		{total: 255, maxValue: 255, want: []uint64{255}},
		{total: 256, maxValue: 255, want: []uint64{255, 1}},
		{total: 528, maxValue: 255, want: []uint64{255, 255, 18}},
		{total: 510, maxValue: 255, want: []uint64{255, 255}},
		{total: math.MaxUint64, maxValue: math.MaxUint64, want: []uint64{math.MaxUint64}},
		{total: 2*math.MaxUint32 + 7, maxValue: math.MaxUint32, want: []uint64{math.MaxUint32, math.MaxUint32, 7}},
	} {
		got, err := splitUpgradeTotal(tc.total, tc.maxValue, 256)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
		var sum uint64
		for _, v := range got {
			require.GreaterOrEqual(t, v, uint64(1))
			require.LessOrEqual(t, v, tc.maxValue)
			sum += v
		}
		require.Equal(t, tc.total, sum)
	}

	_, err := splitUpgradeTotal(0, 255, 256)
	require.EqualError(t, err, "total value is zero")
	_, err = splitUpgradeTotal(1, 0, 256)
	require.EqualError(t, err, "maximum token value is zero")
	_, err = splitUpgradeTotal(256*255, 255, 256)
	require.NoError(t, err)
	_, err = splitUpgradeTotal(256*255+1, 255, 256)
	require.EqualError(t, err, "total value [65281] needs [257] outputs of at most [255], exceeding the limit of [256] outputs")
}
