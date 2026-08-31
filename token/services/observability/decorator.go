/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package observability

import (
	"context"
	"math/big"
	"time"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
)

// WalletServiceDecorator decorates a driver.WalletService with metrics collection and circuit breaking.
//
// Each wallet returned by OwnerWallet, IssuerWallet, AuditorWallet, and CertifierWallet gets
// its own independent CircuitBreaker so that failures in one wallet do not trip the breaker
// for unrelated wallets or wallet types.
type WalletServiceDecorator struct {
	base    driver.WalletService
	metrics *WalletMetrics
	cb      *CircuitBreaker
	cfg     CircuitBreakerConfig
}

// NewWalletServiceDecorator constructs a new WalletServiceDecorator wrapping the provided base service.
// cb is the circuit breaker used for WalletService-level operations (identity registration, etc.).
// Each wallet returned by OwnerWallet/IssuerWallet/AuditorWallet/CertifierWallet receives its own
// independent circuit breaker created from cfg, so wallet-level failures are isolated.
// If cb is nil, a new breaker with DefaultCircuitBreakerConfig is created for service-level calls.
func NewWalletServiceDecorator(base driver.WalletService, provider metrics.Provider, cb *CircuitBreaker) *WalletServiceDecorator {
	cfg := DefaultCircuitBreakerConfig()

	if cb == nil {
		cb = NewCircuitBreaker(cfg)
	}

	return &WalletServiceDecorator{
		base:    base,
		metrics: NewWalletMetrics(provider),
		cb:      cb,
		cfg:     cfg,
	}
}

// RegisterRecipientIdentity delegates to the base service with metrics and circuit breaker protection.
func (d *WalletServiceDecorator) RegisterRecipientIdentity(ctx context.Context, data *driver.RecipientData) error {
	const method = "RegisterRecipientIdentity"
	d.metrics.InFlight.With("method", method).Add(1)
	defer d.metrics.InFlight.With("method", method).Add(-1)

	return d.cb.Execute(func() error {
		start := timeNow()
		err := d.base.RegisterRecipientIdentity(ctx, data)
		d.metrics.Observe(method, start, err)

		return err
	})
}

// GetAuditInfo delegates to the base service with metrics and circuit breaker protection.
func (d *WalletServiceDecorator) GetAuditInfo(ctx context.Context, id driver.Identity) ([]byte, error) {
	const method = "GetAuditInfo"
	d.metrics.InFlight.With("method", method).Add(1)
	defer d.metrics.InFlight.With("method", method).Add(-1)

	var res []byte
	err := d.cb.Execute(func() error {
		start := timeNow()
		var e error
		res, e = d.base.GetAuditInfo(ctx, id)
		d.metrics.Observe(method, start, e)

		return e
	})

	return res, err
}

// GetEnrollmentID delegates to the base service with metrics and circuit breaker protection.
func (d *WalletServiceDecorator) GetEnrollmentID(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, error) {
	const method = "GetEnrollmentID"
	var res string
	err := d.cb.Execute(func() error {
		start := timeNow()
		var e error
		res, e = d.base.GetEnrollmentID(ctx, identity, auditInfo)
		d.metrics.Observe(method, start, e)

		return e
	})

	return res, err
}

// GetRevocationHandle delegates to the base service with metrics and circuit breaker protection.
func (d *WalletServiceDecorator) GetRevocationHandle(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, error) {
	const method = "GetRevocationHandle"
	var res string
	err := d.cb.Execute(func() error {
		start := timeNow()
		var e error
		res, e = d.base.GetRevocationHandle(ctx, identity, auditInfo)
		d.metrics.Observe(method, start, e)

		return e
	})

	return res, err
}

// GetEIDAndRH delegates to the base service with metrics and circuit breaker protection.
func (d *WalletServiceDecorator) GetEIDAndRH(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, string, error) {
	const method = "GetEIDAndRH"
	var eid, rh string
	err := d.cb.Execute(func() error {
		start := timeNow()
		var e error
		eid, rh, e = d.base.GetEIDAndRH(ctx, identity, auditInfo)
		d.metrics.Observe(method, start, e)

		return e
	})

	return eid, rh, err
}

// Wallet delegates to the base service.
func (d *WalletServiceDecorator) Wallet(ctx context.Context, identity driver.Identity) driver.Wallet {
	return d.base.Wallet(ctx, identity)
}

