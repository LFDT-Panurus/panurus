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
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
)

type QueryCallbackFunc func(*token.ID, []byte) error

type QueryCallback2Func func(*token.ID, string, []byte, []byte) error

// TxStatus is the status of a transaction
type TxStatus = int

const (
	// Unknown is the status of a transaction that is unknown
	Unknown TxStatus = iota
	// Pending is the status of a transaction that has been submitted to the ledger
	Pending
	// Confirmed is the status of a transaction that has been confirmed by the ledger
	Confirmed
	// Deleted is the status of a transaction that has been deleted due to a failure to commit
	Deleted
	// Orphan is the status of a transaction that never reached the ledger (e.g. broadcast failure, mempool drop)
	Orphan
)

//go:generate counterfeiter -o mock/uti.go -fake-name UnspentTokensIterator . UnspentTokensIterator
type UnspentTokensIterator = iterators.Iterator[*token.UnspentToken]

type SpendableTokensIterator = iterators.Iterator[*token.UnspentTokenInWallet]

type UnsupportedTokensIterator = iterators.Iterator[*token.LedgerToken]

type LedgerTokensIterator = iterators.Iterator[*token.LedgerToken]

// Vault defines the interface for accessing the token vault, which stores tokens and their certifications.
// It provides a QueryEngine for querying the ledger and a CertificationStorage for managing certifications.
//
//go:generate counterfeiter -o mock/vault.go -fake-name Vault . Vault
type Vault interface {
	// QueryEngine returns an instance of the QueryEngine interface for querying the ledger.
	QueryEngine() QueryEngine
	// CertificationStorage returns an instance of the CertificationStorage interface for managing token certifications.
	CertificationStorage() CertificationStorage
}

// CertificationStorage provides methods for storing and checking the existence of token certifications.
//
//go:generate counterfeiter -o mock/certification_storage.go -fake-name CertificationStorage . CertificationStorage
type CertificationStorage interface {
	// Exists checks if a certification exists for the specified token ID.
	Exists(ctx context.Context, id *token.ID) bool
	// Store saves the provided certifications for the given token IDs.
	Store(ctx context.Context, certifications map[*token.ID][]byte) error
}

// QueryEngine provides a read-only interface for querying token-related data from the ledger.
// It allows for checking transaction status, ownership, and retrieving unspent tokens.
//
//go:generate counterfeiter -o mock/qe.go -fake-name QueryEngine . QueryEngine
type QueryEngine interface {
	// IsPending checks if the transaction associated with the given token ID is still pending.
	IsPending(ctx context.Context, id *token.ID) (bool, error)
	// GetStatus returns the execution status of a specific transaction.
	GetStatus(ctx context.Context, txID string) (TxStatus, string, error)
	// IsMine checks if a specific token is owned by any wallet known to the service.
	IsMine(ctx context.Context, id *token.ID) (bool, error)
	// UnspentTokensIterator returns an iterator to traverse all unspent tokens on the ledger.
	UnspentTokensIterator(ctx context.Context) (UnspentTokensIterator, error)
	// UnspentLedgerTokensIteratorBy returns an iterator to traverse all unspent ledger tokens.
	UnspentLedgerTokensIteratorBy(ctx context.Context) (LedgerTokensIterator, error)
	// UnspentTokensIteratorBy returns an iterator over unspent tokens owned by a specific wallet and optionally filtered by token type.
	UnspentTokensIteratorBy(ctx context.Context, walletID string, tokenType token.Type) (UnspentTokensIterator, error)
	// ListUnspentTokens returns a comprehensive list of all unspent tokens.
	ListUnspentTokens(ctx context.Context) (*token.UnspentTokens, error)
	// ListAuditTokens returns the audited token data for the specified token IDs.
	// The result is in order of ids meaning that the first token correspond to the first id and so on.
	ListAuditTokens(ctx context.Context, ids ...*token.ID) ([]*token.Token, error)
	// ListHistoryIssuedTokens returns a list of all tokens issued by the service.
	ListHistoryIssuedTokens(ctx context.Context) (*token.IssuedTokens, error)
	// PublicParams returns the serialized public parameters used by the driver.
	PublicParams(ctx context.Context) ([]byte, error)
	// GetTokenMetadata retrieves the private information (metadata) for the given token IDs.
	GetTokenMetadata(ctx context.Context, ids []*token.ID) ([][]byte, error)
	// GetTokenOutputs retrieves the raw token outputs as stored on the ledger for the specified token IDs.
	GetTokenOutputs(ctx context.Context, ids []*token.ID, callback QueryCallbackFunc) error
	// GetTokenOutputsAndMeta retrieves both the raw token outputs and their metadata for the specified token IDs.
	GetTokenOutputsAndMeta(ctx context.Context, ids []*token.ID) ([][]byte, [][]byte, []token.Format, error)
	// GetTokens returns the de-obfuscated token data for the specified token IDs.
	GetTokens(ctx context.Context, inputs ...*token.ID) ([]*token.Token, error)
	// WhoDeletedTokens identifies which transaction deleted the specified tokens.
	WhoDeletedTokens(ctx context.Context, inputs ...*token.ID) ([]string, []bool, error)
	// Balance calculates the total value of unspent tokens of a specific type owned by a wallet.
	// The result is returned as a *big.Int to support arbitrary precision and prevent overflow.
	Balance(ctx context.Context, id string, tokenType token.Type) (*big.Int, error)
	// IssuedBalance returns the sum of the quantities of the tokens issued by this node,
	// filtered by the passed options. The result is returned as a *big.Int to support
	// arbitrary precision and prevent overflow.
	IssuedBalance(ctx context.Context, opts IssuerBalanceQuery) (*big.Int, error)
	// RedeemedBalance returns the sum of the quantities of the tokens redeemed against
	// an issuer known to this node, filtered by the passed options. The result is
	// returned as a *big.Int to support arbitrary precision and prevent overflow.
	RedeemedBalance(ctx context.Context, opts IssuerBalanceQuery) (*big.Int, error)
}

// IssuerBalanceQuery defines the filtering options for computing issuer balances
// (issued and redeemed sums) over the token store.
type IssuerBalanceQuery struct {
	// TokenType optionally restricts the sum to a single token type. An empty type means all types.
	TokenType token.Type
	// From, when set, restricts the sum to tokens stored at or after this time.
	From *time.Time
	// To, when set, restricts the sum to tokens stored at or before this time.
	To *time.Time
}

//go:generate counterfeiter -o mock/token_vault.go -fake-name TokenVault . TokenVault

type TokenVault interface {
	IsPending(ctx context.Context, id *token.ID) (bool, error)
	GetTokenOutputsAndMeta(ctx context.Context, ids []*token.ID) ([][]byte, [][]byte, []token.Format, error)
	GetTokenOutputs(ctx context.Context, ids []*token.ID, callback QueryCallbackFunc) error
	UnspentTokensIteratorBy(ctx context.Context, id string, tokenType token.Type) (UnspentTokensIterator, error)
	ListHistoryIssuedTokens(ctx context.Context) (*token.IssuedTokens, error)
	PublicParams(ctx context.Context) ([]byte, error)
	Balance(ctx context.Context, id string, tokenType token.Type) (*big.Int, error)
}

//go:generate counterfeiter -o mock/ledger_token.go -fake-name LedgerToken . LedgerToken

type LedgerToken interface {
	GetOwner() []byte
}

//go:generate counterfeiter -o mock/token_certification_storage.go -fake-name TokenCertificationStorage . TokenCertificationStorage

type TokenCertificationStorage interface {
	GetCertifications(ctx context.Context, ids []*token.ID) ([][]byte, error)
}
