/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package observability

import (
	"container/list"
	"context"
	"math/big"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
)

type lruEntry struct {
	key string
	cb  *CircuitBreaker
}

// breakerLRU is a thread-safe, bounded LRU cache for CircuitBreakers.
type breakerLRU struct {
	mu        sync.Mutex
	capacity  int
	items     map[string]*list.Element
	evictList *list.List
}

func newBreakerLRU(capacity int) *breakerLRU {
	if capacity <= 0 {
		capacity = 1000
	}

	return &breakerLRU{
		capacity:  capacity,
		items:     make(map[string]*list.Element),
		evictList: list.New(),
	}
}

func (l *breakerLRU) getOrSet(key string, factory func() *CircuitBreaker) *CircuitBreaker {
	l.mu.Lock()
	defer l.mu.Unlock()

	if elem, ok := l.items[key]; ok {
		l.evictList.MoveToFront(elem)

		return elem.Value.(*lruEntry).cb
	}

	cb := factory()
	if l.evictList.Len() >= l.capacity {
		oldest := l.evictList.Back()
		if oldest != nil {
			l.evictList.Remove(oldest)
			delete(l.items, oldest.Value.(*lruEntry).key)
		}
	}

	elem := l.evictList.PushFront(&lruEntry{key: key, cb: cb})
	l.items[key] = elem

	return cb
}

// WalletServiceDecorator decorates a driver.WalletService with metrics collection and circuit breaking.
//
// Each wallet returned by OwnerWallet, IssuerWallet, AuditorWallet, and CertifierWallet receives
// an independent, cached CircuitBreaker per wallet ID so that failures in one wallet do not trip
// the breaker for unrelated wallets or wallet types. Per-wallet breakers are managed in a bounded LRU cache.
type WalletServiceDecorator struct {
	base     driver.WalletService
	metrics  *WalletMetrics
	cfg      CircuitBreakerConfig
	sharedCB *CircuitBreaker

	methodBreakers map[string]*CircuitBreaker
	walletLRU      *breakerLRU
}

// NewWalletServiceDecoratorWithConfig constructs a new WalletServiceDecorator wrapping the provided base service
// using the given circuit breaker configuration.
func NewWalletServiceDecoratorWithConfig(base driver.WalletService, provider metrics.Provider, cfg CircuitBreakerConfig) *WalletServiceDecorator {
	if cfg.MaxConsecutiveFailures == 0 {
		cfg.MaxConsecutiveFailures = 5
	}
	if cfg.CooldownTimeout == 0 {
		cfg.CooldownTimeout = 10 * time.Second
	}
	if cfg.MaxWalletBreakers == 0 {
		cfg.MaxWalletBreakers = 1000
	}
	if cfg.IsFailure == nil {
		cfg.IsFailure = defaultIsFailure
	}

	return &WalletServiceDecorator{
		base:    base,
		metrics: NewWalletMetrics(provider),
		cfg:     cfg,
		methodBreakers: map[string]*CircuitBreaker{
			"RegisterRecipientIdentity": NewCircuitBreaker(cfg),
			"RegisterOwnerIdentity":     NewCircuitBreaker(cfg),
			"RegisterIssuerIdentity":    NewCircuitBreaker(cfg),
			"OwnerWalletIDs":            NewCircuitBreaker(cfg),
		},
		walletLRU: newBreakerLRU(cfg.MaxWalletBreakers),
	}
}

// NewWalletServiceDecorator constructs a new WalletServiceDecorator wrapping the provided base service.
// cb is an optional circuit breaker used for WalletService-level operations. If cb is nil,
// independent per-method circuit breakers are created from DefaultCircuitBreakerConfig.
// Each wallet returned by OwnerWallet/IssuerWallet/AuditorWallet/CertifierWallet receives its own
// independent circuit breaker cached per wallet ID in a bounded LRU cache.
func NewWalletServiceDecorator(base driver.WalletService, provider metrics.Provider, cb *CircuitBreaker) *WalletServiceDecorator {
	cfg := DefaultCircuitBreakerConfig()
	d := NewWalletServiceDecoratorWithConfig(base, provider, cfg)
	d.sharedCB = cb

	return d
}

