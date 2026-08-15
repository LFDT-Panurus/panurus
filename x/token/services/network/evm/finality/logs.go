/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"context"
	"slices"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
)

// stateCommittedSignature is the canonical signature of the event TokenState emits when a delta is
// applied. Its Keccak-256 is topic 0 of the log; the anchor is indexed, so it lands in topic 1 and can
// be filtered on directly.
const stateCommittedSignature = "StateCommitted(bytes32,bool,string)"

// StateCommittedTopic returns topic 0 for the StateCommitted event.
func StateCommittedTopic() client.Hash {
	return crypto.Keccak256Hash([]byte(stateCommittedSignature))
}

// TxHashByAnchor finds the Ethereum transaction that applied the given anchor, by filtering the
// TokenState's StateCommitted logs on the indexed anchor. It reports false when no such log exists,
// which means the anchor has not been applied (or the range searched does not reach it).
//
// The hash comes from the log record's transactionHash, which the node supplies as metadata: a
// contract cannot read its own transaction hash, so it is not, and cannot be, in the event payload
// (design §7.4). That is why this is a log query rather than a plain call.
//
// This is the richer of the two recipient paths and deliberately not the default one. It needs a
// block range, and log retention varies between nodes, so StatusByAnchor answers the common "is it
// committed" question with a single call instead. Use this when the transaction hash itself is
// wanted, for instance to read the receipt and, on Besu, the revert reason.
func (m *Manager) TxHashByAnchor(ctx context.Context, anchor [32]byte) (client.Hash, bool, error) {
	if m.tokenState == (client.Address{}) {
		return client.Hash{}, false, errors.New("finality: no token state address configured for log lookups")
	}

	// ToBlock is left at zero, which the filter reads as "latest": the anchor may have been applied at
	// any point up to the head, and this avoids a second round trip just to learn the block number.
	logs, err := m.client.GetLogs(ctx, client.LogFilter{
		Address:   m.tokenState,
		FromBlock: m.fromBlock,
		Topics:    [][]client.Hash{{StateCommittedTopic()}, {anchor}},
	})
	if err != nil {
		return client.Hash{}, false, errors.Wrap(err, "finality: failed to search for the commit event")
	}

	// A log the node marks removed was mined in a block a reorg has since undone: it is no longer
	// part of the canonical chain and must not be treated as evidence the anchor was applied.
	logs = slices.DeleteFunc(logs, func(l client.Log) bool { return l.Removed })
	if len(logs) == 0 {
		return client.Hash{}, false, nil
	}

	// An anchor can be applied at most once: the contract rejects a second delta for it as already
	// processed. More than one log for the same anchor would mean the replay guard is broken, so it is
	// an error rather than something to silently take the first of.
	if len(logs) > 1 {
		return client.Hash{}, false, errors.Errorf(
			"finality: %d commit events for a single anchor, the replay guard is not holding", len(logs))
	}

	return logs[0].TxHash, true, nil
}
