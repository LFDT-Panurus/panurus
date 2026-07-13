/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"
	"errors"
	"math/big"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	"github.com/LFDT-Panurus/panurus/token/token"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
)

type TokenRecord struct {
	// TxID is the ID of the transaction that created the token
	TxID string
	// Index is the index in the transaction
	Index uint64
	// IssuerRaw represents the serialization of the issuer identity
	// if this is an IssuedToken.
	IssuerRaw []byte
	// OwnerRaw is the serialization of the owner TypedIdentity
	OwnerRaw []byte
	// OwnerType is the deserialized type inside OwnerRaw
	OwnerType string
	// OwnerIdentity is the deserialized Identity inside OwnerRaw
	OwnerIdentity []byte
	// OwnerWalletID is the identifier of the wallet that owns this token, it might be empty
	OwnerWalletID string
	// Ledger is the raw token as stored on the ledger
	Ledger []byte
	// LedgerFormat is the type of the raw token as stored on the ledger
	LedgerFormat token.Format
	// LedgerMetadata is the metadata associated to the content of Ledger
	LedgerMetadata []byte
	// Quantity is the number of units of Type carried in the token.
	// It is encoded as a string containing a number in base 16. The string has prefix ``0x''.
	Quantity string
	// Type is the type of token
	Type token.Type
	// Amount is the Quantity converted to decimal
	Amount uint64
	// Owner is used to mark the token as owned by this node
	Owner bool
	// Auditor is used to mark this token as audited by this node
	Auditor bool
	// Issuer issued to mark this token as issued by this node
	Issuer bool
	// Redeemed is used to mark this token as a redeem (empty owner) attributed to an issuer identity of this node
	Redeemed bool
}

// TokenDetails provides details about an owned (spent or unspent) token
type TokenDetails struct {
	// TxID is the ID of the transaction that created the token
	TxID string
	// Index is the index in the transaction
	Index uint64
	// OwnerIdentity is the serialization of the owner identity
	OwnerIdentity []byte
	// OwnerType is the deserialized type inside OwnerRaw
	OwnerType string
	// OwnerEnrollment is the enrollment id of the owner
	OwnerEnrollment string
	// Type is the type of token
	Type string
	// Amount is the Quantity converted to decimal as a big integer to support arbitrary precision
	Amount *big.Int
	// IsSpent is true if the token has been spent
	IsSpent bool
	// SpentBy is the transactionID that spent this token, if available
	SpentBy string
	// StoredAt is the moment the token was stored by this wallet
	StoredAt time.Time
}

// QueryTokenDetailsParams defines the parameters for querying token details
type QueryTokenDetailsParams struct {
	// WalletID is the optional identifier of the wallet owning the token.
	// When WalletIDs is non-empty this field is ignored.
	WalletID string
	// WalletIDs is an optional list of wallet identifiers. When set, tokens
	// owned by any of the listed wallets match (single SQL query with IN).
	// Takes precedence over WalletID; leave empty for the single-wallet path.
	WalletIDs []string
	// OwnerType is the type of owner, for instance 'idemix' or 'htlc'
	OwnerType string
	// TokenType (optional) is the type of token
	TokenType token.Type
	// IDs is an optional list of specific token ids to return
	IDs []*token.ID
	// TransactionIDs selects tokens that are the output of the provided transaction ids.
	TransactionIDs []string
	// IncludeDeleted determines whether to include spent tokens. It defaults to false.
	IncludeDeleted bool
	// Spendable determines whether to include only spendable/non-spendable or any tokens. It defaults to nil (any tokens)
	Spendable SpendableFilter
	// LedgerTokenFormats selects tokens whose output on the ledger has a format in the list
	LedgerTokenFormats []token.Format
}

type SpendableFilter int

const (
	Any SpendableFilter = iota
	SpendableOnly
	NonSpendableOnly
)

// DeletedToken represents a token that has been deleted and is ready for keystore cleanup
type DeletedToken struct {
	// TxID is the ID of the transaction that created the token
	TxID string
	// Index is the index in the transaction
	Index uint64
	// OwnerIdentity is the serialized owner identity used to derive SKIs for key deletion
	OwnerIdentity []byte
	// OwnerType is the type of the owner identity (e.g., "idemix", "x509")
	OwnerType string
	// DeletedAt is when the token was marked as deleted
	DeletedAt time.Time
}

