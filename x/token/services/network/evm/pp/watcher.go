/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp

import (
	"context"
	"sync"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
)

var logger = logging.MustGetLogger()

// DefaultWatchInterval is how often a Watcher asks the contract for its parameters version when no
// interval is configured. Public parameters change rarely, so this trades a negligible amount of
// polling for nodes that notice an update within a second or so of it landing.
const DefaultWatchInterval = time.Second

// UpdateHandler is called when the on-chain public parameters have changed, with the new parameters
// and the version they were stored at. It is called from the watcher's own goroutine, one call at a
// time, so a handler that blocks delays the next poll rather than racing with it.
//
// A non-nil return means this version was not fully applied. The watcher does not advance past a
// version its handler failed on, so the same version is retried on the next poll instead of being
// silently treated as handled: nothing else will ever ask the chain about a version once the watcher
// has moved on from it.
type UpdateHandler func(ctx context.Context, raw []byte, version uint64) error

// Watcher notices when a TMS's public parameters change on chain and hands the new ones to a handler.
//
// A node has no other way to find out. Parameters change through an endorsed setup delta submitted by
// somebody else, so unlike a transaction this node sent, there is nothing local to key off. Fabric
// solves the same problem with a listener on the setup key; here the equivalent signal is the version
// counter the TokenState contract keeps, which the endorsed update increments.
//
// It polls the version rather than subscribing to PublicParametersUpdated logs. The version is a
// single cheap eth_call against current state, so there is no from-block to track across restarts and
// nothing to reconcile if the chain reorganises: whatever the contract says now is the answer.
type Watcher struct {
	provider *ChainProvider
	versions *VersionKeeper
	interval time.Duration
	handler  UpdateHandler

	// lifecycleMu serializes Start against Stop, and Stop against a concurrent Stop. Stop holds it for
	// the full cancel-and-wait, not just the field swap: that is what stops a concurrent Start from
	// observing a half-stopped watcher (cancel == nil while the previous poller is still finishing) and
	// spawning a second poller alongside it, and what stops a second concurrent Stop from reading a
	// cleared cancel and returning early without actually having waited for anything.
	//
	// It must never be held while poll (or anything poll calls) is running, or Stop's wait on <-stopped
	// would deadlock against run() blocking to acquire the same lock. poll only ever takes stateMu, so
	// that never happens.
	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	stopped     chan struct{}

	// stateMu guards seen and hasSeen below. They are written by the polling goroutine and read from
	// tests as well, so they are locked rather than left to a happens-before argument that a later
	// change could quietly break.
	stateMu sync.Mutex
	// seen is the last version successfully applied by the handler. It starts unset, and the first
	// observation is applied rather than merely recorded: see poll.
	seen    uint64
	hasSeen bool
}

// NewWatcher returns a watcher for one TokenState clone. A zero interval means DefaultWatchInterval.
func NewWatcher(
	evmClient client.EVMClient,
	tokenState client.Address,
	blockTag string,
	interval time.Duration,
	handler UpdateHandler,
) (*Watcher, error) {
	if evmClient == nil {
		return nil, errors.New("pp watcher: nil evm client")
	}
	if handler == nil {
		return nil, errors.New("pp watcher: nil update handler")
	}
	if interval <= 0 {
		interval = DefaultWatchInterval
	}

	return &Watcher{
		provider: NewChainProvider(evmClient, tokenState, blockTag),
		versions: NewVersionKeeper(evmClient, tokenState, blockTag),
		interval: interval,
		handler:  handler,
	}, nil
}

// Start begins watching in the background. Calling it twice is a no-op, so a driver that builds the
// same network more than once does not end up with two pollers on one contract.
func (w *Watcher) Start(ctx context.Context) {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()
	if w.cancel != nil {
		return
	}

	ctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.stopped = make(chan struct{})

	go w.run(ctx, w.stopped)
}

// Stop ends the watch and waits for the goroutine to finish, so a stopped watcher is guaranteed not
// to call the handler again.
//
// lifecycleMu is held for the entire cancel-and-wait, not released after reading cancel/stopped: see
// its doc comment for why that is what makes this safe against a concurrent Start or Stop.
func (w *Watcher) Stop() {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()
	if w.cancel == nil {
		return
	}

	w.cancel()
	<-w.stopped
	w.cancel, w.stopped = nil, nil

	// Reset the applied-version bookkeeping so a Stop followed by a Start re-applies the first
	// observation, matching a freshly constructed watcher rather than silently skipping a version that
	// happens to match what was seen before the stop.
	w.stateMu.Lock()
	w.seen, w.hasSeen = 0, false
	w.stateMu.Unlock()
}

func (w *Watcher) run(ctx context.Context, stopped chan struct{}) {
	defer close(stopped)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

// poll checks the version and, if it is not the one already applied, reads the parameters and calls
// the handler. A failed read is logged and retried on the next tick rather than ending the watch: the
// node is worse off with stale parameters, but far worse off if a transient RPC error stopped it
// noticing forever.
//
// The very first observation is applied rather than taken as a baseline. It used to be recorded
// silently, on the reasoning that the node already has whatever the chain has, but that is exactly
// what is not true after a restart: the token layer resolves public parameters from its own storage
// before it ever asks the chain (see loadPublicParams), so a node that was down when an update landed
// comes back holding the old parameters, and a watcher that only reports later changes would leave it
// there indefinitely. Applying the first observation costs nothing when the two already agree, because
// the handler is a no-op for parameters the node is already running.
func (w *Watcher) poll(ctx context.Context) {
	w.versions.Invalidate()
	version, err := w.versions.GetVersion(ctx)
	if err != nil {
		logger.Debugf("failed to read the public parameters version: %v", err)

		return
	}

	w.stateMu.Lock()
	seen, hasSeen := w.seen, w.hasSeen
	w.stateMu.Unlock()

	if hasSeen && version == seen {
		return
	}

	raw, actual, err := w.provider.PublicParams(ctx)
	if err != nil {
		logger.Warnf("public parameters moved to version %d but could not be read: %v", version, err)

		return
	}

	// The handler runs before seen is advanced, and seen only advances once it succeeds. Advancing
	// first would mean a failed reload is treated as handled anyway: the next poll only looks at what
	// changed since seen, so a version it never actually applied would simply never be asked about
	// again.
	if err := w.handler(ctx, raw, actual); err != nil {
		logger.Warnf("failed to apply public parameters version %d, will retry: %v", actual, err)

		return
	}

	// Record what was actually applied rather than what the version poll reported. They can differ if
	// another update landed in between, and the parameters are the thing that matters.
	w.stateMu.Lock()
	w.seen, w.hasSeen = actual, true
	w.stateMu.Unlock()
	logger.Infof("public parameters updated to version %d", actual)
}
