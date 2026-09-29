/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common_test

import (
	"math"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
)

// TestFormatOffsetSeconds covers the truncation reported in #2043: the
// interpreters rendered int(math.Abs(d.Seconds())), which silently dropped any
// sub-second part - a 500ms offset became 0 ("now"), so every row looked older
// than the lease, and 1.5s became 1s.
func TestFormatOffsetSeconds(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	for _, tc := range []struct {
		name     string
		duration time.Duration
		seconds  string
		whole    bool
	}{
		{"zero", 0, "0", true},
		{"whole seconds", 5 * time.Second, "5", true},
		{"whole seconds, negative", -5 * time.Second, "5", true},
		{"minutes are whole seconds", 10 * time.Minute, "600", true},
		{"sub-second is kept", 500 * time.Millisecond, "0.5", false},
		{"sub-second is kept, negative", -500 * time.Millisecond, "0.5", false},
		{"fraction above a second is kept", 1500 * time.Millisecond, "1.5", false},
		{"millisecond", time.Millisecond, "0.001", false},
		{"nanosecond does not round to zero", time.Nanosecond, "0.000000001", false},
		// Past 2^53 nanoseconds (~104 days) a float64 can no longer hold every
		// nanosecond, so rendering the literal from duration.Seconds() could emit
		// a fraction for a duration the whole-second flag calls whole.
		{"whole seconds past float64 precision", 9223372036 * time.Second, "9223372036", true},
		// Duration.Abs saturates the most negative duration to the most positive
		// one instead of overflowing back to a negative magnitude, which would
		// render a "-" into the SQL literal.
		{"most negative duration does not overflow", time.Duration(math.MinInt64), "9223372036.854775807", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seconds, whole := common.FormatOffsetSeconds(tc.duration)
			Expect(seconds).To(Equal(tc.seconds))
			Expect(whole).To(Equal(tc.whole))
		})
	}
}
