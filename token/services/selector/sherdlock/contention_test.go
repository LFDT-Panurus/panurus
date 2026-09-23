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
	common5 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
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
//
// It also conditionally implements BatchLocker (via LockBatch below) when the
// wrapped Locker does: Go's interface embedding only promotes methods declared
// on the embedded interface itself, never methods the underlying concrete type
// happens to have, so without this the selector's s.locker.(BatchTokenLocker)
// assertion in selectInternal would always miss and every strategy would
// silently degrade to the single-token Lock path, making skipLocked
// indistinguishable from insert/onConflict in this test.
// roundTripCounter is implemented by postgres.TokenLockStore, exposing the round-trip and
// unique-violation counts described in tokenlock.go's Lock override doc comment. Declared
// here, rather than imported, so this test does not need to import the postgres package
// just to name the interface it type-asserts against.
type roundTripCounter interface {
	RoundTrips() int64
	UniqueViolations() int64
}

type countingLocker struct {
	Locker
	batch     BatchLocker
	instr     roundTripCounter
	mu        sync.Mutex
	attempts  map[token2.ID]int
	conflicts map[token2.ID]int
}

func newCountingLocker(l Locker) *countingLocker {
	c := &countingLocker{
		Locker:    l,
		attempts:  make(map[token2.ID]int),
		conflicts: make(map[token2.ID]int),
	}
	if bl, ok := l.(BatchLocker); ok {
		c.batch = bl
	}
	if rt, ok := l.(roundTripCounter); ok {
		c.instr = rt
	}

	return c
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

// LockBatch records an attempt against every candidate in tokenIDs, then delegates to the
// wrapped BatchLocker (falling back to Lock, one candidate at a time, if the wrapped Locker
// does not implement BatchLocker). Every candidate LockBatch does not return as won is
// counted as a conflict, mirroring Lock's driver.ErrTokenAlreadyLocked bookkeeping for the
// single-token path.
func (c *countingLocker) LockBatch(ctx context.Context, tokenIDs []*token2.ID, consumerTxID transaction.ID, walletID string) ([]*token2.ID, error) {
	if c.batch == nil {
		won := make([]*token2.ID, 0, len(tokenIDs))
		for _, id := range tokenIDs {
			if err := c.Lock(ctx, id, consumerTxID, walletID); err == nil {
				won = append(won, id)
			}
		}

		return won, nil
	}

	won, err := c.batch.LockBatch(ctx, tokenIDs, consumerTxID, walletID)

	wonSet := make(map[token2.ID]struct{}, len(won))
	for _, id := range won {
		wonSet[*id] = struct{}{}
	}

	c.mu.Lock()
	for _, id := range tokenIDs {
		c.attempts[*id]++
		if _, ok := wonSet[*id]; !ok {
			c.conflicts[*id]++
		}
	}
	c.mu.Unlock()

	return won, err
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

// singleTokenOnlyLocker hides any LockBatch method the wrapped Locker's concrete type happens
// to implement, so selectInternal's BatchTokenLocker type assertion misses and the selector
// falls back to the single-token Lock path - exactly the interface-embedding behaviour
// countingLocker's doc comment above relies on, used here in the opposite direction: to force
// the single-token path even against the real Postgres store, which always implements
// BatchLocker.
type singleTokenOnlyLocker struct {
	Locker
}

// startManagersWithSingleTokenLockCounters is like startManagersWithLockCounters, but forces
// every replica onto the single-token Lock path (see singleTokenOnlyLocker) - the only path
// that can produce a real server-side unique-constraint violation, and so the only path where
// TestHotTokenContention_SingleTokenLockPath's UniqueViolations assertion is meaningful.
func startManagersWithSingleTokenLockCounters(t *testing.T, number int, backoff time.Duration, maxRetries int, lockStrategy string) ([]testutils.EnhancedManager, []*countingLocker, func()) {
	t.Helper()
	terminate, pgConnStr := startContainer(t)
	replicas := make([]testutils.EnhancedManager, number)
	counters := make([]*countingLocker, number)

	for i := range number {
		var counter *countingLocker
		replica, err := createManagerWithLockerAndStrategy(t, pgConnStr, backoff, maxRetries, func(l Locker) Locker {
			counter = newCountingLocker(l)

			return &singleTokenOnlyLocker{Locker: counter}
		}, lockStrategy)
		require.NoError(t, err)
		replicas[i] = replica
		counters[i] = counter
	}

	return replicas, counters, terminate
}

// startManagersWithLockCounters is like startManagers, but wraps each replica's Locker in a
// countingLocker and returns the counters alongside the replicas, so a test can aggregate
// lock-conflict distribution across all replicas once the concurrent workload has finished.
// lockStrategy selects the Postgres lock-acquisition strategy (common5.ConfigKeyLockStrategy);
// empty keeps the default (common5.LockStrategyInsert).
func startManagersWithLockCounters(t *testing.T, number int, backoff time.Duration, maxRetries int, lockStrategy string) ([]testutils.EnhancedManager, []*countingLocker, func()) {
	t.Helper()
	terminate, pgConnStr := startContainer(t)
	replicas := make([]testutils.EnhancedManager, number)
	counters := make([]*countingLocker, number)

	for i := range number {
		var counter *countingLocker
		replica, err := createManagerWithLockerAndStrategy(t, pgConnStr, backoff, maxRetries, func(l Locker) Locker {
			counter = newCountingLocker(l)

			return counter
		}, lockStrategy)
		require.NoError(t, err)
		replicas[i] = replica
		counters[i] = counter
	}

	return replicas, counters, terminate
}

// startManagersWithLockCountersAndLockStore is startManagersWithLockCounters plus
// createManagerAndLockStoreWithStrategy's driver.TokenLockStore return: needed by
// TestStaticHotTokenContentionPareto, which both records per-token attempt/conflict counts via
// countingLocker and asserts via lockDB.ListLocks that every lock is released by the end of the
// run (see TestHotTokenContentionWithSettlement, whose lockDB requirement this mirrors). All
// replicas share one Postgres container and TablePrefix, so any one of their TokenLockStore
// instances sees every lock any replica took - the returned lockDB is arbitrarily the last one.
func startManagersWithLockCountersAndLockStore(t *testing.T, number int, backoff time.Duration, maxRetries int, lockStrategy string) ([]testutils.EnhancedManager, []*countingLocker, driver.TokenLockStore, func()) {
	t.Helper()
	terminate, pgConnStr := startContainer(t)
	replicas := make([]testutils.EnhancedManager, number)
	counters := make([]*countingLocker, number)
	var lockDB driver.TokenLockStore

	for i := range number {
		var counter *countingLocker
		replica, ldb, err := createManagerWithLockerStoreAndStrategy(t, pgConnStr, backoff, maxRetries, func(l Locker) Locker {
			counter = newCountingLocker(l)

			return counter
		}, lockStrategy)
		require.NoError(t, err)
		replicas[i] = replica
		counters[i] = counter
		lockDB = ldb
	}

	return replicas, counters, lockDB, terminate
}

// TestStaticHotTokenContentionPareto drives testutils.TestStaticHotTokenContentionPareto
// against a real Postgres-backed selector, and asserts the CERT-shaped concentration that
// TestHotTokenContention's rotating hot token cannot reproduce (see that test's doc comment
// and testutils.TestStaticHotTokenContentionPareto's): a small, static set of token IDs
// absorbing the large majority of lock conflicts, with at least one of them contested many
// times over. The concentration assertion is gated behind a floor on the absolute conflict
// count first: with too few total conflicts, any concentration percentage is either vacuous (a
// handful of conflicts landing on the hot set by chance) or meaningless to compute at all, so
// asserting a share before that floor is met would be near-unfalsifiable rather than a genuine
// check of the incident's shape.
func TestStaticHotTokenContentionPareto(t *testing.T) {
	replicas, counters, lockDB, terminate := startManagersWithLockCountersAndLockStore(t, 3, 2*time.Second, 60, "")
	defer terminate()

	hotIDs := testutils.TestStaticHotTokenContentionPareto(t, replicas, lockDB, 100)

	totalConflicts := 0
	perTokenConflicts := make(map[token2.ID]int)
	for _, c := range counters {
		_, conflicts := c.snapshot()
		for id, n := range conflicts {
			totalConflicts += n
			perTokenConflicts[id] += n
		}
	}

	hotConflicts := 0
	maxHotTokenConflicts := 0
	for _, id := range hotIDs {
		n := perTokenConflicts[id]
		hotConflicts += n
		if n > maxHotTokenConflicts {
			maxHotTokenConflicts = n
		}
	}

	hotShare := 0.0
	if totalConflicts > 0 {
		hotShare = float64(hotConflicts) / float64(totalConflicts)
	}
	t.Logf(
		"#2395 contention [static hot tokens]: total conflicts=%d, hot-set conflicts=%d, hot-set share=%.2f, max single hot-token conflicts=%d",
		totalConflicts, hotConflicts, hotShare, maxHotTokenConflicts,
	)

	const minTotalConflicts = 50
	require.GreaterOrEqual(t, totalConflicts, minTotalConflicts, "workload did not generate enough contention to make a concentration assertion meaningful")
	require.GreaterOrEqual(t, hotShare, 0.8, "expected the static hot set to absorb the large majority of lock conflicts (#2395)")
	require.GreaterOrEqual(t, maxHotTokenConflicts, 20, "expected at least one static hot token to be repeatedly contested, mirroring the incident's single-token repeat count")
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
// induced), never a genuine insufficient-funds. It runs once per Phase 6
// lock strategy (see common5.ConfigKeyLockStrategy) so the conflict numbers
// logged for each can be compared directly: LockStrategyInsert should
// reproduce the pre-Phase-6 baseline, LockStrategyOnConflict should hold a
// similar conflict rate while dropping server-side unique-constraint errors
// to zero, and LockStrategySkipLocked should cut the conflict rate and
// flatten the max single-token conflict share.
// runHotTokenContention starts number replicas under lockStrategy, runs scenario against
// them, and logs the same aggregate conflict metrics TestHotTokenContention has always
// logged (see its doc comment for what each number means), tagged with label so the two
// scenarios' lines are easy to tell apart in test output.
func runHotTokenContention(t *testing.T, label, strategy string, scenario func(t *testing.T, replicas []testutils.EnhancedManager)) {
	t.Helper()
	replicas, counters, terminate := startManagersWithLockCounters(t, 3, 2*time.Second, 60, strategy)
	defer terminate()

	scenario(t, replicas)

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

	// conflictRate is the number that mirrors the stress report's headline
	// figure (95.2% of lock violations): here it is the share of every lock
	// attempt, across all replicas, that lost the race. maxShare is diluted
	// by design: deleteTokensAndStoreChange mints a fresh token ID each time
	// the hot token is spent, so the same *lineage* of change stays hot
	// across the run without any single ID accumulating a large share.
	t.Logf(
		"#2395 contention [%s, strategy=%s]: distinct tokens attempted=%d, total lock attempts=%d, total conflicts=%d, conflict rate=%.2f, distinct tokens conflicted=%d, max single-token conflict share=%.2f",
		label, strategy, len(distinctTokensAttempted), totalAttempts, totalConflicts, conflictRate, len(perTokenConflicts), maxShare,
	)

	// roundTrips/uniqueViolations are the numbers that actually separate the strategies (see
	// tokenlock.go's roundTrips/uniqueViolations field doc comment): conflictRate above does
	// not move across strategies, since SKIP LOCKED only helps against a genuinely simultaneous
	// holder, not against an already-committed lock (the dominant conflict mode here).
	// roundTrips is expected to come out equal across strategies for the same workload here -
	// the selector always claims a covering window via LockBatch, one round trip per window
	// regardless of strategy, so batching (not strategy choice) is what saves round trips.
	// uniqueViolations is the real, hard count of the server-side errors that caused the CERT
	// log storm: it is 0 in every strategy in this benchmark, because that error is only
	// possible via the single-token Lock path under LockStrategyInsert, which a BatchLocker-
	// capable selector (this one) never takes - see TestHotTokenContention_SingleTokenLockPath
	// for that path exercised directly.
	var totalRoundTrips, totalUniqueViolations int64
	haveInstrumentation := false
	for _, c := range counters {
		if c.instr == nil {
			continue
		}
		haveInstrumentation = true
		totalRoundTrips += c.instr.RoundTrips()
		totalUniqueViolations += c.instr.UniqueViolations()
	}
	if haveInstrumentation {
		t.Logf(
			"#2395 contention [%s, strategy=%s]: DB round trips (Lock+LockBatch)=%d, unique-constraint violations=%d",
			label, strategy, totalRoundTrips, totalUniqueViolations,
		)
	}
}

func TestHotTokenContention(t *testing.T) {
	for _, strategy := range []string{common5.LockStrategyInsert, common5.LockStrategyOnConflict, common5.LockStrategySkipLocked} {
		t.Run(strategy, func(t *testing.T) {
			runHotTokenContention(t, "single-token", strategy, testutils.TestHotTokenContention)
		})
	}
}

// TestHotTokenContentionWideWindow is TestHotTokenContention's counterpart for the
// scenario Phase 6's skipLocked strategy actually targets: see
// testutils.TestHotTokenContentionWideWindow's doc comment for why its requests, unlike
// TestHotTokenContention's, force a covering window wider than one token.
func TestHotTokenContentionWideWindow(t *testing.T) {
	for _, strategy := range []string{common5.LockStrategyInsert, common5.LockStrategyOnConflict, common5.LockStrategySkipLocked} {
		t.Run(strategy, func(t *testing.T) {
			runHotTokenContention(t, "wide-window", strategy, testutils.TestHotTokenContentionWideWindow)
		})
	}
}

// TestHotTokenContention_SingleTokenLockPath exercises the single-token Lock path directly,
// via singleTokenOnlyLocker - the path a BatchLocker-incapable Locker still takes (a custom
// implementation, or a mixed-strategy rolling deploy before every replica upgrades). It is the
// only place LockStrategyInsert can produce a real server-side unique-constraint violation, and
// is the scenario TestHotTokenContention/TestHotTokenContentionWideWindow structurally cannot
// exercise, since their selector always claims a covering window via LockBatch (see
// runHotTokenContention's doc comment): LockStrategyInsert is expected to show unique-constraint
// violations > 0, matching the server error storm reported in #2395; LockStrategyOnConflict and
// LockStrategySkipLocked are expected to show exactly 0, since a lost race there is a clean
// zero-row result instead of a server-side error.
func TestHotTokenContention_SingleTokenLockPath(t *testing.T) {
	for _, strategy := range []string{common5.LockStrategyInsert, common5.LockStrategyOnConflict, common5.LockStrategySkipLocked} {
		t.Run(strategy, func(t *testing.T) {
			replicas, counters, terminate := startManagersWithSingleTokenLockCounters(t, 3, 2*time.Second, 60, strategy)
			defer terminate()

			testutils.TestHotTokenContention(t, replicas)

			var totalViolations int64
			for _, c := range counters {
				require.NotNil(t, c.instr, "expected the wrapped store to implement roundTripCounter")
				totalViolations += c.instr.UniqueViolations()
			}
			t.Logf("#2395 contention [single-token Lock path, strategy=%s]: unique-constraint violations=%d", strategy, totalViolations)

			if strategy == common5.LockStrategyInsert {
				require.Positive(t, totalViolations, "expected LockStrategyInsert's single-token Lock path to surface real server-side unique-constraint violations")
			} else {
				require.Zero(t, totalViolations, "expected %s's single-token Lock path to never surface a real server-side unique-constraint violation", strategy)
			}
		})
	}
}

// TestHotTokenContentionWithSettlement drives testutils.TestHotTokenContentionWithSettlement
// against a real Postgres-backed selector: unlike TestHotTokenContention, which never calls
// Unlock and so can only simulate mechanism 4's leak (#2395), this wires each replica's
// Manager into a real finality.SelectorManagerProvider chain and releases every winning
// transaction's locks through it, then asserts via lockDB.ListLocks that nothing remains.
// All replicas share one Postgres container and TablePrefix, so any one of their
// TokenLockStore instances sees every lock any replica took.
func TestHotTokenContentionWithSettlement(t *testing.T) {
	terminate, pgConnStr := startContainer(t)
	defer terminate()

	const numReplicas = 3
	replicas := make([]testutils.EnhancedManager, numReplicas)
	var lockDB driver.TokenLockStore
	for i := range numReplicas {
		replica, ldb, err := createManagerAndLockStoreWithStrategy(t, pgConnStr, 2*time.Second, 60, "")
		require.NoError(t, err)
		replicas[i] = replica
		lockDB = ldb
	}

	testutils.TestHotTokenContentionWithSettlement(t, replicas, lockDB)
}