// CleanupLeadership represents an acquired leadership session for keystore cleanup operations.
// It uses the same pattern as RecoveryLeadership for consistency.
type CleanupLeadership interface {
	// Close releases the leadership and any associated resources
	Close() error
}

// CertificationStore defines a database to manager token certifications
type CertificationStore interface {
	// ExistsCertification returns true if a certification for the passed token exists,
	// false otherwise
	ExistsCertification(ctx context.Context, id *token.ID) bool

	// StoreCertifications stores the passed certifications
	StoreCertifications(ctx context.Context, certifications map[*token.ID][]byte) error

	// GetCertifications returns the certifications of the passed tokens.
	// For each token, the callback function is invoked.
	// If a token doesn't have a certification, the function returns an error
	GetCertifications(ctx context.Context, ids []*token.ID) ([][]byte, error)
	// Close closes the databases
	Close() error
}

type TokenStoreTransaction interface {
	// GetToken returns the owned tokens and their identifier keys for the passed ids.
	GetToken(ctx context.Context, tokenID token.ID, includeDeleted bool) (*token.Token, []string, error)
	// Delete marks the passed token as deleted by a given identifier (idempotent)
	Delete(ctx context.Context, tokenID token.ID, deletedBy string) error
	// StoreToken stores the passed token record in relation to the passed owner identifiers, if any
	StoreToken(ctx context.Context, tr TokenRecord, owners []string) error
	// SetSpendable updates the spendable flag of the passed token
	SetSpendable(ctx context.Context, tokenID token.ID, spendable bool) error
	// SetSpendableBySupportedTokenFormats sets the spendable flag to true for all the tokens having one of the passed token type.
	// The spendable flag is set to false for the other tokens
	SetSpendableBySupportedTokenFormats(ctx context.Context, formats []token.Format) error
	// Commit commits this transaction
	Commit() error
	// Rollback rollbacks this transaction
	Rollback() error
}

