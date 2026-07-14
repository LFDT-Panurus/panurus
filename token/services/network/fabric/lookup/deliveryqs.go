/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lookup

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/tcc"
	slices2 "github.com/LFDT-Panurus/panurus/token/services/utils/slices"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/events"
)

const (
	QueryStates      = tcc.QueryStates
	NumberPastBlocks = 10
	FirstBlock       = 1
)

type DeliveryScanQueryByID struct {
	Delivery *fabric.Delivery
	Channel  *fabric.Channel
}

func (q *DeliveryScanQueryByID) QueryByID(ctx context.Context, startingBlock driver.BlockNum, evicted map[driver.PKey][]events.ListenerEntry[KeyInfo]) (<-chan []KeyInfo, error) {
	// we are abusing TxID to carry the name of the keys we are looking for.
	// Keys are supposed to be unique
	keys := collections.Keys(evicted) // These are the state keys we are looking for
	ch := make(chan []KeyInfo, len(keys))
	go q.queryByID(ctx, keys, ch, startingBlock, evicted)

	return ch, nil
}

func (q *DeliveryScanQueryByID) queryByID(ctx context.Context, keys []driver.PKey, ch chan []KeyInfo, lastBlock uint64, evicted map[driver.PKey][]events.ListenerEntry[KeyInfo]) {
	defer close(ch)

	keySet := collections.NewSet(keys...)

	// group keys by namespace
	keysByNS := map[driver.Namespace][]driver.PKey{}
	for k, v := range evicted {
		ns := slices2.GetAny(v).Namespace()
		_, ok := keysByNS[ns]
		if !ok {
			keysByNS[ns] = []string{}
		}
		keysByNS[ns] = append(keysByNS[ns], k)
	}

	// for each namespace, have a call to the token chaincode
	startDelivery := false
	for ns, keys := range keysByNS {
		arg, err := json.Marshal(keys)
		if err != nil {
			logger.Errorf("failed marshalling args for query by ids [%v]: [%s]", keys, err)

			return
		}

		logger.DebugfContext(ctx, "querying chaincode [%s] for the states of ids [%v]", ns, keys)
		chaincode := q.Channel.Chaincode(ns)
		res, err := chaincode.Query(QueryStates, arg).Query()
		if err != nil {
			logger.Errorf("failed querying by ids [%v]: [%s]", keys, err)

			return
		}
		values := make([][]byte, 0, len(keys))
		err = json.Unmarshal(res, &values)
		if err != nil {
			logger.Errorf("failed unmarshalling results for query by ids [%v]: [%s]", keys, err)

			return
		}
		found := make([]KeyInfo, 0, len(values))
		var notFound []string
		for i, value := range values {
			if len(value) == 0 {
				startDelivery = true
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
		if len(notFound) == 0 {
			delete(keysByNS, ns)
		} else {
			keysByNS[ns] = notFound
		}
		ch <- found
	}

	if !startDelivery {
		return
	}

	startingBlock := max(FirstBlock, lastBlock-NumberPastBlocks)
	// startingBlock := uint64(0)
	logger.DebugfContext(ctx, "start scanning blocks starting from [%d], looking for remaining keys [%s]", startingBlock, keySet)

	// start delivery for the future
	v := q.Channel.Vault()
	err := q.Delivery.ScanFromBlock(
		ctx,
		startingBlock,
		func(tx *fabric.ProcessedTransaction) (bool, error) {
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
		},
	)
	if err != nil {
		logger.Errorf("failed scanning blocks [%s], started from [%d]", err, startingBlock)

		return
	}
	logger.DebugfContext(ctx, "finished scanning blocks starting from [%d]", startingBlock)
}
