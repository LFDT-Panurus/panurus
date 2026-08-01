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

var logger = logging.MustGetLogger()

const (
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
}

func NewRecipientDataCache(Logger logging.Logger, backed RecipientDataBackendFunc, size int, metrics *Metrics) *RecipientDataCache {
	if size < 0 {
		size = 0
	}
	ci := &RecipientDataCache{
		Logger:       Logger,
		backed:       backed,
		cache:        make(chan *driver.RecipientData, size),
		cacheTimeout: time.Millisecond * 5,
		metrics:      metrics,
	}

	return ci
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
		identity, err = c.backed(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed fetching wallet identity")
		}
		c.Logger.DebugfContext(ctx, "fetching wallet identity from backend after a timeout [%s] took [%v]", identity, time.Since(start))
	case <-ctx.Done():
		return nil, errors.New("context is done")
	}
	logger.DebugfContext(ctx, "fetching wallet recipient data done")

	return identity, nil
}

// provisionIdentities keeps the cache full in the background.
//
// A failed generation is retried with exponential backoff rather than immediately, so a persistent
// failure (storage down, key material unavailable) cannot turn this into a busy loop burning a core
// per wallet. The backoff resets on the first success.
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
