/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role

import (
	"bytes"
	"context"
	"math/big"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/wallet"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
)

// UnspentTokensIterator defines an iterator over unspent tokens
//
//go:generate counterfeiter -o mock/uti.go -fake-name UnspentTokensIterator . UnspentTokensIterator
type UnspentTokensIterator = driver.UnspentTokensIterator

// OwnerTokenVault provides the minimal token-vault operations needed by owner
// wallets: the ability to iterate unspent tokens for a given owner (by id)
// and to query the balance for a specific token type. Implementations are
// expected to return an UnspentTokensIterator that can be consumed by the
// wallet logic.
//
//go:generate counterfeiter -o mock/otv.go -fake-name OwnerTokenVault . OwnerTokenVault
type OwnerTokenVault interface {
	UnspentTokensIteratorBy(ctx context.Context, id string, tokenType token.Type) (UnspentTokensIterator, error)
	Balance(ctx context.Context, id string, tokenType token.Type) (*big.Int, error)
}

// IdentityProvider is a type alias for IdentityProvider. It exposes
// identity-related operations that wallets need (signers, verifiers, audit
// info, registry operations, etc.). Using a type alias keeps the code shorter
// while preserving the underlying interface contract.
//
//go:generate counterfeiter -o mock/ip.go -fake-name IdentityProvider . IdentityProvider
type IdentityProvider = driver.IdentityProvider

// Signer is an interface which wraps the Sign method.
//
//go:generate counterfeiter -o mock/signer.go -fake-name Signer . Signer
type Signer = driver.Signer

// Identity represents a generic identity.
// It is modeled as a byte slice.
type Identity = driver.Identity

// AuditorWallet represents a wallet that holds a single auditor identity.
// Auditor wallets are used to perform audit-related operations and to
// validate whether a given identity belongs to the auditor.
type AuditorWallet struct {
	WalletID string

	Identity Identity
	Signer   Signer
}

// NewAuditorWallet creates a new AuditorWallet bound to a single auditor
// identity. The wallet uses the provided IdentityProvider to obtain signers
// and audit information for the auditor identity.
func NewAuditorWallet(walletID string, identity Identity, signer Signer) *AuditorWallet {
	return &AuditorWallet{
		WalletID: walletID,
		Identity: identity,
		Signer:   signer,
	}
}

// ID returns the wallet identifier.
func (w *AuditorWallet) ID() string {
	return w.WalletID
}

// Contains reports whether the given identity belongs to this auditor
// wallet. The context is accepted for API consistency and future use.
func (w *AuditorWallet) Contains(ctx context.Context, identity Identity) bool {
	return w.Identity.Equal(identity)
}

// ContainsToken returns true if the supplied unspent token is owned by the
// auditor identity associated with this wallet.
func (w *AuditorWallet) ContainsToken(ctx context.Context, token *token.UnspentToken) bool {
	return w.Contains(ctx, token.Owner)
}

// GetAuditorIdentity returns the underlying auditor identity for this
// wallet. It never fails in the current implementation.
func (w *AuditorWallet) GetAuditorIdentity() (Identity, error) {
	return w.Identity, nil
}

// GetSigner returns a signer for the given identity if the identity belongs
// to this wallet; otherwise it returns an error.
func (w *AuditorWallet) GetSigner(ctx context.Context, identity Identity) (Signer, error) {
	if !w.Contains(ctx, identity) {
		return nil, errors.Errorf("identity [%s] does not belong to this wallet [%s]", identity, w.ID())
	}

	return w.Signer, nil
}

// IssuerTokenVault provides a minimal view of a vault used by issuer
// wallets: the ability to list history of issued tokens. This is different
// from the owner vault in that it exposes issued token history instead of
// unspent token queries.
//
//go:generate counterfeiter -o mock/itv.go -fake-name IssuerTokenVault . IssuerTokenVault
type IssuerTokenVault interface {
	ListHistoryIssuedTokens(context.Context) (*token.IssuedTokens, error)
	// IssuedBalance returns the sum of the amounts of the tokens issued by this node, filtered by the passed options.
	IssuedBalance(ctx context.Context, opts driver.IssuerBalanceQuery) (*big.Int, error)
	// RedeemedBalance returns the sum of the amounts of the tokens redeemed against an issuer known to this node.
	RedeemedBalance(ctx context.Context, opts driver.IssuerBalanceQuery) (*big.Int, error)
}

