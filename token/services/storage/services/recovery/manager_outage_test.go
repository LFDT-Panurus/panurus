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

// fsxUnavailableErr builds the error exactly as FSC v0.29.1
// platform/fabricx/core/ledger/ledger.go GetTransactionByID does when the
// committer answers codes.Unavailable.
func fsxUnavailableErr(txID string) error {
	return errors.Wrapf(fsc_finality.TxNotFound, "failed to get tx for txID [%s]: %s", txID, status.Error(codes.Unavailable, "connection refused"))
}

// TestManager_CommitterUnavailableDoesNotPromoteOrphan asserts that an
// unanswered ledger query (committer unavailable) does not mark an old
// Pending tx Orphan, while a genuine NotFound still does.
func TestManager_CommitterUnavailableDoesNotPromoteOrphan(t *testing.T) {
	t.Run("unavailable must not promote", func(t *testing.T) {
		h := &mock2.Handler{}
		h.RecoverReturns(fsxUnavailableErr("txUnavail"))
		db := runOneSweep(t, "txUnavail", h)
		require.GreaterOrEqual(t, h.RecoverCallCount(), 1)
		for i := range db.SetStatusCallCount() {
			_, id, st, msg := db.SetStatusArgsForCall(i)
			assert.NotEqual(t, storage.Orphan, st, "tx [%s] marked Orphan on an Unavailable error: %s", id, msg)
		}
	})

	t.Run("genuine not found promotes", func(t *testing.T) {
		h := &mock2.Handler{}
		h.RecoverReturns(errors.Wrapf(fsc_finality.TxNotFound, "failed to get tx for txID [%s]: %s", "txGone", status.Error(codes.NotFound, "not found in index")))
		db := runOneSweep(t, "txGone", h)
		require.Equal(t, 1, db.SetStatusCallCount())
		_, id, st, _ := db.SetStatusArgsForCall(0)
		assert.Equal(t, "txGone", id)
		assert.Equal(t, storage.Orphan, st)
	})
}

// unavailableSidecar is a SidecarServiceClient whose GetTxByID always fails
// with codes.Unavailable. Other methods are not called.
type unavailableSidecar struct {
	committerpb.SidecarServiceClient
	calls int
}

func (s *unavailableSidecar) GetTxByID(context.Context, *committerpb.TxID, ...grpc.CallOption) (*cb.Envelope, error) {
	s.calls++

	return nil, status.Error(codes.Unavailable, "connection refused")
}

// fakeChannel exposes only the ledger FSC's fabric.Channel needs.
type fakeChannel struct {
	fdriver.Channel
	l fdriver.Ledger
}

func (c *fakeChannel) Ledger() fdriver.Ledger       { return c.l }
func (c *fakeChannel) Committer() fdriver.Committer { return nil }

// TestManager_ChaguarCommitterUnavailableEndToEnd drives the real stack:
// Manager -> finality.TTXRecoveryHandler -> chaguar ledger -> FSC fabric.Ledger
// -> FSC fabricx ledger -> a sidecar that answers codes.Unavailable.
func TestManager_ChaguarCommitterUnavailableEndToEnd(t *testing.T) {
	sidecar := &unavailableSidecar{}
	fsxLedger := fscx_ledger.New(sidecar, nil, context.Background())
	ch := fabric.NewChannel(nil, nil, &fakeChannel{l: fsxLedger})
	chLedger := chaguar.NewLedger(ch, "net", nil, nil)

	handler := finality.NewTTXRecoveryHandler(
		logging.MustGetLogger(), chLedger, "ns", nil, token.TMSID{}, nil, nil, nil,
		noop.NewTracerProvider().Tracer("test"), nil,
	)

	// The error seen by recovery reads as "not found" although the ledger never answered.
	err := handler.Recover(context.Background(), "txE2E")
	require.Error(t, err)
	t.Logf("handler error: %v", err)
	assert.Equal(t, 2, sidecar.calls, "FSC retries Unavailable once")

	db := runOneSweep(t, "txE2E", handler)
	for i := range db.SetStatusCallCount() {
		_, id, st, msg := db.SetStatusArgsForCall(i)
		assert.NotEqual(t, storage.Orphan, st, "tx [%s] marked Orphan while the committer was unavailable: %s", id, msg)
	}
}
