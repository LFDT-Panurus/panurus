/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"
	"math/big"
	"time"

	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// Identity represents a generic identity
type Identity = view.Identity

// IdentityProvider provides services for managing identities, including signing, verification, and audit information.
// It acts as a central registry for identities and their associated cryptographic materials.
//
//go:generate counterfeiter -o mock/ip.go -fake-name IdentityProvider . IdentityProvider
type IdentityProvider interface {
	// RegisterRecipientData stores information about a token recipient, including their identity and audit metadata.
	RegisterRecipientData(ctx context.Context, data *RecipientData) error

	// GetAuditInfo retrieves the audit data associated with a specific identity.
	GetAuditInfo(ctx context.Context, identity Identity) ([]byte, error)

	// GetSigner returns a cryptographic signer for a given identity, enabling signature generation.
	GetSigner(ctx context.Context, identity Identity) (Signer, error)

	// RegisterSigner registers a pair of signer and verifier for a specific identity,
	// along with additional identity information.
	RegisterSigner(ctx context.Context, identity Identity, signer Signer, verifier Verifier, signerInfo []byte, ephemeral bool) error

	// AreMe checks a list of identities and returns those that have signers registered with this provider.
	AreMe(ctx context.Context, identities ...Identity) []string

	// IsMe returns true if a signer has been registered for the specified identity.
	IsMe(ctx context.Context, party Identity) bool

	// GetEnrollmentID extracts the enrollment identifier from the provided audit information for a specific identity.
	GetEnrollmentID(ctx context.Context, identity Identity, auditInfo []byte) (string, error)

	// GetRevocationHandler extracts the revocation handler from the provided audit information for a specific identity.
	GetRevocationHandler(ctx context.Context, identity Identity, auditInfo []byte) (string, error)

	// GetEIDAndRH returns both the enrollment ID and the revocation handle associated with a specific identity and its audit data.
	GetEIDAndRH(ctx context.Context, identity Identity, auditInfo []byte) (string, string, error)

	// Bind associates a long-term identity with one or more ephemeral (short-term) identities.
	Bind(ctx context.Context, longTerm Identity, ephemeralIdentities ...Identity) error

	// RegisterRecipientIdentity registers a third-party recipient's identity without requiring audit information initially.
	RegisterRecipientIdentity(ctx context.Context, id Identity) error
}

// RecipientData captures details about a token's recipient, used for registration and tracking.
type RecipientData struct {
	// Identity is the owner's cryptographic identity.
	Identity Identity
	// AuditInfo contains private metadata used for auditing the owner's identity.
	AuditInfo []byte
	// TokenMetadata contains public information about the token being assigned to this recipient.
	TokenMetadata []byte
	// TokenMetadataAuditInfo contains private metadata used for auditing the token metadata.
	TokenMetadataAuditInfo []byte
}

// ListTokensOptions contains options that can be used to list tokens from a wallet
type ListTokensOptions struct {
	// TokenType is the type of token to list
	TokenType token.Type
}

// Wallet models a generic wallet
//
//go:generate counterfeiter -o mock/w.go -fake-name Wallet . Wallet
type Wallet interface {
	// ID returns the ID of this wallet
	ID() string

	// Contains returns true if the passed identity belongs to this wallet
	Contains(ctx context.Context, identity Identity) bool

	// ContainsToken returns true if the passed token is owned by this wallet
	ContainsToken(ctx context.Context, token *token.UnspentToken) bool

	// GetSigner returns the Signer bound to the passed identity
	GetSigner(ctx context.Context, identity Identity) (Signer, error)
}

