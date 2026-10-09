/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generationalFetcher hands out a brand-new candidate set on every fetch and remembers which
// fetch ("generation") each token was minted by, so a test can tell which cache snapshot a
// given lock attempt drew its candidate from. The generation number is the whole point: token
// identity alone cannot distinguish "re-offered by a fresh fetch" from "left over in
// nextCandidate's lookahead buffer", because in production both yield the same token ids.
type generationalFetcher struct {
	mu         sync.Mutex
	generation int
	origin     map[token2.ID]int
}

func newGenerationalFetcher() *generationalFetcher {
	return &generationalFetcher{origin: make(map[token2.ID]int)}
}

// tokensPerGeneration is three so that every generation leaves leftovers in s.pending:
// nextCandidate peeks up to sufficiencyWindow (4) candidates and buffers all but the one it
// returns, so a three-token generation always buffers two. One token per generation would
// leave s.pending empty and make the bug under test unobservable.
const tokensPerGeneration = 3

func (f *generationalFetcher) nextGeneration() []*token2.UnspentTokenInWallet {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.generation++
	tokens := make([]*token2.UnspentTokenInWallet, 0, tokensPerGeneration)
	for i := range tokensPerGeneration {
		tok := &token2.UnspentTokenInWallet{
			Id:       token2.ID{TxId: fmt.Sprintf("gen%d-tx%d", f.generation, i), Index: 0},
			Type:     "ABC",
			Quantity: "10",
		}
		f.origin[tok.Id] = f.generation
		tokens = append(tokens, tok)
	}

	return tokens
}

// generationOf reports the fetch that minted id, or 0 if no fetch ever did.
func (f *generationalFetcher) generationOf(id token2.ID) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.origin[id]
}

func (f *generationalFetcher) tokenFetcher() *mocks.FakeTokenFetcher {
	m := &mocks.FakeTokenFetcher{}
	m.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
		return &sliceIterator{items: f.nextGeneration()}, nil
	}
	m.HasEnoughSpendableTokensReturns(true, nil)

	return m
}

// TestStubbornSelector_BackoffLegDoesNotReuseLookaheadBufferedCandidates pins the lifecycle of
// nextCandidate's lookahead buffer across a StubbornSelector's backoff legs.
//
// nextCandidate peeks several individually-sufficient candidates, returns one at random and
// buffers the rest in s.pending. refreshCandidates drops that buffer whenever it installs a
// fresh cache — the buffered candidates were peeked from the cache being replaced, so the
// fresh, fully-ordered set supersedes them. But its budget-exceeded early return happens
// *before* any of that, so the leg that gives up on token.SelectorSufficientButLockedFunds
// leaves the buffer populated. The whole point of the backoff that follows is to re-examine the
// world after other processes have had a chance to release their locks, so the next leg must
// start from a fresh fetch; instead it silently consumed the candidates left over from the
// pre-backoff snapshot, bypassing both the refetch and the fresh set's own randomized
// sufficiency window.
//
// The assertion is that lock attempts walk strictly forward through fetch generations: one
// attempt per fetch, never an attempt against a generation that has already been attempted.
// With the buffer left in place, the second leg's first attempt replays the last generation of
// the first leg, so the sequence stalls instead of advancing.
func TestStubbornSelector_BackoffLegDoesNotReuseLookaheadBufferedCandidates(t *testing.T) {
	_, metrics := setupMetricsMocks()

	fetcher := newGenerationalFetcher()
	// failUntil < 0 fails every batch with a generic store error, which is the reachable route
	// to the budget-exceeded early return with a non-empty s.pending: the store-error branch
	// refetches after a window was assembled by nextCandidate's lookahead, so the budget runs
	// out at a point where candidates are still buffered. (The other caller of
	// refreshCandidates, the exhausted-cache branch, is only reached once dequeue has already
	// drained s.pending, so it can never abort with anything buffered.)
	locker := &recordingBatchLocker{failUntil: -1}

	s := sherdlock.NewStubbornSelector(sherdlock.Logger(), fetcher.tokenFetcher(), locker, 64, time.Millisecond, 1, metrics)
	_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "5", "ABC")
	require.Error(t, err)
	require.True(t, errors.Is(err, token.SelectorInsufficientFunds),
		"a permanently failing store must exhaust both legs and report the retries-exhausted error, got: %v", err)

	windows := locker.windows()
	require.NotEmpty(t, windows, "the selector must have attempted at least one batch lock")

	attemptedGeneration := 0
	for i, window := range windows {
		require.Len(t, window, 1, "one 10-unit token already covers the request of 5, so no window needs growing")
		generation := fetcher.generationOf(window[0])
		require.NotZero(t, generation, "attempt %d locked token [%v], which no fetch ever produced", i, window[0])
		assert.Greater(t, generation, attemptedGeneration,
			"attempt %d drew a candidate from fetch generation %d, which had already been attempted: the backoff leg "+
				"started from candidates buffered in s.pending against the pre-backoff cache snapshot instead of from a fresh fetch",
			i, generation)
		attemptedGeneration = generation
	}
}

// scanTracker watches how much of each candidate cache the selector consumes. It hands out one
// counting iterator per fetch and attributes every cache read, and every lock attempt, to the
// generation that was current when it happened.
type scanTracker struct {
	mu          sync.Mutex
	generations []*scanStats
	items       []*token2.UnspentTokenInWallet
}

