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
	"golang.org/x/sync/semaphore"
)

var logger = logging.MustGetLogger()

// Resource limits enforced by RecipientDataCache.
//
// Producing one recipient identity is expensive and remotely reachable: an anonymous owner wallet
// generates a fresh zero-knowledge (idemix) credential and performs several storage writes, and the
// work is driven by whatever counterparty asked this node for a recipient identity (see
// ttx.RespondRequestRecipientIdentityView). Without the bounds below, a burst of such requests
// translates one-to-one into concurrent cryptographic work, and a persistently failing backend
// turns the background provisioning loop into a busy spin.
const (
	// MaxRecipientDataCacheSize is the largest recipient-data cache this package will allocate,
	// regardless of the size requested by configuration (wallets.owners[].cacheSize /
	// wallets.defaultCacheSize). Cached entries are pre-generated identities held in memory, and
	// one cache exists per anonymous owner wallet, so an accidentally huge configured value would
	// otherwise commit an unbounded amount of memory and pre-registered identities.
	MaxRecipientDataCacheSize = 1024

	// MaxConcurrentRecipientDataGenerations bounds how many identities this cache will generate at
	// the same time when callers miss the cache. Callers beyond the bound wait for a slot rather
	// than being rejected, so the effect is backpressure: request latency grows while CPU, memory
	// and storage consumption stay bounded.
	MaxConcurrentRecipientDataGenerations = 8

	// provisionRetryBackoff is the initial pause the background provisioning loop takes after a
	// failed generation attempt. It doubles on each consecutive failure up to
	// maxProvisionRetryBackoff and resets on the first success.
	provisionRetryBackoff = 50 * time.Millisecond

	// maxProvisionRetryBackoff caps the exponential backoff of the background provisioning loop.
	maxProvisionRetryBackoff = 30 * time.Second
)

type RecipientDataBackendFunc func(ctx context.Context) (*driver.RecipientData, error)

type RecipientDataCache struct {
	Logger logging.Logger

	once   sync.Once
	backed RecipientDataBackendFunc

	cache        chan *driver.RecipientData
	cacheTimeout time.Duration
	metrics      *Metrics

	// generations bounds the number of concurrent on-the-spot calls to backed. See
	// MaxConcurrentRecipientDataGenerations.
	generations *semaphore.Weighted
}

// NewRecipientDataCache returns a cache of recipient data of the requested size, backed by backed.
//
// size is clamped to [0, MaxRecipientDataCacheSize]: a size of 0 disables pre-provisioning
// altogether (every call generates on the spot), and a size above the maximum is reduced so that
// misconfiguration cannot commit unbounded memory. Independently of size, at most
// MaxConcurrentRecipientDataGenerations generations run concurrently.
func NewRecipientDataCache(Logger logging.Logger, backed RecipientDataBackendFunc, size int, metrics *Metrics) *RecipientDataCache {
	if size < 0 {
		size = 0
	}
	if size > MaxRecipientDataCacheSize {
		Logger.Warnf("requested recipient data cache size [%d] exceeds the maximum [%d], using the maximum", size, MaxRecipientDataCacheSize)
		size = MaxRecipientDataCacheSize
	}
	ci := &RecipientDataCache{
		Logger:       Logger,
		backed:       backed,
		cache:        make(chan *driver.RecipientData, size),
		cacheTimeout: time.Millisecond * 5,
		metrics:      metrics,
		generations:  semaphore.NewWeighted(MaxConcurrentRecipientDataGenerations),
	}

	return ci
}

// Capacity returns the number of recipient data entries this cache can hold, after the clamping
// described on NewRecipientDataCache. A capacity of 0 means no pre-provisioning takes place.
func (c *RecipientDataCache) Capacity() int {
	return cap(c.cache)
}

func (c *RecipientDataCache) RecipientData(ctx context.Context) (*driver.RecipientData, error) {
	c.once.Do(func() {
		c.Logger.Debugf("provision wallet recipient data with cache size [%d]", cap(c.cache))
		if cap(c.cache) > 0 {
			go c.provisionIdentities()
		}
	})

	var start time.Time
	if c.Logger.IsEnabledFor(zapcore.DebugLevel) {
		start = time.Now()
	}
	timeout := time.NewTimer(c.cacheTimeout)
	defer timeout.Stop()

	var identity *driver.RecipientData
	var err error
	logger.DebugfContext(ctx, "fetching wallet recipient data")
	select {
	case entry := <-c.cache:
		c.metrics.CacheLevelGauge.Add(-1)
		logger.DebugfContext(ctx, "fetched wallet recipient data from cache")
		identity = entry
		c.Logger.DebugfContext(ctx, "fetching wallet identity from cache [%s] took [%v]", identity, time.Since(start))
	case <-timeout.C:
		logger.DebugfContext(ctx, "generating wallet recipient data on the spot")
		identity, err = c.generate(ctx)
		if err != nil {
			return nil, err
		}
		c.Logger.DebugfContext(ctx, "fetching wallet identity from backend after a timeout [%s] took [%v]", identity, time.Since(start))
	case <-ctx.Done():
		return nil, errors.New("context is done")
	}
	logger.DebugfContext(ctx, "fetching wallet recipient data done")

	return identity, nil
}

// generate produces recipient data on the request path, admitting at most
// MaxConcurrentRecipientDataGenerations callers at a time. Callers beyond that wait for a slot and
// are released early if ctx is cancelled, so the bound is backpressure rather than rejection.
//
// Once a slot is held the cache is re-checked without blocking: under a burst, generations that
// completed while this caller was queued may already have filled it, and serving from the cache
// avoids paying for a credential generation that is no longer needed.
func (c *RecipientDataCache) generate(ctx context.Context) (*driver.RecipientData, error) {
	if !c.generations.TryAcquire(1) {
		c.Logger.DebugfContext(ctx, "recipient data generation limit [%d] reached, waiting for a slot", MaxConcurrentRecipientDataGenerations)
		if err := c.generations.Acquire(ctx, 1); err != nil {
			return nil, errors.Wrap(err, "failed waiting for a recipient data generation slot")
		}
	}
	defer c.generations.Release(1)

	select {
	case entry := <-c.cache:
		c.metrics.CacheLevelGauge.Add(-1)
		c.Logger.DebugfContext(ctx, "fetched wallet recipient data from cache after waiting for a generation slot")

		return entry, nil
	default:
	}

	identity, err := c.backed(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed fetching wallet identity")
	}

	return identity, nil
}

// provisionIdentities keeps the cache full in the background. It exits only when the process does.
//
// The loop is throttled from two sides: the send on c.cache blocks once the cache is full, bounding
// how far ahead of consumption it may run, and a failing backend is retried with exponential
// backoff rather than immediately, so a persistent failure (storage down, key material
// unavailable) cannot turn this into a busy loop burning a core per wallet.
func (c *RecipientDataCache) provisionIdentities() {
	ctx := context.Background()
	backoff := provisionRetryBackoff
	for {
		id, err := c.backed(ctx)
		if err != nil {
			c.Logger.Debugf("failed provisioning wallet recipient data, retrying in [%v]: %v", backoff, err)
			time.Sleep(backoff)
			if backoff *= 2; backoff > maxProvisionRetryBackoff {
				backoff = maxProvisionRetryBackoff
			}

			continue
		}
		backoff = provisionRetryBackoff
		c.metrics.CacheLevelGauge.Add(1)
		c.cache <- id
	}
}
