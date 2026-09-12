/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lookup

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/blockscan"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/tcc"
	slices2 "github.com/LFDT-Panurus/panurus/token/services/utils/slices"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/events"
)

const (
	QueryStates = tcc.QueryStates
	// NumberPastBlocks is how far back the fallback block scan rewinds from the last known block.
	NumberPastBlocks = blockscan.NumberPastBlocks
	// FirstBlock is the earliest block the fallback scan may start from.
	FirstBlock = blockscan.FirstBlock
)

// stateQuerier is the subset of the Fabric channel used to ask the token chaincode for the
// current value of a set of state keys in a single namespace.
type stateQuerier interface {
	QueryStates(ns driver.Namespace, arg []byte) ([]byte, error)
}

// rwsetInspector is the subset of the Fabric vault used to read the writes of a scanned
// transaction.
type rwsetInspector interface {
	InspectRWSet(ctx context.Context, rwset []byte, namespaces ...driver.Namespace) (*fabric.RWSet, error)
}

// blockScanner is the subset of the Fabric delivery service used to scan blocks.
type blockScanner interface {
	ScanFromBlock(ctx context.Context, block uint64, callback fabric.DeliveryCallback) error
}

// ChannelStateQuerier queries the token chaincode of a namespace over a Fabric channel.
type ChannelStateQuerier struct {
	Channel *fabric.Channel
}

// QueryStates invokes QueryStates on the token chaincode deployed under ns.
func (c *ChannelStateQuerier) QueryStates(ns driver.Namespace, arg []byte) ([]byte, error) {
	return c.Channel.Chaincode(ns).Query(QueryStates, arg).Query()
}

type DeliveryScanQueryByID struct {
	Delivery blockScanner
	Querier  stateQuerier
	Vault    rwsetInspector
}

func (q *DeliveryScanQueryByID) QueryByID(ctx context.Context, startingBlock driver.BlockNum, evicted map[driver.PKey][]events.ListenerEntry[KeyInfo]) (<-chan []KeyInfo, error) {
	// we are abusing TxID to carry the name of the keys we are looking for.
	// Keys are supposed to be unique
	keys := collections.Keys(evicted) // These are the state keys we are looking for
	ch := make(chan []KeyInfo, len(keys))

	go q.queryByID(ctx, keys, ch, startingBlock, evicted)

	return ch, nil
}

// groupKeysByNamespace groups evicted's keys by the namespace of their (arbitrary) listener entry.
func groupKeysByNamespace(evicted map[driver.PKey][]events.ListenerEntry[KeyInfo]) map[driver.Namespace][]driver.PKey {
	keysByNS := map[driver.Namespace][]driver.PKey{}
	for k, v := range evicted {
		ns := slices2.GetAny(v).Namespace()
		keysByNS[ns] = append(keysByNS[ns], k)
	}

	return keysByNS
}

// queryNamespaceKeys queries the token chaincode for ns's keys, sends any found KeyInfo to ch,
// removes found keys from keySet, and returns the keys that were not found. ok is false when the
// namespace query itself failed (marshal, query, unmarshal, or a peer response of unexpected
// shape) — the caller must fall back to the block scan for this namespace rather than treating it
// as an authoritative "nothing found" (see #1990, and #1426 for the same fix on the finality
// path); nothing is sent to ch or removed from keySet in that case.
func (q *DeliveryScanQueryByID) queryNamespaceKeys(ctx context.Context, ns driver.Namespace, keys []driver.PKey, keySet collections.Set[driver.PKey], ch chan []KeyInfo) (notFound []driver.PKey, ok bool) {
	arg, err := json.Marshal(keys)
	if err != nil {
		logger.Errorf("failed marshalling args for query by ids [%v]: [%s], falling back to block scan", keys, err)

		return nil, false
	}

	logger.DebugfContext(ctx, "querying chaincode [%s] for the states of ids [%v]", ns, keys)
	res, err := q.Querier.QueryStates(ns, arg)
	if err != nil {
		logger.Errorf("failed querying by ids [%v]: [%s], falling back to block scan", keys, err)

		return nil, false
	}
	values := make([][]byte, 0, len(keys))
	if err := json.Unmarshal(res, &values); err != nil {
		logger.Errorf("failed unmarshalling results for query by ids [%v]: [%s], falling back to block scan", keys, err)

		return nil, false
	}
	if len(values) != len(keys) {
		logger.Errorf("peer returned %d values for %d keys in ns [%s]; falling back to block scan",
			len(values), len(keys), ns)

		return nil, false
	}
	found := make([]KeyInfo, 0, len(values))
	for i, value := range values {
		if len(value) == 0 {
			notFound = append(notFound, keys[i])

			continue
		}
		found = append(found, KeyInfo{
			Namespace: ns,
			Key:       keys[i],
			Value:     value,
		})
		keySet.Remove(keys[i])
	}
	ch <- found

	return notFound, true
}

