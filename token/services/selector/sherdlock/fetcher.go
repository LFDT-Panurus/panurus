/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokendb"
	"github.com/LFDT-Panurus/panurus/token/services/utils/cache"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
)

const (
	defaultCacheFreshnessInterval = 1 * time.Second
	defaultCacheMaxQueries        = maxImmediateRetries
)

type FetcherStrategy string

const (
	Lazy     FetcherStrategy = "lazy"
	Eager    FetcherStrategy = "eager"
	Mixed    FetcherStrategy = "mixed"
	Listener FetcherStrategy = "listener"
	Cached   FetcherStrategy = "cached"
)

type fetchFunc func(db *tokendb.StoreService, m *Metrics, cacheSize int64, freshnessInterval time.Duration, maxQueries int) TokenFetcher

type fetcherProvider struct {
	tokenStoreServiceManager tokendb.StoreServiceManager
	metrics                  *Metrics
	fetch                    fetchFunc
	cacheSize                int64
	freshnessInterval        time.Duration
	maxQueries               int
}

var fetchers = map[FetcherStrategy]fetchFunc{
	Mixed: func(db *tokendb.StoreService, m *Metrics, cacheSize int64, freshnessInterval time.Duration, maxQueries int) TokenFetcher {
		return newMixedFetcher(db, m, cacheSize, freshnessInterval, maxQueries)
	},
}

// NewFetcherProvider creates a new fetcher provider with the specified strategy and configuration.
func NewFetcherProvider(storeServiceManager tokendb.StoreServiceManager, metricsProvider metrics.Provider, strategy FetcherStrategy, cacheSize int64, freshnessInterval time.Duration, maxQueries int) *fetcherProvider {
	fetcher, ok := fetchers[strategy]
	if !ok {
		panic("undefined fetcher strategy: " + strategy)
	}

	return &fetcherProvider{
		tokenStoreServiceManager: storeServiceManager,
		metrics:                  NewMetrics(metricsProvider),
		fetch:                    fetcher,
		cacheSize:                cacheSize,
		freshnessInterval:        freshnessInterval,
		maxQueries:               maxQueries,
	}
}

// GetFetcher returns a token fetcher instance for the specified TMS ID.
func (p *fetcherProvider) GetFetcher(tmsID token.TMSID) (TokenFetcher, error) {
	tokenDB, err := p.tokenStoreServiceManager.StoreServiceByTMSId(tmsID)
	if err != nil {
		return nil, err
	}

	return p.fetch(tokenDB, p.metrics, p.cacheSize, p.freshnessInterval, p.maxQueries), nil
}

// mixedFetcher combines both eager and lazy strategies
// In this example we return the eager result only the first time and all subsequent request are served by the lazy fetcher
// Other implementations can make different combinations, e.g. fresh results under a threshold (e.g. 10ms) can be served by the eager fetcher
// or listen for insert events in the database
type mixedFetcher struct {
	lazyFetcher  *lazyFetcher
	eagerFetcher *cachedFetcher
	m            *Metrics
}

// NewMixedFetcher creates a fetcher that combines eager (cached) and lazy (on-demand) strategies.
func NewMixedFetcher(tokenDB TokenDB, m *Metrics, cacheSize int64, freshnessInterval time.Duration, maxQueries int) *mixedFetcher {
	return &mixedFetcher{
		lazyFetcher:  NewLazyFetcher(tokenDB),
		eagerFetcher: NewCachedFetcher(tokenDB, cacheSize, freshnessInterval, maxQueries),
		m:            m,
	}
}

// UnspentTokensIteratorBy returns an iterator for unspent tokens, trying cached results first, falling back to database query.
// The limit parameter is ignored by sherdlock as it uses its own token limiting mechanism.
func (f *mixedFetcher) UnspentTokensIteratorBy(ctx context.Context, walletID string, currency token2.Type, limit int) (Iterator[*token2.UnspentTokenInWallet], error) {
	logger.DebugfContext(ctx, "call unspent tokens iterator")
	it, err := f.eagerFetcher.UnspentTokensIteratorBy(ctx, walletID, currency, limit)
	logger.DebugfContext(ctx, "fetched eager iterator")
	if err == nil && it.(interface{ HasNext() bool }).HasNext() {
		logger.DebugfContext(ctx, "eager iterator had tokens. Returning iterator")
		f.m.UnspentTokensInvocations.With(fetcherTypeLabel, eager).Add(1)

		return it, nil
	}
	logger.DebugfContext(ctx, "eager iterator had no tokens. Returning lazy iterator")

	f.m.UnspentTokensInvocations.With(fetcherTypeLabel, lazy).Add(1)

	return f.lazyFetcher.UnspentTokensIteratorBy(ctx, walletID, currency, limit)
}

// newMixedFetcher is an internal alias for NewMixedFetcher.
func newMixedFetcher(tokenDB TokenDB, m *Metrics, cacheSize int64, freshnessInterval time.Duration, maxQueries int) *mixedFetcher {
	return NewMixedFetcher(tokenDB, m, cacheSize, freshnessInterval, maxQueries)
}