func (d *WalletServiceDecorator) getBreaker(method string) *CircuitBreaker {
	if d.sharedCB != nil {
		return d.sharedCB
	}

	if cb, ok := d.methodBreakers[method]; ok {
		return cb
	}

	return nil
}

func (d *WalletServiceDecorator) getWalletBreaker(walletType string, id string) *CircuitBreaker {
	key := walletType + ":" + id

	return d.walletLRU.getOrSet(key, func() *CircuitBreaker {
		return NewCircuitBreaker(d.cfg)
	})
}

// RegisterRecipientIdentity delegates to the base service with metrics and circuit breaker protection.
func (d *WalletServiceDecorator) RegisterRecipientIdentity(ctx context.Context, data *driver.RecipientData) error {
	const method = "RegisterRecipientIdentity"
	d.metrics.InFlight.With("method", method, "wallet_id", "").Add(1)
	defer d.metrics.InFlight.With("method", method, "wallet_id", "").Add(-1)

	cb := d.getBreaker(method)
	start := timeNow()
	var err error
	if cb != nil {
		err = cb.Execute(func() error {
			return d.base.RegisterRecipientIdentity(ctx, data)
		})
		d.metrics.RecordState(method, "", cb.State())
	} else {
		err = d.base.RegisterRecipientIdentity(ctx, data)
	}
	d.metrics.Observe(method, "", start, err)

	return err
}

// GetAuditInfo delegates to the base service with metrics collection.
//
// Note: GetAuditInfo operates outside the circuit breaker.
// Auditing and cross-party transaction verification routinely query audit info for
// identities that belong to other parties or are not locally registered (resulting in
// benign "not found" misses). Gating GetAuditInfo with a shared circuit breaker would
// allow routine lookup misses to trip the breaker and block valid lookups for known identities.
func (d *WalletServiceDecorator) GetAuditInfo(ctx context.Context, id driver.Identity) ([]byte, error) {
	const method = "GetAuditInfo"
	d.metrics.InFlight.With("method", method, "wallet_id", "").Add(1)
	defer d.metrics.InFlight.With("method", method, "wallet_id", "").Add(-1)

	start := timeNow()
	res, err := d.base.GetAuditInfo(ctx, id)
	d.metrics.Observe(method, "", start, err)

	return res, err
}

// GetEnrollmentID delegates to the base service with metrics collection.
// It operates outside the circuit breaker as it is a CPU-only deserialization operation.
func (d *WalletServiceDecorator) GetEnrollmentID(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, error) {
	const method = "GetEnrollmentID"
	d.metrics.InFlight.With("method", method, "wallet_id", "").Add(1)
	defer d.metrics.InFlight.With("method", method, "wallet_id", "").Add(-1)

	start := timeNow()
	res, err := d.base.GetEnrollmentID(ctx, identity, auditInfo)
	d.metrics.Observe(method, "", start, err)

	return res, err
}

// GetRevocationHandle delegates to the base service with metrics collection.
// It operates outside the circuit breaker as it is a CPU-only deserialization operation.
func (d *WalletServiceDecorator) GetRevocationHandle(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, error) {
	const method = "GetRevocationHandle"
	d.metrics.InFlight.With("method", method, "wallet_id", "").Add(1)
	defer d.metrics.InFlight.With("method", method, "wallet_id", "").Add(-1)

	start := timeNow()
	res, err := d.base.GetRevocationHandle(ctx, identity, auditInfo)
	d.metrics.Observe(method, "", start, err)

	return res, err
}

// GetEIDAndRH delegates to the base service with metrics collection.
// It operates outside the circuit breaker as it is a CPU-only deserialization operation.
func (d *WalletServiceDecorator) GetEIDAndRH(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, string, error) {
	const method = "GetEIDAndRH"
	d.metrics.InFlight.With("method", method, "wallet_id", "").Add(1)
	defer d.metrics.InFlight.With("method", method, "wallet_id", "").Add(-1)

	start := timeNow()
	eid, rh, err := d.base.GetEIDAndRH(ctx, identity, auditInfo)
	d.metrics.Observe(method, "", start, err)

	return eid, rh, err
}

