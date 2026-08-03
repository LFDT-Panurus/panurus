/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/finality"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger/fabric-protos-go-apiv2/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// heightLedgerResult is one scripted GetLedgerInfo outcome.
type heightLedgerResult struct {
	info *fabric.LedgerInfo
	err  error
}

// fakeHeightLedger returns scripted GetLedgerInfo results, replaying the last
// one once the script is exhausted, and counts how often it was called.
type fakeHeightLedger struct {
	results []heightLedgerResult
	calls   atomic.Int32
	// onCall, when set, runs after the nth call has been counted. It lets a test
	// cancel the context from inside an attempt, which is the only way to observe
	// a cancellation that interrupts the retry wait rather than one that precedes
	// the first attempt.
	onCall func(n int)
}

func (l *fakeHeightLedger) GetLedgerInfo() (*fabric.LedgerInfo, error) {
	n := int(l.calls.Add(1))
	r := l.results[min(n, len(l.results))-1]
	if l.onCall != nil {
		l.onCall(n)
	}

	return r.info, r.err
}

// recordingBlockDelivery records the block ScanBlockFrom was asked to start
// from, and whether it was called at all — the observable difference between
// "resumed at the current height", "rescanned from genesis", and "did not scan".
type recordingBlockDelivery struct {
	called        bool
	startingBlock uint64
	callback      fabric.BlockCallback
	err           error
}

func (d *recordingBlockDelivery) ScanBlockFrom(_ context.Context, block uint64, callback fabric.BlockCallback) error {
	d.called = true
	d.startingBlock = block
	d.callback = callback

	return d.err
}

// newTestDelivery builds a Delivery over the two fakes, with a retry delay short
// enough to keep the tests fast.
func newTestDelivery(l *fakeHeightLedger, d *recordingBlockDelivery) *finality.Delivery {
	return &finality.Delivery{
		Delivery:             d,
		Ledger:               l,
		Logger:               logging.MustGetLogger(),
		LedgerInfoRetryDelay: time.Millisecond,
	}
}

// TestScanBlock_StartsFromCurrentLedgerHeight covers the happy path: the scan
// resumes at the height reported by the ledger, with the caller's callback
// passed through untouched.
func TestScanBlock_StartsFromCurrentLedgerHeight(t *testing.T) {
	ledger := &fakeHeightLedger{results: []heightLedgerResult{{info: &fabric.LedgerInfo{Height: 42}}}}
	delivery := &recordingBlockDelivery{}

	callbackInvoked := false
	callback := func(context.Context, *common.Block) (bool, error) {
		callbackInvoked = true

		return false, nil
	}

	require.NoError(t, newTestDelivery(ledger, delivery).ScanBlock(context.Background(), callback))

	assert.True(t, delivery.called)
	assert.Equal(t, uint64(42), delivery.startingBlock)
	assert.Equal(t, int32(1), ledger.calls.Load(), "a successful GetLedgerInfo must not be retried")

	require.NotNil(t, delivery.callback)
	_, err := delivery.callback(context.Background(), nil)
	require.NoError(t, err)
	assert.True(t, callbackInvoked, "the caller's callback must be the one handed to ScanBlockFrom")
}

// TestScanBlock_PropagatesLedgerInfoErrorInsteadOfRescanningFromGenesis is the
// regression test for issue #2058: a persistent GetLedgerInfo failure used to be
// logged and swallowed, leaving startingBlock at 0 and silently triggering a full
// rescan from genesis. The error must now reach the caller, and no scan must start.
func TestScanBlock_PropagatesLedgerInfoErrorInsteadOfRescanningFromGenesis(t *testing.T) {
	transient := errors.New("peer connection reset")
	ledger := &fakeHeightLedger{results: []heightLedgerResult{{err: transient}}}
	delivery := &recordingBlockDelivery{}

	err := newTestDelivery(ledger, delivery).ScanBlock(context.Background(), nil)

	require.Error(t, err, "GetLedgerInfo failure must be propagated, not swallowed after a log line")
	require.ErrorIs(t, err, transient, "the underlying ledger error must stay inspectable")
	require.ErrorIs(t, err, finality.ErrLedgerHeightUnavailable, "the caller must classify this without matching on the message")
	require.NotErrorIs(t, err, finality.ErrNoLedgerInfo, "an unreachable peer is not a driver returning no info")
	assert.False(t, delivery.called, "no scan may start from genesis when the ledger height is unknown")
}

