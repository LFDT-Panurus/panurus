/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFanOutLatencyTracksSlowestParty verifies that fanOut contacts all parties
// concurrently: with 10 workers each taking ~200ms, the total must be far below
// the ~2s a serial loop would take.
func TestFanOutLatencyTracksSlowestParty(t *testing.T) {
	const n = 10
	const delay = 200 * time.Millisecond

	start := time.Now()
	results, err := fanOut(t.Context(), n, func(i int) (int, error) {
		time.Sleep(delay)

		return i * 2, nil
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.Len(t, results, n)
	for i, r := range results {
		assert.Equal(t, i*2, r, "results must be returned in index order")
	}
	assert.Less(t, elapsed, n*delay/2,
		"latency must track the slowest party, not the sum of all parties")
}

// TestFanOutFailsFastOnFirstError verifies that a failing party unblocks fanOut
// immediately instead of waiting for the slow ones still in flight.
func TestFanOutFailsFastOnFirstError(t *testing.T) {
	slow := 3 * time.Second

	start := time.Now()
	_, err := fanOut(t.Context(), 3, func(i int) ([]byte, error) {
		if i == 0 {
			return nil, errors.New("party unreachable")
		}
		time.Sleep(slow)

		return []byte("sigma"), nil
	})
	elapsed := time.Since(start)

	require.ErrorContains(t, err, "party unreachable")
	assert.Less(t, elapsed, slow/2,
		"first error must stop the wait without draining the slow parties")
}

// TestFanOutContextCancellation verifies that a cancelled context unblocks the
// collection while workers are still in flight.
func TestFanOutContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := fanOut(ctx, 2, func(int) (int, error) {
		time.Sleep(3 * time.Second)

		return 0, nil
	})
	require.Error(t, err)
}

// TestFanOutEmpty verifies the no-remote-parties case is a no-op.
func TestFanOutEmpty(t *testing.T) {
	results, err := fanOut(t.Context(), 0, func(int) (int, error) {
		t.Fatal("work must not be invoked for n = 0")

		return 0, nil
	})
	require.NoError(t, err)
	assert.Empty(t, results)
}
