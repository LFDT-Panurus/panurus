/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/finality"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/finality/mock"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/finality/queue"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	fdriver "github.com/hyperledger-labs/fabric-smart-client/platform/fabric/driver"
	"github.com/hyperledger/fabric-x-common/api/committerpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListenerEvent_Process_Valid(t *testing.T) {
	ctx := t.Context()
	txID := "tx123"
	namespace := "token-namespace"
	tokenRequestHash := []byte("hash123")
	key := "token-request-key"

	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	mockKT.CreateTokenRequestKeyReturns(key, nil)
	mockQS.GetStateReturns(&cdriver.VaultValue{Raw: tokenRequestHash}, nil)

	event := &finality.ListenerEvent{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          txID,
		Status:        fdriver.Valid,
		StatusMessage: "",
		Namespace:     namespace,
	}

	err := event.Process(ctx)
	require.NoError(t, err)

	assert.Equal(t, 1, mockKT.CreateTokenRequestKeyCallCount())
	assert.Equal(t, txID, mockKT.CreateTokenRequestKeyArgsForCall(0))

	assert.Equal(t, 1, mockQS.GetStateCallCount())
	ns, k := mockQS.GetStateArgsForCall(0)
	assert.Equal(t, namespace, ns)
	assert.Equal(t, key, k)

	assert.Equal(t, 1, mockListener.OnStatusCallCount())
	callCtx, callTxID, callStatus, callMsg, callHash := mockListener.OnStatusArgsForCall(0)
	assert.Equal(t, ctx, callCtx)
	assert.Equal(t, txID, callTxID)
	assert.Equal(t, fdriver.Valid, callStatus)
	assert.Empty(t, callMsg)
	assert.Equal(t, tokenRequestHash, callHash)
}

func TestListenerEvent_Process_Invalid(t *testing.T) {
	ctx := t.Context()
	txID := "tx123"
	statusMessage := "validation failed"

	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	event := &finality.ListenerEvent{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          txID,
		Status:        fdriver.Invalid,
		StatusMessage: statusMessage,
		Namespace:     "token-namespace",
	}

	err := event.Process(ctx)
	require.NoError(t, err)

	// Should not fetch token request hash for invalid transactions
	assert.Equal(t, 0, mockKT.CreateTokenRequestKeyCallCount())
	assert.Equal(t, 0, mockQS.GetStateCallCount())

	assert.Equal(t, 1, mockListener.OnStatusCallCount())
	callCtx, callTxID, callStatus, callMsg, callHash := mockListener.OnStatusArgsForCall(0)
	assert.Equal(t, ctx, callCtx)
	assert.Equal(t, txID, callTxID)
	assert.Equal(t, fdriver.Invalid, callStatus)
	assert.Equal(t, statusMessage, callMsg)
	assert.Nil(t, callHash)
}

func TestListenerEvent_Process_Unknown_TxCheckSucceeds(t *testing.T) {
	ctx := t.Context()
	txID := "tx123"
	namespace := "token-namespace"
	tokenRequestHash := []byte("hash123")
	key := "token-request-key"

	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	// TxCheck will query the transaction status
	mockQS.GetTransactionStatusReturns(int32(committerpb.Status_COMMITTED), nil)
	mockKT.CreateTokenRequestKeyReturns(key, nil)
	mockQS.GetStateReturns(&cdriver.VaultValue{Raw: tokenRequestHash}, nil)

	event := &finality.ListenerEvent{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          txID,
		Status:        fdriver.Unknown,
		StatusMessage: "",
		Namespace:     namespace,
	}

	err := event.Process(ctx)
	require.NoError(t, err)

	// TxCheck should have been executed
	assert.Equal(t, 1, mockQS.GetTransactionStatusCallCount())
	assert.Equal(t, txID, mockQS.GetTransactionStatusArgsForCall(0))

	// Should fetch token request hash since status is valid
	assert.Equal(t, 1, mockKT.CreateTokenRequestKeyCallCount())
	assert.Equal(t, 1, mockQS.GetStateCallCount())

	assert.Equal(t, 1, mockListener.OnStatusCallCount())
}