// TestScanBlock_RetriesTransientLedgerInfoFailure verifies the transient case the
// issue describes — an ordinary RPC hiccup — is absorbed by a retry and resumes at
// the real height, rather than either failing or rescanning from block 0.
func TestScanBlock_RetriesTransientLedgerInfoFailure(t *testing.T) {
	ledger := &fakeHeightLedger{results: []heightLedgerResult{
		{err: errors.New("peer connection reset")},
		{info: &fabric.LedgerInfo{Height: 7}},
	}}
	delivery := &recordingBlockDelivery{}

	require.NoError(t, newTestDelivery(ledger, delivery).ScanBlock(context.Background(), nil))

	assert.Equal(t, int32(2), ledger.calls.Load())
	assert.True(t, delivery.called)
	assert.Equal(t, uint64(7), delivery.startingBlock, "must resume at the height read on the retry, not at genesis")
}

// TestScanBlock_RetriesAreBounded verifies the retry loop honours the configured
// attempt budget instead of spinning on a permanently failing ledger.
func TestScanBlock_RetriesAreBounded(t *testing.T) {
	ledger := &fakeHeightLedger{results: []heightLedgerResult{{err: errors.New("peer connection reset")}}}
	delivery := &recordingBlockDelivery{}

	d := newTestDelivery(ledger, delivery)
	d.LedgerInfoAttempts = 5

	require.Error(t, d.ScanBlock(context.Background(), nil))
	assert.Equal(t, int32(5), ledger.calls.Load())
	assert.False(t, delivery.called)
}

// TestScanBlock_NilLedgerInfoIsReportedNotDereferenced covers a driver that
// reports neither info nor error: that used to be a nil dereference waiting on
// info.Height, and must instead surface as an error.
func TestScanBlock_NilLedgerInfoIsReportedNotDereferenced(t *testing.T) {
	ledger := &fakeHeightLedger{results: []heightLedgerResult{{}}}
	delivery := &recordingBlockDelivery{}

	err := newTestDelivery(ledger, delivery).ScanBlock(context.Background(), nil)

	require.Error(t, err)
	require.ErrorIs(t, err, finality.ErrNoLedgerInfo, "the (nil, nil) contract violation must be identifiable, not just described")
	require.ErrorIs(t, err, finality.ErrLedgerHeightUnavailable, "it is also a failure to read the height")
	require.NotErrorIs(t, err, context.Canceled, "a nil-info driver is not a cancellation")
	assert.False(t, delivery.called)
}

// TestScanBlock_CancelledContextStopsRetrying verifies shutdown does not have to
// wait out the retry budget: a cancelled context ends the loop with its own error.
func TestScanBlock_CancelledContextStopsRetrying(t *testing.T) {
	transient := errors.New("peer connection reset")
	ledger := &fakeHeightLedger{results: []heightLedgerResult{{err: transient}}}
	delivery := &recordingBlockDelivery{}

	d := newTestDelivery(ledger, delivery)
	d.LedgerInfoAttempts = 100
	d.LedgerInfoRetryDelay = time.Hour // only cancellation can end this loop in time

	ctx, cancel := context.WithCancel(context.Background())
	// Cancelled from inside the first attempt, so the cancellation lands on the
	// retry wait: a context cancelled beforehand would return before any attempt
	// ran, leaving no ledger failure to report alongside it.
	ledger.onCall = func(int) { cancel() }

	done := make(chan error, 1)
	go func() { done <- d.ScanBlock(ctx, nil) }()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, transient, "the failure that was being retried must stay inspectable alongside the cancellation")
		require.ErrorIs(t, err, finality.ErrLedgerHeightUnavailable,
			"cancellation is still a failure to resolve the starting block: ErrLedgerHeightUnavailable is the single test for \"no scan started\"")
		assert.Equal(t, int32(1), ledger.calls.Load(), "must not keep retrying after cancellation")
		assert.False(t, delivery.called)
	case <-time.After(10 * time.Second):
		t.Fatal("ScanBlock kept retrying after its context was cancelled")
	}
}

// TestScanBlock_CancellationDuringNilInfoKeepsBothSentinels pins the corner where
// the two refining sentinels coincide: a (nil, nil) driver whose retry wait is
// then cancelled. ErrNoLedgerInfo must survive alongside the context error, and
// ErrLedgerHeightUnavailable must still be present — documentation states it
// accompanies every height failure, so this path may not be the exception.
func TestScanBlock_CancellationDuringNilInfoKeepsBothSentinels(t *testing.T) {
	ledger := &fakeHeightLedger{results: []heightLedgerResult{{}}}
	delivery := &recordingBlockDelivery{}

	d := newTestDelivery(ledger, delivery)
	d.LedgerInfoAttempts = 100
	d.LedgerInfoRetryDelay = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	ledger.onCall = func(int) { cancel() }

	done := make(chan error, 1)
	go func() { done <- d.ScanBlock(ctx, nil) }()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, finality.ErrNoLedgerInfo, "the contract violation being retried must stay inspectable")
		require.ErrorIs(t, err, finality.ErrLedgerHeightUnavailable)
		assert.False(t, delivery.called)
	case <-time.After(10 * time.Second):
		t.Fatal("ScanBlock kept retrying after its context was cancelled")
	}
}

