/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package interactive_test

import (
	"context"
	"testing"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/certifier/interactive"
	"github.com/LFDT-Panurus/panurus/token/services/certifier/interactive/mock"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// makePopulatedQueryEngine returns a QueryEngineMock whose UnspentTokensIterator
// returns a fresh iterator over the given items on every call.
func makePopulatedQueryEngine(items []*token.UnspentToken) *mock.QueryEngineMock {
	qe := &mock.QueryEngineMock{}
	qe.UnspentTokensIteratorStub = func(_ context.Context) (*token2.UnspentTokensIterator, error) {
		iter := &drivermock.UnspentTokensIterator{}
		for i, item := range items {
			iter.NextReturnsOnCall(i, item, nil)
		}

		return &token2.UnspentTokensIterator{UnspentTokensIterator: iter}, nil
	}

	return qe
}

// unknownTopicEvent carries a valid TokenMessage but an unregistered topic so
// OnReceive hits the "not subscribed" early-return path.
type unknownTopicEvent struct{}

func (e *unknownTopicEvent) Topic() string { return "unregistered-topic" }
func (e *unknownTopicEvent) Message() any {
	return tokens.TokenMessage{TxID: "tx-unk", Index: 0}
}

// wrongMsgTypeEvent has the right topic but a payload that cannot be cast to
// tokens.TokenMessage, exercising the first early-return path in OnReceive.
type wrongMsgTypeEvent struct{}

func (e *wrongMsgTypeEvent) Topic() string { return tokens.AddToken }
func (e *wrongMsgTypeEvent) Message() any  { return 42 }

// ---------------------------------------------------------------------------
// NewCertificationClient — notifier path
// ---------------------------------------------------------------------------

// TestNewCertificationClient_WithNotifier verifies that when a Subscriber is
// provided the client registers itself for the expected topics.
func TestNewCertificationClient_WithNotifier(t *testing.T) {
	sub := &mock.SubscriberMock{}
	storage := newFakeCertificationStorage()

	cc := interactive.NewCertificationClient(
		context.Background(),
		"test-network",
		"ch", "ns",
		&fakeQueryEngine{},
		storage,
		&fakeViewManager{},
		[]view.Identity{view.Identity([]byte("c"))},
		sub,
		interactive.DefaultMaxAttempts, interactive.DefaultWaitTime,
		interactive.DefaultBatchSize, interactive.DefaultBufferSize,
		interactive.DefaultFlushInterval, interactive.DefaultWorkers,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)

	assert.NotNil(t, cc)
	require.Equal(t, 1, sub.SubscribeCallCount(), "expected exactly one Subscribe call")
	topic, _ := sub.SubscribeArgsForCall(0)
	assert.Equal(t, tokens.AddToken, topic, "expected AddToken topic to be subscribed")
}

// ---------------------------------------------------------------------------
// RequestCertification
// ---------------------------------------------------------------------------

// TestRequestCertification_AllAlreadyCertified verifies that when every
// supplied token is already certified, the ViewManager is never called.
func TestRequestCertification_AllAlreadyCertified(t *testing.T) {
	vm := &fakeViewManager{}
	storage := newFakeCertificationStorage()

	id1 := &token.ID{TxId: "already-1", Index: 0}
	id2 := &token.ID{TxId: "already-2", Index: 0}
	storage.data["already-1"] = []byte("cert-1")
	storage.data["already-2"] = []byte("cert-2")

	cc := newTestClient(t, vm, storage, 10, 1*time.Second, 1)

	err := cc.RequestCertification(context.Background(), id1, id2)
	require.NoError(t, err)
	assert.Equal(t, 0, vm.callCount(), "ViewManager must not be called when all tokens are already certified")
}

// TestRequestCertification_PartialAlreadyCertified verifies that only the
// uncertified subset is passed to the ViewManager.
func TestRequestCertification_PartialAlreadyCertified(t *testing.T) {
	vm := &fakeViewManager{}
	storage := newFakeCertificationStorage()

	idCertified := &token.ID{TxId: "already", Index: 0}
	storage.data["already"] = []byte("cert-already")

	idNew := &token.ID{TxId: "new-tx", Index: 0}

	cc := newTestClient(t, vm, storage, 10, 1*time.Second, 1)

	err := cc.RequestCertification(context.Background(), idCertified, idNew)
	require.NoError(t, err)
	assert.Equal(t, 1, vm.callCount(), "ViewManager should be called once for the uncertified token")
	assert.True(t, storage.Exists(context.Background(), idNew), "new token should be certified after the call")
	assert.True(t, storage.Exists(context.Background(), idCertified), "pre-certified token should still be present")
}

// TestRequestCertification_RetriesExhausted verifies that when the
// ViewManager keeps failing, RequestCertification returns an error after
// exhausting maxAttempts.
func TestRequestCertification_RetriesExhausted(t *testing.T) {
	certErr := errors.New("certifier down")
	vm := &mock.ViewManagerMock{}
	vm.InitiateViewReturns(nil, certErr)
	storage := newFakeCertificationStorage()

	cc := interactive.NewCertificationClient(
		context.Background(),
		"test-network",
		"ch", "ns",
		&fakeQueryEngine{},
		storage,
		vm,
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		2,                  // maxAttempts=2
		1*time.Millisecond, // short retry gap
		10, 1000, 1*time.Second, 1,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)

	id := &token.ID{TxId: "retry-tx", Index: 0}
	err := cc.RequestCertification(context.Background(), id)

	require.Error(t, err)
	require.ErrorIs(t, err, certErr)
	assert.Equal(t, 2, vm.InitiateViewCallCount(), "ViewManager should be called exactly maxAttempts times")
}

// TestRequestCertification_ContextCancelled verifies that cancelling the
// context during a retry wait causes RequestCertification to return
// context.DeadlineExceeded promptly instead of waiting for the full retry gap.
func TestRequestCertification_ContextCancelled(t *testing.T) {
	vm := &mock.ViewManagerMock{}
	vm.InitiateViewReturns(nil, errors.New("always fails"))
	storage := newFakeCertificationStorage()

	cc := interactive.NewCertificationClient(
		context.Background(),
		"test-network",
		"ch", "ns",
		&fakeQueryEngine{},
		storage,
		vm,
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		10,             // many attempts — context must cancel first
		10*time.Second, // long wait between retries
		10, 1000, 1*time.Second, 1,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	id := &token.ID{TxId: "ctx-cancel-tx", Index: 0}
	err := cc.RequestCertification(ctx, id)

	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestRequestCertification_InvalidReturnType verifies the error path when the
// ViewManager returns a type that cannot be cast to map[*token.ID][]byte.
func TestRequestCertification_InvalidReturnType(t *testing.T) {
	vm := &mock.ViewManagerMock{}
	vm.InitiateViewReturns("not-a-map", nil)
	storage := newFakeCertificationStorage()

	cc := interactive.NewCertificationClient(
		context.Background(),
		"test-network",
		"ch", "ns",
		&fakeQueryEngine{},
		storage,
		vm,
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		1, 1*time.Millisecond,
		10, 1000, 1*time.Second, 1,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)

	id := &token.ID{TxId: "bad-type-tx", Index: 0}
	err := cc.RequestCertification(context.Background(), id)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid type")
}

// TestRequestCertification_StoreError verifies that an error from
// CertificationStorage.Store is propagated back to the caller.
func TestRequestCertification_StoreError(t *testing.T) {
	vm := &fakeViewManager{}
	storeErr := errors.New("storage is unavailable")
	storage := &mock.CertificationStorageMock{}
	storage.ExistsReturns(false)
	storage.StoreReturns(storeErr)

	cc := interactive.NewCertificationClient(
		context.Background(),
		"test-network",
		"ch", "ns",
		&fakeQueryEngine{},
		storage,
		vm,
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		1, 1*time.Millisecond,
		10, 1000, 1*time.Second, 1,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)

	id := &token.ID{TxId: "store-err-tx", Index: 0}
	err := cc.RequestCertification(context.Background(), id)

	require.Error(t, err)
	require.ErrorIs(t, err, storeErr)
}

// TestRequestCertification_RetryThenSuccess verifies that a transient error on
// the first attempt is retried and the subsequent success is accepted.
func TestRequestCertification_RetryThenSuccess(t *testing.T) {
	transientErr := errors.New("transient error")
	// fakeViewManager.failErr is cleared after the first failure, so the
	// second attempt succeeds automatically.
	vm := &fakeViewManager{failErr: transientErr}
	storage := newFakeCertificationStorage()

	cc := interactive.NewCertificationClient(
		context.Background(),
		"test-network",
		"ch", "ns",
		&fakeQueryEngine{},
		storage,
		vm,
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		3, // 3 attempts available
		1*time.Millisecond,
		10, 1000, 1*time.Second, 1,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)

	id := &token.ID{TxId: "retry-ok-tx", Index: 0}
	err := cc.RequestCertification(context.Background(), id)

	require.NoError(t, err)
	assert.Equal(t, 2, vm.callCount(), "ViewManager called twice: once failed, once succeeded")
	assert.True(t, storage.Exists(context.Background(), id), "token should be certified after successful retry")
}

// ---------------------------------------------------------------------------
// OnReceive edge cases
// ---------------------------------------------------------------------------

// TestOnReceive_WrongMessageType verifies that an event whose Message() cannot
// be cast to tokens.TokenMessage is silently dropped (no panic, no enqueue).
func TestOnReceive_WrongMessageType(t *testing.T) {
	vm := &fakeViewManager{}
	storage := newFakeCertificationStorage()
	cc := newTestClient(t, vm, storage, 10, 1*time.Second, 1)

	assert.NotPanics(t, func() {
		cc.OnReceive(&wrongMsgTypeEvent{})
	})
	assert.Empty(t, interactive.ClientTokensChan(cc), "wrong-type event must not enqueue a token")
}

// TestOnReceive_UnknownTopic verifies that an event with an unregistered topic
// is silently dropped even when the message type is correct.
func TestOnReceive_UnknownTopic(t *testing.T) {
	vm := &fakeViewManager{}
	storage := newFakeCertificationStorage()
	cc := newTestClient(t, vm, storage, 10, 1*time.Second, 1)

	assert.NotPanics(t, func() {
		cc.OnReceive(&unknownTopicEvent{})
	})
	assert.Empty(t, interactive.ClientTokensChan(cc), "unknown-topic event must not enqueue a token")
}

// ---------------------------------------------------------------------------
// Scan
// ---------------------------------------------------------------------------

// TestScan_EmptyVault verifies that Scan on an empty vault succeeds without
// calling the ViewManager.
func TestScan_EmptyVault(t *testing.T) {
	vm := &fakeViewManager{}
	storage := newFakeCertificationStorage()

	cc := newTestClient(t, vm, storage, 10, 1*time.Second, 1)

	err := cc.Scan()
	require.NoError(t, err)
	assert.Equal(t, 0, vm.callCount(), "empty vault should not trigger any certification requests")
}

// TestScan_UncertifiedTokens verifies that Scan picks up tokens from the vault
// and requests their certification.
func TestScan_UncertifiedTokens(t *testing.T) {
	vm := &fakeViewManager{}
	storage := newFakeCertificationStorage()

	unspent := []*token.UnspentToken{
		{Id: token.ID{TxId: "vault-tx-1", Index: 0}},
		{Id: token.ID{TxId: "vault-tx-2", Index: 0}},
	}

	cc := interactive.NewCertificationClient(
		context.Background(),
		"test-network",
		"ch", "ns",
		makePopulatedQueryEngine(unspent),
		storage,
		vm,
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		2, 1*time.Millisecond,
		10, 1000, 1*time.Second, 1,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)

	err := cc.Scan()
	require.NoError(t, err)

	for _, ut := range unspent {
		id := &ut.Id
		assert.True(t, storage.Exists(context.Background(), id), "token %s should be certified after Scan", ut.Id.TxId)
	}
}

// TestScan_AlreadyCertified verifies that tokens already present in storage
// are skipped — the ViewManager is not called for them.
func TestScan_AlreadyCertified(t *testing.T) {
	vm := &fakeViewManager{}
	storage := newFakeCertificationStorage()

	// Pre-certify the vault token.
	storage.data["pre-certified"] = []byte("existing-cert")

	unspent := []*token.UnspentToken{
		{Id: token.ID{TxId: "pre-certified", Index: 0}},
	}

	cc := interactive.NewCertificationClient(
		context.Background(),
		"test-network",
		"ch", "ns",
		makePopulatedQueryEngine(unspent),
		storage,
		vm,
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		2, 1*time.Millisecond,
		10, 1000, 1*time.Second, 1,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)

	err := cc.Scan()
	require.NoError(t, err)
	assert.Equal(t, 0, vm.callCount(), "already-certified token should not trigger a ViewManager call")
}

// ---------------------------------------------------------------------------
// processBatch
// ---------------------------------------------------------------------------

// TestProcessBatch_EmptyBatch_TriggersVaultScan verifies that an empty batch
// causes processBatch to fall through to a vault scan, certifying any
// uncertified tokens it finds.
func TestProcessBatch_EmptyBatch_TriggersVaultScan(t *testing.T) {
	vm := &fakeViewManager{}
	storage := newFakeCertificationStorage()

	unspent := []*token.UnspentToken{
		{Id: token.ID{TxId: "vault-via-empty-batch", Index: 0}},
	}

	cc := interactive.NewCertificationClient(
		context.Background(),
		"test-network",
		"ch", "ns",
		makePopulatedQueryEngine(unspent),
		storage,
		vm,
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		2, 1*time.Millisecond,
		10, 1000, 1*time.Second, 1,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)

	// Invoke directly — no Start() needed.
	interactive.ClientProcessBatch(cc, []*token.ID{})

	id := &unspent[0].Id
	assert.True(t, storage.Exists(context.Background(), id),
		"vault token should be certified after empty-batch scan")
}

// TestProcessBatch_CertificationFailure_PushesBackToBuffer verifies that when
// the ViewManager fails, the tokens in the batch are pushed back into the
// tokens channel for a future retry rather than silently dropped.
func TestProcessBatch_CertificationFailure_PushesBackToBuffer(t *testing.T) {
	certErr := errors.New("certification service unavailable")
	vm := &mock.ViewManagerMock{}
	vm.InitiateViewReturns(nil, certErr)
	storage := newFakeCertificationStorage()

	cc := interactive.NewCertificationClient(
		context.Background(),
		"test-network",
		"ch", "ns",
		&fakeQueryEngine{},
		storage,
		vm,
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		1, // maxAttempts=1 — fail immediately, no retry loop
		1*time.Millisecond,
		10,
		100, // bufferSize=100, enough room for push-back
		1*time.Second, 1,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)
	// Do NOT Start() — we call processBatch directly so the tokens channel
	// is not being consumed by the accumulator.

	id := &token.ID{TxId: "pushback-tx", Index: 0}
	interactive.ClientProcessBatch(cc, []*token.ID{id})

	assert.Len(t, interactive.ClientTokensChan(cc), 1, "failed token should be pushed back to the tokens channel")
}

// ---------------------------------------------------------------------------
// Driver construction
// ---------------------------------------------------------------------------

// TestNewDriver_Construction verifies that NewDriver initialises an empty
// client map and a nil CertificationService ready for lazy initialisation.
func TestNewDriver_Construction(t *testing.T) {
	mp := &disabled.Provider{}

	d := interactive.NewDriver(nil, nil, nil, &fakeViewManager{}, &mock.ResponderRegistryMock{}, mp)

	assert.NotNil(t, d)
	assert.NotNil(t, d.CertificationClients, "CertificationClients map should be initialised")
	assert.Empty(t, d.CertificationClients, "no clients should exist before any TMS is registered")
	assert.Nil(t, d.CertificationService, "CertificationService should be nil until first use")
	assert.Equal(t, mp, d.MetricsProvider)
}

// ---------------------------------------------------------------------------
// Scan — error paths
// ---------------------------------------------------------------------------

// TestScan_QueryEngineError verifies that an error from UnspentTokensIterator
// is propagated back to the caller.
func TestScan_QueryEngineError(t *testing.T) {
	queryErr := errors.New("vault iterator failure")
	qe := &mock.QueryEngineMock{}
	qe.UnspentTokensIteratorReturns(nil, queryErr)

	cc := interactive.NewCertificationClient(
		context.Background(),
		"net", "ch", "ns",
		qe,
		newFakeCertificationStorage(),
		&fakeViewManager{},
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		1, 1*time.Millisecond,
		10, 1000, 1*time.Second, 1,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)

	err := cc.Scan()
	require.Error(t, err)
	require.ErrorIs(t, err, queryErr)
}

// TestScan_RequestCertificationError verifies that when RequestCertification
// fails for uncertified vault tokens, Scan propagates the error.
func TestScan_RequestCertificationError(t *testing.T) {
	certErr := errors.New("certification service down")
	vm := &mock.ViewManagerMock{}
	vm.InitiateViewReturns(nil, certErr)

	unspent := []*token.UnspentToken{
		{Id: token.ID{TxId: "scan-fail-tx", Index: 0}},
	}

	cc := interactive.NewCertificationClient(
		context.Background(),
		"net", "ch", "ns",
		makePopulatedQueryEngine(unspent),
		newFakeCertificationStorage(),
		vm,
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		1, 1*time.Millisecond,
		10, 1000, 1*time.Second, 1,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)

	err := cc.Scan()
	require.Error(t, err)
	require.ErrorIs(t, err, certErr)
}

// ---------------------------------------------------------------------------
// processBatch — remaining edge cases
// ---------------------------------------------------------------------------

// TestProcessBatch_EmptyBatch_ScanError verifies that when the vault scan
// triggered by an empty batch returns an error, processBatch logs and returns
// without panicking.
func TestProcessBatch_EmptyBatch_ScanError(t *testing.T) {
	qe := &mock.QueryEngineMock{}
	qe.UnspentTokensIteratorReturns(nil, errors.New("vault error"))

	cc := interactive.NewCertificationClient(
		context.Background(),
		"net", "ch", "ns",
		qe,
		newFakeCertificationStorage(),
		&fakeViewManager{},
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		1, 1*time.Millisecond,
		10, 1000, 1*time.Second, 1,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)

	require.NotPanics(t, func() {
		interactive.ClientProcessBatch(cc, []*token.ID{})
	})
}

// TestProcessBatch_FailureBufferFull_DropsTokens verifies that when the
// push-back buffer is already full after a certification failure, the token
// is dropped rather than causing a deadlock.
func TestProcessBatch_FailureBufferFull_DropsTokens(t *testing.T) {
	certErr := errors.New("certifier unavailable")
	vm := &mock.ViewManagerMock{}
	vm.InitiateViewReturns(nil, certErr)

	cc := interactive.NewCertificationClient(
		context.Background(),
		"net", "ch", "ns",
		&fakeQueryEngine{},
		newFakeCertificationStorage(),
		vm,
		[]view.Identity{view.Identity([]byte("c"))},
		nil,
		1, 1*time.Millisecond,
		1,
		1, // bufferSize=1 — filled below so push-back overflows
		1*time.Second, 1,
		interactive.DefaultResponseTimeout,
		&disabled.Provider{},
	)

	// Fill the buffer to capacity so push-back hits the default (drop) branch.
	interactive.ClientTokensChan(cc) <- &token.ID{TxId: "pre-fill", Index: 0}

	done := make(chan struct{})
	go func() {
		interactive.ClientProcessBatch(cc, []*token.ID{{TxId: "fail-tx", Index: 0}})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("processBatch deadlocked when buffer was full after failure")
	}
}