// Wallet delegates to the base service.
func (d *WalletServiceDecorator) Wallet(ctx context.Context, identity driver.Identity) driver.Wallet {
	return d.base.Wallet(ctx, identity)
}

// RegisterOwnerIdentity delegates to the base service with circuit breaker protection.
func (d *WalletServiceDecorator) RegisterOwnerIdentity(ctx context.Context, config driver.IdentityConfiguration) error {
	const method = "RegisterOwnerIdentity"
	d.metrics.InFlight.With("method", method, "wallet_id", "").Add(1)
	defer d.metrics.InFlight.With("method", method, "wallet_id", "").Add(-1)

	cb := d.getBreaker(method)
	start := timeNow()
	var err error
	if cb != nil {
		err = cb.Execute(func() error {
			return d.base.RegisterOwnerIdentity(ctx, config)
		})
		d.metrics.RecordState(method, "", cb.State())
	} else {
		err = d.base.RegisterOwnerIdentity(ctx, config)
	}
	d.metrics.Observe(method, "", start, err)

	return err
}

// RegisterIssuerIdentity delegates to the base service with circuit breaker protection.
func (d *WalletServiceDecorator) RegisterIssuerIdentity(ctx context.Context, config driver.IdentityConfiguration) error {
	const method = "RegisterIssuerIdentity"
	d.metrics.InFlight.With("method", method, "wallet_id", "").Add(1)
	defer d.metrics.InFlight.With("method", method, "wallet_id", "").Add(-1)

	cb := d.getBreaker(method)
	start := timeNow()
	var err error
	if cb != nil {
		err = cb.Execute(func() error {
			return d.base.RegisterIssuerIdentity(ctx, config)
		})
		d.metrics.RecordState(method, "", cb.State())
	} else {
		err = d.base.RegisterIssuerIdentity(ctx, config)
	}
	d.metrics.Observe(method, "", start, err)

	return err
}

// OwnerWalletIDs delegates to the base service with metrics and circuit breaker protection.
func (d *WalletServiceDecorator) OwnerWalletIDs(ctx context.Context) ([]string, error) {
	const method = "OwnerWalletIDs"
	d.metrics.InFlight.With("method", method, "wallet_id", "").Add(1)
	defer d.metrics.InFlight.With("method", method, "wallet_id", "").Add(-1)

	cb := d.getBreaker(method)
	start := timeNow()
	var res []string
	var err error
	if cb != nil {
		err = cb.Execute(func() error {
			var e error
			res, e = d.base.OwnerWalletIDs(ctx)

			return e
		})
		d.metrics.RecordState(method, "", cb.State())
	} else {
		res, err = d.base.OwnerWalletIDs(ctx)
	}
	d.metrics.Observe(method, "", start, err)

	return res, err
}

// OwnerWallet delegates to the base service, wrapping the returned wallet with its own circuit breaker.
// Note: OwnerWallet lookup itself must remain outside the circuit breaker.
// WalletBasedAuthorization.IsMine (and related authorization checks) treats any OwnerWallet error
// as "not mine". Gating wallet lookup behind a breaker would cause IsMine to falsely report not-mine
// on unrelated transient failures, permanently dropping token tracking.
// Instead, the returned wallet is decorated with an independent circuit breaker cached per wallet ID.
func (d *WalletServiceDecorator) OwnerWallet(ctx context.Context, id driver.WalletLookupID) (driver.OwnerWallet, error) {
	const method = "OwnerWallet"
	d.metrics.InFlight.With("method", method, "wallet_id", "").Add(1)
	defer d.metrics.InFlight.With("method", method, "wallet_id", "").Add(-1)

	start := timeNow()
	w, err := d.base.OwnerWallet(ctx, id)
	d.metrics.Observe(method, "", start, err)

	if err != nil || w == nil {
		return nil, err
	}

	cb := d.getWalletBreaker("owner", w.ID())

	return NewOwnerWalletDecorator(w, d.metrics, cb), nil
}