// TokenStore defines a database to store token related info
type TokenStore interface {
	CertificationStore
	// DeleteTokens marks the passsed tokens as deleted
	DeleteTokens(ctx context.Context, deletedBy string, toDelete ...*token.ID) error
	// IsMine return true if the passed token was stored before
	IsMine(ctx context.Context, txID string, index uint64) (bool, error)
	// UnspentTokensIterator returns an iterator over all owned tokens
	UnspentTokensIterator(ctx context.Context) (driver.UnspentTokensIterator, error)
	// UnspentLedgerTokensIteratorBy returns an iterator over all unspent ledger tokens
	UnspentLedgerTokensIteratorBy(ctx context.Context) (driver.LedgerTokensIterator, error)
	// UnspentTokensIteratorBy returns an iterator over all tokens owned by the passed wallet identifier and of a given type
	UnspentTokensIteratorBy(ctx context.Context, walletID string, tokenType token.Type) (driver.UnspentTokensIterator, error)
	// SpendableTokensIteratorBy returns an iterator over all tokens owned solely by the passed wallet identifier and of a given type
	SpendableTokensIteratorBy(ctx context.Context, walletID string, typ token.Type) (driver.SpendableTokensIterator, error)
	// UnsupportedTokensIteratorBy returns the minimum information for upgrade about the tokens that are not supported
	UnsupportedTokensIteratorBy(ctx context.Context, walletID string, tokenType token.Type) (driver.UnsupportedTokensIterator, error)
	// ListUnspentTokensBy returns the list of all tokens owned by the passed identifier of a given type
	ListUnspentTokensBy(ctx context.Context, walletID string, typ token.Type) (*token.UnspentTokens, error)
	// ListUnspentTokensByWallets returns unspent tokens owned by any of the
	// provided wallets, partitioned by wallet id. An optional token type
	// filter narrows the result set (empty type means any type).
	//
	// The returned map contains only wallet ids that had at least one
	// matching unspent token — callers should treat a missing key as "no
	// tokens" (same semantics as an empty *token.UnspentTokens). An empty
	// or nil walletIDs slice returns an empty map without touching the
	// database.
	//
	// This is a single-query alternative to calling ListUnspentTokensBy
	// per wallet in a loop: one SQL statement with an IN clause, then
	// partitioned on the read side.
	//
	// Scale note: the IN clause expands to two placeholders per wallet id
	// (one per wallet column). Postgres's default parameter limit (~65k)
	// leaves ample headroom; SQLite's default (999) caps the practical
	// batch size at a few hundred wallet ids. Callers expecting thousands
	// of ids should chunk on the caller side and merge the returned maps.
	//
	// Position in the ListUnspentTokens family:
	//
	//	UnspentTokensIterator(ctx)                       → iterator (all)
	//	UnspentTokensIteratorBy(ctx, walletID, typ)      → iterator (one wallet)
	//	ListUnspentTokens(ctx)                           → *UnspentTokens (all)
	//	ListUnspentTokensBy(ctx, walletID, typ)          → *UnspentTokens (one wallet)
	//	ListUnspentTokensByWallets(ctx, walletIDs, typ)  → map[walletID]*UnspentTokens (batch)
	ListUnspentTokensByWallets(ctx context.Context, walletIDs []string, typ token.Type) (map[string]*token.UnspentTokens, error)
	// ListUnspentTokens returns the list of all owned tokens
	ListUnspentTokens(ctx context.Context) (*token.UnspentTokens, error)
	// ListAuditTokens returns the audited tokens for the passed ids
	ListAuditTokens(ctx context.Context, ids ...*token.ID) ([]*token.Token, error)
	// ListHistoryIssuedTokens returns the list of all issued tokens
	ListHistoryIssuedTokens(ctx context.Context) (*token.IssuedTokens, error)
	// GetTokenOutputs returns the value of the tokens as they appear on the ledger for the passed ids.
	// For each token, the call-back function is invoked. The call-back function is invoked respecting the order of the passed ids.
	GetTokenOutputs(ctx context.Context, ids []*token.ID, callback driver.QueryCallbackFunc) error
	// GetTokenMetadata returns the metadata of the tokens for the passed ids.
	GetTokenMetadata(ctx context.Context, ids []*token.ID) ([][]byte, error)
	// GetTokenOutputsAndMeta retrieves both the token output, metadata, and type for the passed ids.
	GetTokenOutputsAndMeta(ctx context.Context, ids []*token.ID) ([][]byte, [][]byte, []token.Format, error)
	// GetTokens returns the owned tokens and their identifier keys for the passed ids.
	GetTokens(ctx context.Context, inputs ...*token.ID) ([]*token.Token, error)
	// WhoDeletedTokens for each id, the function return if it was deleted and by who as per the Delete function
	WhoDeletedTokens(ctx context.Context, inputs ...*token.ID) ([]string, []bool, error)
	// TransactionExists returns true if a token with that transaction id exists in the db
	TransactionExists(ctx context.Context, id string) (bool, error)
	// StorePublicParams stores the public parameters.
	// If they already exist, the function return with no error. No changes are applied.
	StorePublicParams(ctx context.Context, raw []byte) error
	// PublicParams returns the stored public parameters.
	// If not public parameters are available, it returns nil with no error
	PublicParams(ctx context.Context) ([]byte, error)
	// PublicParamsByHash returns the public parameters whose hash matches the passed one.
	// If not public parameters are available for that hash, it returns an error
	PublicParamsByHash(ctx context.Context, rawHash driver.PPHash) ([]byte, error)
	// NewTokenDBTransaction returns a new Transaction to commit atomically multiple operations
	NewTokenDBTransaction() (TokenStoreTransaction, error)
	// ContinueTokenDBTransaction returns a new TokenStoreTransaction building upon the passed transaction.
	ContinueTokenDBTransaction(tx Transaction) (TokenStoreTransaction, error)
	// QueryTokenDetails provides detailed information about tokens
	QueryTokenDetails(ctx context.Context, params QueryTokenDetailsParams) ([]TokenDetails, error)
	// Balance returns the sum of the amounts of the tokens with type and EID equal to those passed as arguments.
	// The result is returned as a *big.Int to support arbitrary precision and prevent overflow.
	Balance(ctx context.Context, ownerEID string, typ token.Type) (*big.Int, error)
	// IssuedBalance returns the sum of the amounts of the tokens issued by this node, filtered by the passed options.
	// The result is returned as a *big.Int to support arbitrary precision and prevent overflow.
	IssuedBalance(ctx context.Context, opts driver.IssuerBalanceQuery) (*big.Int, error)
	// RedeemedBalance returns the sum of the amounts of the tokens redeemed against an issuer known to this node,
	// filtered by the passed options.
	// The result is returned as a *big.Int to support arbitrary precision and prevent overflow.
	RedeemedBalance(ctx context.Context, opts driver.IssuerBalanceQuery) (*big.Int, error)
	// SetSupportedTokenFormats sets the supported token formats
	SetSupportedTokenFormats(formats []token.Format) error
	// Notifier returns a TokenNotifier for this store to subscribe to token changes.
	Notifier() (TokenNotifier, error)
	// GetDeletedTokensPendingSKICleanup returns deleted tokens older than the specified duration that haven't had their SKI keys cleaned yet.
	// This is used by the keystore cleanup service to identify tokens whose cryptographic keys can be safely removed.
	// Only tokens without a record in the token_ski_cleanups table are returned, and only tokens owned by this node,
	// since this node only holds the secret keys for tokens it owns, not for tokens it merely audited or issued.
	GetDeletedTokensPendingSKICleanup(ctx context.Context, olderThan time.Duration, limit int) ([]DeletedToken, error)
	// MarkTokenCleaned marks a token as having its cryptographic keys cleaned up.
	// This prevents the cleanup service from processing the same token multiple times.
	// The cleanedBy parameter identifies which instance performed the cleanup for audit purposes.
	MarkTokenCleaned(ctx context.Context, txID string, index uint64, cleanedBy string) error
	// AcquireCleanupLeadership attempts to acquire leadership for keystore cleanup operations.
	// Returns (leadership, true, nil) if leadership was acquired.
	// Returns (nil, false, nil) if leadership is held by another instance.
	// Returns (nil, false, error) if an error occurred.
	AcquireCleanupLeadership(ctx context.Context, lockID int64) (CleanupLeadership, bool, error)
}

