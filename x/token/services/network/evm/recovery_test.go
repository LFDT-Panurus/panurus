/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	token2 "github.com/LFDT-Panurus/panurus/token"
	tokendriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/recovery"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
)

func testTMSID() token2.TMSID {
	return token2.TMSID{Network: "evm-net", Channel: "", Namespace: "token"}
}

// TestStartRecoveryWithoutStores covers a node wired without the stores recovery sweeps.
//
// Recovery is a safety net for transactions left Pending by a previous run. A node that has nowhere
// to sweep still works, so this is a downgrade rather than a failure: returning an error here would
// take out a whole network over a facility that node may never need.
func TestStartRecoveryWithoutStores(t *testing.T) {
	d := newRecoveryDriver()

	require.NoError(t, d.startRecovery(testTMSID(), nil))
	assert.Empty(t, d.recoveries, "nothing may be recorded as running")
}

// TestStartRecoveryIsIdempotent is the reason the map exists at all.
//
// Connect is called once per namespace, but a network can be built more than once over a node's
// life, and every build would otherwise start a second pair of sweeps over the same store. Two
// managers sweeping one store claim each other's transactions: each sees rows the other has already
// leased, and recovery turns into contention over exactly the transactions it exists to rescue.
func TestStartRecoveryIsIdempotent(t *testing.T) {
	d := newRecoveryDriver()
	tmsID := testTMSID()

	// Stand in for a first start that already happened. The sweeps themselves need the full store
	// stack, which the integration suite supplies; what is pinned here is that a second call sees
	// the first and declines rather than starting alongside it.
	d.recoveries[tmsID.String()] = nil

	require.NoError(t, d.startRecovery(tmsID, nil))
	assert.Len(t, d.recoveries, 1, "a second start must not add a second set of sweeps")
}

// TestStopAllToleratesNothingToStop covers the unwind path when a TMS fails part-way through
// starting: whatever already started has to be stopped, and the set may be empty.
func TestStopAllToleratesNothingToStop(t *testing.T) {
	assert.NotPanics(t, func() { stopAll(nil) })
	assert.NotPanics(t, func() { stopAll([]*recovery.Manager{}) })
}

// TestRecoveryConfigFallsBackToDefaults pins the downgrade path. Recovery settings are optional, and
// a TMS that carries none - or carries one that cannot be read - gets the defaults rather than
// blocking the network from coming up without recovery.
func TestRecoveryConfigFallsBackToDefaults(t *testing.T) {
	// fakeResolver's ConfigurationFor always reports no configuration, which is the case every
	// deployment that has not opted into custom recovery settings hits.
	d := &Driver{resolver: fakeResolver{evm: map[string]bool{"evm-net|": true}}}

	cfg := d.recoveryConfig(testTMSID())
	assert.Equal(t, recovery.DefaultConfig(), cfg)
	assert.True(t, cfg.Enabled, "recovery is on unless a deployment turns it off")
	assert.Positive(t, cfg.TTL, "a zero TTL would sweep transactions that are merely in flight")
	assert.Positive(t, cfg.ScanInterval)
	assert.Less(t, cfg.ScanInterval, 5*time.Minute, "a sweep this rare would not rescue anything usefully")
}

