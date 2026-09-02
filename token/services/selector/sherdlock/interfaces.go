/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
)

// Iterator represents a generic iterator with error return and Close method.
//
//go:generate counterfeiter -o mocks/iterator.go -fake-name FakeIterator . Iterator
type Iterator[k any] interface {
	Next() (k, error)
	Close()
}

// TokenLocker interface for token locking operations.
//
//go:generate counterfeiter -o mocks/token_locker.go -fake-name FakeTokenLocker . TokenLocker
type TokenLocker interface {
	// TryLock attempts to lock the token for the selecting wallet (walletID).
	// It returns whether the lock was acquired and, when it was not, the underlying
	// error. When that error wraps token.SelectorRateLimited the selector aborts.
	TryLock(ctx context.Context, tokenID *token2.ID, walletID string) (bool, error)
	UnlockAll(ctx context.Context) error
}

// TokenFetcher interface for fetching tokens.
//
//go:generate counterfeiter -o mocks/token_fetcher.go -fake-name FakeTokenFetcher . TokenFetcher
type TokenFetcher interface {
	UnspentTokensIteratorBy(ctx context.Context, walletID string, currency token2.Type, limit int) (Iterator[*token2.UnspentTokenInWallet], error)
}

// FetcherProvider interface for providing fetcher instances.
//
//go:generate counterfeiter -o mocks/fetcher_provider.go -fake-name FakeFetcherProvider . FetcherProvider
type FetcherProvider interface {
	GetFetcher(tmsID token.TMSID) (TokenFetcher, error)
}

// TokenDB interface for database token operations.
//
//go:generate counterfeiter -o mocks/tokendb.go -fake-name FakeTokenDB . TokenDB
type TokenDB interface {
	SpendableTokensIteratorBy(ctx context.Context, walletID string, typ token2.Type) (driver.SpendableTokensIterator, error)
}

// ConfigProvider interface for configuration provider.
//
//go:generate counterfeiter -o mocks/config_provider.go -fake-name FakeConfigProvider . ConfigProvider
type ConfigProvider interface {
	UnmarshalKey(key string, rawVal any) error
}

type TMS interface {
	ID() token.TMSID
	PublicParameters() *token.PublicParameters
}

// Locker interface for manager locking.
//
//go:generate counterfeiter -o mocks/locker.go -fake-name FakeLocker . Locker
type Locker interface {
	// Lock locks a specific token for the consumer TX on behalf of walletID (the wallet
	// the tokens are selected for). A Locker implementation may use walletID to apply
	// per-wallet policies such as rate limiting, returning an error wrapping
	// token.SelectorRateLimited to make the selection fail fast.
	Lock(ctx context.Context, tokenID *token2.ID, consumerTxID transaction.ID, walletID string) error
	// UnlockByTxID unlocks all tokens locked by the consumer TX
	UnlockByTxID(ctx context.Context, consumerTxID transaction.ID) error
	// Cleanup removes the locks such that either:
	// 1. The transaction that locked that token is valid or invalid;
	// 2. The lock is too old.
	Cleanup(ctx context.Context, leaseExpiry time.Duration) error
	// AcquireCleanupLeadership attempts to acquire leadership for the
	// cleanup tick, so only one replica runs Cleanup per tick. Non-distributed
	// backends (sqlite, in-memory) always grant leadership locally. The lock
	// id (if any) is owned internally by the implementation. See #1798.
	AcquireCleanupLeadership(ctx context.Context) (dbdriver.CleanupLeadership, bool, error)
}

// TokenSelectorUnlocker interface combines Selector and UnlockAll.
type TokenSelectorUnlocker interface {
	token.Selector
	UnlockAll(ctx context.Context) error
}

// Metrics related interfaces

//go:generate counterfeiter -o mocks/metrics_counter.go -fake-name FakeCounter github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics.Counter
type Counter interface {
	Add(float64)
	With(labelValues ...string) Counter
}

//go:generate counterfeiter -o mocks/metrics_histogram.go -fake-name FakeHistogram github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics.Histogram
type Histogram interface {
	Observe(float64)
	With(labelValues ...string) Histogram
}

//go:generate counterfeiter -o mocks/metrics_provider.go -fake-name FakeProvider github.com/LFDT-Panurus/panurus/token/core/common/metrics.Provider
type Provider interface {
	NewCounter(opts struct {
		Namespace  string
		Subsystem  string
		Name       string
		Help       string
		LabelNames []string
		Statsd     struct{ LabelNames []string }
	}) Counter
	NewHistogram(opts struct {
		Namespace  string
		Subsystem  string
		Name       string
		Help       string
		Buckets    []float64
		LabelNames []string
		Statsd     struct{ LabelNames []string }
	}) Histogram
}

//go:generate counterfeiter -o mocks/spendable_tokens_iterator.go -fake-name FakeSpendableTokensIterator github.com/LFDT-Panurus/panurus/token/driver.SpendableTokensIterator
//go:generate counterfeiter -o mocks/store_service_manager.go -fake-name FakeTokenDBStoreServiceManager github.com/LFDT-Panurus/panurus/token/services/storage/tokendb.StoreServiceManager
//go:generate counterfeiter -o mocks/token_lock_store_service_manager.go -fake-name FakeTokenLockStoreServiceManager github.com/LFDT-Panurus/panurus/token/services/storage/tokenlockdb.StoreServiceManager

type StoreServiceManager interface {
	StoreServiceByTMSId(id struct {
		Network   string
		Channel   string
		Namespace string
		Public    bool
	}) (*struct{}, error)
}