// lazyFetcher only looks up the results when requested
type lazyFetcher struct {
	tokenDB TokenDB
}

// NewLazyFetcher creates a fetcher that queries the database on every request.
func NewLazyFetcher(tokenDB TokenDB) *lazyFetcher {
	return &lazyFetcher{tokenDB: tokenDB}
}

// UnspentTokensIteratorBy queries the database directly for unspent tokens.
// The limit parameter is ignored by sherdlock as it uses its own token limiting mechanism.
func (f *lazyFetcher) UnspentTokensIteratorBy(ctx context.Context, walletID string, currency token2.Type, limit int) (Iterator[*token2.UnspentTokenInWallet], error) {
	logger.DebugfContext(ctx, "Query the DB for new tokens")
	it, err := f.tokenDB.SpendableTokensIteratorBy(ctx, walletID, currency)
	if err != nil {
		return nil, err
	}

	return collections.NewPermutatedIterator[token2.UnspentTokenInWallet](it)
}

type permutatableIterator[T any] interface {
	iterators.Iterator[T]
	NewPermutation() iterators.Iterator[T]
}

type tokenCache interface {
	Get(key string) (permutatableIterator[*token2.UnspentTokenInWallet], bool)
	Add(key string, value permutatableIterator[*token2.UnspentTokenInWallet])
	Delete(key string)
	Clear()
}

// cachedFetcher eagerly fetches all the tokens from the DB at regular intervals and returns the cached result
type cachedFetcher struct {
	tokenDB TokenDB
	cache   tokenCache
	// freshnessInterval is the time between periodical updates
	freshnessInterval time.Duration
	// maxQueriesBeforeRefresh is the number of times the fetcher will respond with the cached result before refreshing.
	maxQueriesBeforeRefresh uint32

	// TODO: A better strategy is to keep following variables per cache key (type/owner combination) and lock/fetch only the 'expired' entry
	lastFetched      int64
	queriesResponded uint32
	// prevKeys tracks cache keys from the previous update cycle to identify stale entries that need removal.
	prevKeys map[string]struct{}
	// isUpdating indicates if a cache refresh is currently in progress.
	isUpdating bool
	// updateCond allows goroutines to wait for an in-progress update to complete.
	updateCond *sync.Cond
	mu         sync.RWMutex
}

// NewCachedFetcher creates a fetcher that maintains a periodically refreshed cache of all tokens.
func NewCachedFetcher(tokenDB TokenDB, cacheSize int64, freshnessInterval time.Duration, maxQueriesBeforeRefresh int) *cachedFetcher {
	// Use defaults if values are not provided (zero values)
	if freshnessInterval <= 0 {
		freshnessInterval = defaultCacheFreshnessInterval
	}
	if maxQueriesBeforeRefresh <= 0 {
		maxQueriesBeforeRefresh = defaultCacheMaxQueries
	}

	var ristrettoCache tokenCache
	var err error

	// If cacheSize <= 0, use default size; otherwise use custom size
	// Both use the same default NumCounters and BufferItems
	if cacheSize <= 0 {
		ristrettoCache, err = cache.NewDefaultRistrettoCache[permutatableIterator[*token2.UnspentTokenInWallet]]()
	} else {
		ristrettoCache, err = cache.NewRistrettoCacheWithSize[permutatableIterator[*token2.UnspentTokenInWallet]](cacheSize)
	}

	if err != nil {
		panic("failed to create ristretto cache: " + err.Error())
	}

	f := &cachedFetcher{
		tokenDB:                 tokenDB,
		cache:                   ristrettoCache,
		freshnessInterval:       freshnessInterval,
		maxQueriesBeforeRefresh: uint32(maxQueriesBeforeRefresh),
		prevKeys:                make(map[string]struct{}),
	}
	f.updateCond = sync.NewCond(&f.mu)

	return f
}

// finishUpdate releases the update lock and signals all waiting goroutines.
// finishUpdate cleans up after an update operation: marks updating as complete,
// broadcasts to waiting goroutines, and releases the lock.
// Must be called while holding f.mu.
func (f *cachedFetcher) finishUpdate() {
	f.isUpdating = false
	f.updateCond.Broadcast()
	f.mu.Unlock()
}

