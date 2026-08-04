/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"context"
	"errors"

	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	events2 "github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/events"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/blockscan"
)

const (
	// NumberPastBlocks is how far back the fallback block scan rewinds from the last known block.
	NumberPastBlocks = blockscan.NumberPastBlocks
	// FirstBlock is the earliest block the fallback scan may start from.
	FirstBlock = blockscan.FirstBlock
)

// ErrTxNotFound is the sentinel returned by txLedger when a transaction does not
// exist on the ledger. The wiring layer (deliveryflm.go) translates FSC's
// string-based "not found" errors into this typed error so that callers can use
// errors.Is instead of fragile substring matching.
var ErrTxNotFound = errors.New("transaction not found")

type txLedger interface {
	GetTransactionByID(txID string) (*fabric.ProcessedTransaction, error)
}

type blockScanner interface {
	ScanFromBlock(ctx context.Context, block uint64, callback fabric.DeliveryCallback) error
}

// txMapper is the subset of events.EventInfoMapper used by DeliveryScanQueryByID.
// It only needs MapProcessedTx; MapTxData (the block-path method) is not called here.
type txMapper interface {
	MapProcessedTx(tx *fabric.ProcessedTransaction) ([]TxInfo, error)
}

type DeliveryScanQueryByID struct {
	Delivery blockScanner
	Ledger   txLedger
	Mapper   txMapper
}

func (q *DeliveryScanQueryByID) QueryByID(ctx context.Context, lastBlock driver.BlockNum, evicted map[driver.TxID][]events2.ListenerEntry[TxInfo]) (<-chan []TxInfo, error) {
	keys := collections.Keys(evicted)
	ch := make(chan []TxInfo, len(keys))
	go q.queryByID(ctx, keys, ch, lastBlock)

	return ch, nil
}

func (q *DeliveryScanQueryByID) queryByID(ctx context.Context, keys []driver.TxID, ch chan []TxInfo, lastBlock uint64) {
	defer close(ch)

	keySet := collections.NewSet(keys...)

	// for each txID, fetch the corresponding transaction.
	// if the transaction is not found, start a delivery for it
	startDelivery := false
	for _, txID := range keySet.ToSlice() {
		logger.DebugfContext(ctx, "loading transaction [%s] from ledger...", txID)
		pt, err := q.Ledger.GetTransactionByID(txID)
		if err == nil {
			logger.DebugfContext(ctx, "transaction [%s] found on ledger", txID)
			infos, err := q.Mapper.MapProcessedTx(pt)
			if err != nil {
				logger.Errorf("failed to map tx [%s]: [%s], skipping", txID, err)
				keySet.Remove(txID)

				continue
			}
			keySet.Remove(txID)
			ch <- infos

			continue
		}

		if errors.Is(err, ErrTxNotFound) {
			// transaction was not found on the ledger; fall back to block scan
			logger.Errorf("tx [%s] not found on the ledger [%s]", txID, err)
			startDelivery = true

			continue
		}

		// transient ledger error; fall back to block scan for this txID
		logger.Errorf("scan for tx [%s] failed with err [%s], falling back to block scan", txID, err)
		startDelivery = true
	}

	if !startDelivery {
		return
	}

	startingBlock := blockscan.StartingBlock(lastBlock)
	logger.DebugfContext(ctx, "start scanning blocks starting from [%d], looking for remaining keys [%s]", startingBlock, keySet)

	// start delivery for the future
	err := q.Delivery.ScanFromBlock(
		ctx,
		startingBlock,
		func(tx *fabric.ProcessedTransaction) (bool, error) {
			if !keySet.Contains(tx.TxID()) {
				return false, nil
			}
			logger.DebugfContext(ctx, "received result for tx [%s, %v, %d]...", tx.TxID(), tx.ValidationCode(), len(tx.Results()))
			infos, err := q.Mapper.MapProcessedTx(tx)
			if err != nil {
				logger.Errorf("failed mapping tx [%s]: %v", tx.TxID(), err)

				return true, err
			}
			ch <- infos
			keySet.Remove(tx.TxID())
			logger.DebugfContext(ctx, "removing [%s] from searching list, remaining keys [%d]", tx.TxID(), keySet.Length())

			return keySet.Length() == 0, nil
		},
	)
	if err != nil {
		logger.Errorf("failed scanning blocks [%s], started from [%d]", err, startingBlock)

		return
	}
	logger.DebugfContext(ctx, "finished scanning blocks starting from [%d]", startingBlock)
}
