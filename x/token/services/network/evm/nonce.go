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
// to be strictly sequential per account, so two broadcasts must never collide: WithNonce holds a lock
// across the whole allocate-and-use step, not just the allocation, so nothing else can be mid-flight
// when a failed attempt needs the sequence walked back.
//
// The chain is the only source of truth, so nothing here is persisted. On a fresh start (or after a
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

// WithNonce allocates the next nonce and runs use with it, holding the manager's lock for the whole
// step. The sequence advances only if use succeeds; on failure the nonce is left exactly where it
// was, so the next call gets the same value, with no round trip to the chain needed to know that is
// safe.
//
// It is safe because of what use's contract already is, not despite it: every caller in this driver
// treats a failure as proof the transaction never reached the chain (gas estimation and fee
// suggestion are read-only, signing is local, and a rejected broadcast is documented as never judged
// by the chain), and while use runs, no other call can be allocating a nonce for this account at all.
// A nonce handed out twice used to happen exactly in the gap between those two things: one call's
// failure re-derived the whole sequence from the chain's current mempool view, which does not yet
// include a nonce a different, still in-flight call already holds and has not broadcast yet.
func (n *NonceManager) WithNonce(ctx context.Context, use func(nonce uint64) error) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.initialized {
		pending, err := n.client.PendingNonceAt(ctx, n.submitter)
		if err != nil {
			return errors.Wrapf(err, "failed to recover the nonce for submitter [%s]", n.submitter)
		}
		n.next = pending
		n.initialized = true
	}

	if err := use(n.next); err != nil {
		return err
	}
	n.next++

	return nil
}

// Cached returns the next nonce the manager would hand out and whether it has been initialized,
// without contacting the node. It exists for diagnostics and tests.
func (n *NonceManager) Cached() (uint64, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()

	return n.next, n.initialized
}