func TestListenerEvent_Process_Unknown_TxCheckFails(t *testing.T) {
	ctx := t.Context()
	txID := "tx123"

	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	// TxCheck will fail to query the transaction status
	mockQS.GetTransactionStatusReturns(int32(committerpb.Status_STATUS_UNSPECIFIED), errors.New("query failed"))

	event := &finality.ListenerEvent{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          txID,
		Status:        fdriver.Unknown,
		StatusMessage: "",
		Namespace:     "token-namespace",
	}

	err := event.Process(ctx)
	require.NoError(t, err)

	// TxCheck failed, so the event should still notify with Unknown status
	assert.Equal(t, 1, mockListener.OnStatusCallCount())
	_, _, callStatus, _, callHash := mockListener.OnStatusArgsForCall(0)
	assert.Equal(t, fdriver.Unknown, callStatus)
	assert.Nil(t, callHash)
}

func TestListenerEvent_Process_Busy_TxCheckSucceeds(t *testing.T) {
	ctx := t.Context()
	txID := "tx123"
	namespace := "token-namespace"
	tokenRequestHash := []byte("hash123")
	key := "token-request-key"

	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	// TxCheck will query the transaction status and find it committed
	mockQS.GetTransactionStatusReturns(int32(committerpb.Status_COMMITTED), nil)
	mockKT.CreateTokenRequestKeyReturns(key, nil)
	mockQS.GetStateReturns(&cdriver.VaultValue{Raw: tokenRequestHash}, nil)

	event := &finality.ListenerEvent{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          txID,
		Status:        fdriver.Busy,
		StatusMessage: "",
		Namespace:     namespace,
	}

	err := event.Process(ctx)
	require.NoError(t, err)

	// TxCheck should have been executed
	assert.Equal(t, 1, mockQS.GetTransactionStatusCallCount())
	assert.Equal(t, 1, mockListener.OnStatusCallCount())
}

func TestListenerEvent_Process_RetriesOnTransientError(t *testing.T) {
	ctx := t.Context()
	txID := "tx123"
	namespace := "token-namespace"
	tokenRequestHash := []byte("hash123")
	key := "token-request-key"

	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	var attempts int
	// Fail twice, succeed on third attempt
	mockQS.GetStateStub = func(_ string, _ string) (*cdriver.VaultValue, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("transient peer error")
		}

		return &cdriver.VaultValue{Raw: tokenRequestHash}, nil
	}
	mockKT.CreateTokenRequestKeyReturns(key, nil)

	event := &finality.ListenerEvent{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          txID,
		Status:        fdriver.Valid,
		Namespace:     namespace,
		MaxRetries:    3,
		RetryInterval: 10 * time.Millisecond,
	}

	err := event.Process(ctx)
	require.NoError(t, err)

	// Should have retried and eventually called OnStatus
	assert.Equal(t, 3, attempts)
	assert.Equal(t, 1, mockListener.OnStatusCallCount())
	assert.Equal(t, 0, mockListener.OnErrorCallCount())
}

func TestListenerEvent_Process_CallsOnErrorAfterAllRetriesExhausted(t *testing.T) {
	ctx := t.Context()
	txID := "tx123"
	key := "token-request-key"

	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	mockKT.CreateTokenRequestKeyReturns(key, nil)
	mockQS.GetStateReturns(nil, errors.New("persistent peer error"))

	event := &finality.ListenerEvent{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          txID,
		Status:        fdriver.Valid,
		Namespace:     "token-namespace",
		MaxRetries:    2,
		RetryInterval: 10 * time.Millisecond,
	}

	err := event.Process(ctx)
	require.NoError(t, err)

	// All retries exhausted — OnStatus must NOT be called, OnError must be called once
	assert.Equal(t, 0, mockListener.OnStatusCallCount())
	assert.Equal(t, 1, mockListener.OnErrorCallCount())
	_, callTxID, callErr := mockListener.OnErrorArgsForCall(0)
	assert.Equal(t, txID, callTxID)
	assert.Contains(t, callErr.Error(), "persistent peer error")
}

func TestListenerEvent_Process_CreateTokenRequestKeyError(t *testing.T) {
	ctx := t.Context()
	txID := "tx123"

	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	mockKT.CreateTokenRequestKeyReturns("", errors.New("key creation failed"))

	event := &finality.ListenerEvent{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          txID,
		Status:        fdriver.Valid,
		StatusMessage: "",
		Namespace:     "token-namespace",
		MaxRetries:    2,
		RetryInterval: 10 * time.Millisecond,
	}

	err := event.Process(ctx)
	require.NoError(t, err)
	// All retries exhausted — listener must be notified via OnError
	assert.Equal(t, 0, mockListener.OnStatusCallCount())
	assert.Equal(t, 1, mockListener.OnErrorCallCount())
	_, callTxID, callErr := mockListener.OnErrorArgsForCall(0)
	assert.Equal(t, txID, callTxID)
	assert.Contains(t, callErr.Error(), "key creation failed")
}