// IssuerWallet delegates to the base service, wrapping the returned wallet with its own circuit breaker.
// Each issuer wallet gets an independent breaker cached per wallet ID so failures are isolated.
func (d *WalletServiceDecorator) IssuerWallet(ctx context.Context, id driver.WalletLookupID) (driver.IssuerWallet, error) {
	const method = "IssuerWallet"
	d.metrics.InFlight.With("method", method, "wallet_id", "").Add(1)
	defer d.metrics.InFlight.With("method", method, "wallet_id", "").Add(-1)

	start := timeNow()
	w, err := d.base.IssuerWallet(ctx, id)
	d.metrics.Observe(method, "", start, err)

	if err != nil || w == nil {
		return nil, err
	}

	cb := d.getWalletBreaker("issuer", w.ID())

	return NewIssuerWalletDecorator(w, d.metrics, cb), nil
}

// AuditorWallet delegates to the base service, wrapping the returned wallet with metrics and
// an independent circuit breaker cached per wallet ID.
func (d *WalletServiceDecorator) AuditorWallet(ctx context.Context, id driver.WalletLookupID) (driver.AuditorWallet, error) {
	const method = "AuditorWallet"
	d.metrics.InFlight.With("method", method, "wallet_id", "").Add(1)
	defer d.metrics.InFlight.With("method", method, "wallet_id", "").Add(-1)

	start := timeNow()
	w, err := d.base.AuditorWallet(ctx, id)
	d.metrics.Observe(method, "", start, err)

	if err != nil || w == nil {
		return nil, err
	}

	cb := d.getWalletBreaker("auditor", w.ID())

	return NewAuditorWalletDecorator(w, d.metrics, cb), nil
}

// CertifierWallet delegates to the base service, wrapping the returned wallet with metrics and
// an independent circuit breaker cached per wallet ID.
func (d *WalletServiceDecorator) CertifierWallet(ctx context.Context, id driver.WalletLookupID) (driver.CertifierWallet, error) {
	const method = "CertifierWallet"
	d.metrics.InFlight.With("method", method, "wallet_id", "").Add(1)
	defer d.metrics.InFlight.With("method", method, "wallet_id", "").Add(-1)

	start := timeNow()
	w, err := d.base.CertifierWallet(ctx, id)
	d.metrics.Observe(method, "", start, err)

	if err != nil || w == nil {
		return nil, err
	}

	cb := d.getWalletBreaker("certifier", w.ID())

	return NewCertifierWalletDecorator(w, d.metrics, cb), nil
}

// SpendIDs delegates to the base service.
func (d *WalletServiceDecorator) SpendIDs(ids ...*token.ID) ([]string, error) {
	return d.base.SpendIDs(ids...)
}

// Done delegates to the base service.
func (d *WalletServiceDecorator) Done() error {
	return d.base.Done()
}

// OwnerWalletDecorator decorates a driver.OwnerWallet with metrics and an independent circuit breaker.
type OwnerWalletDecorator struct {
	base    driver.OwnerWallet
	metrics *WalletMetrics
	cb      *CircuitBreaker
}

// NewOwnerWalletDecorator constructs a new OwnerWalletDecorator.
func NewOwnerWalletDecorator(base driver.OwnerWallet, m *WalletMetrics, cb *CircuitBreaker) *OwnerWalletDecorator {
	return &OwnerWalletDecorator{base: base, metrics: m, cb: cb}
}

// ID returns the wallet ID.
func (w *OwnerWalletDecorator) ID() string { return w.base.ID() }

// Contains delegates to the base wallet.
func (w *OwnerWalletDecorator) Contains(ctx context.Context, identity driver.Identity) bool {
	return w.base.Contains(ctx, identity)
}

// ContainsToken delegates to the base wallet.
func (w *OwnerWalletDecorator) ContainsToken(ctx context.Context, t *token.UnspentToken) bool {
	return w.base.ContainsToken(ctx, t)
}

// GetSigner delegates to the base wallet.
func (w *OwnerWalletDecorator) GetSigner(ctx context.Context, identity driver.Identity) (driver.Signer, error) {
	return w.base.GetSigner(ctx, identity)
}

// GetRecipientIdentity delegates to the base wallet with circuit breaker protection.
func (w *OwnerWalletDecorator) GetRecipientIdentity(ctx context.Context) (driver.Identity, error) {
	const method = "OwnerWallet.GetRecipientIdentity"
	w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(1)
	defer w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(-1)

	start := timeNow()
	var res driver.Identity
	err := w.cb.Execute(func() error {
		var e error
		res, e = w.base.GetRecipientIdentity(ctx)

		return e
	})
	w.metrics.Observe(method, w.ID(), start, err)
	w.metrics.RecordState(method, w.ID(), w.cb.State())

	return res, err
}

