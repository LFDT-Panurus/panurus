/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role

import (
	"context"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.uber.org/zap/zapcore"
)

// provisionRetryDelay is how long the background provisioning loop waits after a
// backend failure before trying again. Without it, a persistently failing identity
// backend would turn the loop into a busy spin.
const provisionRetryDelay = time.Second

type RecipientDataBackendFunc func(ctx context.Context) (*driver.RecipientData, error)

type RecipientDataCache struct {
	Logger logging.Logger

	once   sync.Once
	backed RecipientDataBackendFunc

	cache        chan *driver.RecipientData
	cacheTimeout time.Duration
	metrics      *Metrics

	// provisionCtx governs the lifetime of the background provisioning goroutine.
	// It is created at construction time and cancelled by Close.
	provisionCtx context.Context //nolint:containedctx
	// cancel cancels provisionCtx. It is written once, at construction time, so
	// that Close is safe to call concurrently with RecipientData.
	cancel context.CancelFunc
}

func NewRecipientDataCache(Logger logging.Logger, backed RecipientDataBackendFunc, size int, metrics *Metrics) *RecipientDataCache {
	if size < 0 {
		size = 0
	}
	// The provisioning goroutine must not inherit any caller's request context, but
	// it must still be cancellable, hence a cancellable child of context.Background.
	provisionCtx, cancel := context.WithCancel(context.Background())
	ci := &RecipientDataCache{
		Logger:       Logger,
		backed:       backed,
		cache:        make(chan *driver.RecipientData, size),
		cacheTimeout: time.Millisecond * 5,
		metrics:      metrics,
		provisionCtx: provisionCtx,
		cancel:       cancel,
	}

	return ci
}

// Close stops the background recipient data provisioning. It is idempotent and
// safe to call even if provisioning was never started. After Close, RecipientData
// still serves requests directly from the backend; it just no longer pre-provisions.
func (c *RecipientDataCache) Close() {
	c.cancel()
}

func (c *RecipientDataCache) RecipientData(ctx context.Context) (*driver.RecipientData, error) {
	c.once.Do(func() {
		c.Logger.Debugf("provision wallet recipient data with cache size [%d]", cap(c.cache))
		// Do not spawn the goroutine if the cache has already been closed, otherwise
		// a late first call would start a goroutine that nothing will ever stop.
		if cap(c.cache) > 0 && c.provisionCtx.Err() == nil {
			go c.provisionIdentities(c.provisionCtx)
		}
	})

	// start is only meaningful when the timing is actually going to be logged, so every read of
	// it is guarded by the same check that sets it: time.Since and the argument boxing it feeds
	// are evaluated eagerly, before Debugf gets to decide whether to format anything.
	debug := c.Logger.IsEnabledFor(zapcore.DebugLevel)
	var start time.Time
	if debug {
		start = time.Now()
	}
	timeout := time.NewTimer(c.cacheTimeout)
	defer timeout.Stop()

	var identity *driver.RecipientData
	var err error
	c.Logger.DebugfContext(ctx, "fetching wallet recipient data")
	select {
	case entry := <-c.cache:
		c.metrics.CacheLevelGauge.Add(-1)
		identity = entry
		if debug {
			c.Logger.DebugfContext(ctx, "fetched wallet recipient data from cache [%s], took [%v]", identity, time.Since(start))
		}
	case <-timeout.C:
		c.Logger.DebugfContext(ctx, "generating wallet recipient data on the spot")
		identity, err = c.backed(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed fetching wallet identity")
		}
		if debug {
			c.Logger.DebugfContext(ctx, "fetching wallet identity from backend after a timeout [%s] took [%v]", identity, time.Since(start))
		}
	case <-ctx.Done():
		return nil, errors.New("context is done")
	}
	c.Logger.DebugfContext(ctx, "fetching wallet recipient data done")

	return identity, nil
}

// provisionIdentities keeps the cache filled with pre-generated recipient data until
// ctx is cancelled. It returns as soon as ctx is done, both while backing off after a
// failure and while blocked handing an entry to the cache channel.
func (c *RecipientDataCache) provisionIdentities(ctx context.Context) {
	defer c.Logger.Debugf("stopped provisioning wallet recipient data")

	for {
		id, err := c.backed(ctx)
		if err != nil {
			// Log, count and back off: a bare continue here turns a persistently
			// failing backend into a silent busy loop that saturates a core.
			c.metrics.ProvisionFailuresCount.Add(1)
			c.Logger.Errorf("failed provisioning wallet recipient data, retrying in [%v]: [%s]", provisionRetryDelay, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(provisionRetryDelay):
			}

			continue
		}

		// The gauge is incremented only once the entry is actually in the channel,
		// so a cancellation mid-send cannot leave the reported cache level skewed.
		select {
		case c.cache <- id:
			c.metrics.CacheLevelGauge.Add(1)
		case <-ctx.Done():
			return
		}
	}
}
