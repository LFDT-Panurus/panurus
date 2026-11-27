/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package recovery_test

import (
	"context"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/chaguar"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	recovery2 "github.com/LFDT-Panurus/panurus/token/services/storage/services/recovery"
	mock2 "github.com/LFDT-Panurus/panurus/token/services/storage/services/recovery/mock"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/finality"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	fsc_finality "github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/finality"
	fdriver "github.com/hyperledger-labs/fabric-smart-client/platform/fabric/driver"
	fscx_ledger "github.com/hyperledger-labs/fabric-smart-client/platform/fabricx/core/ledger"
	cb "github.com/hyperledger/fabric-protos-go-apiv2/common"
	"github.com/hyperledger/fabric-x-common/api/committerpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// runOneSweep runs one recovery sweep over a single Pending row stored an
// hour ago, with a grace period far below that age, and returns the storage
// mock so the caller can inspect SetStatus calls.
func runOneSweep(t *testing.T, txID string, handler recovery2.Handler) *mock2.Storage {
	t.Helper()
	mockDB := &mock2.Storage{}
	config := recovery2.Config{
		Enabled:             true,
		TTL:                 100 * time.Millisecond,
		ScanInterval:        100 * time.Millisecond,
		BatchSize:           100,
		WorkerCount:         1,
		LeaseDuration:       time.Second,
		TransactionTimeout:  500 * time.Millisecond,
		InstanceID:          "test-instance",
		NotFoundGracePeriod: 10 * time.Millisecond,
	}
	leadership1 := &mock2.Leadership{}
	leadership2 := &mock2.Leadership{}
	mockDB.AcquireRecoveryLeadershipReturnsOnCall(0, leadership1, true, nil)
	mockDB.AcquireRecoveryLeadershipReturns(leadership2, true, nil)
	mockDB.ClaimPendingTransactionsReturnsOnCall(0, []*ttxdb.RecoveryClaim{{TxID: txID, StoredAt: time.Now().Add(-time.Hour)}}, nil)
	mockDB.ClaimPendingTransactionsReturns([]*ttxdb.RecoveryClaim{}, nil)

	manager := recovery2.NewManager(logging.MustGetLogger(), mockDB, handler, config)
	require.NoError(t, manager.Start())
	// Wait for the initial sweep (jitter up to 1s + handler invocation).
	time.Sleep(1300 * time.Millisecond)
	require.NoError(t, manager.Stop())

	return mockDB
}

// fakeSidecar is a SidecarServiceClient whose GetTxByID always fails with err.
// Other methods are not called.
type fakeSidecar struct {
	committerpb.SidecarServiceClient
	err   error
	calls int
}

func (s *fakeSidecar) GetTxByID(context.Context, *committerpb.TxID, ...grpc.CallOption) (*cb.Envelope, error) {
	s.calls++

	return nil, s.err
}

// fakeChannel exposes only the ledger FSC's fabric.Channel needs.
type fakeChannel struct {
	fdriver.Channel
	l fdriver.Ledger
}

func (c *fakeChannel) Ledger() fdriver.Ledger       { return c.l }
func (c *fakeChannel) Committer() fdriver.Committer { return nil }

// newTTXHandler builds the real stack below the Manager:
// finality.TTXRecoveryHandler -> chaguar ledger -> FSC fabric.Ledger -> FSC
// fabricx ledger, which takes its committer client from newClient.
func newTTXHandler(newClient func(skip string) (committerpb.SidecarServiceClient, string, error)) recovery2.Handler {
	fsxLedger := fscx_ledger.NewWithClientFactory(newClient, nil, context.Background())
	ch := fabric.NewChannel(nil, nil, &fakeChannel{l: fsxLedger})
	chLedger := chaguar.NewLedger(ch, "net", nil, nil)

	return finality.NewTTXRecoveryHandler(
		logging.MustGetLogger(), chLedger, "ns", nil, token.TMSID{}, nil, nil, nil,
		noop.NewTracerProvider().Tracer("test"), nil,
	)
}

// sidecarFactory returns a client factory that always hands out s.
func sidecarFactory(s *fakeSidecar) func(string) (committerpb.SidecarServiceClient, string, error) {
	return func(string) (committerpb.SidecarServiceClient, string, error) { return s, "committer:7001", nil }
}

// fsxLedgerErr returns the error the real FSC fabricx ledger returns when the
// committer answers GetTxByID with rpcErr.
func fsxLedgerErr(t *testing.T, txID string, rpcErr error) error {
	t.Helper()
	_, err := fscx_ledger.New(&fakeSidecar{err: rpcErr}, nil, context.Background()).GetTransactionByID(txID)
	require.Error(t, err)

	return err
}

// assertNotOrphan fails if any SetStatus call on db wrote Orphan.
func assertNotOrphan(t *testing.T, db *mock2.Storage) {
	t.Helper()
	for i := range db.SetStatusCallCount() {
		_, id, st, msg := db.SetStatusArgsForCall(i)
		assert.NotEqual(t, storage.Orphan, st, "tx [%s] marked Orphan while the committer did not answer: %s", id, msg)
	}
}

// TestManager_CommitterUnavailableDoesNotPromoteOrphan asserts that an
// unanswered ledger query (committer unavailable) does not mark an old
// Pending tx Orphan, while a genuine NotFound still does. The handler returns
// the errors the FSC fabricx ledger produces for these committer answers.
func TestManager_CommitterUnavailableDoesNotPromoteOrphan(t *testing.T) {
	t.Run("unavailable must not promote", func(t *testing.T) {
		h := &mock2.Handler{}
		ledgerErr := fsxLedgerErr(t, "txUnavail", status.Error(codes.Unavailable, "connection refused"))
		require.False(t, errors.HasCause(ledgerErr, fsc_finality.TxNotFound), "Unavailable reported as TxNotFound: %v", ledgerErr)
		h.RecoverReturns(ledgerErr)
		db := runOneSweep(t, "txUnavail", h)
		require.GreaterOrEqual(t, h.RecoverCallCount(), 1)
		assertNotOrphan(t, db)
	})

	t.Run("genuine not found promotes", func(t *testing.T) {
		h := &mock2.Handler{}
		ledgerErr := fsxLedgerErr(t, "txGone", status.Error(codes.NotFound, "not found in index"))
		require.True(t, errors.HasCause(ledgerErr, fsc_finality.TxNotFound), "NotFound not reported as TxNotFound: %v", ledgerErr)
		h.RecoverReturns(ledgerErr)
		db := runOneSweep(t, "txGone", h)
		require.Equal(t, 1, db.SetStatusCallCount())
		_, id, st, _ := db.SetStatusArgsForCall(0)
		assert.Equal(t, "txGone", id)
		assert.Equal(t, storage.Orphan, st)
	})
}

// TestManager_ChaguarCommitterUnavailableEndToEnd drives the real stack:
// Manager -> finality.TTXRecoveryHandler -> chaguar ledger -> FSC fabric.Ledger
// -> FSC fabricx ledger -> a sidecar. A committer that does not answer must
// not mark the tx Orphan; a committer that answers NotFound must.
func TestManager_ChaguarCommitterUnavailableEndToEnd(t *testing.T) {
	t.Run("unavailable", func(t *testing.T) {
		sidecar := &fakeSidecar{err: status.Error(codes.Unavailable, "connection refused")}
		handler := newTTXHandler(sidecarFactory(sidecar))

		err := handler.Recover(context.Background(), "txE2E")
		require.Error(t, err)
		t.Logf("handler error: %v", err)
		assert.Equal(t, 2, sidecar.calls, "FSC retries Unavailable once")
		assert.Equal(t, codes.Unavailable, status.Code(err))

		before := sidecar.calls
		assertNotOrphan(t, runOneSweep(t, "txE2E", handler))
		assert.Greater(t, sidecar.calls, before, "the sweep did not query the committer")
	})

	t.Run("deadline exceeded", func(t *testing.T) {
		sidecar := &fakeSidecar{err: status.Error(codes.DeadlineExceeded, "context deadline exceeded")}
		handler := newTTXHandler(sidecarFactory(sidecar))

		assertNotOrphan(t, runOneSweep(t, "txDeadline", handler))
		assert.GreaterOrEqual(t, sidecar.calls, 1)
	})

	t.Run("no committer reachable", func(t *testing.T) {
		picks := 0
		handler := newTTXHandler(func(string) (committerpb.SidecarServiceClient, string, error) {
			picks++

			return nil, "", errors.New("expected at least one endpoint")
		})

		assertNotOrphan(t, runOneSweep(t, "txNoCommitter", handler))
		assert.GreaterOrEqual(t, picks, 1, "the sweep did not ask for a committer")
	})

	t.Run("genuine not found promotes", func(t *testing.T) {
		sidecar := &fakeSidecar{err: status.Error(codes.NotFound, "not found in index")}
		handler := newTTXHandler(sidecarFactory(sidecar))

		db := runOneSweep(t, "txGone", handler)
		require.Equal(t, 1, db.SetStatusCallCount())
		_, id, st, _ := db.SetStatusArgsForCall(0)
		assert.Equal(t, "txGone", id)
		assert.Equal(t, storage.Orphan, st)
	})
}
