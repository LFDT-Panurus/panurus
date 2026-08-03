/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"sync"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
)

// NonceManager hands out the submitter account's Ethereum transaction nonces. Ethereum requires them
// to be strictly sequential per account, so concurrent broadcasts must not read the same value: the
// manager serializes allocation and tracks the next nonce locally rather than asking the node each
// time, which would hand the same nonce to two callers racing between the query and the send.
//
// The chain is the source of truth, so there is nothing to persist. On a fresh start (or after a
// restart) the first allocation recovers from eth_getTransactionCount at the pending tag, which
// already accounts for transactions still in the mempool. That makes a node restart transparent, at
// the cost of one round trip.
type NonceManager struct {
	client    client.EVMClient
	submitter client.Address

	mu          sync.Mutex
	next        uint64
	initialized bool
}

// NewNonceManager returns a manager for the submitter account.
func NewNonceManager(evmClient client.EVMClient, submitter client.Address) *NonceManager {
	return &NonceManager{client: evmClient, submitter: submitter}
}

// Next returns the nonce to use for the next transaction and advances the counter. The caller owns
// the returned nonce: if it does not end up broadcasting, it should Reset so the sequence does not
// develop a gap that stalls every later transaction.
func (n *NonceManager) Next(ctx context.Context) (uint64, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.initialized {
		pending, err := n.client.PendingNonceAt(ctx, n.submitter)
		if err != nil {
			return 0, errors.Wrapf(err, "failed to recover the nonce for submitter [%s]", n.submitter)
		}
		n.next = pending
		n.initialized = true
	}

	nonce := n.next
	n.next++

	return nonce, nil
}

// Reset drops the cached counter so the next allocation re-reads from the node. It is the recovery
// path for a failed broadcast: rather than guess whether the node saw the transaction, re-derive the
// sequence from the pending nonce, which is authoritative.
func (n *NonceManager) Reset() {
	n.mu.Lock()
	n.initialized = false
	n.mu.Unlock()
}

// Cached returns the next nonce the manager would hand out and whether it has been initialized,
// without contacting the node. It exists for diagnostics and tests.
func (n *NonceManager) Cached() (uint64, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()

	return n.next, n.initialized
}