// IssuerWallet represents a wallet that manages a single issuer identity.
// Issuer wallets can enumerate tokens that were issued by the wallet's
// identity and obtain signers to sign issuance transactions.
type IssuerWallet struct {
	Logger     logging.Logger
	TokenVault IssuerTokenVault
	WalletID   string
	Identity   Identity
	Signer     Signer
}

// NewIssuerWallet constructs an IssuerWallet bound to the given issuer
// identity and backed by the provided token vault and identity provider.
func NewIssuerWallet(
	logger logging.Logger,
	tokenVault IssuerTokenVault,
	id string,
	identity Identity,
	signer Signer,
) *IssuerWallet {
	return &IssuerWallet{
		Logger:     logger,
		TokenVault: tokenVault,
		WalletID:   id,
		Identity:   identity,
		Signer:     signer,
	}
}

// ID returns the issuer wallet identifier.
func (w *IssuerWallet) ID() string {
	return w.WalletID
}

// Contains reports whether the given identity belongs to this issuer
// wallet. The context parameter is provided for API compatibility.
func (w *IssuerWallet) Contains(_ context.Context, identity Identity) bool {
	return w.Identity.Equal(identity)
}

// ContainsToken returns true if the provided token is issued to an identity
// managed by this issuer wallet.
func (w *IssuerWallet) ContainsToken(ctx context.Context, token *token.UnspentToken) bool {
	return w.Contains(ctx, token.Owner)
}

// GetIssuerIdentity returns the issuer identity for the requested token
// type. In this simple wallet the issuer identity does not vary by token
// type and is returned directly.
func (w *IssuerWallet) GetIssuerIdentity(tokenType token.Type) (Identity, error) {
	return w.Identity, nil
}

// GetSigner returns a signer for the given identity if it belongs to this
// issuer wallet; otherwise an error is returned.
func (w *IssuerWallet) GetSigner(ctx context.Context, identity Identity) (Signer, error) {
	if !w.Contains(ctx, identity) {
		return nil, errors.Errorf("failed getting signer, the passed identity [%s] does not belong to this wallet [%s]", identity, w.ID())
	}

	return w.Signer, nil
}

// HistoryTokens returns the history of tokens issued by this wallet that
// match the provided listing options. It filters by token type and verifies
// that tokens belong to this wallet's issuer identity.
func (w *IssuerWallet) HistoryTokens(ctx context.Context, opts *driver.ListTokensOptions) (*token.IssuedTokens, error) {
	w.Logger.DebugfContext(ctx, "issuer wallet [%s]: history tokens, type [%d]", w.ID(), opts.TokenType)
	source, err := w.TokenVault.ListHistoryIssuedTokens(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "token selection failed")
	}

	unspentTokens := &token.IssuedTokens{}
	for _, t := range source.Tokens {
		if len(opts.TokenType) != 0 && t.Type != opts.TokenType {
			w.Logger.DebugfContext(ctx, "issuer wallet [%s]: discarding token of type [%s]!=[%s]", w.ID(), t.Type, opts.TokenType)

			continue
		}

		if !w.Contains(ctx, t.Issuer) {
			w.Logger.DebugfContext(ctx, "issuer wallet [%s]: discarding token, issuer does not belong to wallet", w.ID())

			continue
		}

		w.Logger.DebugfContext(ctx, "issuer wallet [%s]: adding token of type [%s], quantity [%s]", w.ID(), t.Type, t.Quantity)
		unspentTokens.Tokens = append(unspentTokens.Tokens, t)
	}
	w.Logger.DebugfContext(ctx, "issuer wallet [%s]: history tokens done, found [%d] issued tokens", w.ID(), len(unspentTokens.Tokens))

	return unspentTokens, nil
}