type (
	// Operation defines the type of database operation (Insert, Update, etc.).
	Operation = driver2.Operation
	// ColumnKey defines the name of a database column.
	ColumnKey = driver2.ColumnKey
)

const (
	// Insert indicates a record was added to the table.
	Insert = driver2.Insert
	// Update indicates an existing record was modified.
	Update = driver2.Update
	// Delete indicates an existing record was deleted.
	Delete = driver2.Delete
)

// TokenRecordReference contains the primary key fields of a token record.
type TokenRecordReference struct {
	// TxID is the unique identifier of the transaction that created the token.
	TxID string
	// Index is the index of the token in the transaction.
	Index uint64
}

// TokenNotifier is used to subscribe to token changes in the storage.
type TokenNotifier interface {
	// Subscribe registers a callback function to be called when a token record is inserted, updated or deleted.
	Subscribe(callback func(Operation, TokenRecordReference)) error
	// UnsubscribeAll unregisters all callbacks.
	UnsubscribeAll() error
}

// TokenNotifierDriver is the interface for a token database driver
type TokenNotifierDriver interface {
	// Open opens a token database with its listeners
	Open(cp ConfigProvider, tmsID token2.TMSID) (TokenNotifier, error)
}

// TokenLockStore enforces that a token be used only by one process
// A housekeeping job can clean up expired locks (e.g. created_at is more than 5 minutes ago) in order to:
// - avoid that the table grows infinitely
// - unlock tokens that were locked by a process that exited unexpectedly
type TokenLockStore interface {
	common.DBObject
	// Lock locks a specific token for the consumer TX
	Lock(ctx context.Context, tokenID *token.ID, consumerTxID transaction.ID) error
	// UnlockByTxID unlocks all tokens locked by the consumer TX
	UnlockByTxID(ctx context.Context, consumerTxID transaction.ID) error
	// Cleanup removes the locks such that either:
	// 1. The transaction that locked that token is valid or invalid;
	// 2. The lock is too old.
	Cleanup(ctx context.Context, leaseExpiry time.Duration) error
	// Close closes the database
	Close() error
}

var ErrTokenDoesNotExist = errors.New("token does not exist")