// TestSettledNetworkResolvesAnAbsentAnchor is the fix for a rejected transaction being swept forever.
//
// A revert writes nothing to the chain, so an anchor that never appears is all a rejection ever looks
// like. Reporting that as Unknown made the shared recovery handler treat it as transient and look
// again on the next sweep, so the record stayed Pending, the holding it reserved was never released,
// and the sweep repeated every few seconds indefinitely.
func TestSettledNetworkResolvesAnAbsentAnchor(t *testing.T) {
	const timeout = time.Minute

	// settled builds the wrapper over a node whose eth_call returns raw, and a store that claims the
	// transaction was written age ago.
	settled := func(t *testing.T, raw []byte, callErr error, age time.Duration) (settledNetwork, string) {
		t.Helper()
		evm := &mock.EVMClient{}
		evm.ChainIDReturns(bigInt(testChainID), nil)
		evm.CallReturns(raw, callErr)
		n := testNetwork(t, evm, nil)

		return settledNetwork{
			Network: n,
			store:   &agedStore{age: age},
			timeout: timeout,
		}, n.ComputeTxID(&driver.TxID{Creator: []byte("creator")})
	}

	// An all-zero return is the contract reporting no token request hash for the anchor, which is what
	// a reverted apply leaves behind.
	absent := make([]byte, 32)

	t.Run("an anchor absent past the timeout is invalid", func(t *testing.T) {
		n, txID := settled(t, absent, nil, 2*timeout)

		status, _, message, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Invalid, status, "the sweep must reach a verdict rather than repeat forever")
		assert.NotEmpty(t, message, "the record should say why it was closed")
	})

	// The patient half, and the reason the age is read rather than assumed: a transaction younger than
	// the timeout may still be on its way, and condemning it would delete a transfer that then lands.
	t.Run("an anchor absent within the timeout stays unknown", func(t *testing.T) {
		n, txID := settled(t, absent, nil, timeout/2)

		status, _, _, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Unknown, status, "a transaction still in flight must be left alone")
	})

	// Only Unknown is reinterpreted. A committed transaction still recovers as Valid however old it
	// is, and its token request hash has to survive the wrapper or the handler cannot verify it.
	t.Run("a committed anchor is untouched", func(t *testing.T) {
		hash := make([]byte, 32)
		hash[31] = 0x7C
		n, txID := settled(t, hash, nil, 2*timeout)

		status, got, _, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Valid, status)
		assert.Equal(t, hash, got, "the token request hash must reach the handler intact")
	})

	// A record with no timestamp is the one input that reads as catastrophically old: time.Since of
	// the zero time is two thousand years, which would clear the timeout on the very first sweep and
	// condemn a transaction that may have been submitted seconds ago. "Not known" is the honest answer
	// and the safe one, matching what an unreadable store already does.
	t.Run("a record with no timestamp stays unknown", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.ChainIDReturns(bigInt(testChainID), nil)
		evm.CallReturns(absent, nil)
		n := testNetwork(t, evm, nil)
		settled := settledNetwork{Network: n, store: &agedStore{unset: true}, timeout: timeout}
		txID := n.ComputeTxID(&driver.TxID{Creator: []byte("creator")})

		status, _, _, err := settled.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Unknown, status, "an unknown age must not be read as an infinite one")
	})

	// A node that could not be reached has established nothing about the transaction, so it stays a
	// failure to be retried rather than becoming a verdict.
	t.Run("an unreachable node is not a verdict", func(t *testing.T) {
		n, txID := settled(t, nil, errors.New("connection refused"), 2*timeout)

		_, _, _, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.Error(t, err)
	})

	// A store that cannot say how old the row is leaves the transaction in the sweep. Condemning it on
	// the strength of a failed database read would be the one irreversible move available here.
	t.Run("an unreadable store leaves it for the next sweep", func(t *testing.T) {
		n, txID := settled(t, absent, nil, 2*timeout)
		n.store = &agedStore{err: errors.New("store is down")}

		status, _, _, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Unknown, status)
	})

	// A store that returns a nil iterator with no error must not be dereferenced: it is treated the
	// same as an unreadable store, leaving the transaction for the next sweep instead of panicking the
	// sweep goroutine.
	t.Run("a nil iterator leaves it for the next sweep", func(t *testing.T) {
		n, txID := settled(t, absent, nil, 2*timeout)
		n.store = &agedStore{nilIterator: true}

		status, _, _, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Unknown, status)
	})
}

