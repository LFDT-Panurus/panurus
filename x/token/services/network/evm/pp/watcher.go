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
type UpdateHandler func(ctx context.Context, raw []byte, version uint64)

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

	// mu guards everything below it. seen and hasSeen are only written by the polling goroutine, but
	// they are read from tests and from baselineSet, so they are locked rather than left to a
	// happens-before argument that a later change could quietly break.
	mu      sync.Mutex
	cancel  context.CancelFunc
	stopped chan struct{}
	// seen is the last version handed to the handler. It starts unset so the first observation
	// establishes a baseline instead of replaying the parameters the node already started with.
	seen    uint64
	hasSeen bool
}

// baselineSet reports whether the watcher has observed the chain at least once, which is when it
// starts treating a version change as an update rather than as its starting point.
func (w *Watcher) baselineSet() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.hasSeen
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
	w.mu.Lock()
	defer w.mu.Unlock()
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
func (w *Watcher) Stop() {
	w.mu.Lock()
	cancel, stopped := w.cancel, w.stopped
	w.cancel, w.stopped = nil, nil
	w.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	<-stopped
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

// poll checks the version once and, if it moved, reads the new parameters and calls the handler. A
// failed read is logged and retried on the next tick rather than ending the watch: the node is worse
// off with stale parameters, but far worse off if a transient RPC error stopped it noticing forever.
func (w *Watcher) poll(ctx context.Context) {
	w.versions.Invalidate()
	version, err := w.versions.GetVersion(ctx)
	if err != nil {
		logger.Debugf("failed to read the public parameters version: %v", err)

		return
	}

	w.mu.Lock()
	seen, hasSeen := w.seen, w.hasSeen
	if !hasSeen {
		w.seen, w.hasSeen = version, true
	}
	w.mu.Unlock()

	if !hasSeen || version == seen {
		return
	}

	// The provider caches the version behind its own keeper, so without this it would pair the freshly
	// read parameters with whatever version it last saw. That mismatch is invisible until two updates
	// land in one run, which is exactly when it matters.
	w.provider.Invalidate()
	raw, actual, err := w.provider.PublicParams(ctx)
	if err != nil {
		logger.Warnf("public parameters moved to version %d but could not be read: %v", version, err)

		return
	}

	// Record what was actually read rather than what the version poll reported. They can differ if
	// another update landed in between, and the parameters are the thing that matters.
	w.mu.Lock()
	w.seen = actual
	w.mu.Unlock()
	logger.Infof("public parameters updated to version %d", actual)
	w.handler(ctx, raw, actual)
}