func TestListenerEvent_Process_GetStateError(t *testing.T) {
	ctx := t.Context()
	txID := "tx123"
	key := "token-request-key"

	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	mockKT.CreateTokenRequestKeyReturns(key, nil)
	mockQS.GetStateReturns(nil, errors.New("state retrieval failed"))

	event := &finality.ListenerEvent{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          txID,
		Status:        fdriver.Valid,
		StatusMessage: "",
		Namespace:     "token-namespace",
		MaxRetries:    2,
		RetryInterval: 10 * time.Millisecond,
	}

	err := event.Process(ctx)
	require.NoError(t, err)
	// All retries exhausted — listener must be notified via OnError
	assert.Equal(t, 0, mockListener.OnStatusCallCount())
	assert.Equal(t, 1, mockListener.OnErrorCallCount())
	_, callTxID, callErr := mockListener.OnErrorArgsForCall(0)
	assert.Equal(t, txID, callTxID)
	assert.Contains(t, callErr.Error(), "state retrieval failed")
}

func TestListenerEvent_String(t *testing.T) {
	event := &finality.ListenerEvent{
		TxID: "tx123",
	}

	str := event.String()
	assert.Equal(t, "ListenerEvent[tx123]", str)
}

func TestTxCheck_Process_Valid(t *testing.T) {
	ctx := t.Context()
	txID := "tx123"
	namespace := "token-namespace"
	tokenRequestHash := []byte("hash123")
	key := "token-request-key"

	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	mockQS.GetTransactionStatusReturns(int32(committerpb.Status_COMMITTED), nil)
	mockKT.CreateTokenRequestKeyReturns(key, nil)
	mockQS.GetStateReturns(&cdriver.VaultValue{Raw: tokenRequestHash}, nil)

	txCheck := &finality.TxCheck{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          txID,
		Namespace:     namespace,
	}

	err := txCheck.Process(ctx)
	require.NoError(t, err)

	assert.Equal(t, 1, mockQS.GetTransactionStatusCallCount())
	assert.Equal(t, txID, mockQS.GetTransactionStatusArgsForCall(0))

	assert.Equal(t, 1, mockKT.CreateTokenRequestKeyCallCount())
	assert.Equal(t, 1, mockQS.GetStateCallCount())

	assert.Equal(t, 1, mockListener.OnStatusCallCount())
	callCtx, callTxID, callStatus, callMsg, callHash := mockListener.OnStatusArgsForCall(0)
	assert.Equal(t, ctx, callCtx)
	assert.Equal(t, txID, callTxID)
	assert.Equal(t, fdriver.Valid, callStatus)
	assert.Empty(t, callMsg)
	assert.Equal(t, tokenRequestHash, callHash)
}

func TestTxCheck_Process_Invalid(t *testing.T) {
	ctx := t.Context()
	txID := "tx123"

	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	mockQS.GetTransactionStatusReturns(int32(committerpb.Status_ABORTED_SIGNATURE_INVALID), nil)

	txCheck := &finality.TxCheck{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          txID,
		Namespace:     "token-namespace",
	}

	err := txCheck.Process(ctx)
	require.NoError(t, err)

	// Should not fetch token request hash for invalid transactions
	assert.Equal(t, 0, mockKT.CreateTokenRequestKeyCallCount())
	assert.Equal(t, 0, mockQS.GetStateCallCount())

	assert.Equal(t, 1, mockListener.OnStatusCallCount())
	_, _, callStatus, _, callHash := mockListener.OnStatusArgsForCall(0)
	assert.Equal(t, fdriver.Invalid, callStatus)
	assert.Nil(t, callHash)
}