// TestSettledNetworkCondemnsASpentInput covers the evidence path: an anchor absent from the chain
// plus an input already spent by some other, already-applied transaction is condemned in roughly
// ConflictGrace rather than waiting out the full finality timeout - and, symmetrically, must not be
// condemned before ConflictGrace has elapsed, which is what lets a transaction merely prepared but not
// yet broadcast stay Pending in the window the shared fungible integration bodies rely on.
func TestSettledNetworkCondemnsASpentInput(t *testing.T) {
	const (
		timeout = time.Minute
		grace   = 10 * time.Second
	)
	absent := make([]byte, 32)
	inputID := &token.ID{TxId: anchorHex(0x01), Index: 0}

	// build assembles a settledNetwork whose anchor read always answers absent (unless overridden via
	// callReturnsOnCall), wired to a store that reports the given age and stored request bytes, a
	// parser that returns request unconditionally, and a spent reader that returns spent/spentErr.
	build := func(t *testing.T, age time.Duration, request *token2.Request, spent []bool, spentErr error) (settledNetwork, string) {
		t.Helper()
		evm := &mock.EVMClient{}
		evm.ChainIDReturns(bigInt(testChainID), nil)
		evm.CallReturns(absent, nil)
		n := testNetwork(t, evm, nil)

		return settledNetwork{
			Network:   n,
			store:     &agedStore{age: age, tokenRequest: []byte("raw")},
			timeout:   timeout,
			grace:     grace,
			parser:    &fakeParser{request: request},
			spent:     &fakeSpentReader{spent: spent, err: spentErr},
			conflicts: &conflictWatch{entries: map[string]conflictEntry{}},
		}, n.ComputeTxID(&driver.TxID{Creator: []byte("creator")})
	}

	transferRequest := func(ids ...*token.ID) *token2.Request {
		inputs := make([]*tokendriver.TransferInputMetadata, 0, len(ids))
		for _, id := range ids {
			inputs = append(inputs, &tokendriver.TransferInputMetadata{TokenID: id})
		}

		return &token2.Request{
			Metadata: &tokendriver.TokenRequestMetadata{
				Actions: []*tokendriver.ActionMetadataEntry{
					{TransferMetadata: &tokendriver.TransferMetadata{Inputs: inputs}},
				},
			},
		}
	}

	t.Run("a spent input past the grace window is invalid", func(t *testing.T) {
		n, txID := build(t, 2*timeout, transferRequest(inputID), []bool{true}, nil)

		// The first check only observes the conflict and starts the clock; it must not condemn yet
		// even though the row's age already clears the (unrelated) finality timeout.
		status, _, _, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Unknown, status, "the grace window must run its course even on an old row")

		n.conflicts.mu.Lock()
		n.conflicts.entries[txID] = conflictEntry{
			inputs:    n.conflicts.entries[txID].inputs,
			firstSeen: time.Now().Add(-2 * grace),
			touched:   time.Now(),
		}
		n.conflicts.mu.Unlock()

		status, _, message, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Invalid, status)
		assert.Contains(t, message, "already spent", "the message must be distinguishable from the timeout one")
	})

	t.Run("a spent input within the grace window stays unknown", func(t *testing.T) {
		n, txID := build(t, 2*timeout, transferRequest(inputID), []bool{true}, nil)

		status, _, _, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Unknown, status, "tests.go asserts the loser of a double-spend is still "+
			"pending immediately after the winner lands; condemning on sight would break that")
	})

	t.Run("an unspent input leaves the timeout in charge", func(t *testing.T) {
		n, txID := build(t, 2*timeout, transferRequest(inputID), []bool{false}, nil)

		status, _, message, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Invalid, status)
		assert.Contains(t, message, "finality timeout", "the age gate, not the conflict path, must have decided this")
	})

	t.Run("no inputs are never condemned by conflict", func(t *testing.T) {
		n, txID := build(t, 2*timeout, transferRequest(), nil, nil)

		status, _, _, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Invalid, status, "the age gate still applies")

		n.conflicts.mu.Lock()
		entry := n.conflicts.entries[txID]
		n.conflicts.mu.Unlock()
		assert.True(t, entry.noCandidates)
	})

	t.Run("nil and foreign input ids are filtered before any spent check", func(t *testing.T) {
		spentReader := &fakeSpentReader{spent: []bool{true}}
		// nil is what a recipient's filtered metadata leaves for an input it did not itself send
		// (token/metadata.go, filterTransfer); a TxId that is not a valid anchor is the shape a foreign
		// or malformed id would take.
		foreign := &token.ID{TxId: "not-an-anchor", Index: 0}
		n, txID := build(t, 2*timeout, transferRequest(nil, foreign), nil, nil)
		n.spent = spentReader

		status, _, message, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Invalid, status, "only the age gate can have fired: every candidate id was unusable")
		assert.Contains(t, message, "finality timeout")
		assert.Zero(t, spentReader.calls, "no usable id means AreTokensSpent must never be called")
	})

	t.Run("a store or chain error is never treated as evidence", func(t *testing.T) {
		n, txID := build(t, timeout/2, transferRequest(inputID), []bool{true}, errors.New("node is unhappy"))

		status, _, _, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Unknown, status)

		n2, txID2 := build(t, timeout/2, nil, nil, nil)
		n2.store = &agedStore{age: timeout / 2, tokenRequestErr: errors.New("store is down")}

		status, _, _, err = n2.GetTransactionStatus(t.Context(), "token", txID2)
		require.NoError(t, err)
		assert.Equal(t, driver.Unknown, status)
	})

	t.Run("a conflict that goes away resets the grace window", func(t *testing.T) {
		n, txID := build(t, 2*timeout, transferRequest(inputID), []bool{true}, nil)

		_, _, _, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		n.conflicts.mu.Lock()
		assert.False(t, n.conflicts.entries[txID].firstSeen.IsZero(), "the first check must have started the clock")
		n.conflicts.mu.Unlock()

		n.spent = &fakeSpentReader{spent: []bool{false}}
		_, _, _, err = n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		n.conflicts.mu.Lock()
		assert.True(t, n.conflicts.entries[txID].firstSeen.IsZero(), "the clock must reset once the input reads unspent again")
		n.conflicts.mu.Unlock()
	})

	t.Run("input ids are parsed once per transaction", func(t *testing.T) {
		parser := &fakeParser{request: transferRequest(inputID)}
		n, txID := build(t, timeout/2, nil, []bool{false}, nil)
		n.parser = parser

		_, _, _, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		_, _, _, err = n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, 1, parser.calls, "the second sweep must reuse the memoised ids rather than re-parsing")
	})

	t.Run("a revert checking spent status degrades to the timeout, as under graph hiding", func(t *testing.T) {
		n, txID := build(t, 2*timeout, transferRequest(inputID), nil, client.ErrExecutionReverted)

		status, _, message, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Invalid, status)
		assert.Contains(t, message, "finality timeout")
	})

	t.Run("an anchor that appears between the two reads is not condemned", func(t *testing.T) {
		hash := make([]byte, 32)
		hash[31] = 0x7C
		evm := &mock.EVMClient{}
		evm.ChainIDReturns(bigInt(testChainID), nil)
		evm.CallReturnsOnCall(0, absent, nil)
		evm.CallReturnsOnCall(1, hash, nil)
		n := testNetwork(t, evm, nil)
		// ComputeTxID generates a fresh random nonce whenever Nonce is empty, so it must be called
		// exactly once and the result reused - calling it twice would seed the conflict-watch entry
		// under one anchor and then look it up under a different, unrelated one.
		txID := n.ComputeTxID(&driver.TxID{Creator: []byte("creator")})
		settled := settledNetwork{
			Network: n,
			store:   &agedStore{age: 2 * timeout, tokenRequest: []byte("raw")},
			timeout: timeout,
			grace:   grace,
			parser:  &fakeParser{request: transferRequest(inputID)},
			spent:   &fakeSpentReader{spent: []bool{true}},
			conflicts: &conflictWatch{entries: map[string]conflictEntry{
				txID: {inputs: []*token.ID{inputID}, firstSeen: time.Now().Add(-2 * grace), touched: time.Now()},
			}},
		}

		status, got, _, err := settled.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Valid, status, "the re-read must see the anchor that just landed")
		assert.Equal(t, hash, got)
	})
}