// RegisterOwnerIdentity delegates to the base service with circuit breaker protection.
func (d *WalletServiceDecorator) RegisterOwnerIdentity(ctx context.Context, config driver.IdentityConfiguration) error {
	const method = "RegisterOwnerIdentity"

	return d.cb.Execute(func() error {
		start := timeNow()
		err := d.base.RegisterOwnerIdentity(ctx, config)
		d.metrics.Observe(method, start, err)

		return err
	})
}

// RegisterIssuerIdentity delegates to the base service with circuit breaker protection.
func (d *WalletServiceDecorator) RegisterIssuerIdentity(ctx context.Context, config driver.IdentityConfiguration) error {
	const method = "RegisterIssuerIdentity"

	return d.cb.Execute(func() error {
		start := timeNow()
		err := d.base.RegisterIssuerIdentity(ctx, config)
		d.metrics.Observe(method, start, err)

		return err
	})
}

// OwnerWalletIDs delegates to the base service with metrics and circuit breaker protection.
func (d *WalletServiceDecorator) OwnerWalletIDs(ctx context.Context) ([]string, error) {
	const method = "OwnerWalletIDs"
	var res []string
	err := d.cb.Execute(func() error {
		start := timeNow()
		var e error
		res, e = d.base.OwnerWalletIDs(ctx)
		d.metrics.Observe(method, start, e)

		return e
	})

	return res, err
}

// OwnerWallet delegates to the base service, wrapping the returned wallet with its own circuit breaker.
// Each owner wallet gets an independent breaker so that failures in one wallet do not affect others.
func (d *WalletServiceDecorator) OwnerWallet(ctx context.Context, id driver.WalletLookupID) (driver.OwnerWallet, error) {
	const method = "OwnerWallet"
	start := timeNow()
	w, err := d.base.OwnerWallet(ctx, id)
	d.metrics.Observe(method, start, err)

	if err != nil || w == nil {
		return nil, err
	}

	return NewOwnerWalletDecorator(w, d.metrics, NewCircuitBreaker(d.cfg)), nil
}

// IssuerWallet delegates to the base service, wrapping the returned wallet with its own circuit breaker.
// Each issuer wallet gets an independent breaker so that failures in one wallet do not affect others.
func (d *WalletServiceDecorator) IssuerWallet(ctx context.Context, id driver.WalletLookupID) (driver.IssuerWallet, error) {
	const method = "IssuerWallet"
	start := timeNow()
	w, err := d.base.IssuerWallet(ctx, id)
	d.metrics.Observe(method, start, err)

	if err != nil || w == nil {
		return nil, err
	}

	return NewIssuerWalletDecorator(w, d.metrics, NewCircuitBreaker(d.cfg)), nil
}

// AuditorWallet delegates to the base service, wrapping the returned wallet with metrics and
// its own independent circuit breaker. Previously this bypassed the decorator — now it is
// consistent with OwnerWallet and IssuerWallet.
func (d *WalletServiceDecorator) AuditorWallet(ctx context.Context, id driver.WalletLookupID) (driver.AuditorWallet, error) {
	const method = "AuditorWallet"
	start := timeNow()
	w, err := d.base.AuditorWallet(ctx, id)
	d.metrics.Observe(method, start, err)

	if err != nil || w == nil {
		return nil, err
	}

	return NewAuditorWalletDecorator(w, d.metrics, NewCircuitBreaker(d.cfg)), nil
}

// CertifierWallet delegates to the base service, wrapping the returned wallet with metrics and
// its own independent circuit breaker. Previously this bypassed the decorator — now it is
// consistent with the other wallet types.
func (d *WalletServiceDecorator) CertifierWallet(ctx context.Context, id driver.WalletLookupID) (driver.CertifierWallet, error) {
	const method = "CertifierWallet"
	start := timeNow()
	w, err := d.base.CertifierWallet(ctx, id)
	d.metrics.Observe(method, start, err)

	if err != nil || w == nil {
		return nil, err
	}

	return NewCertifierWalletDecorator(w, d.metrics, NewCircuitBreaker(d.cfg)), nil
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
	var res driver.Identity
	err := w.cb.Execute(func() error {
		start := timeNow()
		var e error
		res, e = w.base.GetRecipientIdentity(ctx)
		w.metrics.Observe(method, start, e)

		return e
	})

	return res, err
}

// GetRecipientData delegates to the base wallet with circuit breaker protection.
func (w *OwnerWalletDecorator) GetRecipientData(ctx context.Context) (*driver.RecipientData, error) {
	const method = "OwnerWallet.GetRecipientData"
	var res *driver.RecipientData
	err := w.cb.Execute(func() error {
		start := timeNow()
		var e error
		res, e = w.base.GetRecipientData(ctx)
		w.metrics.Observe(method, start, e)

		return e
	})

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
	var res *token.UnspentTokens
	err := w.cb.Execute(func() error {
		start := timeNow()
		var e error
		res, e = w.base.ListTokens(ctx, opts)
		w.metrics.Observe(method, start, e)

		return e
	})

	return res, err
}