// GetRecipientData delegates to the base wallet with circuit breaker protection.
func (w *OwnerWalletDecorator) GetRecipientData(ctx context.Context) (*driver.RecipientData, error) {
	const method = "OwnerWallet.GetRecipientData"
	w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(1)
	defer w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(-1)

	start := timeNow()
	var res *driver.RecipientData
	err := w.cb.Execute(func() error {
		var e error
		res, e = w.base.GetRecipientData(ctx)

		return e
	})
	w.metrics.Observe(method, w.ID(), start, err)
	w.metrics.RecordState(method, w.ID(), w.cb.State())

	return res, err
}

// GetAuditInfo delegates to the base wallet.
func (w *OwnerWalletDecorator) GetAuditInfo(ctx context.Context, id driver.Identity) ([]byte, error) {
	return w.base.GetAuditInfo(ctx, id)
}

// GetTokenMetadata delegates to the base wallet.
func (w *OwnerWalletDecorator) GetTokenMetadata(id driver.Identity) ([]byte, error) {
	return w.base.GetTokenMetadata(id)
}

// GetTokenMetadataAuditInfo delegates to the base wallet.
func (w *OwnerWalletDecorator) GetTokenMetadataAuditInfo(id driver.Identity) ([]byte, error) {
	return w.base.GetTokenMetadataAuditInfo(id)
}

// ListTokens delegates to the base wallet with metrics and circuit breaker protection.
func (w *OwnerWalletDecorator) ListTokens(ctx context.Context, opts *driver.ListTokensOptions) (*token.UnspentTokens, error) {
	const method = "OwnerWallet.ListTokens"
	w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(1)
	defer w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(-1)

	start := timeNow()
	var res *token.UnspentTokens
	err := w.cb.Execute(func() error {
		var e error
		res, e = w.base.ListTokens(ctx, opts)

		return e
	})
	w.metrics.Observe(method, w.ID(), start, err)
	w.metrics.RecordState(method, w.ID(), w.cb.State())

	return res, err
}

// ListTokensIterator delegates to the base wallet.
func (w *OwnerWalletDecorator) ListTokensIterator(ctx context.Context, opts *driver.ListTokensOptions) (driver.UnspentTokensIterator, error) {
	return w.base.ListTokensIterator(ctx, opts)
}

// Balance delegates to the base wallet with metrics and circuit breaker protection.
func (w *OwnerWalletDecorator) Balance(ctx context.Context, opts *driver.ListTokensOptions) (*big.Int, error) {
	const method = "OwnerWallet.Balance"
	w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(1)
	defer w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(-1)

	start := timeNow()
	var res *big.Int
	err := w.cb.Execute(func() error {
		var e error
		res, e = w.base.Balance(ctx, opts)

		return e
	})
	w.metrics.Observe(method, w.ID(), start, err)
	w.metrics.RecordState(method, w.ID(), w.cb.State())

	return res, err
}

// EnrollmentID returns the wallet enrollment ID.
func (w *OwnerWalletDecorator) EnrollmentID() string { return w.base.EnrollmentID() }

// RegisterRecipient delegates to the base wallet with circuit breaker protection.
func (w *OwnerWalletDecorator) RegisterRecipient(ctx context.Context, data *driver.RecipientData) error {
	const method = "OwnerWallet.RegisterRecipient"
	w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(1)
	defer w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(-1)

	start := timeNow()
	err := w.cb.Execute(func() error {
		return w.base.RegisterRecipient(ctx, data)
	})
	w.metrics.Observe(method, w.ID(), start, err)
	w.metrics.RecordState(method, w.ID(), w.cb.State())

	return err
}

// Remote returns whether the wallet is remote.
func (w *OwnerWalletDecorator) Remote() bool { return w.base.Remote() }

// IssuerWalletDecorator decorates a driver.IssuerWallet with metrics and an independent circuit breaker.
type IssuerWalletDecorator struct {
	base    driver.IssuerWallet
	metrics *WalletMetrics
	cb      *CircuitBreaker
}