// TestScanBlock_EveryObservedFailureIsClassified pins the reach of the refining
// sentinels: failures accumulate across attempts, so an earlier (nil, nil) stays
// inspectable even when a later attempt fails differently. The contract is "any
// attempt", not "the last one".
func TestScanBlock_EveryObservedFailureIsClassified(t *testing.T) {
	transient := errors.New("peer connection reset")
	ledger := &fakeHeightLedger{results: []heightLedgerResult{{}, {err: transient}}}
	delivery := &recordingBlockDelivery{}

	d := newTestDelivery(ledger, delivery)
	d.LedgerInfoAttempts = 2

	err := d.ScanBlock(context.Background(), nil)

	require.ErrorIs(t, err, finality.ErrLedgerHeightUnavailable)
	require.ErrorIs(t, err, transient, "the last attempt's failure must be inspectable")
	require.ErrorIs(t, err, finality.ErrNoLedgerInfo,
		"an earlier contract violation must not be dropped just because a later attempt failed differently")
	assert.False(t, delivery.called)
}

// TestScanBlock_ContextCancelledBeforeFirstAttempt covers shutdown racing the
// start of a scan: the height read is abandoned without touching the ledger, and
// the failure still classifies as "no scan started" so the caller does not have
// to special-case it.
func TestScanBlock_ContextCancelledBeforeFirstAttempt(t *testing.T) {
	ledger := &fakeHeightLedger{results: []heightLedgerResult{{info: &fabric.LedgerInfo{Height: 9}}}}
	delivery := &recordingBlockDelivery{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := newTestDelivery(ledger, delivery).ScanBlock(ctx, nil)

	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, finality.ErrLedgerHeightUnavailable)
	assert.Equal(t, int32(0), ledger.calls.Load(), "an already-cancelled context must not cost an RPC")
	assert.False(t, delivery.called)
}

// TestScanBlock_DefaultAttemptBudgetOutlastsAPeerRestart guards the sizing
// decision: nothing retries ScanBlock, so a single transient failure must not
// disable block-based finality, and the default budget has to cover a peer
// restart rather than just a dropped packet. Only the attempt count is asserted —
// the real delay would make this test sleep for the whole ~31.5s schedule, so it
// is overridden and the schedule itself is pinned by the const comments.
func TestScanBlock_DefaultAttemptBudgetOutlastsAPeerRestart(t *testing.T) {
	const minimumAttempts = 7

	ledger := &fakeHeightLedger{results: []heightLedgerResult{{err: errors.New("peer connection reset")}}}
	delivery := &recordingBlockDelivery{}

	// LedgerInfoAttempts left unset so the default applies.
	d := &finality.Delivery{
		Delivery:             delivery,
		Ledger:               ledger,
		Logger:               logging.MustGetLogger(),
		LedgerInfoRetryDelay: time.Millisecond,
	}

	require.Error(t, d.ScanBlock(context.Background(), nil))
	assert.GreaterOrEqual(t, int(ledger.calls.Load()), minimumAttempts,
		"the default attempt budget must absorb a peer restart: nothing retries ScanBlock, so a failure here is terminal")
	assert.False(t, delivery.called)
}

// TestScanBlock_NilLedgerScansFromGenesis pins the one remaining path that
// legitimately starts at block 0: no ledger is configured, so there is no height
// to resume from and the whole chain is the intended range.
func TestScanBlock_NilLedgerScansFromGenesis(t *testing.T) {
	delivery := &recordingBlockDelivery{}
	d := &finality.Delivery{Delivery: delivery, Logger: logging.MustGetLogger()}

	require.NoError(t, d.ScanBlock(context.Background(), nil))
	assert.True(t, delivery.called)
	assert.Equal(t, uint64(0), delivery.startingBlock)
}

// TestScanBlock_PropagatesScanError verifies the scan's own error is returned
// unchanged once the starting block has been resolved.
func TestScanBlock_PropagatesScanError(t *testing.T) {
	scanErr := errors.New("delivery stream broken")
	ledger := &fakeHeightLedger{results: []heightLedgerResult{{info: &fabric.LedgerInfo{Height: 3}}}}
	delivery := &recordingBlockDelivery{err: scanErr}

	err := newTestDelivery(ledger, delivery).ScanBlock(context.Background(), nil)

	require.ErrorIs(t, err, scanErr)
	require.NotErrorIs(t, err, finality.ErrLedgerHeightUnavailable, "a scan failure must not be mistaken for a failure to read the height")
	assert.Equal(t, uint64(3), delivery.startingBlock)
}
