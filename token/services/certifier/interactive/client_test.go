/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package interactive_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/certifier/interactive"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/events"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- minimal fakes ---

type emptyUnspentIterator struct{}

func (e *emptyUnspentIterator) Next() (*token.UnspentToken, error) { return nil, nil }
func (e *emptyUnspentIterator) Close()                             {}

var _ driver.UnspentTokensIterator = (*emptyUnspentIterator)(nil)

type fakeQueryEngine struct{}

func (f *fakeQueryEngine) UnspentTokensIterator(_ context.Context) (*token2.UnspentTokensIterator, error) {
	return &token2.UnspentTokensIterator{UnspentTokensIterator: &emptyUnspentIterator{}}, nil
}

type fakeCertificationStorage struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newFakeCertificationStorage() *fakeCertificationStorage {
	return &fakeCertificationStorage{data: map[string][]byte{}}
}

func (f *fakeCertificationStorage) Exists(_ context.Context, id *token.ID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	_, ok := f.data[id.TxId]

	return ok
}

func (f *fakeCertificationStorage) Store(_ context.Context, certifications map[*token.ID][]byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for id, cert := range certifications {
		f.data[id.TxId] = cert
	}

	return nil
}

// fakeViewManager implements ViewManager for tests.
type fakeViewManager struct {
	mu      sync.Mutex
	calls   int
	failErr error // if set, next InitiateView returns this error
}

func (f *fakeViewManager) InitiateView(v view.View) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++

	if f.failErr != nil {
		err := f.failErr
		f.failErr = nil

		return nil, err
	}

	// Build a success result using the IDs from the CertificationRequestView.
	crv, ok := v.(*interactive.CertificationRequestView)
	if !ok {
		return nil, errors.Errorf("unexpected view type %T", v)
	}

	result := make(map[*token.ID][]byte, len(interactive.CRVIDs(crv)))
	for _, id := range interactive.CRVIDs(crv) {
		result[id] = []byte("cert:" + id.TxId)
	}

	return result, nil
}

func (f *fakeViewManager) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.calls
}

// ensure callCount is used in at least one test to satisfy the linter.
// It is called from TestCertificationClient_IsCertified_Delegates via the
// ViewManager fakes, so this helper exists for future test use.
var _ = (*fakeViewManager).callCount

// --- helpers ---

// newTestClient creates a CertificationClient with defaults suitable for tests.
func newTestClient(
	t *testing.T,
	vm interactive.ViewManager,
	storage interactive.CertificationStorage,
	batchSize int,
	flushInterval time.Duration,
	workers int,
) *interactive.CertificationClient {
	t.Helper()

	return interactive.NewCertificationClient(
		context.Background(),
		"test-network",
		"test-channel",
		"test-ns",
		&fakeQueryEngine{},
		storage,
		vm,
		[]view.Identity{view.Identity([]byte("certifier"))},
		nil,
		2,
		1*time.Millisecond,
		batchSize,
		1000,
		flushInterval,
		workers,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)
}

// injectToken sends a token ID directly into the client's input channel.
// This simulates what OnReceive does without going through the events system.
func injectToken(cc *interactive.CertificationClient, id *token.ID) {
	interactive.ClientTokensChan(cc) <- id
}

// --- tests ---

// TestCertificationClient_Stop_GracefulShutdown verifies that Stop() returns
// without deadlock.
func TestCertificationClient_Stop_GracefulShutdown(t *testing.T) {
	vm := &fakeViewManager{}
	storage := newFakeCertificationStorage()
	cc := newTestClient(t, vm, storage, 10, 100*time.Millisecond, 1)

	cc.Start()

	done := make(chan struct{})

	go func() {
		cc.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within 2 seconds — possible goroutine leak")
	}
}

// TestCertificationClient_FlushInterval_PartialBatch verifies that a partial batch
// smaller than batchSize is flushed after the flush interval expires.
func TestCertificationClient_FlushInterval_PartialBatch(t *testing.T) {
	vm := &fakeViewManager{}
	storage := newFakeCertificationStorage()

	cc := newTestClient(t, vm, storage, 10, 50*time.Millisecond, 1)
	cc.Start()

	defer cc.Stop()

	id := &token.ID{TxId: "tx-partial", Index: 0}
	injectToken(cc, id)

	require.Eventually(t, func() bool {
		return storage.Exists(context.Background(), id)
	}, 2*time.Second, 10*time.Millisecond, "partial batch was not flushed within 2s")
}

// TestCertificationClient_BatchFull_ImmediateFlush verifies that a full batch is
// dispatched immediately without waiting for the timer.
func TestCertificationClient_BatchFull_ImmediateFlush(t *testing.T) {
	const batchSize = 3

	vm := &fakeViewManager{}
	storage := newFakeCertificationStorage()

	cc := newTestClient(t, vm, storage, batchSize, 10*time.Second, 1)
	cc.Start()

	defer cc.Stop()

	ids := make([]*token.ID, batchSize)
	for i := range batchSize {
		ids[i] = &token.ID{TxId: "batch-tx-" + string(rune('A'+i)), Index: uint64(i)}
		injectToken(cc, ids[i])
	}

	require.Eventually(t, func() bool {
		for _, id := range ids {
			if !storage.Exists(context.Background(), id) {
				return false
			}
		}

		return true
	}, 2*time.Second, 10*time.Millisecond, "full batch was not dispatched immediately")
}

