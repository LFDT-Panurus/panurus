/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package locks

import (
	"database/sql"
	"fmt"

	driver3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/postgres"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
	scommon "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Stores groups the store(s) needed by the locks command.
type Stores struct {
	TokenLock driver3.TokenLockStore
	db        *sql.DB
}

// Close closes the underlying database connection.
func (s *Stores) Close() error {
	if err := s.TokenLock.Close(); err != nil {
		return fmt.Errorf("token lock store close: %w", err)
	}

	return nil
}

// NewStores opens the database described by cfg and returns a TokenLockStore
// pointing at the existing schema (no CREATE TABLE is issued).
func NewStores(cfg Config) (*Stores, error) {
	storeCfg := sqlcommon.StorageConfig{
		TableNames: cfg.TableNames,
		SkipPrefix: cfg.SkipPrefix,
	}
	tableNames, err := sqlcommon.GetTableNamesWithConfig(cfg.TablePrefix, storeCfg)
	if err != nil {
		return nil, fmt.Errorf("derive table names: %w", err)
	}

	switch cfg.Driver {
	case "sqlite":
		return newSQLiteStores(cfg.DataSource, tableNames)
	case "postgres":
		return newPostgresStores(cfg.DataSource, tableNames)
	default:
		return nil, fmt.Errorf("unsupported driver: %s", cfg.Driver)
	}
}

func newSQLiteStores(dataSource string, tableNames sqlcommon.TableNames) (*Stores, error) {
	db, err := sql.Open("sqlite", dataSource)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	dbs := &scommon.RWDB{ReadDB: db, WriteDB: db}

	tokenLockStore, err := sqlite.NewTokenLockStore(dbs, tableNames)
	if err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("create sqlite token lock store: %w", err)
	}

	return &Stores{TokenLock: tokenLockStore, db: db}, nil
}

func newPostgresStores(dataSource string, tableNames sqlcommon.TableNames) (*Stores, error) {
	db, err := sql.Open("pgx", dataSource)
	if err != nil {
		return nil, fmt.Errorf("open postgres db: %w", err)
	}

	dbs := &scommon.RWDB{ReadDB: db, WriteDB: db}

	tokenLockStore, err := postgres.NewTokenLockStore(dbs, tableNames)
	if err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("create postgres token lock store: %w", err)
	}

	return &Stores{TokenLock: tokenLockStore, db: db}, nil
}
