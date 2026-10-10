/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScannableTime_Scan covers every shape a created_at value can reach scannableTime in.
//
// Postgres hands the database/sql driver a native time.Time, but sqlite does that only for
// columns declared DATE/DATETIME/TIMESTAMP, and the shared schema declares created_at as
// TIMESTAMPTZ, so on sqlite the value comes back as the raw text the driver wrote. Which text
// that is depends on the writer and on the driver's _time_format, so pinning the scanner to the
// single layout modernc.org/sqlite happens to default to today makes ListLocks fail on any
// value that deviates - most sharply on a time.Time that still carries a monotonic reading,
// whose String() form gains a trailing " m=±<seconds>" that no layout matches. Writers strip
// that today (TokenLockStore.LockAt binds createdAt.UTC(), and Time.UTC() drops the monotonic
// reading), but the scanner should not depend on every present and future writer remembering to.
func TestScannableTime_Scan(t *testing.T) {
	// A fixed instant, in a zone with a numeric offset, so the abbreviation-less layouts are
	// exercised on something other than UTC.
	instant := time.Date(2026, 9, 29, 13, 55, 28, 282000000, time.FixedZone("", 2*60*60))
	monotonic := time.Now()

	for _, tc := range []struct {
		name     string
		src      any
		expected time.Time
		errMsg   string
	}{
		{
			name:     "native time.Time from Postgres",
			src:      instant,
			expected: instant,
		},
		{
			name:     "time.Time.String, the modernc.org/sqlite default",
			src:      instant.UTC().Format("2006-01-02 15:04:05.999999999 -0700 MST"),
			expected: instant,
		},
		{
			name: "time.Time.String carrying a monotonic reading",
			// What a writer that forgets .UTC()/.Round(0) produces: String() appends the
			// monotonic clock reading, which is not part of any layout.
			src:      monotonic.String(),
			expected: monotonic.Round(0),
		},
		{
			name:     "no zone abbreviation",
			src:      instant.Format("2006-01-02 15:04:05.999999999 -0700"),
			expected: instant,
		},
		{
			name:     "RFC3339 with nanoseconds",
			src:      instant.Format(time.RFC3339Nano),
			expected: instant,
		},
		{
			name:     "space-separated with a colon offset",
			src:      instant.Format("2006-01-02 15:04:05.999999999-07:00"),
			expected: instant,
		},
		{
			name:     "no zone at all, read back as UTC",
			src:      "2026-09-29 13:55:28.282",
			expected: time.Date(2026, 9, 29, 13, 55, 28, 282000000, time.UTC),
		},
		{
			name:     "second precision only",
			src:      "2026-09-29 13:55:28",
			expected: time.Date(2026, 9, 29, 13, 55, 28, 0, time.UTC),
		},
		{
			name:     "bytes rather than string",
			src:      []byte(instant.UTC().Format("2006-01-02 15:04:05.999999999 -0700 MST")),
			expected: instant,
		},
		{
			name:   "unparseable text",
			src:    "not a timestamp",
			errMsg: "not a timestamp",
		},
		{
			name:   "unsupported type",
			src:    42,
			errMsg: "cannot scan value of type [int]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got scannableTime
			err := got.Scan(tc.src)
			if tc.errMsg != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errMsg)

				return
			}
			require.NoError(t, err)
			assert.True(t, tc.expected.Equal(got.Time),
				"expected the instant %s, got %s", tc.expected.Format(time.RFC3339Nano), got.Format(time.RFC3339Nano))
		})
	}
}