// TestSpentTokenIDsOfRequest exercises spentTokenIDsOf directly, rather than only indirectly through
// GetTransactionStatus/condemnedByConflict as the rest of this file does: it pins the filtering rule
// on its own, decoupled from the grace-window and anchor-re-read logic those other tests are actually
// about.
func TestSpentTokenIDsOfRequest(t *testing.T) {
	inputID := &token.ID{TxId: anchorHex(0x01), Index: 0}
	foreign := &token.ID{TxId: "not-an-anchor", Index: 0}

	transferRequest := func(ids ...*token.ID) *token2.Request {
		inputs := make([]*tokendriver.TransferInputMetadata, 0, len(ids))
		for _, id := range ids {
			inputs = append(inputs, &tokendriver.TransferInputMetadata{TokenID: id})
		}

		return &token2.Request{
			Metadata: &tokendriver.TokenRequestMetadata{
				Actions: []*tokendriver.ActionMetadataEntry{
					{TransferMetadata: &tokendriver.TransferMetadata{Inputs: inputs}},
				},
			},
		}
	}

	build := func(t *testing.T, raw []byte, rawErr error, request *token2.Request, parseErr error) settledNetwork {
		t.Helper()

		return settledNetwork{
			store:  &agedStore{tokenRequest: raw, tokenRequestErr: rawErr},
			parser: &fakeParser{request: request, err: parseErr},
		}
	}

	t.Run("a transfer's usable inputs come back in order", func(t *testing.T) {
		n := build(t, []byte("raw"), nil, transferRequest(inputID, nil, foreign), nil)

		ids, err := n.spentTokenIDsOf(t.Context(), "tx")
		require.NoError(t, err)
		assert.Equal(t, []*token.ID{inputID}, ids, "nil and foreign ids must be dropped, the real one kept in place")
	})

	t.Run("an issue action with no inputs yields nothing", func(t *testing.T) {
		n := build(t, []byte("raw"), nil, &token2.Request{Metadata: &tokendriver.TokenRequestMetadata{
			Actions: []*tokendriver.ActionMetadataEntry{{IssueMetadata: &tokendriver.IssueMetadata{}}},
		}}, nil)

		ids, err := n.spentTokenIDsOf(t.Context(), "tx")
		require.NoError(t, err)
		assert.Empty(t, ids)
	})

	t.Run("no stored request yields nothing without an error", func(t *testing.T) {
		n := build(t, nil, nil, nil, nil)

		ids, err := n.spentTokenIDsOf(t.Context(), "tx")
		require.NoError(t, err)
		assert.Nil(t, ids)
	})

	t.Run("a store read error is propagated, not swallowed as no evidence", func(t *testing.T) {
		n := build(t, nil, errors.New("store is unhappy"), nil, nil)

		_, err := n.spentTokenIDsOf(t.Context(), "tx")
		require.Error(t, err)
	})

	t.Run("an unparsable request's error is propagated", func(t *testing.T) {
		n := build(t, []byte("raw"), nil, nil, errors.New("cannot parse"))

		_, err := n.spentTokenIDsOf(t.Context(), "tx")
		require.Error(t, err)
	})

	t.Run("a request with no metadata yields nothing", func(t *testing.T) {
		n := build(t, []byte("raw"), nil, &token2.Request{}, nil)

		ids, err := n.spentTokenIDsOf(t.Context(), "tx")
		require.NoError(t, err)
		assert.Nil(t, ids)
	})
}

