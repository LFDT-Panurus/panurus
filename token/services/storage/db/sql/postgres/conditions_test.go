/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
)

// TestTimeOffset pins the rendering of an interval offset. Whole-second offsets
// must keep the exact text they had before #2043, since every store's age
// comparison already relies on it; sub-second offsets must survive instead of
// truncating to zero.
func TestTimeOffset(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{"zero renders bare NOW()", 0, "NOW()"},
		{"past", -5 * time.Second, "NOW() - INTERVAL '5 seconds'"},
		{"future", 10 * time.Minute, "NOW() + INTERVAL '600 seconds'"},
		{"sub-second past", -500 * time.Millisecond, "NOW() - INTERVAL '0.5 seconds'"},
		{"fractional future", 1500 * time.Millisecond, "NOW() + INTERVAL '1.5 seconds'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sb := common.NewBuilder()
			NewConditionInterpreter().TimeOffset(tc.duration, sb)
			query, params := sb.Build()
			require.Equal(t, tc.expected, query)
			require.Empty(t, params)
		})
	}
}