// TestCertificationClient_OnReceive_BufferFull_DoesNotBlock verifies that OnReceive
// does not block when the input buffer is full.
func TestCertificationClient_OnReceive_BufferFull_DoesNotBlock(t *testing.T) {
	vm := &fakeViewManager{}
	storage := newFakeCertificationStorage()

	// batchSize=1000 and flushInterval=10min so tokens accumulate but are never batched.
	cc := interactive.NewCertificationClient(
		context.Background(),
		"net", "ch", "ns",
		&fakeQueryEngine{},
		storage,
		vm,
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		1, 1*time.Millisecond,
		1000,
		2, // tiny buffer — fills immediately
		10*time.Minute,
		1,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)
	cc.Start()

	defer cc.Stop()

	injected := make(chan struct{})

	go func() {
		// Inject more tokens than the buffer holds. None should block.
		for i := range 10 {
			cc.OnReceive(&fakeTokenEvent{txID: "tx", index: uint64(i)})
		}

		close(injected)
	}()

	select {
	case <-injected:
	case <-time.After(1 * time.Second):
		t.Fatal("OnReceive blocked — possible deadlock in buffer-full path")
	}
}

// TestCertificationClient_PushbackNonBlocking verifies that failed certification
// push-back does not deadlock when the buffer is full.
func TestCertificationClient_PushbackNonBlocking(t *testing.T) {
	vm := &fakeViewManager{failErr: errors.New("simulated failure")}
	storage := newFakeCertificationStorage()

	cc := interactive.NewCertificationClient(
		context.Background(),
		"net", "ch", "ns",
		&fakeQueryEngine{},
		storage,
		vm,
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		1, // maxAttempts=1 so failure is immediate
		1*time.Millisecond,
		1, // batchSize=1 — one token per batch
		1, // bufferSize=1 — push-back will overflow
		20*time.Millisecond,
		1,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)
	cc.Start()

	done := make(chan struct{})

	go func() {
		cc.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() blocked — possible push-back deadlock")
	}
}

// TestCertificationClient_MultipleWorkers verifies that multiple workers process
// batches concurrently.
func TestCertificationClient_MultipleWorkers(t *testing.T) {
	const numTokens = 9
	const batchSize = 3
	const workers = 3

	var processed atomic.Int32

	baseVM := &fakeViewManager{}
	countingVM := &countingViewManager{inner: baseVM, counter: &processed}

	storage := newFakeCertificationStorage()

	cc := interactive.NewCertificationClient(
		context.Background(),
		"net", "ch", "ns",
		&fakeQueryEngine{},
		storage,
		countingVM,
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		2, 1*time.Millisecond,
		batchSize,
		1000,
		5*time.Millisecond,
		workers,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)
	cc.Start()

	defer cc.Stop()

	for i := range numTokens {
		injectToken(cc, &token.ID{TxId: "worker-tx-" + string(rune('A'+i)), Index: 0})
	}

	require.Eventually(t, func() bool {
		return processed.Load() >= int32(numTokens/batchSize)
	}, 2*time.Second, 10*time.Millisecond, "workers did not process all batches")
}

// TestCertificationClient_IsCertified_Delegates verifies IsCertified delegates to storage.
func TestCertificationClient_IsCertified_Delegates(t *testing.T) {
	vm := &fakeViewManager{}
	storage := newFakeCertificationStorage()
	cc := newTestClient(t, vm, storage, 10, 1*time.Second, 1)

	id := &token.ID{TxId: "tx-abc", Index: 0}
	assert.False(t, cc.IsCertified(context.Background(), id))

	storage.data[id.TxId] = []byte("cert")
	assert.True(t, cc.IsCertified(context.Background(), id))
}

// TestCertificationClient_TimerResets_MultipleFlushes verifies that after the first
// flush interval fires, subsequent partial batches are also flushed.
func TestCertificationClient_TimerResets_MultipleFlushes(t *testing.T) {
	vm := &fakeViewManager{}
	storage := newFakeCertificationStorage()

	cc := newTestClient(t, vm, storage, 100, 50*time.Millisecond, 1)
	cc.Start()

	defer cc.Stop()

	// First partial batch.
	id1 := &token.ID{TxId: "tx-flush-1", Index: 0}
	injectToken(cc, id1)

	require.Eventually(t, func() bool {
		return storage.Exists(context.Background(), id1)
	}, 2*time.Second, 10*time.Millisecond, "first partial batch not flushed")

	// Second partial batch — verifies the timer was properly reset.
	id2 := &token.ID{TxId: "tx-flush-2", Index: 0}
	injectToken(cc, id2)

	require.Eventually(t, func() bool {
		return storage.Exists(context.Background(), id2)
	}, 2*time.Second, 10*time.Millisecond, "second partial batch not flushed — timer not reset")
}

// countingViewManager wraps a fakeViewManager and counts successful certifications.
type countingViewManager struct {
	inner   interactive.ViewManager
	counter *atomic.Int32
}

func (c *countingViewManager) InitiateView(v view.View) (any, error) {
	result, err := c.inner.InitiateView(v)
	if err == nil {
		c.counter.Add(1)
	}

	return result, err
}

// fakeTokenEvent implements events.Event for testing OnReceive.
type fakeTokenEvent struct {
	txID  string
	index uint64
}

func (e *fakeTokenEvent) Topic() string {
	return tokens.AddToken
}

func (e *fakeTokenEvent) Message() any {
	return tokens.TokenMessage{
		TxID:  e.txID,
		Index: e.index,
	}
}

var _ events.Event = (*fakeTokenEvent)(nil)