// OwnerWallet models the wallet of a token recipient.
//
//go:generate counterfeiter -o mock/ow.go -fake-name OwnerWallet . OwnerWallet
type OwnerWallet interface {
	Wallet

	// GetRecipientIdentity returns a recipient identity.
	// Depending on the underlying wallet implementation, this can be a long-term or ephemeral identity.
	// Using the returned identity as an index, one can retrieve the following information:
	// - Identity audit info via GetAuditInfo;
	// - TokenMetadata via GetTokenMetadata;
	// - TokenIdentityMetadata via GetTokenMetadataAuditInfo.
	GetRecipientIdentity(ctx context.Context) (Identity, error)

	// GetRecipientData returns a recipient data struct, it does not include the token metadata audit info
	GetRecipientData(ctx context.Context) (*RecipientData, error)

	// GetAuditInfo returns auditing information for the passed identity
	GetAuditInfo(ctx context.Context, id Identity) ([]byte, error)

	// GetTokenMetadata returns the public information related to the token to be assigned to passed recipient identity.
	GetTokenMetadata(id Identity) ([]byte, error)

	// GetTokenMetadataAuditInfo returns private information about the token metadata assigned to the passed recipient identity.
	GetTokenMetadataAuditInfo(id Identity) ([]byte, error)

	// ListTokens returns the list of unspent tokens owned by this wallet filtered using the passed options.
	ListTokens(ctx context.Context, opts *ListTokensOptions) (*token.UnspentTokens, error)

	// ListTokensIterator returns an iterator of unspent tokens owned by this wallet filtered using the passed options.
	ListTokensIterator(ctx context.Context, opts *ListTokensOptions) (UnspentTokensIterator, error)

	// Balance returns the sum of the amounts of the tokens with type and EID equal to those passed as arguments.
	// The result is returned as a *big.Int to support arbitrary precision and prevent overflow.
	Balance(ctx context.Context, opts *ListTokensOptions) (*big.Int, error)

	// EnrollmentID returns the enrollment ID of the owner wallet
	EnrollmentID() string

	// RegisterRecipient register the passed recipient data.
	// The data is passed as pointer to allow the underlying token driver to modify them if needed.
	RegisterRecipient(ctx context.Context, data *RecipientData) error

	// Remote returns true if this wallet is verify only, meaning that the corresponding secret key is external to this wallet
	Remote() bool
}

// IssuerBalanceOptions contains options that can be used to compute issuer balances
// (issued and redeemed sums).
type IssuerBalanceOptions struct {
	// TokenType optionally restricts the sum to a single token type. An empty type means all types.
	TokenType token.Type
	// From, when set, restricts the sum to tokens recorded at or after this time.
	From *time.Time
	// To, when set, restricts the sum to tokens recorded at or before this time.
	To *time.Time
}

// IssuerWallet models the wallet of an issuer
//
//go:generate counterfeiter -o mock/iw.go -fake-name IssuerWallet . IssuerWallet
type IssuerWallet interface {
	Wallet

	// GetIssuerIdentity returns an issuer identity for the passed token type.
	// Depending on the underlying wallet implementation, this can be a long-term or ephemeral identity.
	GetIssuerIdentity(tokenType token.Type) (Identity, error)

	// HistoryTokens returns the list of tokens issued by this wallet filtered using the passed options.
	HistoryTokens(ctx context.Context, opts *ListTokensOptions) (*token.IssuedTokens, error)

	// IssuedBalance returns the sum of the quantities of the tokens issued by this wallet,
	// filtered using the passed options.
	// The result is returned as a *big.Int to support arbitrary precision and prevent overflow.
	IssuedBalance(ctx context.Context, opts *IssuerBalanceOptions) (*big.Int, error)

	// RedeemedBalance returns the sum of the quantities of the tokens redeemed against this issuer,
	// filtered using the passed options. A redeemed token is an empty-owner output in a transfer
	// action signed by the issuer.
	// The result is returned as a *big.Int to support arbitrary precision and prevent overflow.
	RedeemedBalance(ctx context.Context, opts *IssuerBalanceOptions) (*big.Int, error)

	// Balance returns the net issued supply of this wallet: IssuedBalance minus RedeemedBalance,
	// filtered using the passed options.
	// The result is returned as a *big.Int to support arbitrary precision and prevent overflow.
	Balance(ctx context.Context, opts *IssuerBalanceOptions) (*big.Int, error)
}

// AuditorWallet models the wallet of an auditor
//
//go:generate counterfeiter -o mock/aw.go -fake-name AuditorWallet . AuditorWallet
type AuditorWallet interface {
	Wallet

	// GetAuditorIdentity returns an auditor identity.
	// Depending on the underlying wallet implementation, this can be a long-term or ephemeral identity.
	GetAuditorIdentity() (Identity, error)
}