// TestConflictWatchPruning pins the fix for the review finding that conflictWatch grew without bound:
// every txID a sweep ever looked at accumulated a permanent entry, even long after it resolved. Prune
// is throttled to at most once per maxAge, so this drives it directly rather than through real time.
func TestConflictWatchPruning(t *testing.T) {
	t.Run("an entry untouched past maxAge is evicted", func(t *testing.T) {
		w := &conflictWatch{entries: map[string]conflictEntry{
			"stale": {touched: time.Now().Add(-time.Hour)},
		}}

		w.pruneLocked(time.Minute)

		assert.Empty(t, w.entries, "an hour-stale entry must not survive a one-minute window")
	})

	t.Run("an entry touched within maxAge survives", func(t *testing.T) {
		w := &conflictWatch{entries: map[string]conflictEntry{
			"fresh": {touched: time.Now()},
		}}

		w.pruneLocked(time.Minute)

		assert.Contains(t, w.entries, "fresh")
	})

	t.Run("a non-positive maxAge disables pruning outright", func(t *testing.T) {
		w := &conflictWatch{entries: map[string]conflictEntry{
			"ancient": {touched: time.Now().Add(-24 * time.Hour)},
		}}

		w.pruneLocked(0)

		assert.Contains(t, w.entries, "ancient", "pruning must be opt-in, not a default sweep-everything")
	})

	t.Run("a second call within maxAge of the first is throttled and does nothing", func(t *testing.T) {
		w := &conflictWatch{entries: map[string]conflictEntry{
			"fresh": {touched: time.Now()},
		}}
		// The first call, over a fresh entry, evicts nothing but still schedules the next prune an
		// hour out. A second, immediately-following call must be a no-op - even though the entry has
		// since gone stale - because a prune walks the whole map and is throttled by when it last ran,
		// not by how old any individual entry has become.
		w.pruneLocked(time.Hour)
		w.entries["fresh"] = conflictEntry{touched: time.Now().Add(-2 * time.Hour)}

		w.pruneLocked(time.Hour)

		assert.Contains(t, w.entries, "fresh", "the second call must be throttled until nextPrune elapses")
	})

	t.Run("pruneWindow derives from timeout, falling back to grace, disabled if both are zero", func(t *testing.T) {
		assert.Equal(t, 10*time.Minute, settledNetwork{timeout: time.Minute}.pruneWindow())
		assert.Equal(t, 10*time.Second, settledNetwork{grace: time.Second}.pruneWindow())
		assert.Equal(t, time.Duration(0), settledNetwork{}.pruneWindow())
	})

	// condemnedByConflict must sweep the whole map even on a run where txID itself has no usable
	// inputs - an issue-only ledger, or per spentTokenIDsOf's own doc, every transfer as seen from a
	// recipient's own store - since that is the only path through this settledNetwork that ever
	// touches conflicts, and a watch never given a transaction with real inputs would otherwise never
	// prune at all.
	t.Run("a no-candidates check still sweeps stale entries out of the watch", func(t *testing.T) {
		n := settledNetwork{
			store:   &agedStore{tokenRequest: nil},
			timeout: time.Nanosecond,
			grace:   time.Second,
			parser:  &fakeParser{request: nil},
			spent:   &fakeSpentReader{},
			conflicts: &conflictWatch{entries: map[string]conflictEntry{
				"unrelated-stale-tx": {touched: time.Now().Add(-time.Hour)},
			}},
		}

		_, _, _ = n.condemnedByConflict(t.Context(), "token", "tx-with-no-inputs")

		n.conflicts.mu.Lock()
		defer n.conflicts.mu.Unlock()
		assert.NotContains(t, n.conflicts.entries, "unrelated-stale-tx",
			"a no-candidates run must still prune the map, not only a run that finds a conflict")
	})
}

