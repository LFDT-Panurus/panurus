/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"sync"
	"time"

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
//
// The cached sequence can also run ahead of the chain without any broadcast failing: a transaction the
// node accepted can leave its pool unmined (evicted, or lost when the node restarts, since a pool is
// not persisted). Every later nonce is then queued behind the hole and never mines. So before each
// allocation that comes at least gapCheckEvery after the last read, the pending nonce is read again,
// and if it stays below the cached one for gapGrace the sequence goes back to fill the hole (see
// checkGap). The interval keeps a busy submitter at about one extra read per interval, not one per
// transaction.
type NonceManager struct {
	client        client.EVMClient
	submitter     client.Address
	gapGrace      time.Duration
	gapCheckEvery time.Duration
	readTimeout   time.Duration
	now           func() time.Time

	mu          sync.Mutex
	next        uint64
	initialized bool
	// lastRead is when the pending nonce was last read from the node.
	lastRead time.Time
	// behind is the pending nonce last seen below next, and since when, while that lasts.
	behind      uint64
	behindSince time.Time
	// refill is set when next was moved back into a hole: once that nonce is used, the transactions
	// queued after it become minable, so the following allocation re-reads the chain instead.
	refill bool
}

// DefaultNonceGapGrace is how long the node's pending nonce must stay below the local sequence before
// the sequence is treated as having a hole. A node behind a load balancer can briefly answer from a
// replica that has not seen the latest broadcast yet, and reusing that nonce would replace a live
// transaction.
const DefaultNonceGapGrace = 30 * time.Second

// DefaultNonceGapCheckInterval is the least time between two reads of the pending nonce made only to
// look for a gap.
const DefaultNonceGapCheckInterval = 10 * time.Second

// DefaultNonceReadTimeout bounds each read of the pending nonce. The read runs under the manager's
// lock, so a node that never answers would otherwise block every submitter on the account.
const DefaultNonceReadTimeout = 5 * time.Second

// NewNonceManager returns a manager for the submitter account.
func NewNonceManager(evmClient client.EVMClient, submitter client.Address) *NonceManager {
	return &NonceManager{
		client:        evmClient,
		submitter:     submitter,
		gapGrace:      DefaultNonceGapGrace,
		gapCheckEvery: DefaultNonceGapCheckInterval,
		readTimeout:   DefaultNonceReadTimeout,
		now:           time.Now,
	}
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
		pending, err := n.readPending(ctx)
		if err != nil {
			return errors.Wrapf(err, "failed to recover the nonce for submitter [%s]", n.submitter)
		}
		n.next = pending
		n.initialized = true
		n.lastRead = n.now()
		// A gap sighting from before the re-read is about a sequence that no longer exists. Kept, it
		// would let a later sighting of the same value skip the grace period.
		n.behind, n.behindSince = 0, time.Time{}
		n.refill = false
	} else {
		n.checkGap(ctx)
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
	if n.refill {
		// This re-read can hit a lagging replica, as the initial read can, and it has no grace period:
		// a replica that has not seen the filled nonce yet hands it out again. The node then refuses
		// the broadcast as a nonce error, which carries ErrNonceMayBeConsumed and re-reads once more.
		n.refill = false
		n.initialized = false
	}

	return nil
}

// checkGap compares the cached sequence with the node's pending nonce. The lock is held and nothing
// is in flight, so every nonce below next has been accepted by the node: a pending nonce below next
// means one of those transactions has left the pool unmined, and the ones after it are stuck.
//
// The same pending value must be seen for gapGrace before the sequence is moved back to it, so a
// lagging replica cannot cause a live transaction to be replaced. Once moved back, the next allocation
// fills the hole, the queued transactions become minable again, and the manager re-reads the chain
// rather than reissue their nonces (see WithNonce). A failed read changes nothing: the broadcast goes
// ahead with the cached nonce, as it would have without the check.
func (n *NonceManager) checkGap(ctx context.Context) {
	if n.now().Sub(n.lastRead) < n.gapCheckEvery {
		return
	}
	n.lastRead = n.now()
	pending, err := n.readPending(ctx)
	if err != nil {
		logger.Debugf("could not check the nonce of submitter [%s] for a gap: %v", n.submitter, err)

		return
	}
	if pending >= n.next {
		n.behindSince = time.Time{}

		return
	}
	now := n.now()
	if n.behindSince.IsZero() || n.behind != pending {
		n.behind, n.behindSince = pending, now

		return
	}
	if now.Sub(n.behindSince) < n.gapGrace {
		return
	}
	logger.Warnf("submitter [%s]: the node's pending nonce has been %d, below the local %d, for %s; "+
		"a transaction left the pool unmined, filling the gap", n.submitter, pending, n.next, now.Sub(n.behindSince))
	n.next = pending
	n.behindSince = time.Time{}
	n.refill = true
}

// readPending reads the node's pending nonce, bounded by readTimeout since the lock is held.
func (n *NonceManager) readPending(ctx context.Context) (uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, n.readTimeout)
	defer cancel()

	return n.client.PendingNonceAt(ctx, n.submitter)
}

// Cached returns the next nonce the manager would hand out and whether it has been initialized,
// without contacting the node. It exists for diagnostics and tests.
func (n *NonceManager) Cached() (uint64, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()

	return n.next, n.initialized
}
