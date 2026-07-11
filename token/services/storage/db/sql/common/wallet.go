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

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"
)

type walletTables struct {
	Wallets                string
	IdentityConfigurations string
}

type WalletStore struct {
	readDB  *sql.DB
	writeDB *sql.DB
	table   walletTables
	ci      common3.CondInterpreter
}

func newWalletStore(readDB, writeDB *sql.DB, tables walletTables, ci common3.CondInterpreter) *WalletStore {
	return &WalletStore{
		readDB:  readDB,
		writeDB: writeDB,
		table:   tables,
		ci:      ci,
	}
}

func NewWalletStore(readDB, writeDB *sql.DB, tables TableNames, ci common3.CondInterpreter) (*WalletStore, error) {
	return newWalletStore(readDB, writeDB, walletTables{
		Wallets:                tables.Wallets,
		IdentityConfigurations: tables.IdentityConfigurations,
	}, ci), nil
}

func (db *WalletStore) CreateSchema() error {
	return common.InitSchema(db.writeDB, []string{db.GetSchema()}...)
}

func (db *WalletStore) GetWalletID(ctx context.Context, identity token.Identity, roleID int) (driver.WalletID, error) {
	idHash := identity.UniqueID()
	query, args := q.Select().
		FieldsByName("wallet_id").
		From(q.Table(db.table.Wallets)).
		Where(cond.And(cond.Eq("identity_hash", idHash), cond.Eq("role_id", roleID))).
		Format(db.ci)

	result, err := common.QueryUniqueContext[driver.WalletID](ctx, db.readDB, query, args...)
	if err != nil {
		return "", errors.Wrapf(err, "failed getting wallet id for identity [%v]", idHash)
	}
	logger.DebugfContext(ctx, "found wallet id for identity [%v]: %v", idHash, result)

	return result, nil
}

func (db *WalletStore) GetWalletIDs(ctx context.Context, roleID int) ([]driver.WalletID, error) {
	query, args := q.SelectDistinct().
		FieldsByName("wallet_id").
		From(q.Table(db.table.Wallets)).
		Where(cond.Eq("role_id", roleID)).
		Format(db.ci)
	logging.Debug(logger, query)
	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	it := common.NewIterator(rows, func(walletID *driver.WalletID) error { return rows.Scan(walletID) })

	return iterators.ReadAllValues(it)
}

func (db *WalletStore) StoreIdentity(ctx context.Context, identity token.Identity, eID string, wID driver.WalletID, roleID int, meta []byte, confID string) error {
	query, args := q.InsertInto(db.table.Wallets).
		Fields("identity_hash", "meta", "wallet_id", "role_id", "created_at", "enrollment_id", "conf_id").
		Row(identity.UniqueID(), meta, wID, roleID, time.Now().UTC(), eID, confID).
		OnConflictDoNothing().
		Format()
	logging.Debug(logger, query)

	_, err := db.writeDB.ExecContext(ctx, query, args...)
	if err != nil {
		return errors.Wrapf(err, "failed storing wallet [%v] for identity [%s]", wID, identity)
	}
	logger.DebugfContext(ctx, "stored wallet [%v] for identity [%s]", wID, identity)

	return nil
}

func (db *WalletStore) GetConfID(ctx context.Context, identity token.Identity) (string, error) {
	idHash := identity.UniqueID()
	query, args := q.Select().
		FieldsByName("conf_id").
		From(q.Table(db.table.Wallets)).
		Where(cond.Eq("identity_hash", idHash)).
		Format(db.ci)

	result, err := common.QueryUniqueContext[string](ctx, db.readDB, query, args...)
	if err != nil {
		return "", errors.Wrapf(err, "failed getting conf id for identity [%v]", idHash)
	}
	logger.DebugfContext(ctx, "found conf id for identity [%v]: %v", idHash, result)

	return result, nil
}

func (db *WalletStore) LoadMeta(ctx context.Context, identity token.Identity, wID driver.WalletID, roleID int) ([]byte, error) {
	idHash := identity.UniqueID()
	query, args := q.Select().
		FieldsByName("meta").
		From(q.Table(db.table.Wallets)).
		Where(cond.And(cond.Eq("identity_hash", idHash), cond.Eq("wallet_id", wID), cond.Eq("role_id", roleID))).
		Format(db.ci)
	result, err := common.QueryUniqueContext[[]byte](ctx, db.readDB, query, args...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed loading meta for id [%v]", idHash)
	}
	logger.DebugfContext(ctx, "loaded meta for id [%v, %v]: %v", identity, idHash, result)

	return result, nil
}

func (db *WalletStore) IdentityExists(ctx context.Context, identity token.Identity, wID driver.WalletID, roleID int) bool {
	idHash := identity.UniqueID()
	query, args := q.Select().
		FieldsByName("wallet_id").
		From(q.Table(db.table.Wallets)).
		Where(cond.And(cond.Eq("identity_hash", idHash), cond.Eq("wallet_id", wID), cond.Eq("role_id", roleID))).
		Format(db.ci)
	result, err := common.QueryUniqueContext[driver.WalletID](ctx, db.readDB, query, args...)
	if err != nil {
		logger.Errorf("failed looking up wallet-identity [%s-%s]: %w", wID, idHash, err)
	}
	logger.DebugfContext(ctx, "found identity for wallet-identity [%v-%v]: %v", wID, idHash, result)

	return result != ""
}

func (db *WalletStore) GetSchema() string {
	return fmt.Sprintf(`
		-- Wallets
		CREATE TABLE IF NOT EXISTS %s (
			identity_hash TEXT NOT NULL,
			wallet_id TEXT NOT NULL,
			meta BYTEA,
            role_id INT NOT NULL,
			enrollment_id TEXT NOT NULL,
			created_at TIMESTAMP,
			conf_id TEXT NOT NULL,
			PRIMARY KEY(identity_hash, wallet_id, role_id),
			FOREIGN KEY(conf_id) REFERENCES %s(conf_id)
		);
		CREATE INDEX IF NOT EXISTS idx_identity_hash_and_role%s ON %s ( identity_hash, role_id );
		CREATE INDEX IF NOT EXISTS idx_role_id_%s ON %s ( role_id );
		CREATE INDEX IF NOT EXISTS idx_conf_id_%s ON %s ( conf_id )
		`,
		db.table.Wallets, db.table.IdentityConfigurations,
		db.table.Wallets, db.table.Wallets,
		db.table.Wallets, db.table.Wallets,
		db.table.Wallets, db.table.Wallets,
	)
}

func (db *WalletStore) Close() error {
	return common2.Close(db.readDB, db.writeDB)
}
