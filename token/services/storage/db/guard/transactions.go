/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package guard

import (
	"context"
	"math/big"

	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	driver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/pagination"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
)

// WrapOwnerTransaction wraps an owner transaction store with the guard policy.
func WrapOwnerTransaction(s driver.TokenTransactionStore, p Policy) driver.TokenTransactionStore {
	if s == nil {
		return s
	}

	return &guardedOwnerTx{TokenTransactionStore: s, policy: p}
}

// WrapAuditTransaction wraps an audit transaction store with the guard policy.
func WrapAuditTransaction(s driver.AuditTransactionStore, p Policy) driver.AuditTransactionStore {
	if s == nil {
		return s
	}

	return &guardedAuditTx{AuditTransactionStore: s, policy: p}
}

type guardedOwnerTx struct {
	driver.TokenTransactionStore
	policy Policy
}

func (g *guardedOwnerTx) QueryTransactions(ctx context.Context, params driver.QueryTransactionsParams, p driver2.Pagination) (*driver2.PageIterator[*driver.TransactionRecord], error) {
	if err := pagination.ValidateLimited(p, g.policy.MaxPageSize); err != nil {
		return nil, err
	}

	return g.TokenTransactionStore.QueryTransactions(ctx, params, p)
}

// QueryTokenRequests is intentionally not row-capped on either transaction store
// and delegates to the embedded store unchanged: QueryTokenRequestsParams carries
// only a status filter, with no page size or cursor, so a caller that hits a cap
// cannot page around it. Bounding this read needs a SQL-level LIMIT on the query
// itself (tracked follow-up). QueryTransactions above is capped instead, because
// it does take a pagination argument, so rejecting an unbounded page is actionable.

func (g *guardedOwnerTx) AddTransactionEndorsementAck(ctx context.Context, txID string, endorser token.Identity, sigma []byte) error {
	if err := CheckPayload("AddTransactionEndorsementAck", len(txID)+len(endorser)+len(sigma), g.policy.MaxPayloadSize); err != nil {
		return err
	}

	return g.TokenTransactionStore.AddTransactionEndorsementAck(ctx, txID, endorser, sigma)
}

func (g *guardedOwnerTx) NewTransactionStoreTransaction() (driver.TransactionStoreTransaction, error) {
	w, err := g.TokenTransactionStore.NewTransactionStoreTransaction()
	if err != nil {
		return nil, err
	}

	return &guardedStoreTx{TransactionStoreTransaction: w, policy: g.policy}, nil
}

type guardedAuditTx struct {
	driver.AuditTransactionStore
	policy Policy
}

func (g *guardedAuditTx) QueryTransactions(ctx context.Context, params driver.QueryTransactionsParams, p driver2.Pagination) (*driver2.PageIterator[*driver.TransactionRecord], error) {
	if err := pagination.ValidateLimited(p, g.policy.MaxPageSize); err != nil {
		return nil, err
	}

	return g.AuditTransactionStore.QueryTransactions(ctx, params, p)
}

func (g *guardedAuditTx) NewTransactionStoreTransaction() (driver.TransactionStoreTransaction, error) {
	w, err := g.AuditTransactionStore.NewTransactionStoreTransaction()
	if err != nil {
		return nil, err
	}

	return &guardedStoreTx{TransactionStoreTransaction: w, policy: g.policy}, nil
}

// guardedStoreTx enforces the write payload limit on the atomic write path used
// by both the owner and audit transaction stores.
type guardedStoreTx struct {
	driver.TransactionStoreTransaction
	policy Policy
}

func (w *guardedStoreTx) AddTransaction(ctx context.Context, records ...driver.TransactionRecord) error {
	// The summed fields mirror the columns the SQL store actually persists for a
	// transaction row (tx_id, sender/recipient EID, token type, amount); the
	// record's metadata maps are stored via AddTokenRequest, which is guarded
	// separately, so they are deliberately not counted here.
	size := 0
	for _, r := range records {
		size += len(r.TxID) + len(r.SenderEID) + len(r.RecipientEID) + len(r.TokenType) + amountLen(r.Amount)
	}
	if err := CheckPayload("AddTransaction", size, w.policy.MaxPayloadSize); err != nil {
		return err
	}

	return w.TransactionStoreTransaction.AddTransaction(ctx, records...)
}

func (w *guardedStoreTx) AddMovement(ctx context.Context, records ...driver.MovementRecord) error {
	size := 0
	for _, r := range records {
		size += len(r.TxID) + len(r.EnrollmentID) + len(r.TokenType) + amountLen(r.Amount)
	}
	if err := CheckPayload("AddMovement", size, w.policy.MaxPayloadSize); err != nil {
		return err
	}

	return w.TransactionStoreTransaction.AddMovement(ctx, records...)
}

func (w *guardedStoreTx) AddTokenRequest(ctx context.Context, txID string, tr []byte, applicationMetadata, publicMetadata map[string][]byte, ppHash tdriver.PPHash) error {
	size := len(tr) + BlobMapSize(applicationMetadata) + BlobMapSize(publicMetadata)
	if err := CheckPayload("AddTokenRequest", size, w.policy.MaxPayloadSize); err != nil {
		return err
	}

	return w.TransactionStoreTransaction.AddTokenRequest(ctx, txID, tr, applicationMetadata, publicMetadata, ppHash)
}

// amountLen returns the decimal-string length of a token amount, treating a nil
// amount as zero-length (the underlying store validates the amount itself).
func amountLen(amount *big.Int) int {
	if amount == nil {
		return 0
	}

	return len(amount.String())
}
