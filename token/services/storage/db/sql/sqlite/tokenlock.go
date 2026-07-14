/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"context"
	"time"

	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	common3 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"

	common4 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

type TokenLockStore struct {
	*common4.TokenLockStore
	ci common2.CondInterpreter
}

func IsStale(tokenLocks common2.TableName, requests common2.TableName, leaseExpiry time.Duration) *isStale {
	return &isStale{tokenLocks: tokenLocks, requests: requests, leaseExpiry: leaseExpiry}
}

type isStale struct {
	tokenLocks  common2.TableName
	requests    common2.TableName
	leaseExpiry time.Duration
}

func (c *isStale) WriteString(ci common2.CondInterpreter, sb common2.Builder) {
	tokenLocks, tokenRequests := q.AliasedTable(string(c.tokenLocks), "tl"), q.AliasedTable(string(c.requests), "tr")

	sb.WriteString("tx_id IN (")
	q.Select().
		Fields(tokenLocks.Field("tx_id")).
		From(tokenLocks.Join(tokenRequests, cond.Cmp(tokenLocks.Field("tx_id"), "=", tokenRequests.Field("tx_id")))).
		Where(common4.IsExpiredToken(tokenRequests, tokenLocks, c.leaseExpiry)).
		FormatTo(ci, sb)
	sb.WriteRune(')')
}

func (db *TokenLockStore) Cleanup(ctx context.Context, leaseExpiry time.Duration) error {
	query, args := q.DeleteFrom(db.Table.TokenLocks).
		Where(IsStale(common2.TableName(db.Table.TokenLocks), common2.TableName(db.Table.Requests), leaseExpiry)).
		Format(db.ci)

	db.Logger.Debug(query, args)
	_, err := db.WriteDB.ExecContext(ctx, query, args...)
	if err != nil {
		db.Logger.Errorf("query failed: %s", query)
	}

	return err
}

func NewTokenLockStore(dbs *common3.RWDB, tableNames common4.TableNames) (*TokenLockStore, error) {
	tldb, err := common4.NewTokenLockStore(dbs.ReadDB, dbs.WriteDB, tableNames, NewConditionInterpreter())
	if err != nil {
		return nil, err
	}

	return &TokenLockStore{TokenLockStore: tldb, ci: NewConditionInterpreter()}, nil
}