// NewIssuerWalletDecorator constructs a new IssuerWalletDecorator.
func NewIssuerWalletDecorator(base driver.IssuerWallet, m *WalletMetrics, cb *CircuitBreaker) *IssuerWalletDecorator {
	return &IssuerWalletDecorator{base: base, metrics: m, cb: cb}
}

// ID returns the wallet ID.
func (w *IssuerWalletDecorator) ID() string { return w.base.ID() }

// Contains delegates to the base wallet.
func (w *IssuerWalletDecorator) Contains(ctx context.Context, identity driver.Identity) bool {
	return w.base.Contains(ctx, identity)
}

// ContainsToken delegates to the base wallet.
func (w *IssuerWalletDecorator) ContainsToken(ctx context.Context, t *token.UnspentToken) bool {
	return w.base.ContainsToken(ctx, t)
}

// GetSigner delegates to the base wallet.
func (w *IssuerWalletDecorator) GetSigner(ctx context.Context, identity driver.Identity) (driver.Signer, error) {
	return w.base.GetSigner(ctx, identity)
}

// GetIssuerIdentity delegates to the base wallet with circuit breaker protection.
func (w *IssuerWalletDecorator) GetIssuerIdentity(tokenType token.Type) (driver.Identity, error) {
	const method = "IssuerWallet.GetIssuerIdentity"
	w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(1)
	defer w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(-1)

	start := timeNow()
	var res driver.Identity
	err := w.cb.Execute(func() error {
		var e error
		res, e = w.base.GetIssuerIdentity(tokenType)

		return e
	})
	w.metrics.Observe(method, w.ID(), start, err)
	w.metrics.RecordState(method, w.ID(), w.cb.State())

	return res, err
}

// HistoryTokens delegates to the base wallet with metrics and circuit breaker protection.
func (w *IssuerWalletDecorator) HistoryTokens(ctx context.Context, opts *driver.ListTokensOptions) (*token.IssuedTokens, error) {
	const method = "IssuerWallet.HistoryTokens"
	w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(1)
	defer w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(-1)

	start := timeNow()
	var res *token.IssuedTokens
	err := w.cb.Execute(func() error {
		var e error
		res, e = w.base.HistoryTokens(ctx, opts)

		return e
	})
	w.metrics.Observe(method, w.ID(), start, err)
	w.metrics.RecordState(method, w.ID(), w.cb.State())

	return res, err
}

// IssuedBalance delegates to the base wallet with metrics and circuit breaker protection.
func (w *IssuerWalletDecorator) IssuedBalance(ctx context.Context, opts *driver.IssuerBalanceOptions) (*big.Int, error) {
	const method = "IssuerWallet.IssuedBalance"
	w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(1)
	defer w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(-1)

	start := timeNow()
	var res *big.Int
	err := w.cb.Execute(func() error {
		var e error
		res, e = w.base.IssuedBalance(ctx, opts)

		return e
	})
	w.metrics.Observe(method, w.ID(), start, err)
	w.metrics.RecordState(method, w.ID(), w.cb.State())

	return res, err
}

// RedeemedBalance delegates to the base wallet with metrics and circuit breaker protection.
func (w *IssuerWalletDecorator) RedeemedBalance(ctx context.Context, opts *driver.IssuerBalanceOptions) (*big.Int, error) {
	const method = "IssuerWallet.RedeemedBalance"
	w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(1)
	defer w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(-1)

	start := timeNow()
	var res *big.Int
	err := w.cb.Execute(func() error {
		var e error
		res, e = w.base.RedeemedBalance(ctx, opts)

		return e
	})
	w.metrics.Observe(method, w.ID(), start, err)
	w.metrics.RecordState(method, w.ID(), w.cb.State())

	return res, err
}

// Balance delegates to the base wallet with metrics and circuit breaker protection.
func (w *IssuerWalletDecorator) Balance(ctx context.Context, opts *driver.IssuerBalanceOptions) (*big.Int, error) {
	const method = "IssuerWallet.Balance"
	w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(1)
	defer w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(-1)

	start := timeNow()
	var res *big.Int
	err := w.cb.Execute(func() error {
		var e error
		res, e = w.base.Balance(ctx, opts)

		return e
	})
	w.metrics.Observe(method, w.ID(), start, err)
	w.metrics.RecordState(method, w.ID(), w.cb.State())

	return res, err
}

