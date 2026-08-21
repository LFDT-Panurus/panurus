/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"context"
	"sync"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
)

// logger reports transient finality-resolution problems that are retried rather than surfaced.
var logger = logging.MustGetLogger()

// StateReader is the slice of the TokenState the finality manager reads. It is defined here, on the
// consumer side, so this package does not import the driver package that constructs it.
type StateReader interface {
	// TokenRequestHash returns the token-request hash recorded for an anchor and whether the anchor
	// has been applied at all.
	TokenRequestHash(ctx context.Context, anchor [32]byte) (hash []byte, found bool, err error)
}

// Manager resolves the finality of EVM token transactions and notifies listeners.
//
// The chain is the only source of truth, which shapes what can be observed (design §7.1, §7.4):
//
//   - By eth transaction hash, the full lifecycle is visible: a receipt with status 1 is Valid,
//     status 0 is Invalid, a known but unmined transaction is Busy, and one the node has never seen
//     is dropped.
//   - By anchor, only success is observable. applyStateDelta records the anchor as its last step, so
//     a recorded anchor proves the transaction succeeded; but a failure REVERTS, leaving no trace at
//     all, so a missing anchor is indistinguishable from one still pending. Failure is therefore
//     reachable only through the timeout, which is why the timeout is a correctness parameter here
//     rather than a convenience.
//
// A restart needs no persisted state: both resolutions re-read from the chain.
type Manager struct {
	client       client.EVMClient
	state        StateReader
	tokenState   client.Address
	fromBlock    uint64
	pollInterval time.Duration
	timeout      time.Duration

	mu      sync.Mutex
	pending map[string]struct{}
}

// NewManager returns a finality manager. Non-positive intervals fall back to sane defaults so a
// partially filled configuration cannot produce a busy loop.
// The tokenState address and fromBlock are only needed by the log-based lookup (TxHashByAnchor); the
// status paths work without them.
func NewManager(
	evmClient client.EVMClient,
	state StateReader,
	tokenState client.Address,
	fromBlock uint64,
	pollInterval, timeout time.Duration,
) *Manager {
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	return &Manager{
		client:       evmClient,
		state:        state,
		tokenState:   tokenState,
		fromBlock:    fromBlock,
		pollInterval: pollInterval,
		timeout:      timeout,
		pending:      map[string]struct{}{},
	}
}

// StatusByTxHash resolves a transaction from its Ethereum hash, the path available to whoever
// submitted it.
func (m *Manager) StatusByTxHash(ctx context.Context, txHash client.Hash) (driver.ValidationCode, string, error) {
	receipt, err := m.client.GetTransactionReceipt(ctx, txHash)
	if err != nil {
		return driver.Unknown, "", errors.Wrapf(err, "failed to read the receipt for [%s]", txHash)
	}
	if receipt != nil && receipt.BlockNumber != nil {
		if receipt.Status == 1 {
			return driver.Valid, "", nil
		}

		return driver.Invalid, "transaction reverted", nil
	}

	// No receipt yet: distinguish a transaction the node is holding from one it has never seen.
	pending, found, err := m.client.IsPending(ctx, txHash)
	if err != nil {
		return driver.Unknown, "", errors.Wrapf(err, "failed to check whether [%s] is pending", txHash)
	}
	switch {
	case found && pending:
		return driver.Busy, "", nil
	case found:
		// Known but not pending and without a receipt: it is being mined right now.
		return driver.Busy, "", nil
	default:
		return driver.Unknown, "transaction unknown to the node", nil
	}
}

// StatusByAnchor resolves a transaction from its token-request anchor, the only identifier a
// recipient has. A recorded anchor yields Valid together with the stored token-request hash; an
// absent one yields Unknown, because a reverted apply leaves nothing behind to observe.
func (m *Manager) StatusByAnchor(ctx context.Context, anchor [32]byte) (driver.ValidationCode, []byte, string, error) {
	hash, found, err := m.state.TokenRequestHash(ctx, anchor)
	if err != nil {
		return driver.Unknown, nil, "", err
	}
	if found {
		return driver.Valid, hash, "", nil
	}

	return driver.Unknown, nil, "", nil
}

