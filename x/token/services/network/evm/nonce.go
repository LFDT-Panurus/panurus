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

// ErrNonceMayBeConsumed marks a failure that happened once the transaction had already been handed to
// the node, where the failure is not evidence that the chain never took it. A broadcast that times out
// or loses its connection is the case that matters: the node may well have accepted and mined the
// transaction, and only the reply was lost.
//
// Everything before that point (gas estimation, fee suggestion, signing) either does not reach the
// node or cannot consume a nonce, so those failures leave the sequence provably untouched and must not
// carry this.
var ErrNonceMayBeConsumed = errors.New("the nonce may have been consumed on chain")

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
		// A failure that reached the node leaves the local sequence unverifiable: the transaction may
		// have been accepted and only the reply lost, in which case this nonce is spent and every later
		// one derived from it is too low. Dropping the cached value makes the next allocation re-read
		// the chain, which settles the question either way.
		//
		// Without this a single lost broadcast reply wedges the account permanently: the manager keeps
		// reissuing a nonce the chain has already used, every send fails "nonce too low", and nothing
		// ever re-reads the chain to notice. Re-reading is safe precisely because this lock is held
		// across the whole step, so there is no other in-flight broadcast whose nonce a fresh read
		// could fail to account for.
		if errors.Is(err, ErrNonceMayBeConsumed) {
			n.initialized = false
		}

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
