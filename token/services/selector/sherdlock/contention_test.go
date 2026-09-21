/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/selector/testutils"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"
)

// countingLocker decorates a Locker to record, per token ID, how many times
// Lock was attempted and how many of those attempts lost the race (returned
// driver.ErrTokenAlreadyLocked). It exists only for TestHotTokenContention,
// to turn the incident reported in #2395 (a handful of tokens absorbing the
// overwhelming majority of lock conflicts) into a number a test can assert
// on and a PR description can cite as a baseline.
type countingLocker struct {
	Locker
	mu        sync.Mutex
	attempts  map[token2.ID]int
	conflicts map[token2.ID]int
}

func newCountingLocker(l Locker) *countingLocker {
	return &countingLocker{
		Locker:    l,
		attempts:  make(map[token2.ID]int),
		conflicts: make(map[token2.ID]int),
	}
}

// Lock records the attempt against tokenID, then delegates to the wrapped
// Locker. A lost race (errors.Is(err, driver.ErrTokenAlreadyLocked)) is
// additionally counted as a conflict.
func (c *countingLocker) Lock(ctx context.Context, tokenID *token2.ID, consumerTxID transaction.ID, walletID string) error {
	err := c.Locker.Lock(ctx, tokenID, consumerTxID, walletID)

	c.mu.Lock()
	c.attempts[*tokenID]++
	if errors.Is(err, driver.ErrTokenAlreadyLocked) {
		c.conflicts[*tokenID]++
	}
	c.mu.Unlock()

	return err
}

// snapshot returns a defensive copy of the current attempts and conflicts,
// safe to read after all concurrent Lock calls have completed.
func (c *countingLocker) snapshot() (attempts, conflicts map[token2.ID]int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	attempts = make(map[token2.ID]int, len(c.attempts))
	maps.Copy(attempts, c.attempts)
	conflicts = make(map[token2.ID]int, len(c.conflicts))
	maps.Copy(conflicts, c.conflicts)

	return attempts, conflicts
}

// startManagersWithLockCounters is like startManagers, but wraps each
// replica's Locker in a countingLocker and returns the counters alongside
// the replicas, so a test can aggregate lock-conflict distribution across
// all replicas once the concurrent workload has finished.
func startManagersWithLockCounters(t *testing.T, number int, backoff time.Duration, maxRetries int) ([]testutils.EnhancedManager, []*countingLocker, func()) {
	t.Helper()
	terminate, pgConnStr := startContainer(t)
	replicas := make([]testutils.EnhancedManager, number)
	counters := make([]*countingLocker, number)

	for i := range number {
		var counter *countingLocker
		replica, err := createManagerWithLocker(t, pgConnStr, backoff, maxRetries, func(l Locker) Locker {
			counter = newCountingLocker(l)

			return counter
		})
		require.NoError(t, err)
		replicas[i] = replica
		counters[i] = counter
	}

	return replicas, counters, terminate
}

// TestHotTokenContention reproduces the incident reported in #2395 against a
// real Postgres-backed selector: a wallet with a few small tokens and one
// much larger, rotating hot token, and far more concurrent requests than
// tokens. It records, per token ID across all replicas, how many lock
// attempts were made and how many lost the race, so contention can be
// quantified rather than merely observed as an occasional flaky failure.
//
// The only hard assertion is the functional invariant that must hold no
// matter how contention is distributed: total demand exactly equals the
// wallet's total balance, so any Select error is spurious (contention-
// induced), never a genuine insufficient-funds. Phases 3-5 of #2395 are
// expected to reduce the conflict counts and the max single-token share
// logged here; this test's log output is the baseline they should be
// compared against.
func TestHotTokenContention(t *testing.T) {
	replicas, counters, terminate := startManagersWithLockCounters(t, 3, 2*time.Second, 60)
	defer terminate()

	testutils.TestHotTokenContention(t, replicas)

	totalAttempts, totalConflicts := 0, 0
	distinctTokensAttempted := make(map[token2.ID]struct{})
	perTokenConflicts := make(map[token2.ID]int)
	for _, c := range counters {
		attempts, conflicts := c.snapshot()
		for id, n := range attempts {
			totalAttempts += n
			distinctTokensAttempted[id] = struct{}{}
		}
		for id, n := range conflicts {
			totalConflicts += n
			perTokenConflicts[id] += n
		}
	}

	maxConflictsForToken := 0
	for _, n := range perTokenConflicts {
		if n > maxConflictsForToken {
			maxConflictsForToken = n
		}
	}

	maxShare := 0.0
	if totalConflicts > 0 {
		maxShare = float64(maxConflictsForToken) / float64(totalConflicts)
	}
	conflictRate := 0.0
	if totalAttempts > 0 {
		conflictRate = float64(totalConflicts) / float64(totalAttempts)
	}

	// conflictRate is the number that mirrors the CERT report's headline
	// figure (95.2% of lock violations): here it is the share of every lock
	// attempt, across all replicas, that lost the race. maxShare is diluted
	// by design: deleteTokensAndStoreChange mints a fresh token ID each time
	// the hot token is spent, so the same *lineage* of change stays hot
	// across the run without any single ID accumulating a large share.
	t.Logf(
		"#2395 contention baseline: distinct tokens attempted=%d, total lock attempts=%d, total conflicts=%d, conflict rate=%.2f, distinct tokens conflicted=%d, max single-token conflict share=%.2f",
		len(distinctTokensAttempted), totalAttempts, totalConflicts, conflictRate, len(perTokenConflicts), maxShare,
	)
}