func TestTxCheck_Process_Unknown(t *testing.T) {
	ctx := t.Context()
	txID := "tx123"

	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	mockQS.GetTransactionStatusReturns(int32(committerpb.Status_STATUS_UNSPECIFIED), nil)

	txCheck := &finality.TxCheck{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          txID,
		Namespace:     "token-namespace",
	}

	err := txCheck.Process(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transaction [tx123] is not in a valid state")

	// Should not notify listener
	assert.Equal(t, 0, mockListener.OnStatusCallCount())
}

func TestTxCheck_Process_GetTransactionStatusError(t *testing.T) {
	ctx := t.Context()
	txID := "tx123"

	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	mockQS.GetTransactionStatusReturns(0, errors.New("status query failed"))

	txCheck := &finality.TxCheck{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          txID,
		Namespace:     "token-namespace",
	}

	err := txCheck.Process(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "can't get status for tx")
	assert.Contains(t, err.Error(), "status query failed")
}

func TestTxCheck_String(t *testing.T) {
	txCheck := &finality.TxCheck{
		TxID: "tx123",
	}

	str := txCheck.String()
	assert.Equal(t, "TxCheck[tx123]", str)
}

func TestNSFinalityListener_OnStatus(t *testing.T) {
	ctx := t.Context()
	txID := "tx123"
	namespace := "token-namespace"

	mockQueue := &mock.Queue{}
	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	var enqueuedEvent queue.Event
	mockQueue.EnqueueBlockingCalls(func(ctx context.Context, event queue.Event) error {
		enqueuedEvent = event

		return nil
	})

	listener := finality.NewNSFinalityListener(namespace, mockListener, mockQueue, mockQS, mockKT)

	listener.OnStatus(ctx, txID, fdriver.Valid, "")

	assert.Equal(t, 1, mockQueue.EnqueueBlockingCallCount())

	// Verify the enqueued event
	require.NotNil(t, enqueuedEvent)
	listenerEvent, ok := enqueuedEvent.(*finality.ListenerEvent)
	require.True(t, ok)
	assert.Equal(t, txID, listenerEvent.TxID)
	assert.Equal(t, fdriver.Valid, listenerEvent.Status)
	assert.Equal(t, namespace, listenerEvent.Namespace)
}

func TestNSFinalityListener_OnStatus_EnqueueError(t *testing.T) {
	ctx := t.Context()
	txID := "tx123"
	namespace := "token-namespace"

	mockQueue := &mock.Queue{}
	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	mockQueue.EnqueueBlockingReturns(errors.New("queue full"))

	listener := finality.NewNSFinalityListener(namespace, mockListener, mockQueue, mockQS, mockKT)

	// Should not panic even if enqueue fails
	listener.OnStatus(ctx, txID, fdriver.Valid, "")

	assert.Equal(t, 1, mockQueue.EnqueueBlockingCallCount())
}

// testPollerConfig polls fast so tests don't wait on the default interval.
type testPollerConfig struct{}

func (testPollerConfig) PollInterval() time.Duration { return 10 * time.Millisecond }
func (testPollerConfig) PollBatchSize() int          { return finality.DefaultPollBatchSize }
func (testPollerConfig) PendingTTL() time.Duration   { return time.Minute }

func TestNSListenerManager_AddFinalityListener(t *testing.T) {
	txID := "tx123"
	namespace := "token-namespace"

	mockLM := &mock.ListenerManager{}
	mockQueue := &mock.Queue{}
	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	mockQS.GetTransactionStatusReturns(int32(committerpb.Status_COMMITTED), nil)
	mockQS.GetTransactionStatusesReturns(map[string]int32{
		txID: int32(committerpb.Status_COMMITTED),
	}, nil)
	mockKT.CreateTokenRequestKeyReturns("key", nil)
	mockQS.GetStatesReturns(map[cdriver.Namespace]map[cdriver.PKey]cdriver.VaultValue{
		namespace: {"key": {Raw: []byte("hash")}},
	}, nil)

	events := make(chan queue.Event, 1)
	mockQueue.EnqueueCalls(func(event queue.Event) error {
		events <- event

		return nil
	})

	manager := finality.NewNSListenerManager(mockLM, mockQueue, mockQS, mockKT, testPollerConfig{})

	err := manager.AddFinalityListener(namespace, txID, mockListener)
	require.NoError(t, err)

	// The shared poller resolves the pending tx and enqueues a notification event.
	var event queue.Event
	select {
	case event = <-events:
	case <-time.After(5 * time.Second):
		t.Fatal("poller did not resolve the pending tx")
	}

	require.NoError(t, event.Process(t.Context()))

	require.Equal(t, 1, mockListener.OnStatusCallCount())
	_, gotTxID, gotStatus, _, gotHash := mockListener.OnStatusArgsForCall(0)
	assert.Equal(t, txID, gotTxID)
	assert.Equal(t, fdriver.Valid, gotStatus)
	assert.Equal(t, []byte("hash"), gotHash)
}

func TestNSListenerManager_EnqueueErrorRetriesNextSweep(t *testing.T) {
	txID := "tx123"
	namespace := "token-namespace"

	mockLM := &mock.ListenerManager{}
	mockQueue := &mock.Queue{}
	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	mockQS.GetTransactionStatusReturns(int32(committerpb.Status_COMMITTED), nil)
	mockQS.GetTransactionStatusesReturns(map[string]int32{
		txID: int32(committerpb.Status_COMMITTED),
	}, nil)
	mockKT.CreateTokenRequestKeyReturns("key", nil)
	mockQS.GetStatesReturns(map[cdriver.Namespace]map[cdriver.PKey]cdriver.VaultValue{
		namespace: {"key": {Raw: []byte("hash")}},
	}, nil)
	mockQueue.EnqueueReturns(errors.New("queue full"))

	manager := finality.NewNSListenerManager(mockLM, mockQueue, mockQS, mockKT, testPollerConfig{})

	// Registration itself does not fail; the tx stays pending and each sweep
	// retries the enqueue.
	err := manager.AddFinalityListener(namespace, txID, mockListener)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return mockQueue.EnqueueCallCount() >= 2
	}, 5*time.Second, 10*time.Millisecond)

	assert.Equal(t, 0, mockListener.OnStatusCallCount())
}