// queryAllNamespaces queries every namespace in keysByNS, updating it in place to only the keys
// still not found (removing namespaces that were fully found), and reports whether any key was
// not found (so the caller should fall back to scanning blocks).
func (q *DeliveryScanQueryByID) queryAllNamespaces(ctx context.Context, keysByNS map[driver.Namespace][]driver.PKey, keySet collections.Set[driver.PKey], ch chan []KeyInfo) (startDelivery bool, err error) {
	for ns, keys := range keysByNS {
		notFound, ok := q.queryNamespaceKeys(ctx, ns, keys, keySet, ch)
		if !ok {
			// A failure here concerns this namespace only. Leave its keys in keysByNS and keySet
			// and fall back to the block scan, rather than dropping every other namespace in the
			// batch along with it (see #1990, and #1426 for the same fix on the finality path).
			startDelivery = true

			continue
		}
		if len(notFound) == 0 {
			delete(keysByNS, ns)
		} else {
			keysByNS[ns] = notFound
			startDelivery = true
		}
	}

	return startDelivery, nil
}

// scanTxForKeys inspects tx's RWSet (via v) for any of keysByNS's remaining keys, sends any found
// to ch, and removes them from keySet. It reports whether every key has now been found (the
// ScanFromBlock stop signal).
func scanTxForKeys(ctx context.Context, v rwsetInspector, tx *fabric.ProcessedTransaction, keysByNS map[driver.Namespace][]driver.PKey, keySet collections.Set[driver.PKey], ch chan []KeyInfo) (bool, error) {
	rws, err := v.InspectRWSet(ctx, tx.Results())
	if err != nil {
		return false, err
	}

	var txInfos []KeyInfo
	for namespace, keys := range keysByNS {
		if !slices.Contains(rws.Namespaces(), namespace) {
			logger.DebugfContext(ctx, "scanning [%s] does not contain namespace [%s]", tx.TxID(), namespace)

			continue
		}

		//nolint:intrange
		for i := 0; i < rws.NumWrites(namespace); i++ {
			k, v, err := rws.GetWriteAt(namespace, i)
			if err != nil {
				logger.DebugfContext(ctx, "scanning [%s]: failed to get key [%s]", tx.TxID(), err)

				return false, err
			}
			if slices.Contains(keys, k) {
				logger.DebugfContext(ctx, "scanning [%s]: found key [%s]", tx.TxID(), k)
				txInfos = append(txInfos, KeyInfo{
					Namespace: namespace,
					Key:       k,
					Value:     v,
				})
				logger.DebugfContext(ctx, "removing [%s] from searching list, remaining keys [%d]", k, keySet.Length())
				keySet.Remove(k)
			}
		}
	}
	if len(txInfos) != 0 {
		ch <- txInfos
	}

	return keySet.Length() == 0, nil
}

func (q *DeliveryScanQueryByID) queryByID(ctx context.Context, keys []driver.PKey, ch chan []KeyInfo, lastBlock uint64, evicted map[driver.PKey][]events.ListenerEntry[KeyInfo]) {
	defer close(ch)

	keySet := collections.NewSet(keys...)
	keysByNS := groupKeysByNamespace(evicted)

	// for each namespace, have a call to the token chaincode
	startDelivery, err := q.queryAllNamespaces(ctx, keysByNS, keySet, ch)
	if err != nil {
		return
	}
	if !startDelivery {
		return
	}

	startingBlock := blockscan.StartingBlock(lastBlock)
	logger.DebugfContext(ctx, "start scanning blocks starting from [%d], looking for remaining keys [%s]", startingBlock, keySet)

	// start delivery for the future
	v := q.Vault
	err = q.Delivery.ScanFromBlock(
		ctx,
		startingBlock,
		func(tx *fabric.ProcessedTransaction) (bool, error) {
			return scanTxForKeys(ctx, v, tx, keysByNS, keySet, ch)
		},
	)
	if err != nil {
		logger.Errorf("failed scanning blocks [%s], started from [%d]", err, startingBlock)

		return
	}
	logger.DebugfContext(ctx, "finished scanning blocks starting from [%d]", startingBlock)
}
