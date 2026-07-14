/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	"github.com/LFDT-Panurus/panurus/token/token"
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"
)

type tokenLockTables struct {
	TokenLocks string
	Tokens     string
	Requests   string
}

type TokenLockStore struct {
	ReadDB  *sql.DB
	WriteDB *sql.DB
	Table   tokenLockTables
	Logger  logging.Logger
	ci      common3.CondInterpreter
}

func newTokenLockStore(readDB, writeDB *sql.DB, tables tokenLockTables, ci common3.CondInterpreter) *TokenLockStore {
	return &TokenLockStore{
		ReadDB:  readDB,
		WriteDB: writeDB,
		Table:   tables,
		Logger:  logger,
		ci:      ci,
	}
}

func NewTokenLockStore(readDB, writeDB *sql.DB, tables TableNames, ci common3.CondInterpreter) (*TokenLockStore, error) {
	return newTokenLockStore(
		readDB,
		writeDB,
		tokenLockTables{
			TokenLocks: tables.TokenLocks,
			Tokens:     tables.Tokens,
			Requests:   tables.Requests,
		},
		ci), nil
}

func (db *TokenLockStore) CreateSchema() error {
	return common.InitSchema(db.WriteDB, []string{db.GetSchema()}...)
}

func (db *TokenLockStore) Lock(ctx context.Context, tokenID *token.ID, consumerTxID transaction.ID) error {
	query, args := q.InsertInto(db.Table.TokenLocks).
		Fields("consumer_tx_id", "tx_id", "idx", "created_at").
		Row(consumerTxID, tokenID.TxId, tokenID.Index, time.Now().UTC()).
		Format()
	logging.Debug(logger, query, tokenID, consumerTxID)
	_, err := db.WriteDB.ExecContext(ctx, query, args...)

	return err
}

func (db *TokenLockStore) UnlockByTxID(ctx context.Context, consumerTxID transaction.ID) error {
	query, args := q.DeleteFrom(db.Table.TokenLocks).
		Where(cond.Eq("consumer_tx_id", consumerTxID)).
		Format(db.ci)
	logging.Debug(logger, query, consumerTxID)

	_, err := db.WriteDB.ExecContext(ctx, query, args...)

	return err
}

func (db *TokenLockStore) GetSchema() string {
	return fmt.Sprintf(`
		-- TokenLocks
		CREATE TABLE IF NOT EXISTS %s (
			tx_id TEXT NOT NULL,
			idx INT NOT NULL,
			consumer_tx_id TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			PRIMARY KEY(tx_id, idx),
			FOREIGN KEY (tx_id, idx) REFERENCES %s
		);`,
		db.Table.TokenLocks,
		db.Table.Tokens,
	)
}

func (db *TokenLockStore) Close() error {
	return common2.Close(db.ReadDB, db.WriteDB)
}

func IsExpiredToken(tokenRequests, tokenLocks common3.Table, leaseExpiry time.Duration) cond.Condition {
	return cond.Or(
		cond.FieldIn(tokenRequests.Field("status"), driver.Deleted, driver.Orphan),
		cond.OlderThan(tokenLocks.Field("created_at"), leaseExpiry),
	)
}