// IssuedBalance returns the sum of the quantities of the tokens issued by this wallet,
// filtered by the passed options. The result is returned as a *big.Int to support arbitrary
// precision and prevent overflow.
func (w *IssuerWallet) IssuedBalance(ctx context.Context, opts *driver.IssuerBalanceOptions) (*big.Int, error) {
	balance, err := w.TokenVault.IssuedBalance(ctx, issuerBalanceQuery(opts))
	if err != nil {
		return nil, errors.Wrap(err, "failed to compute issued balance")
	}

	return balance, nil
}

// RedeemedBalance returns the sum of the quantities of the tokens redeemed against this
// issuer, filtered by the passed options. A redeemed token is an empty-owner output in a
// transfer action signed by the issuer. The result is returned as a *big.Int to support
// arbitrary precision and prevent overflow.
func (w *IssuerWallet) RedeemedBalance(ctx context.Context, opts *driver.IssuerBalanceOptions) (*big.Int, error) {
	balance, err := w.TokenVault.RedeemedBalance(ctx, issuerBalanceQuery(opts))
	if err != nil {
		return nil, errors.Wrap(err, "failed to compute redeemed balance")
	}

	return balance, nil
}

// Balance returns the net issued supply of this wallet: IssuedBalance minus RedeemedBalance,
// filtered by the passed options. The result is returned as a *big.Int to support arbitrary
// precision and prevent overflow.
func (w *IssuerWallet) Balance(ctx context.Context, opts *driver.IssuerBalanceOptions) (*big.Int, error) {
	issued, err := w.IssuedBalance(ctx, opts)
	if err != nil {
		return nil, err
	}
	redeemed, err := w.RedeemedBalance(ctx, opts)
	if err != nil {
		return nil, err
	}

	return new(big.Int).Sub(issued, redeemed), nil
}

// issuerBalanceQuery converts the public IssuerBalanceOptions into the driver-level
// IssuerBalanceQuery used by the token vault. A nil opts yields an empty query (all tokens).
func issuerBalanceQuery(opts *driver.IssuerBalanceOptions) driver.IssuerBalanceQuery {
	if opts == nil {
		return driver.IssuerBalanceQuery{}
	}

	return driver.IssuerBalanceQuery{
		TokenType: opts.TokenType,
		From:      opts.From,
		To:        opts.To,
	}
}

// CertifierWallet represents a wallet bounded to a single certifier
// identity. It provides access to a signer and exposes whether a given
// identity is the certifier for this wallet.
type CertifierWallet struct {
	WalletID string
	Identity Identity
	Signer   Signer
}

// NewCertifierWallet creates a new CertifierWallet for the provided
// certifier identity.
func NewCertifierWallet(walletID string, identity Identity, signer Signer) *CertifierWallet {
	return &CertifierWallet{
		WalletID: walletID,
		Identity: identity,
		Signer:   signer,
	}
}

// ID returns the certifier wallet identifier.
func (w *CertifierWallet) ID() string {
	return w.WalletID
}

// Contains reports whether the given identity belongs to the certifier
// identity for this wallet.
func (w *CertifierWallet) Contains(ctx context.Context, identity Identity) bool {
	return w.Identity.Equal(identity)
}

// ContainsToken returns true if the token is owned by the certifier
// identity associated with this wallet.
func (w *CertifierWallet) ContainsToken(ctx context.Context, token *token.UnspentToken) bool {
	return w.Contains(ctx, token.Owner)
}

// GetCertifierIdentity returns the certifier identity this wallet manages.
func (w *CertifierWallet) GetCertifierIdentity() (Identity, error) {
	return w.Identity, nil
}

// GetSigner returns a signer for the certifier identity if it belongs to
// this wallet. Returns an error otherwise.
func (w *CertifierWallet) GetSigner(ctx context.Context, identity Identity) (Signer, error) {
	if !w.Contains(ctx, identity) {
		return nil, errors.Errorf("identity does not belong to this AnonymousOwnerWallet [%s]", identity)
	}

	return w.Signer, nil
}