// update refreshes the token cache from the database. It releases the lock during the
// potentially slow DB operation to avoid blocking other goroutines, then re-acquires
// the lock to atomically update the cache. A re-check of staleness is performed
// after the DB call completes to avoid overwriting a cache that was refreshed by
// another goroutine while waiting for the database.
func (f *cachedFetcher) update(ctx context.Context) {
	f.mu.Lock()
	if f.isUpdating {
		// Wait for the in-progress update to finish
		for f.isUpdating {
			f.updateCond.Wait()
		}
		f.mu.Unlock()

		return
	}

	if !f.isCacheStale() && !f.isCacheOverused() {
		logger.DebugfContext(ctx, "Cache renewed in the meantime by another process")
		f.mu.Unlock()

		return
	}
	logger.DebugfContext(ctx, "Renew token cache")
	f.isUpdating = true

	// Release lock during slow DB operation to not block other token operations
	f.mu.Unlock()

	it, err := f.tokenDB.SpendableTokensIteratorBy(ctx, "", "")
	if err != nil {
		logger.Warnf("Failed to get token iterator: %v", err)
		f.mu.Lock()
		f.finishUpdate()

		return
	}
	defer it.Close()

	m, err := f.groupTokensByKey(ctx, it)
	if err != nil {
		logger.Warnf("Failed to group tokens from iterator: %v", err)
		f.mu.Lock()
		f.finishUpdate()

		return
	}

	f.mu.Lock()
	// Re-check: another goroutine may have refreshed while we waited for DB
	if !f.isCacheStale() && !f.isCacheOverused() {
		logger.DebugfContext(ctx, "Cache renewed in the meantime by another process, skipping")
		f.finishUpdate()

		return
	}

	f.updateCache(ctx, m)
	atomic.StoreInt64(&f.lastFetched, time.Now().UnixNano())
	atomic.StoreUint32(&f.queriesResponded, 0)
	f.finishUpdate()
}

// groupTokensByKey reads tokens from the iterator and groups them by wallet/currency key.
// It returns an error if the iterator fails mid-way to prevent partial updates.
func (f *cachedFetcher) groupTokensByKey(ctx context.Context, it driver.SpendableTokensIterator) (map[string][]*token2.UnspentTokenInWallet, error) {
	m := map[string][]*token2.UnspentTokenInWallet{}
	for t, err := it.Next(); err == nil && t != nil; t, err = it.Next() {
		key := tokenKey(t.WalletID, t.Type)
		logger.DebugfContext(ctx, "Adding token with key [%s]", key)
		m[key] = append(m[key], t)
	}
	// Re-check for error after loop termination
	_, err := it.Next()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	return m, nil
}

// updateCache updates the cache by adding new entries before removing stale ones.
// This prevents concurrent readers from finding an empty cache during updates.
func (f *cachedFetcher) updateCache(ctx context.Context, tokensByKey map[string][]*token2.UnspentTokenInWallet) {
	// Add new entries before removing stale ones to keep the cache populated for concurrent readers.
	// This prevents "insufficient funds" errors that occur when readers find an empty cache during updates.

	// Step 1: Add/update new entries first
	newKeys := make(map[string]struct{}, len(tokensByKey))
	for key, toks := range tokensByKey {
		f.cache.Add(key, iterators.Slice(toks))
		newKeys[key] = struct{}{}
	}

	// Step 2: Remove stale keys (keys that existed before but not in new data)
	// By tracking prevKeys, we can identify and remove only outdated entries.
	for oldKey := range f.prevKeys {
		if _, exists := newKeys[oldKey]; !exists {
			logger.DebugfContext(ctx, "Removing stale key [%s] from cache", oldKey)
			f.cache.Delete(oldKey)
		}
	}

	// Step 3: Update tracked keys for next cycle
	f.prevKeys = newKeys
}

// UnspentTokensIteratorBy returns cached unspent tokens, triggering a refresh if the cache is stale or overused.
// The limit parameter is ignored by sherdlock as it uses its own token limiting mechanism.
func (f *cachedFetcher) UnspentTokensIteratorBy(ctx context.Context, walletID string, currency token2.Type, limit int) (Iterator[*token2.UnspentTokenInWallet], error) {
	defer atomic.AddUint32(&f.queriesResponded, 1)
	if f.isCacheOverused() {
		logger.DebugfContext(ctx, "Overused data. Soft refresh (in the background)...")
		go f.update(ctx)
	}
	f.mu.RLock()
	if f.isCacheStale() {
		f.mu.RUnlock()
		logger.DebugfContext(ctx, "Stale data. Hard refresh (now)...")
		f.update(ctx)
		f.mu.RLock()
	}

	it, ok := f.cache.Get(tokenKey(walletID, currency))
	f.mu.RUnlock()
	if ok {
		return it.NewPermutation(), nil
	}
	logger.DebugfContext(ctx, "No tokens found in cache for [%s]. Returning empty iterator.", tokenKey(walletID, currency))

	return collections.NewEmptyIterator[*token2.UnspentTokenInWallet](), nil
}

// isCacheOverused checks if the cache has been queried too many times since the last refresh.
func (f *cachedFetcher) isCacheOverused() bool {
	return atomic.LoadUint32(&f.queriesResponded) >= f.maxQueriesBeforeRefresh
}

// isCacheStale checks if the cache has exceeded its freshness interval.
func (f *cachedFetcher) isCacheStale() bool {
	lastFetched := atomic.LoadInt64(&f.lastFetched)
	if lastFetched == 0 {
		return true
	}

	return time.Since(time.Unix(0, lastFetched)) > f.freshnessInterval
}
