/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/finality"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	events2 "github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/events"
	"github.com/hyperledger/fabric-protos-go-apiv2/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- minimal fakes ---

type fakeLedger struct {
	results map[string]fakeLedgerResult
}

type fakeLedgerResult struct {
	pt  *fabric.ProcessedTransaction
	err error
}

func (f *fakeLedger) GetTransactionByID(txID string) (*fabric.ProcessedTransaction, error) {
	r, ok := f.results[txID]
	if !ok {
		// The real ledger's raw string error is normalized by normalizedLedger (deliveryflm.go)
		// before reaching DeliveryScanQueryByID. Fakes must return the typed sentinel directly.
		return nil, fmt.Errorf("%w: TXID [%s] not available", finality.ErrTxNotFound, txID)
	}

	return r.pt, r.err
}

type fakeScanner struct {
	called     bool
	startBlock uint64
}

func (f *fakeScanner) ScanFromBlock(_ context.Context, block uint64, _ fabric.DeliveryCallback) error {
	f.called = true
	f.startBlock = block

	return nil
}

type fakeMapper struct {
	results map[*fabric.ProcessedTransaction]fakeMapperResult
}

type fakeMapperResult struct {
	infos []finality.TxInfo
	err   error
}

func (f *fakeMapper) MapProcessedTx(tx *fabric.ProcessedTransaction) ([]finality.TxInfo, error) {
	r, ok := f.results[tx]
	if !ok {
		return nil, errors.New("unexpected tx in mapper")
	}

	return r.infos, r.err
}

func (f *fakeMapper) MapTxData(_ context.Context, _ []byte, _ *common.BlockMetadata, _ cdriver.BlockNum, _ cdriver.TxNum) (map[cdriver.Namespace]finality.TxInfo, error) {
	return nil, nil
}

// evicted builds a minimal evicted map for the given txIDs with nil listener slices.
func evicted(txIDs ...string) map[cdriver.TxID][]events2.ListenerEntry[finality.TxInfo] {
	m := make(map[cdriver.TxID][]events2.ListenerEntry[finality.TxInfo], len(txIDs))
	for _, id := range txIDs {
		m[id] = nil
	}

	return m
}

func drain(ch <-chan []finality.TxInfo) []finality.TxInfo {
	var all []finality.TxInfo
	for batch := range ch {
		all = append(all, batch...)
	}

	return all
}

// --- tests ---

// TestQueryByID_MappingFailure_ContinuesToNextTx verifies that when MapProcessedTx
// fails for one txID, the goroutine continues processing the remaining txIDs instead
// of returning early (the bug this PR fixed).
func TestQueryByID_MappingFailure_ContinuesToNextTx(t *testing.T) {
	ctx := context.Background()
	// Use zero-value ProcessedTransactions as stand-ins; the mapper is also mocked.
	pt1 := new(fabric.ProcessedTransaction)
	pt2 := new(fabric.ProcessedTransaction)

	wantInfo := finality.TxInfo{TxId: "tx2"}

	scanner := &fakeScanner{}
	q := &finality.DeliveryScanQueryByID{
		Delivery: scanner,
		Ledger: &fakeLedger{results: map[string]fakeLedgerResult{
			"tx1": {pt: pt1, err: nil},
			"tx2": {pt: pt2, err: nil},
		}},
		Mapper: &fakeMapper{results: map[*fabric.ProcessedTransaction]fakeMapperResult{
			pt1: {err: errors.New("mapping failed")},
			pt2: {infos: []finality.TxInfo{wantInfo}},
		}},
	}

	ch, err := q.QueryByID(ctx, 20, evicted("tx1", "tx2"))
	require.NoError(t, err)

	received := drain(ch)
	assert.Contains(t, received, wantInfo, "tx2 info must be delivered even though tx1 mapping failed")
	assert.False(t, scanner.called, "no delivery scan should be triggered when all txs were found on ledger")
}

// TestQueryByID_MappingFailureOnly_NoDelivery verifies that a mapping failure alone
// (with no TxNotFound / transient errors) does NOT trigger a block delivery scan.
func TestQueryByID_MappingFailureOnly_NoDelivery(t *testing.T) {
	ctx := context.Background()
	pt1 := new(fabric.ProcessedTransaction)

	scanner := &fakeScanner{}
	q := &finality.DeliveryScanQueryByID{
		Delivery: scanner,
		Ledger: &fakeLedger{results: map[string]fakeLedgerResult{
			"tx1": {pt: pt1, err: nil},
		}},
		Mapper: &fakeMapper{results: map[*fabric.ProcessedTransaction]fakeMapperResult{
			pt1: {err: errors.New("mapping failed")},
		}},
	}

	ch, err := q.QueryByID(ctx, 20, evicted("tx1"))
	require.NoError(t, err)

	received := drain(ch)
	assert.Empty(t, received)
	assert.False(t, scanner.called, "mapping failure must not trigger delivery scan")
}

// TestQueryByID_TxNotFound_TriggersDelivery verifies that a TxNotFound ledger error
// causes the goroutine to fall back to a block scan (startDelivery = true).
func TestQueryByID_TxNotFound_TriggersDelivery(t *testing.T) {
	ctx := context.Background()

	scanner := &fakeScanner{}
	// fakeLedger returns "TXID [tx1] not available" for unknown keys by default.
	q := &finality.DeliveryScanQueryByID{
		Delivery: scanner,
		Ledger:   &fakeLedger{results: map[string]fakeLedgerResult{}},
		Mapper:   &fakeMapper{results: map[*fabric.ProcessedTransaction]fakeMapperResult{}},
	}

	ch, err := q.QueryByID(ctx, 20, evicted("tx1"))
	require.NoError(t, err)
	drain(ch)

	assert.True(t, scanner.called, "TxNotFound must trigger delivery scan")
	// startingBlock = max(1, 20-10) = 10
	assert.Equal(t, uint64(10), scanner.startBlock)
}

// TestQueryByID_TransientError_ContinuesToNextTx verifies that a transient ledger
// error for one txID triggers delivery and does NOT prevent other txIDs in the same
// batch from being resolved via the ledger (the second fix in this PR).
func TestQueryByID_TransientError_ContinuesToNextTx(t *testing.T) {
	ctx := context.Background()
	pt2 := new(fabric.ProcessedTransaction)
	wantInfo := finality.TxInfo{TxId: "tx2"}

	scanner := &fakeScanner{}
	q := &finality.DeliveryScanQueryByID{
		Delivery: scanner,
		Ledger: &fakeLedger{results: map[string]fakeLedgerResult{
			// tx1 returns a transient (non-TxNotFound) error
			"tx1": {err: errors.New("peer connection reset")},
			// tx2 is found successfully
			"tx2": {pt: pt2},
		}},
		Mapper: &fakeMapper{results: map[*fabric.ProcessedTransaction]fakeMapperResult{
			pt2: {infos: []finality.TxInfo{wantInfo}},
		}},
	}

	ch, err := q.QueryByID(ctx, 20, evicted("tx1", "tx2"))
	require.NoError(t, err)

	received := drain(ch)
	assert.Contains(t, received, wantInfo, "tx2 info must be delivered despite tx1 transient error")
	assert.True(t, scanner.called, "transient error must trigger delivery scan for tx1")
}