// LongTermOwnerWallet is the representation of a wallet that holds a
// long-term owner identity (potentially a real, non-pseudonymous identity).
// It provides methods to list tokens, check balances, and obtain recipient
// data and signers. The wallet binds to an identity.Info which can produce
// pseudonyms or other identities when required.
type LongTermOwnerWallet struct {
	IdentityProvider  IdentityProvider
	TokenVault        OwnerTokenVault
	WalletID          idriver.WalletID
	OwnerIdentityInfo idriver.IdentityInfo
	OwnerIdentity     Identity
	OwnerAuditInfo    []byte
}

// NewLongTermOwnerWallet constructs a LongTermOwnerWallet by resolving the
// provided identity.Info into an actual Identity and its audit info.
func NewLongTermOwnerWallet(
	ctx context.Context,
	IdentityProvider IdentityProvider,
	TokenVault OwnerTokenVault,
	id idriver.WalletID,
	identityInfo idriver.IdentityInfo,
) (*LongTermOwnerWallet, error) {
	identity, auditInfo, err := identityInfo.Get(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get identity info")
	}

	return &LongTermOwnerWallet{
		IdentityProvider:  IdentityProvider,
		TokenVault:        TokenVault,
		WalletID:          id,
		OwnerIdentityInfo: identityInfo,
		OwnerIdentity:     identity,
		OwnerAuditInfo:    auditInfo,
	}, nil
}

// ID returns the wallet identifier for the long-term owner wallet.
func (w *LongTermOwnerWallet) ID() string {
	return w.WalletID
}

// Contains reports whether the provided identity is the long-term owner
// identity bound to this wallet.
func (w *LongTermOwnerWallet) Contains(ctx context.Context, identity Identity) bool {
	return w.OwnerIdentity.Equal(identity)
}

// ContainsToken returns true if the token is owned by the long-term owner
// identity managed by this wallet.
func (w *LongTermOwnerWallet) ContainsToken(ctx context.Context, token *token.UnspentToken) bool {
	return w.Contains(ctx, token.Owner)
}

// GetRecipientIdentity returns the owner identity used as a recipient in
// transfer operations.
func (w *LongTermOwnerWallet) GetRecipientIdentity(context.Context) (Identity, error) {
	return w.OwnerIdentity, nil
}

// GetRecipientData returns the recipient data (identity + audit info)
// associated with the long-term owner identity.
func (w *LongTermOwnerWallet) GetRecipientData(context.Context) (*driver.RecipientData, error) {
	return &driver.RecipientData{
		Identity:  w.OwnerIdentity,
		AuditInfo: w.OwnerAuditInfo,
	}, nil
}

// GetAuditInfo delegates to the identity provider to fetch audit info for
// the supplied identity.
func (w *LongTermOwnerWallet) GetAuditInfo(ctx context.Context, id Identity) ([]byte, error) {
	return w.IdentityProvider.GetAuditInfo(ctx, id)
}

// GetTokenMetadata returns associated token metadata for the given owner
// identity. Not implemented in the current wallet and returns nil.
func (w *LongTermOwnerWallet) GetTokenMetadata(id Identity) ([]byte, error) {
	return nil, nil
}

// GetTokenMetadataAuditInfo returns audit info for token metadata. Not
// implemented in the current wallet and returns nil.
func (w *LongTermOwnerWallet) GetTokenMetadataAuditInfo(id Identity) ([]byte, error) {
	return nil, nil
}

// GetSigner returns a signer for the provided identity if it belongs to
// this long-term owner wallet; otherwise an error is returned.
func (w *LongTermOwnerWallet) GetSigner(ctx context.Context, identity Identity) (Signer, error) {
	if !w.Contains(ctx, identity) {
		return nil, errors.Errorf("identity [%s] does not belong to this wallet [%s]", identity, w.ID())
	}

	return w.IdentityProvider.GetSigner(ctx, identity)
}

// ListTokens returns all unspent tokens for the wallet matching the
// provided listing options.
func (w *LongTermOwnerWallet) ListTokens(ctx context.Context, opts *driver.ListTokensOptions) (*token.UnspentTokens, error) {
	it, err := w.TokenVault.UnspentTokensIteratorBy(ctx, w.WalletID, opts.TokenType)
	if err != nil {
		return nil, errors.Wrap(err, "token selection failed")
	}
	tokens, err := iterators.ReadAllPointers(it)
	if err != nil {
		return nil, err
	}

	return &token.UnspentTokens{Tokens: tokens}, nil
}

