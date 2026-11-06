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
	"github.com/hyperledger/fabric-protos-go-apiv2/common"

	ndriver "github.com/LFDT-Panurus/panurus/token/services/network/driver"
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

// txMapper is the subset of events.EventInfoMapper used by DeliveryScanQueryByID:
// MapProcessedTx for a transaction read back from the ledger, and MapTxData for the
// honest envelope resolved out of a block when a point lookup returned a forgery.
type txMapper interface {
	MapProcessedTx(tx *fabric.ProcessedTransaction) ([]TxInfo, error)
	MapTxData(ctx context.Context, tx []byte, block *common.BlockMetadata, blockNum driver.BlockNum, txNum driver.TxNum) (map[driver.Namespace]TxInfo, error)
}

type DeliveryScanQueryByID struct {
	Delivery blockScanner
	Ledger   txLedger
	Mapper   txMapper

	// BlockNumberByTxID and BlockDelivery resolve the honest envelope of a txID
	// whose point lookup returned a non-VALID (possibly forged) envelope that a
	// VALID envelope in the same block shadows. When either is nil, the point-lookup
	// verdict is used unchanged. See finality.HonestEnvelope.
	BlockNumberByTxID BlockNumberByTxID
	BlockDelivery     BlockScanner
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
			infos, ok := q.mapFound(ctx, txID, pt)
			keySet.Remove(txID)
			if ok {
				ch <- infos
			}

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

// mapFound maps a transaction read back from the ledger for an evicted listener. A
// point lookup returns the lowest (block, txNum) envelope for a txID, which a forged
// envelope ordered ahead of the honest one would win; so when the looked-up envelope
// maps to an Invalid verdict and the shadow-resolution dependencies are wired, its
// block is scanned for a VALID envelope with the same txID, whose verdict is mapped
// instead.
//
// ok is false when the transaction could not be mapped at all (a mapping error on the
// looked-up envelope, which is logged); the caller then drops the key without
// emitting an event, as before. A failure to resolve or map the honest envelope falls
// back to the point-lookup verdict rather than dropping the key.
func (q *DeliveryScanQueryByID) mapFound(ctx context.Context, txID driver.TxID, pt *fabric.ProcessedTransaction) ([]TxInfo, bool) {
	infos, err := q.Mapper.MapProcessedTx(pt)
	if err != nil {
		logger.Errorf("failed to map tx [%s]: [%s], skipping", txID, err)

		return nil, false
	}

	// Only a non-VALID point-lookup verdict can be a forgery shadowing a committed
	// transaction; a VALID one is already the honest envelope. Resolving the honest
	// envelope costs a block scan, so it is attempted only when the lookup mapped to
	// Invalid and the shadow-resolution dependencies are wired.
	if q.BlockNumberByTxID != nil && q.BlockDelivery != nil && isInvalid(infos) {
		if honest, ok := q.mapHonestEnvelope(ctx, txID); ok {
			return honest, true
		}
	}

	return infos, true
}

// isInvalid reports whether a mapped transaction's verdict is Invalid. A mapper emits
// a single Invalid TxInfo for a transaction Fabric rejected (see
// EndorserTxInfoMapper.MapProcessedTx); a VALID transaction yields one info per
// namespace carrying a token request, none of them Invalid.
func isInvalid(infos []TxInfo) bool {
	for _, info := range infos {
		if info.Status == ndriver.Invalid {
			return true
		}
	}

	return false
}

// mapHonestEnvelope resolves and maps the VALID envelope that carries txID in its
// block, overriding a forged point-lookup verdict. It returns false (so the caller
// uses the point-lookup verdict) when no VALID envelope shadows the lookup, or when
// the block could not be read or mapped.
func (q *DeliveryScanQueryByID) mapHonestEnvelope(ctx context.Context, txID driver.TxID) ([]TxInfo, bool) {
	block, txNum, found, err := HonestEnvelope(ctx, q.BlockNumberByTxID, q.BlockDelivery, txID)
	if err != nil {
		logger.DebugfContext(ctx, "failed resolving honest envelope for [%s], using point-lookup verdict: %s", txID, err)

		return nil, false
	}
	if !found {
		return nil, false
	}

	txInfos, err := q.Mapper.MapTxData(ctx, block.Data.Data[txNum], block.Metadata, block.Header.GetNumber(), txNum)
	if err != nil {
		logger.DebugfContext(ctx, "failed mapping honest envelope for [%s], using point-lookup verdict: %s", txID, err)

		return nil, false
	}

	return collections.Values(txInfos), true
}