// ListTokensIterator delegates to the base wallet.
func (w *OwnerWalletDecorator) ListTokensIterator(ctx context.Context, opts *driver.ListTokensOptions) (driver.UnspentTokensIterator, error) {
	return w.base.ListTokensIterator(ctx, opts)
}

// Balance delegates to the base wallet with metrics and circuit breaker protection.
func (w *OwnerWalletDecorator) Balance(ctx context.Context, opts *driver.ListTokensOptions) (*big.Int, error) {
	const method = "OwnerWallet.Balance"
	var res *big.Int
	err := w.cb.Execute(func() error {
		start := timeNow()
		var e error
		res, e = w.base.Balance(ctx, opts)
		w.metrics.Observe(method, start, e)

		return e
	})

	return res, err
}

// EnrollmentID returns the wallet enrollment ID.
func (w *OwnerWalletDecorator) EnrollmentID() string { return w.base.EnrollmentID() }

// RegisterRecipient delegates to the base wallet with circuit breaker protection.
func (w *OwnerWalletDecorator) RegisterRecipient(ctx context.Context, data *driver.RecipientData) error {
	const method = "OwnerWallet.RegisterRecipient"

	return w.cb.Execute(func() error {
		start := timeNow()
		err := w.base.RegisterRecipient(ctx, data)
		w.metrics.Observe(method, start, err)

		return err
	})
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
	var res driver.Identity
	err := w.cb.Execute(func() error {
		start := timeNow()
		var e error
		res, e = w.base.GetIssuerIdentity(tokenType)
		w.metrics.Observe(method, start, e)

		return e
	})

	return res, err
}

// HistoryTokens delegates to the base wallet with metrics and circuit breaker protection.
func (w *IssuerWalletDecorator) HistoryTokens(ctx context.Context, opts *driver.ListTokensOptions) (*token.IssuedTokens, error) {
	const method = "IssuerWallet.HistoryTokens"
	var res *token.IssuedTokens
	err := w.cb.Execute(func() error {
		start := timeNow()
		var e error
		res, e = w.base.HistoryTokens(ctx, opts)
		w.metrics.Observe(method, start, e)

		return e
	})

	return res, err
}

// IssuedBalance delegates to the base wallet with metrics and circuit breaker protection.
func (w *IssuerWalletDecorator) IssuedBalance(ctx context.Context, opts *driver.IssuerBalanceOptions) (*big.Int, error) {
	const method = "IssuerWallet.IssuedBalance"
	var res *big.Int
	err := w.cb.Execute(func() error {
		start := timeNow()
		var e error
		res, e = w.base.IssuedBalance(ctx, opts)
		w.metrics.Observe(method, start, e)

		return e
	})

	return res, err
}

// RedeemedBalance delegates to the base wallet with metrics and circuit breaker protection.
func (w *IssuerWalletDecorator) RedeemedBalance(ctx context.Context, opts *driver.IssuerBalanceOptions) (*big.Int, error) {
	const method = "IssuerWallet.RedeemedBalance"
	var res *big.Int
	err := w.cb.Execute(func() error {
		start := timeNow()
		var e error
		res, e = w.base.RedeemedBalance(ctx, opts)
		w.metrics.Observe(method, start, e)

		return e
	})

	return res, err
}

// Balance delegates to the base wallet with metrics and circuit breaker protection.
func (w *IssuerWalletDecorator) Balance(ctx context.Context, opts *driver.IssuerBalanceOptions) (*big.Int, error) {
	const method = "IssuerWallet.Balance"
	var res *big.Int
	err := w.cb.Execute(func() error {
		start := timeNow()
		var e error
		res, e = w.base.Balance(ctx, opts)
		w.metrics.Observe(method, start, e)

		return e
	})

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
	var res driver.Identity
	err := w.cb.Execute(func() error {
		start := timeNow()
		var e error
		res, e = w.base.GetAuditorIdentity()
		w.metrics.Observe(method, start, e)

		return e
	})

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
	var res driver.Identity
	err := w.cb.Execute(func() error {
		start := timeNow()
		var e error
		res, e = w.base.GetCertifierIdentity()
		w.metrics.Observe(method, start, e)

		return e
	})

	return res, err
}

// timeNow is a package-internal clock hook, replaceable in tests.
func timeNow() time.Time {
	return time.Now()
}