// Balance returns the on-chain balance for the wallet for the given token
// type in the listing options. The result is returned as a *big.Int to support
// arbitrary precision and prevent overflow.
func (w *LongTermOwnerWallet) Balance(ctx context.Context, opts *driver.ListTokensOptions) (*big.Int, error) {
	balance, err := w.TokenVault.Balance(ctx, w.WalletID, opts.TokenType)
	if err != nil {
		return nil, errors.Wrap(err, "token selection failed")
	}

	return balance, nil
}

// ListTokensIterator returns an iterator to scan unspent tokens instead of
// materializing them in memory.
func (w *LongTermOwnerWallet) ListTokensIterator(ctx context.Context, opts *driver.ListTokensOptions) (driver.UnspentTokensIterator, error) {
	it, err := w.TokenVault.UnspentTokensIteratorBy(ctx, w.WalletID, opts.TokenType)
	if err != nil {
		return nil, errors.Wrap(err, "token selection failed")
	}

	return it, nil
}

// EnrollmentID returns the enrollment id associated with the underlying
// identity info.
func (w *LongTermOwnerWallet) EnrollmentID() string {
	return w.OwnerIdentityInfo.EnrollmentID()
}

// RegisterRecipient validates that the passed recipient data (identity +
// audit info) matches the long-term identity this wallet is bound to.
// Unlike AnonymousOwnerWallet, no registration/binding is performed here
// because the wallet's identity is already resolved and bound at
// construction time (see NewLongTermOwnerWallet); this is purely a guard
// against mismatched recipient data.
func (w *LongTermOwnerWallet) RegisterRecipient(ctx context.Context, data *driver.RecipientData) error {
	if data == nil {
		return errors.Wrapf(wallet.ErrNilRecipientData, "invalid recipient data")
	}
	if !w.Contains(ctx, data.Identity) {
		return errors.Errorf("identity [%s] does not belong to this wallet [%s]", data.Identity, w.ID())
	}
	if !bytes.Equal(data.AuditInfo, w.OwnerAuditInfo) {
		return errors.Errorf("audit info does not match the one this wallet [%s] is bound to", w.ID())
	}

	return nil
}

// Remote reports whether the underlying identity info is remote.
func (w *LongTermOwnerWallet) Remote() bool {
	return w.OwnerIdentityInfo.Remote()
}

// AnonymousOwnerWallet represents an owner wallet that uses ephemeral
// pseudonyms for privacy. It embeds a LongTermOwnerWallet for shared
// functionality and adds a cache and registry interactions to produce and
// manage anonymous recipient identities.
type AnonymousOwnerWallet struct {
	*LongTermOwnerWallet
	Logger          logging.Logger
	Deserializer    Deserializer
	IdentitySupport IdentitySupport
	IdentityCache   *RecipientDataCache
}

// NewAnonymousOwnerWallet creates an AnonymousOwnerWallet. It initializes
// a RecipientDataCache (used to cache pseudonyms/recipient data) and stores
// references to the identity provider, vault, deserializer and wallet
// registry used to validate and register pseudonyms.
func NewAnonymousOwnerWallet(
	logger logging.Logger,
	IdentityProvider IdentityProvider,
	TokenVault OwnerTokenVault,
	Deserializer Deserializer,
	identitySupport IdentitySupport,
	id idriver.WalletID,
	identityInfo idriver.IdentityInfo,
	cacheSize int,
	metricsProvider metrics.Provider,
) (*AnonymousOwnerWallet, error) {
	w := &AnonymousOwnerWallet{
		LongTermOwnerWallet: &LongTermOwnerWallet{
			IdentityProvider:  IdentityProvider,
			TokenVault:        TokenVault,
			WalletID:          id,
			OwnerIdentityInfo: identityInfo,
		},
		Logger:          logger,
		IdentitySupport: identitySupport,
		Deserializer:    Deserializer,
	}
	w.IdentityCache = NewRecipientDataCache(logger, w.getRecipientIdentity, cacheSize, NewMetrics(metricsProvider))
	logger.Debugf("added wallet cache for id %s with cache of size %d", id+"@"+identityInfo.EnrollmentID(), cacheSize)

	return w, nil
}