// AddListener watches an anchor until it is applied or the timeout expires, then notifies listener
// exactly once. If the anchor is already final it notifies immediately, matching the driver contract.
//
// Because failure is not observable by anchor, an anchor that has not appeared by the timeout is
// reported Invalid: at that point the transaction either reverted or was never submitted, and either
// way the caller must stop waiting for it.
func (m *Manager) AddListener(ctx context.Context, anchor [32]byte, anchorID string, listener driver.FinalityListener) error {
	if listener == nil {
		return errors.New("finality: nil listener")
	}

	// Fast path: already final. A read failure here is not fatal, because the node may be briefly
	// unavailable and failing the registration would lose the notification altogether; the watcher
	// below retries and still resolves, at the timeout in the worst case.
	code, hash, message, err := m.StatusByAnchor(ctx, anchor)
	switch {
	case err != nil:
		logger.Debugf("finality: initial status check for [%s] failed, watching anyway: %v", anchorID, err)
	case code == driver.Valid:
		listener.OnStatus(ctx, anchorID, code, message, hash)

		return nil
	}

	m.mu.Lock()
	if _, watching := m.pending[anchorID]; watching {
		m.mu.Unlock()

		return errors.Errorf("finality: already watching [%s]", anchorID)
	}
	m.pending[anchorID] = struct{}{}
	m.mu.Unlock()

	// The watcher deliberately outlives the caller's context: a listener registered during a
	// short-lived request must still be notified when the transaction settles, typically long after
	// that request returns. Its lifetime is bounded by the finality timeout instead.
	go m.watch(anchor, anchorID, once(listener)) // #nosec G118 -- detached by design, bounded by the timeout

	return nil
}

// watch polls until the anchor is applied or the timeout expires. It runs detached from the caller's
// context so a listener registered during a short-lived request still gets its notification.
func (m *Manager) watch(anchor [32]byte, anchorID string, listener driver.FinalityListener) {
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	defer func() {
		m.mu.Lock()
		delete(m.pending, anchorID)
		m.mu.Unlock()
	}()

	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	// observed becomes true the moment any poll actually reaches the chain, whether or not the anchor
	// was found. It is what separates "checked repeatedly and it is genuinely absent" from "the chain
	// was unreachable for the whole window": only the first is evidence the transaction is invalid, and
	// conflating them would let a persistent connectivity failure masquerade as a revert.
	observed := false

	for {
		select {
		case <-ticker.C:
			code, hash, message, err := m.StatusByAnchor(ctx, anchor)
			if err != nil {
				continue // a transient read failure is not a verdict; keep polling until the timeout
			}
			observed = true
			if code == driver.Valid {
				listener.OnStatus(ctx, anchorID, code, message, hash)

				return
			}
		case <-ctx.Done():
			if !observed {
				// Every read attempt in the window errored: the chain was never actually reached, so
				// there is no evidence the anchor is invalid, only that it could not be observed.
				listener.OnError(context.Background(), anchorID,
					errors.New("finality: could not reach the chain before the timeout"))

				return
			}
			// The anchor never appeared, and at least one read genuinely confirmed its absence. A
			// reverted apply emits nothing, so this is the only failure signal available by anchor.
			listener.OnStatus(context.Background(), anchorID, driver.Invalid, "finality timeout", nil)

			return
		}
	}
}

// once wraps a listener so it is notified at most one time, however the resolution is reached.
func once(listener driver.FinalityListener) driver.FinalityListener {
	return &onceListener{listener: listener}
}

type onceListener struct {
	listener driver.FinalityListener
	fired    sync.Once
}

func (o *onceListener) OnStatus(ctx context.Context, txID string, status int, message string, tokenRequestHash []byte) {
	o.fired.Do(func() { o.listener.OnStatus(ctx, txID, status, message, tokenRequestHash) })
}

func (o *onceListener) OnError(ctx context.Context, txID string, err error) {
	o.fired.Do(func() { o.listener.OnError(ctx, txID, err) })
}