// CertifierWallet models the wallet of a certifier
//
//go:generate counterfeiter -o mock/cw.go -fake-name CertifierWallet . CertifierWallet
type CertifierWallet interface {
	Wallet

	// GetCertifierIdentity returns a certifier identity.
	// Depending on the underlying wallet implementation, this can be a long-term or ephemeral identity.
	GetCertifierIdentity() (Identity, error)
}

// IdentityConfiguration contains configuration-related information of an identity.
// It is used to describe how an identity should be loaded and managed by Panurus.
type IdentityConfiguration struct {
	// ID is the unique identifier for this identity configuration.
	ID string
	// Type is the type of the identity (e.g., "bccsp", "idemix").
	Type string
	// URL is the location of the identity's credential material (e.g., path to MSP folder).
	URL string
	// Config contains driver-specific configuration options in encoded format.
	Config []byte
	// Raw contains the raw identity material if already loaded.
	Raw []byte
}

// WalletLookupID defines the type of identifiers that can be used to retrieve a given wallet.
// It can be a string, as the name of the wallet, or an identity contained in that wallet.
// Ultimately, it is the token driver to decide which types are allowed.
type WalletLookupID = any

// IdentityType identifies the type of identity
type IdentityType = int32

// This is a list of reserved identities
const (
	ZeroIdentityType       IdentityType = 0 //
	IdemixIdentityType     IdentityType = 1
	X509IdentityType       IdentityType = 2
	IdemixNymIdentityType  IdentityType = 3
	HTLCScriptIdentityType IdentityType = 4
	MultiSigIdentityType   IdentityType = 5
	PolicyIdentityType     IdentityType = 6
)

// IdentityTypeString identifies the type of identity as a string
type IdentityTypeString = string

const (
	IdemixIdentityTypeString     IdentityTypeString = "idemix"
	X509IdentityTypeString       IdentityTypeString = "x509"
	IdemixNymIdentityTypeString  IdentityTypeString = "idemixnym"
	HTLCScriptIdentityTypeString IdentityTypeString = "htlc"
	MultiSigIdentityTypeString   IdentityTypeString = "multisig"
	PolicyIdentityTypeString     IdentityTypeString = "policy"
)

// Authorization checks the relationship between a token and different wallet types (owner, issuer, auditor).
// It determines if a given wallet can perform specific actions on a token.
//
//go:generate counterfeiter -o mock/authorization.go -fake-name Authorization . Authorization
type Authorization interface {
	// IsMine determines if a given token belongs to a known owner wallet.
	// It returns the ID of the wallet (if any) and additional identifiers that may indicate ownership,
	// along with a boolean indicating if the token is indeed owned.
	IsMine(ctx context.Context, tok *token.Token) (walletID string, additionalOwners []string, mine bool)

	// AmIAnAuditor checks if the service has auditor privileges based on its configuration and identities.
	AmIAnAuditor() bool

	// Issued checks if a specific issuer, identified by their identity, was the one that issued the token.
	Issued(ctx context.Context, issuer Identity, tok *token.Token) bool

	// OwnerType determines the type of the token's owner and extracts the owner's raw identity.
	OwnerType(raw []byte) (IdentityType, []byte, error)
}