func TestNSListenerManager_MultipleListenersSameTx(t *testing.T) {
	txID := "tx123"
	namespace := "token-namespace"

	mockLM := &mock.ListenerManager{}
	mockQueue := &mock.Queue{}
	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	listener1 := &mock.Listener{}
	listener2 := &mock.Listener{}

	mockQS.GetTransactionStatusReturns(int32(committerpb.Status_COMMITTED), nil)
	mockQS.GetTransactionStatusesReturns(map[string]int32{
		txID: int32(committerpb.Status_COMMITTED),
	}, nil)
	mockKT.CreateTokenRequestKeyReturns("key", nil)
	mockQS.GetStatesReturns(map[cdriver.Namespace]map[cdriver.PKey]cdriver.VaultValue{
		namespace: {"key": {Raw: []byte("hash")}},
	}, nil)

	events := make(chan queue.Event, 4)
	mockQueue.EnqueueCalls(func(event queue.Event) error {
		events <- event

		return nil
	})

	manager := finality.NewNSListenerManager(mockLM, mockQueue, mockQS, mockKT, testPollerConfig{})

	require.NoError(t, manager.AddFinalityListener(namespace, txID, listener1))
	require.NoError(t, manager.AddFinalityListener(namespace, txID, listener2))

	// Both waiters are notified, regardless of how many sweeps it takes.
	deadline := time.After(5 * time.Second)
	for listener1.OnStatusCallCount() == 0 || listener2.OnStatusCallCount() == 0 {
		select {
		case event := <-events:
			require.NoError(t, event.Process(t.Context()))
		case <-deadline:
			t.Fatal("not all listeners were notified")
		}
	}

	assert.Equal(t, 1, listener1.OnStatusCallCount())
	assert.Equal(t, 1, listener2.OnStatusCallCount())
}

func TestNSListenerManager_FallbackSkipsFailingTx(t *testing.T) {
	namespace := "token-namespace"

	mockLM := &mock.ListenerManager{}
	mockQueue := &mock.Queue{}
	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	listenerA := &mock.Listener{}
	listenerB := &mock.Listener{}

	// txA is not yet committed and errors on the per-tx query; txB is committed.
	mockQS.GetTransactionStatusCalls(func(txID string) (int32, error) {
		if txID == "txA" {
			return 0, errors.New("transaction not found")
		}

		return int32(committerpb.Status_COMMITTED), nil
	})
	mockQS.GetTransactionStatusesReturns(map[string]int32{
		"txB": int32(committerpb.Status_COMMITTED),
	}, nil)
	mockKT.CreateTokenRequestKeyReturns("key", nil)
	mockQS.GetStatesReturns(map[cdriver.Namespace]map[cdriver.PKey]cdriver.VaultValue{
		namespace: {"key": {Raw: []byte("hash")}},
	}, nil)

	events := make(chan queue.Event, 4)
	mockQueue.EnqueueCalls(func(event queue.Event) error {
		events <- event

		return nil
	})

	manager := finality.NewNSListenerManager(mockLM, mockQueue, mockQS, mockKT, testPollerConfig{})

	require.NoError(t, manager.AddFinalityListener(namespace, "txA", listenerA))
	require.NoError(t, manager.AddFinalityListener(namespace, "txB", listenerB))

	var event queue.Event
	select {
	case event = <-events:
	case <-time.After(5 * time.Second):
		t.Fatal("poller did not resolve txB")
	}

	require.NoError(t, event.Process(t.Context()))

	assert.Equal(t, 1, listenerB.OnStatusCallCount())
	assert.Equal(t, 0, listenerA.OnStatusCallCount())
}

