/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/semaphore"
)

// MaxConcurrentRecipientDataGenerations bounds how many recipient identities this node produces at
// the same time, across all wallets.
//
// Producing one is remotely reachable and not free: a counterparty opens a session and asks this
// node for a recipient identity (see ttx.RespondRequestRecipientIdentityView), and the node derives
// a pseudonym and writes it to the identity and wallet stores. Without a bound, a burst of such
// requests translates one-to-one into concurrent work.
//
// Callers beyond the bound wait for a slot rather than being rejected, so the effect is
// backpressure: request latency grows while CPU, memory and storage consumption stay bounded. A
// caller whose context is cancelled while queued is released instead of holding its place.
//
// The bound is process-wide rather than per wallet on purpose: the resource being protected is this
// node's CPU and storage, which every wallet draws from, so a per-wallet bound would scale with the
// number of wallets and stop being a bound at all.
const MaxConcurrentRecipientDataGenerations = 8

// RecipientDataBackendFunc produces fresh recipient data.
type RecipientDataBackendFunc func(ctx context.Context) (*driver.RecipientData, error)

// RecipientDataProvider produces recipient data on demand, admitting at most
// MaxConcurrentRecipientDataGenerations generations at a time.
//
// It deliberately does not cache. The expensive part of producing a recipient identity is the
// credential generation, and that is already served from a pre-provisioned cache one layer down
// (idemix/cache.IdentityCache, wired in idemix.KeyManagerProvider). A second cache here bought a
// few milliseconds on the request path while holding pre-generated identities in memory for the
// lifetime of the node and writing a binding row for each one, whether or not any counterparty
// ever asked for it.
type RecipientDataProvider struct {
	Logger logging.Logger

	backed RecipientDataBackendFunc

	// generations bounds concurrent calls to backed. See MaxConcurrentRecipientDataGenerations.
	generations *semaphore.Weighted
}

// NewRecipientDataProvider returns a provider that generates recipient data through backed, with at
// most MaxConcurrentRecipientDataGenerations generations running concurrently.
//
// generations is shared by every wallet built from the same factory, so the bound applies to the
// node rather than to each wallet. Passing nil creates a provider with its own bound, which is
// useful in tests but should not be done in production wiring.
func NewRecipientDataProvider(logger logging.Logger, backed RecipientDataBackendFunc, generations *semaphore.Weighted) *RecipientDataProvider {
	if generations == nil {
		generations = semaphore.NewWeighted(MaxConcurrentRecipientDataGenerations)
	}

	return &RecipientDataProvider{
		Logger:      logger,
		backed:      backed,
		generations: generations,
	}
}

// RecipientData returns freshly generated recipient data, waiting for a generation slot if the node
// is already at MaxConcurrentRecipientDataGenerations. It returns an error if ctx is cancelled while
// waiting.
func (c *RecipientDataProvider) RecipientData(ctx context.Context) (*driver.RecipientData, error) {
	var start time.Time
	if c.Logger.IsEnabledFor(zapcore.DebugLevel) {
		start = time.Now()
	}

	if !c.generations.TryAcquire(1) {
		c.Logger.DebugfContext(ctx, "recipient data generation limit [%d] reached, waiting for a slot", MaxConcurrentRecipientDataGenerations)
		if err := c.generations.Acquire(ctx, 1); err != nil {
			return nil, errors.Wrap(err, "failed waiting for a recipient data generation slot")
		}
	}
	defer c.generations.Release(1)

	c.Logger.DebugfContext(ctx, "generating wallet recipient data")
	identity, err := c.backed(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed fetching wallet identity")
	}
	c.Logger.DebugfContext(ctx, "generating wallet recipient data [%s] took [%v]", identity, time.Since(start))

	return identity, nil
}
