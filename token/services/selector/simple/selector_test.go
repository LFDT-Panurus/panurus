/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRetryBackoffIsJittered verifies the retry backoff is randomized over
// [0, timeout) instead of a constant sleep, so transactions that lost a race
// for the same funds don't all retry at the same instant.
func TestRetryBackoffIsJittered(t *testing.T) {
	timeout := 5 * time.Second
	s := &selector{timeout: timeout}

	seen := map[time.Duration]struct{}{}
	for range 100 {
		d := s.retryBackoff()
		require.GreaterOrEqual(t, d, time.Duration(0))
		require.Less(t, d, timeout)
		seen[d] = struct{}{}
	}
	require.Greater(t, len(seen), 1, "backoff must vary across retries, not be a constant")
}