func TestNSListenerManagerProvider_NewManager(t *testing.T) {
	network := "test-network"
	channel := "test-channel"

	mockQSP := &mock.QueryServiceProvider{}
	mockLMP := &mock.ListenerManagerProvider{}
	mockLM := &mock.ListenerManager{}
	mockQS := &mock.QueryService{}

	mockLMP.NewManagerReturns(mockLM, nil)
	mockQSP.GetReturns(mockQS, nil)

	mockQueue := &mock.Queue{}
	provider := finality.NewNotificationServiceBased(mockQSP, mockLMP, mockQueue, testPollerConfig{})
	require.NotNil(t, provider)

	manager, err := provider.NewManager(network, channel)
	require.NoError(t, err)
	require.NotNil(t, manager)

	assert.Equal(t, 1, mockLMP.NewManagerCallCount())
	callNetwork, callChannel := mockLMP.NewManagerArgsForCall(0)
	assert.Equal(t, network, callNetwork)
	assert.Equal(t, channel, callChannel)

	assert.Equal(t, 1, mockQSP.GetCallCount())
	callNetwork, callChannel = mockQSP.GetArgsForCall(0)
	assert.Equal(t, network, callNetwork)
	assert.Equal(t, channel, callChannel)
}

func TestNSListenerManagerProvider_NewManager_ListenerManagerError(t *testing.T) {
	network := "test-network"
	channel := "test-channel"

	mockQSP := &mock.QueryServiceProvider{}
	mockLMP := &mock.ListenerManagerProvider{}

	mockLMP.NewManagerReturns(nil, errors.New("listener manager creation failed"))

	mockQueue := &mock.Queue{}
	provider := finality.NewNotificationServiceBased(mockQSP, mockLMP, mockQueue, testPollerConfig{})

	manager, err := provider.NewManager(network, channel)
	require.Error(t, err)
	assert.Nil(t, manager)
	assert.Contains(t, err.Error(), "failed creating finality manager")
	assert.Contains(t, err.Error(), "listener manager creation failed")
}

func TestNSListenerManagerProvider_NewManager_QueryServiceError(t *testing.T) {
	network := "test-network"
	channel := "test-channel"

	mockQSP := &mock.QueryServiceProvider{}
	mockLMP := &mock.ListenerManagerProvider{}
	mockLM := &mock.ListenerManager{}

	mockLMP.NewManagerReturns(mockLM, nil)
	mockQSP.GetReturns(nil, errors.New("query service retrieval failed"))

	mockQueue := &mock.Queue{}
	provider := finality.NewNotificationServiceBased(mockQSP, mockLMP, mockQueue, testPollerConfig{})

	manager, err := provider.NewManager(network, channel)
	require.Error(t, err)
	assert.Nil(t, manager)
	assert.Contains(t, err.Error(), "failed getting query service")
	assert.Contains(t, err.Error(), "query service retrieval failed")
}

