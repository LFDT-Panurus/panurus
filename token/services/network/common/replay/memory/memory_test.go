/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package memory_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/network/common/replay"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/replay/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGuard_FirstCheckSucceeds(t *testing.T) {
	g := memory.New(0, time.Minute, 0)

	err := g.Check(context.Background(), replay.Key{
		TxID:      "tx1",
		Creator:   []byte("creator1"),
		Nonce:     []byte("nonce1"),
		Timestamp: time.Now(),
	})

	require.NoError(t, err)
}

func TestGuard_DuplicateCheckFails(t *testing.T) {
	g := memory.New(0, time.Minute, 0)
	key := replay.Key{
		TxID:      "tx1",
		Creator:   []byte("creator1"),
		Nonce:     []byte("nonce1"),
		Timestamp: time.Now(),
	}

	require.NoError(t, g.Check(context.Background(), key))
	err := g.Check(context.Background(), key)

	require.ErrorIs(t, err, replay.ErrAlreadyProcessed)
}

func TestGuard_DistinctKeysDoNotCollide(t *testing.T) {
	g := memory.New(0, time.Minute, 0)
	base := replay.Key{
		TxID:      "tx1",
		Creator:   []byte("creator1"),
		Nonce:     []byte("nonce1"),
		Timestamp: time.Now(),
	}
	require.NoError(t, g.Check(context.Background(), base))

	variants := []replay.Key{
		{TxID: "tx2", Creator: base.Creator, Nonce: base.Nonce, Timestamp: base.Timestamp},
		{TxID: base.TxID, Creator: []byte("creator2"), Nonce: base.Nonce, Timestamp: base.Timestamp},
		{TxID: base.TxID, Creator: base.Creator, Nonce: []byte("nonce2"), Timestamp: base.Timestamp},
		{TxID: base.TxID, Creator: base.Creator, Nonce: base.Nonce, Timestamp: base.Timestamp.Add(time.Second)},
	}
	for _, v := range variants {
		assert.NoError(t, g.Check(context.Background(), v))
	}
}

func TestGuard_EntryExpiresAfterTTL(t *testing.T) {
	g := memory.New(0, 10*time.Millisecond, 0)
	key := replay.Key{
		TxID:      "tx1",
		Creator:   []byte("creator1"),
		Nonce:     []byte("nonce1"),
		Timestamp: time.Now(),
	}

	require.NoError(t, g.Check(context.Background(), key))
	require.Eventually(t, func() bool {
		return g.Check(context.Background(), key) == nil
	}, time.Second, 5*time.Millisecond)
}

func TestGuard_MaxEntriesEvictsOldest(t *testing.T) {
	g := memory.New(0, time.Minute, 1)
	first := replay.Key{TxID: "tx1", Creator: []byte("c"), Nonce: []byte("n"), Timestamp: time.Now()}
	second := replay.Key{TxID: "tx2", Creator: []byte("c"), Nonce: []byte("n"), Timestamp: time.Now()}

	require.NoError(t, g.Check(context.Background(), first))
	require.NoError(t, g.Check(context.Background(), second))

	// first was evicted to make room for second, so it is no longer remembered.
	require.NoError(t, g.Check(context.Background(), first))
}

func TestGuard_ConcurrentDuplicateChecksOnlyOneSucceeds(t *testing.T) {
	g := memory.New(0, time.Minute, 0)
	key := replay.Key{
		TxID:      "tx1",
		Creator:   []byte("creator1"),
		Nonce:     []byte("nonce1"),
		Timestamp: time.Now(),
	}

	const attempts = 50
	var wg sync.WaitGroup
	var successes int
	var mu sync.Mutex
	wg.Add(attempts)
	for range attempts {
		go func() {
			defer wg.Done()
			if g.Check(context.Background(), key) == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, successes)
}

func TestGuard_TimestampTooOldIsRejected(t *testing.T) {
	now := time.Now()
	g := memory.New(time.Minute, time.Hour, 0, memory.WithClock(func() time.Time { return now }))

	err := g.Check(context.Background(), replay.Key{
		TxID:      "tx1",
		Creator:   []byte("creator1"),
		Nonce:     []byte("nonce1"),
		Timestamp: now.Add(-2 * time.Minute),
	})

	require.ErrorIs(t, err, replay.ErrOutOfWindow)
}

func TestGuard_TimestampTooFarInFutureIsRejected(t *testing.T) {
	now := time.Now()
	g := memory.New(time.Minute, time.Hour, 0, memory.WithClock(func() time.Time { return now }))

	err := g.Check(context.Background(), replay.Key{
		TxID:      "tx1",
		Creator:   []byte("creator1"),
		Nonce:     []byte("nonce1"),
		Timestamp: now.Add(2 * time.Minute),
	})

	require.ErrorIs(t, err, replay.ErrOutOfWindow)
}

func TestGuard_TimestampWithinWindowIsAccepted(t *testing.T) {
	now := time.Now()
	g := memory.New(time.Minute, time.Hour, 0, memory.WithClock(func() time.Time { return now }))

	err := g.Check(context.Background(), replay.Key{
		TxID:      "tx1",
		Creator:   []byte("creator1"),
		Nonce:     []byte("nonce1"),
		Timestamp: now.Add(30 * time.Second),
	})

	require.NoError(t, err)
}

func TestGuard_WindowMovesWithClock(t *testing.T) {
	current := time.Now()
	g := memory.New(time.Minute, time.Hour, 0, memory.WithClock(func() time.Time { return current }))
	key := replay.Key{
		TxID:      "tx1",
		Creator:   []byte("creator1"),
		Nonce:     []byte("nonce1"),
		Timestamp: current,
	}

	require.NoError(t, g.Check(context.Background(), key), "sanity: timestamp equal to now must be in-window")

	current = current.Add(2 * time.Minute)
	err := g.Check(context.Background(), replay.Key{
		TxID:      "tx2",
		Creator:   []byte("creator1"),
		Nonce:     []byte("nonce1"),
		Timestamp: key.Timestamp,
	})

	require.ErrorIs(t, err, replay.ErrOutOfWindow, "key's timestamp is now outside the window that moved forward with the clock")
}

func TestGuard_ZeroWindowDisablesFreshnessCheck(t *testing.T) {
	g := memory.New(0, time.Minute, 0)

	err := g.Check(context.Background(), replay.Key{
		TxID:      "tx1",
		Creator:   []byte("creator1"),
		Nonce:     []byte("nonce1"),
		Timestamp: time.Now().Add(-24 * time.Hour),
	})

	require.NoError(t, err)
}