// AuditorWalletDecorator decorates a driver.AuditorWallet with metrics and an independent circuit breaker.
type AuditorWalletDecorator struct {
	base    driver.AuditorWallet
	metrics *WalletMetrics
	cb      *CircuitBreaker
}

// NewAuditorWalletDecorator constructs a new AuditorWalletDecorator.
func NewAuditorWalletDecorator(base driver.AuditorWallet, m *WalletMetrics, cb *CircuitBreaker) *AuditorWalletDecorator {
	return &AuditorWalletDecorator{base: base, metrics: m, cb: cb}
}

// ID returns the wallet ID.
func (w *AuditorWalletDecorator) ID() string { return w.base.ID() }

// Contains delegates to the base wallet.
func (w *AuditorWalletDecorator) Contains(ctx context.Context, identity driver.Identity) bool {
	return w.base.Contains(ctx, identity)
}

// ContainsToken delegates to the base wallet.
func (w *AuditorWalletDecorator) ContainsToken(ctx context.Context, t *token.UnspentToken) bool {
	return w.base.ContainsToken(ctx, t)
}

// GetSigner delegates to the base wallet.
func (w *AuditorWalletDecorator) GetSigner(ctx context.Context, identity driver.Identity) (driver.Signer, error) {
	return w.base.GetSigner(ctx, identity)
}

// GetAuditorIdentity delegates to the base wallet with circuit breaker protection.
func (w *AuditorWalletDecorator) GetAuditorIdentity() (driver.Identity, error) {
	const method = "AuditorWallet.GetAuditorIdentity"
	w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(1)
	defer w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(-1)

	start := timeNow()
	var res driver.Identity
	err := w.cb.Execute(func() error {
		var e error
		res, e = w.base.GetAuditorIdentity()

		return e
	})
	w.metrics.Observe(method, w.ID(), start, err)
	w.metrics.RecordState(method, w.ID(), w.cb.State())

	return res, err
}

// CertifierWalletDecorator decorates a driver.CertifierWallet with metrics and an independent circuit breaker.
type CertifierWalletDecorator struct {
	base    driver.CertifierWallet
	metrics *WalletMetrics
	cb      *CircuitBreaker
}

// NewCertifierWalletDecorator constructs a new CertifierWalletDecorator.
func NewCertifierWalletDecorator(base driver.CertifierWallet, m *WalletMetrics, cb *CircuitBreaker) *CertifierWalletDecorator {
	return &CertifierWalletDecorator{base: base, metrics: m, cb: cb}
}

// ID returns the wallet ID.
func (w *CertifierWalletDecorator) ID() string { return w.base.ID() }

// Contains delegates to the base wallet.
func (w *CertifierWalletDecorator) Contains(ctx context.Context, identity driver.Identity) bool {
	return w.base.Contains(ctx, identity)
}

// ContainsToken delegates to the base wallet.
func (w *CertifierWalletDecorator) ContainsToken(ctx context.Context, t *token.UnspentToken) bool {
	return w.base.ContainsToken(ctx, t)
}

// GetSigner delegates to the base wallet.
func (w *CertifierWalletDecorator) GetSigner(ctx context.Context, identity driver.Identity) (driver.Signer, error) {
	return w.base.GetSigner(ctx, identity)
}

// GetCertifierIdentity delegates to the base wallet with circuit breaker protection.
func (w *CertifierWalletDecorator) GetCertifierIdentity() (driver.Identity, error) {
	const method = "CertifierWallet.GetCertifierIdentity"
	w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(1)
	defer w.metrics.InFlight.With("method", method, "wallet_id", w.ID()).Add(-1)

	start := timeNow()
	var res driver.Identity
	err := w.cb.Execute(func() error {
		var e error
		res, e = w.base.GetCertifierIdentity()

		return e
	})
	w.metrics.Observe(method, w.ID(), start, err)
	w.metrics.RecordState(method, w.ID(), w.cb.State())

	return res, err
}