// resolveThroughPoller registers a listener and returns the notification event
// the shared poller enqueues once the tx resolves.
func resolveThroughPoller(t *testing.T, mockListener *mock.Listener) queue.Event {
	t.Helper()
	txID := "tx123"
	namespace := "token-namespace"

	mockLM := &mock.ListenerManager{}
	mockQueue := &mock.Queue{}
	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}

	mockQS.GetTransactionStatusReturns(int32(committerpb.Status_COMMITTED), nil)
	mockQS.GetTransactionStatusesReturns(map[string]int32{
		txID: int32(committerpb.Status_COMMITTED),
	}, nil)
	mockKT.CreateTokenRequestKeyReturns("key", nil)
	mockQS.GetStatesReturns(map[cdriver.Namespace]map[cdriver.PKey]cdriver.VaultValue{
		namespace: {"key": {Raw: []byte("hash")}},
	}, nil)

	events := make(chan queue.Event, 1)
	mockQueue.EnqueueCalls(func(event queue.Event) error {
		events <- event

		return nil
	})

	manager := finality.NewNSListenerManager(mockLM, mockQueue, mockQS, mockKT, testPollerConfig{})

	require.NoError(t, manager.AddFinalityListener(namespace, txID, mockListener))

	select {
	case event := <-events:
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("poller did not resolve the pending tx")

		return nil
	}
}

func TestOnlyOnceListener_SingleCall(t *testing.T) {
	ctx := t.Context()
	mockListener := &mock.Listener{}

	event := resolveThroughPoller(t, mockListener)

	// Process the same notification event multiple times: the OnlyOnceListener
	// wrapper must notify the underlying listener exactly once.
	require.NoError(t, event.Process(ctx))
	require.NoError(t, event.Process(ctx))
	require.NoError(t, event.Process(ctx))

	assert.Equal(t, 1, mockListener.OnStatusCallCount())
}

func TestOnlyOnceListener_Concurrent(t *testing.T) {
	ctx := t.Context()

	mockListener := &mock.Listener{}
	callCount := 0
	var mu sync.Mutex

	mockListener.OnStatusCalls(func(ctx context.Context, txID string, status int, message string, hash []byte) {
		mu.Lock()
		callCount++
		mu.Unlock()
	})

	event := resolveThroughPoller(t, mockListener)

	// Process the notification event concurrently
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			_ = event.Process(ctx)
		})
	}
	wg.Wait()

	// The underlying listener should only be called once despite concurrent calls
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, callCount, "OnStatus should only be called once despite concurrent calls")
}

func TestFabricXFSCStatus_Committed(t *testing.T) {
	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	mockQS.GetTransactionStatusReturns(int32(committerpb.Status_COMMITTED), nil)
	mockKT.CreateTokenRequestKeyReturns("key", nil)
	mockQS.GetStateReturns(&cdriver.VaultValue{Raw: []byte("hash")}, nil)

	txCheck := &finality.TxCheck{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          "tx123",
		Namespace:     "namespace",
	}

	err := txCheck.Process(t.Context())
	require.NoError(t, err)

	assert.Equal(t, 1, mockListener.OnStatusCallCount())
	_, _, status, _, _ := mockListener.OnStatusArgsForCall(0)
	assert.Equal(t, fdriver.Valid, status)
}

func TestFabricXFSCStatus_NotValidated(t *testing.T) {
	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	mockQS.GetTransactionStatusReturns(int32(committerpb.Status_STATUS_UNSPECIFIED), nil)

	txCheck := &finality.TxCheck{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          "tx123",
		Namespace:     "namespace",
	}

	err := txCheck.Process(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in a valid state")
}

func TestFabricXFSCStatus_Invalid(t *testing.T) {
	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	mockQS.GetTransactionStatusReturns(int32(committerpb.Status_ABORTED_SIGNATURE_INVALID), nil)

	txCheck := &finality.TxCheck{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          "tx123",
		Namespace:     "namespace",
	}

	err := txCheck.Process(t.Context())
	require.NoError(t, err)

	assert.Equal(t, 1, mockListener.OnStatusCallCount())
	_, _, status, _, _ := mockListener.OnStatusArgsForCall(0)
	assert.Equal(t, fdriver.Invalid, status)
}

func TestFabricXFSCStatus_UnknownCode(t *testing.T) {
	mockQS := &mock.QueryService{}
	mockKT := &mock.KeyTranslator{}
	mockListener := &mock.Listener{}

	mockQS.GetTransactionStatusReturns(999, nil)

	txCheck := &finality.TxCheck{
		QueryService:  mockQS,
		KeyTranslator: mockKT,
		Listener:      mockListener,
		TxID:          "tx123",
		Namespace:     "namespace",
	}

	err := txCheck.Process(t.Context())
	require.NoError(t, err)

	assert.Equal(t, 1, mockListener.OnStatusCallCount())
	_, _, status, _, _ := mockListener.OnStatusArgsForCall(0)
	assert.Equal(t, fdriver.Invalid, status)
}