// scanStats is one fetch's worth of bookkeeping: how many times its iterator was read, and how
// many lock attempts were made while it was the installed cache.
type scanStats struct {
	reads    int
	tryLocks int
}

func newScanTracker(items []*token2.UnspentTokenInWallet) *scanTracker {
	return &scanTracker{items: items}
}

func (s *scanTracker) current() *scanStats {
	if len(s.generations) == 0 {
		return nil
	}

	return s.generations[len(s.generations)-1]
}

func (s *scanTracker) fetcher() *mocks.FakeTokenFetcher {
	m := &mocks.FakeTokenFetcher{}
	m.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
		s.mu.Lock()
		defer s.mu.Unlock()

		stats := &scanStats{}
		s.generations = append(s.generations, stats)

		return &countingIterator{items: s.items, tracker: s, stats: stats}, nil
	}
	m.HasEnoughSpendableTokensReturns(true, nil)

	return m
}

// locker denies every lock as an ordinary lost race, so the blacklist fills up with the whole
// wallet and the scan that follows the next refetch sees nothing but already-lost candidates.
func (s *scanTracker) locker() *mocks.FakeTokenLocker {
	m := &mocks.FakeTokenLocker{}
	m.TryLockStub = func(context.Context, *token2.ID, string) (bool, error) {
		s.mu.Lock()
		if stats := s.current(); stats != nil {
			stats.tryLocks++
		}
		s.mu.Unlock()

		return false, errors.Wrapf(driver.ErrTokenAlreadyLocked, "held by another process")
	}

	return m
}

func (s *scanTracker) stats() []scanStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]scanStats, 0, len(s.generations))
	for _, g := range s.generations {
		out = append(out, *g)
	}

	return out
}

// countingIterator is a sliceIterator that reports every read to its generation's stats,
// including the reads past the end that return a nil candidate: those are exactly what a
// re-walk of an already-exhausted cache shows up as.
type countingIterator struct {
	items   []*token2.UnspentTokenInWallet
	pos     int
	tracker *scanTracker
	stats   *scanStats
}

func (i *countingIterator) Next() (*token2.UnspentTokenInWallet, error) {
	i.tracker.mu.Lock()
	i.stats.reads++
	i.tracker.mu.Unlock()

	if i.pos >= len(i.items) {
		return nil, nil
	}
	t := i.items[i.pos]
	i.pos++

	return t, nil
}

func (i *countingIterator) Close() {}

// TestSelector_BlacklistedCandidatesAreSkippedNotWindowed pins that nextCandidate keeps tokens
// this Select call has already lost a lock race on out of its sufficiency window.
//
// The window exists to spread contention: on finding a candidate that covers the request on its
// own, nextCandidate peeks up to sufficiencyWindow of them and returns one at random. A
// blacklisted token cannot be locked by this call at all, so letting it into that window spends
// part of the randomized pick on a guaranteed no-op - diluting the very spread the window
// provides - and then re-buffers it into s.pending, where the next call dequeues it, builds
// another window around it, and skips it again. The result is that a scan over a cache whose
// every candidate is already blacklisted re-walks that cache once per candidate instead of once.
//
// The locker denies every lock as a lost race, so the blacklist fills with the whole wallet and
// the scan after the next refetch sees nothing but blacklisted candidates. Such a scan attempts
// no lock at all, which is what identifies it here, and it must read its cache exactly once:
// one read per already-lost candidate, plus the terminating nil.
func TestSelector_BlacklistedCandidatesAreSkippedNotWindowed(t *testing.T) {
	_, metrics := setupMetricsMocks()

	// Three equal, individually-sufficient tokens: every one of them is an anchor that triggers
	// the lookahead, and three is under the sufficiencyWindow cap of 4, so a window built from
	// this cache spans the whole wallet.
	items := make([]*token2.UnspentTokenInWallet, 0, tokensPerGeneration)
	for i := range tokensPerGeneration {
		items = append(items, &token2.UnspentTokenInWallet{
			Id:       token2.ID{TxId: fmt.Sprintf("tx%d", i), Index: 0},
			Type:     "ABC",
			Quantity: "10",
		})
	}

	tracker := newScanTracker(items)
	s := sherdlock.NewSelector(sherdlock.Logger(), tracker.fetcher(), tracker.locker(), 64, metrics)
	_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "5", "ABC")
	require.Error(t, err)
	require.True(t, errors.Is(err, token.SelectorSufficientButLockedFunds),
		"a wallet whose every token is held elsewhere must report locked funds, got: %v", err)

	generations := tracker.stats()
	require.NotEmpty(t, generations, "the selector must have fetched at least one candidate cache")

	fullyBlacklistedScans := 0
	for i, generation := range generations {
		if generation.tryLocks > 0 {
			continue
		}
		fullyBlacklistedScans++
		assert.Equal(t, tokensPerGeneration+1, generation.reads,
			"fetch %d attempted no lock, so every one of its %d candidates was already blacklisted; such a scan must read "+
				"the cache exactly once (one read per candidate plus the terminating nil), but it read it %d times: "+
				"blacklisted candidates are being pulled into nextCandidate's sufficiency window and re-buffered into "+
				"s.pending instead of being skipped",
			i, tokensPerGeneration, generation.reads)
	}
	require.NotZero(t, fullyBlacklistedScans,
		"the scenario is vacuous unless at least one refetch was scanned with the whole wallet blacklisted")
}