// WalletService manages different types of token wallets: issuer, owner, auditor, and certifier.
// It provides methods for looking up wallets, registering identities, and extracting audit data.
//
//go:generate counterfeiter -o mock/ws.go -fake-name WalletService . WalletService
type WalletService interface {
	// RegisterRecipientIdentity registers a recipient identity and its audit data.
	RegisterRecipientIdentity(ctx context.Context, data *RecipientData) error

	// GetAuditInfo retrieves the audit data associated with a specific identity.
	GetAuditInfo(ctx context.Context, id Identity) ([]byte, error)

	// GetEnrollmentID extracts the enrollment identifier from audit data for a given identity.
	GetEnrollmentID(ctx context.Context, identity Identity, auditInfo []byte) (string, error)

	// GetRevocationHandle extracts the revocation handler from audit data for a given identity.
	GetRevocationHandle(ctx context.Context, identity Identity, auditInfo []byte) (string, error)

	// GetEIDAndRH returns both the enrollment ID and the revocation handle for a given identity and audit data.
	GetEIDAndRH(ctx context.Context, identity Identity, auditInfo []byte) (string, string, error)

	// Wallet returns a generic wallet interface associated with the specified identity, if any.
	Wallet(ctx context.Context, identity Identity) Wallet

	// RegisterOwnerIdentity registers a long-term owner identity using the provided configuration.
	RegisterOwnerIdentity(ctx context.Context, config IdentityConfiguration) error

	// RegisterIssuerIdentity registers a long-term issuer identity using the provided configuration.
	RegisterIssuerIdentity(ctx context.Context, config IdentityConfiguration) error

	// OwnerWalletIDs returns a list of identifiers for all known owner wallets.
	OwnerWalletIDs(ctx context.Context) ([]string, error)

	// OwnerWallet retrieves an OwnerWallet instance based on its identifier or an identity belonging to it.
	OwnerWallet(ctx context.Context, id WalletLookupID) (OwnerWallet, error)

	// IssuerWallet retrieves an IssuerWallet instance based on its identifier or an identity belonging to it.
	IssuerWallet(ctx context.Context, id WalletLookupID) (IssuerWallet, error)

	// AuditorWallet retrieves an AuditorWallet instance based on its identifier or an identity belonging to it.
	AuditorWallet(ctx context.Context, id WalletLookupID) (AuditorWallet, error)

	// CertifierWallet retrieves a CertifierWallet instance based on its identifier or an identity belonging to it.
	CertifierWallet(ctx context.Context, id WalletLookupID) (CertifierWallet, error)

	// SpendIDs returns unique identifiers representing the potential spending of the specified token IDs.
	SpendIDs(ids ...*token.ID) ([]string, error)

	// Done releases all the resources allocated by this service.
	Done() error
}

//go:generate counterfeiter -o mock/wallet_service_factory.go -fake-name WalletServiceFactory . WalletServiceFactory
type WalletServiceFactory interface {
	PPReader
	// NewWalletService returns an instance of the WalletService interface for the passed arguments
	NewWalletService(tmsConfig Configuration, params PublicParameters) (WalletService, error)
}

// Matcher models a matcher that can be used to match identities
//
//go:generate counterfeiter -o mock/matcher.go -fake-name Matcher . Matcher
type Matcher interface {
	// Match returns true if the passed identity matches this matcher
	Match(ctx context.Context, identity []byte) error
}

// AuditInfoProvider models a provider of audit information
//
//go:generate counterfeiter -o mock/audit_info_provider.go -fake-name AuditInfoProvider . AuditInfoProvider
type AuditInfoProvider interface {
	// GetAuditInfo returns the audit information for the given identity, if available.
	GetAuditInfo(ctx context.Context, identity Identity) ([]byte, error)
}

// Deserializer provides methods for converting serialized identities (owner, issuer, auditor)
// into cryptographic signature verifiers, which are used to validate transaction signatures.
//
//go:generate counterfeiter -o mock/deserializer.go -fake-name Deserializer . Deserializer
type Deserializer interface {
	// GetOwnerVerifier returns a signature verifier for a given owner identity.
	GetOwnerVerifier(ctx context.Context, id Identity) (Verifier, error)

	// GetIssuerVerifier returns a signature verifier for a given issuer identity.
	GetIssuerVerifier(ctx context.Context, id Identity) (Verifier, error)

	// GetAuditorVerifier returns a signature verifier for a given auditor identity.
	GetAuditorVerifier(ctx context.Context, id Identity) (Verifier, error)

	// Recipients extracts all recipient identities from a given serialized identity.
	Recipients(raw Identity) ([]Identity, error)

	// GetAuditInfoMatcher provides an identity matcher for a given identity and its audit metadata.
	GetAuditInfoMatcher(ctx context.Context, owner Identity, auditInfo []byte) (Matcher, error)

	// MatchIdentity checks if a specific identity corresponds to the provided audit data.
	MatchIdentity(ctx context.Context, id Identity, ai []byte) error

	// GetAuditInfo retrieves the audit information for a specific identity using an AuditInfoProvider.
	GetAuditInfo(ctx context.Context, id Identity, p AuditInfoProvider) ([]byte, error)
}
