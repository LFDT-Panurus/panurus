/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package vault

import (
	"context"
	"runtime/debug"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokendb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/token"
)

// Vault provides access to token storage, query engine, and certification storage.
type Vault struct {
	tmsID   token2.TMSID
	tokenDB *tokendb.StoreService

	queryEngine          *QueryEngine
	certificationStorage driver.CertificationStorage
}

// NewVault creates a vault with the given TMS ID and storage services.
func NewVault(tmsID token2.TMSID, auditdb *auditdb.StoreService, ttxdb *ttxdb.StoreService, tokenDB *tokendb.StoreService) (*Vault, error) {
	return &Vault{
		tmsID:   tmsID,
		tokenDB: tokenDB,
		queryEngine: &QueryEngine{
			StoreService: tokenDB,
			auditDB:      auditdb,
			ttxdb:        ttxdb,
		},
		certificationStorage: &CertificationStorage{StoreService: tokenDB},
	}, nil
}

// QueryEngine returns the vault's query engine for token and transaction queries.
func (v *Vault) QueryEngine() driver.QueryEngine {
	return v.queryEngine
}

// CertificationStorage returns the vault's certification storage.
func (v *Vault) CertificationStorage() driver.CertificationStorage {
	return v.certificationStorage
}

// DeleteTokens removes the specified tokens from storage.
func (v *Vault) DeleteTokens(ctx context.Context, ids ...*token.ID) error {
	return v.tokenDB.DeleteTokens(ctx, string(debug.Stack()), ids...)
}

// QueryEngine provides query operations across token, audit, and transaction databases.
type QueryEngine struct {
	*tokendb.StoreService
	auditDB *auditdb.StoreService
	ttxdb   *ttxdb.StoreService
}

// IsPending checks if the token's transaction is in pending status.
func (q *QueryEngine) IsPending(ctx context.Context, id *token.ID) (bool, error) {
	vd, _, err := q.GetStatus(ctx, id.TxId)
	if err != nil {
		return false, err
	}

	return vd == ttxdb.Pending, nil
}

// GetStatus returns the transaction status, checking ttx database first, then audit database.
func (q *QueryEngine) GetStatus(ctx context.Context, txID string) (driver.TxStatus, string, error) {
	vd, msg, err := q.ttxdb.GetStatus(ctx, txID)
	if err != nil || vd == ttxdb.Unknown {
		vd, msg, err = q.auditDB.GetStatus(ctx, txID)
	}

	return vd, msg, err
}

// IsMine checks if the token belongs to the current wallet.
func (q *QueryEngine) IsMine(ctx context.Context, id *token.ID) (bool, error) {
	if id == nil {
		return false, nil
	}

	return q.StoreService.IsMine(ctx, id.TxId, id.Index)
}

// CertificationStorage manages token certifications in the database.
type CertificationStorage struct {
	*tokendb.StoreService
}

// Exists checks if a certification exists for the given token ID.
func (t *CertificationStorage) Exists(ctx context.Context, id *token.ID) bool {
	return t.ExistsCertification(ctx, id)
}

// Store saves certifications for multiple tokens.
func (t *CertificationStorage) Store(ctx context.Context, certifications map[*token.ID][]byte) error {
	return t.StoreCertifications(ctx, certifications)
}