// Contains reports whether the provided identity is bound to this anonymous
// owner wallet according to the wallet registry.
func (w *AnonymousOwnerWallet) Contains(ctx context.Context, identity Identity) bool {
	return w.IdentitySupport.ContainsIdentity(ctx, identity, w.WalletID)
}

// ContainsToken returns true if the token is owned by an identity bound to
// this anonymous owner wallet.
func (w *AnonymousOwnerWallet) ContainsToken(ctx context.Context, token *token.UnspentToken) bool {
	return w.Contains(ctx, token.Owner)
}

// GetRecipientIdentity returns the current recipient identity (pseudonym)
// from the cache, creating a new one if necessary.
func (w *AnonymousOwnerWallet) GetRecipientIdentity(ctx context.Context) (Identity, error) {
	rd, err := w.IdentityCache.RecipientData(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get recipient data")
	}

	return rd.Identity, nil
}

// GetRecipientData returns recipient data (identity + audit info) from the
// identity cache.
func (w *AnonymousOwnerWallet) GetRecipientData(ctx context.Context) (*driver.RecipientData, error) {
	return w.IdentityCache.RecipientData(ctx)
}

// RegisterRecipient validates and registers the provided recipient data
// (verifier and audit info) into the identity provider and wallet registry.
func (w *AnonymousOwnerWallet) RegisterRecipient(ctx context.Context, data *driver.RecipientData) error {
	if data == nil {
		return errors.Wrapf(wallet.ErrNilRecipientData, "invalid recipient data")
	}
	w.Logger.DebugfContext(ctx, "register recipient identity [%s] with audit info [%s]", data.Identity, utils.Hashable(data.AuditInfo))

	// recognize identity and register it
	// match identity and audit info
	err := w.Deserializer.MatchIdentity(ctx, data.Identity, data.AuditInfo)
	if err != nil {
		return errors.Wrapf(err, "failed to match identity to audit infor for [%s]:[%s]", data.Identity, utils.Hashable(data.AuditInfo))
	}
	// register recipient data
	if err := w.IdentityProvider.RegisterRecipientData(ctx, data); err != nil {
		return errors.Wrapf(err, "failed registering audit info for owner [%s]", data.Identity)
	}
	// bind to enrollment id and wallet id
	if err := w.IdentitySupport.BindIdentity(ctx, data.Identity, w.EnrollmentID(), w.WalletID, nil, w.OwnerIdentityInfo.ConfigurationID()); err != nil {
		return errors.WithMessagef(err, "failed storing recipient identity in wallet [%s]", w.WalletID)
	}

	return nil
}

// getRecipientIdentity generates a fresh pseudonym and registers it with
// the wallet registry, returning the resulting RecipientData.
func (w *AnonymousOwnerWallet) getRecipientIdentity(ctx context.Context) (*driver.RecipientData, error) {
	// Get a new pseudonym
	pseudonym, auditInfo, err := w.OwnerIdentityInfo.Get(ctx)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting recipient identity from wallet [%s:%s]", w.ID(), w.OwnerIdentityInfo.EnrollmentID())
	}

	// Register the pseudonym
	if err := w.IdentitySupport.BindIdentity(ctx, pseudonym, w.OwnerIdentityInfo.EnrollmentID(), w.WalletID, nil, w.OwnerIdentityInfo.ConfigurationID()); err != nil {
		return nil, errors.WithMessagef(err, "failed storing recipient identity in wallet [%s]", w.ID())
	}

	return &driver.RecipientData{
		Identity:  pseudonym,
		AuditInfo: auditInfo,
	}, nil
}

// GetSigner returns a signer for the provided identity if it is bound to
// this anonymous owner wallet; otherwise an error is returned.
func (w *AnonymousOwnerWallet) GetSigner(ctx context.Context, identity Identity) (Signer, error) {
	if !w.Contains(ctx, identity) {
		return nil, errors.Errorf("identity [%s] does not belong to this wallet [%s]", identity, w.ID())
	}

	return w.IdentityProvider.GetSigner(ctx, identity)
}