// TestSettledNetworkConflictCheckIsConcurrencySafe drives condemnedByConflict from many goroutines
// against one shared conflictWatch, the way the recovery manager's worker pool (WorkerCount 8) does
// in production. The mutex around conflictWatch.entries is the only thing standing between that and a
// data race; run with -race, this is what actually exercises it rather than just inspecting the lock
// pattern by eye.
func TestSettledNetworkConflictCheckIsConcurrencySafe(t *testing.T) {
	inputID := &token.ID{TxId: anchorHex(0x01), Index: 0}
	request := &token2.Request{
		Metadata: &tokendriver.TokenRequestMetadata{
			Actions: []*tokendriver.ActionMetadataEntry{
				{TransferMetadata: &tokendriver.TransferMetadata{
					Inputs: []*tokendriver.TransferInputMetadata{{TokenID: inputID}},
				}},
			},
		},
	}

	n := settledNetwork{
		store:     &agedStore{tokenRequest: []byte("raw")},
		timeout:   time.Minute,
		grace:     10 * time.Millisecond,
		parser:    &concurrentParser{request: request},
		spent:     &concurrentSpentReader{spent: []bool{true}},
		conflicts: &conflictWatch{entries: map[string]conflictEntry{}},
	}

	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		txID := anchorHex(byte(i))
		go func() {
			defer wg.Done()
			for range 20 {
				_, _, _ = n.condemnedByConflict(t.Context(), "token", txID)
			}
		}()
	}
	wg.Wait()
}

// concurrentParser is a tokenRequestParser with no mutable state, safe to call from many goroutines
// at once - unlike fakeParser, which counts calls without a lock and is only ever used sequentially.
type concurrentParser struct {
	request *token2.Request
}

func (p *concurrentParser) ProcessTokenRequest(_ context.Context, _ []byte) (*token2.Request, []byte, error) {
	return p.request, nil, nil
}

// concurrentSpentReader is a spentReader with no mutable state, for the same reason as concurrentParser.
type concurrentSpentReader struct {
	spent []bool
}

func (r *concurrentSpentReader) AreTokensSpent(
	_ context.Context, _ string, _ []*token.ID, _ []string,
) ([]bool, error) {
	return r.spent, nil
}

// fakeParser is a tokenRequestParser returning a fixed request, or an error, and counting calls.
type fakeParser struct {
	request *token2.Request
	err     error
	calls   int
}

func (f *fakeParser) ProcessTokenRequest(_ context.Context, _ []byte) (*token2.Request, []byte, error) {
	f.calls++

	return f.request, nil, f.err
}

// fakeSpentReader is a spentReader returning a fixed spent slice, or an error, and counting calls.
type fakeSpentReader struct {
	spent []bool
	err   error
	calls int
}

func (f *fakeSpentReader) AreTokensSpent(
	_ context.Context, _ string, _ []*token.ID, _ []string,
) ([]bool, error) {
	f.calls++

	return f.spent, f.err
}

// agedStore is a recoveryStore that only answers the age question, which is all settledNetwork asks
// of it. Every other method is present to satisfy the interface and panics if the code under test
// starts depending on it.
type agedStore struct {
	recoveryStore
	age time.Duration
	err error
	// unset makes the record come back with no timestamp, the shape a store that never populated the
	// column would produce.
	unset bool
	// nilIterator makes Transactions return (nil, nil), the shape a store implementation bug would
	// produce: success, but nothing to read from.
	nilIterator bool
	// tokenRequest and tokenRequestErr back GetTokenRequest, used by the conflict-check tests. A zero
	// value returns (nil, nil): no stored bytes, which reads as "no candidates" further up the stack.
	tokenRequest    []byte
	tokenRequestErr error
}

// GetTokenRequest returns the raw bytes configured on the fake, or an error.
func (s *agedStore) GetTokenRequest(_ context.Context, _ string) ([]byte, error) {
	return s.tokenRequest, s.tokenRequestErr
}

func (s *agedStore) Transactions(
	_ context.Context,
	_ dbdriver.QueryTransactionsParams,
	_ cdriver.Pagination,
) (*cdriver.PageIterator[*dbdriver.TransactionRecord], error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.nilIterator {
		return nil, nil
	}

	record := &dbdriver.TransactionRecord{Timestamp: time.Now().Add(-s.age)}
	if s.unset {
		record.Timestamp = time.Time{}
	}

	return &cdriver.PageIterator[*dbdriver.TransactionRecord]{
		Items: iterators.Slice([]*dbdriver.TransactionRecord{record}),
	}, nil
}

// newRecoveryDriver returns a Driver with the recovery bookkeeping initialized and no stores, which
// is what NewDriver produces before anything binds to it.
func newRecoveryDriver() *Driver {
	return &Driver{
		resolver:   fakeResolver{evm: map[string]bool{"evm-net|": true}},
		recoveries: map[string][]*recovery.Manager{},
	}
}
