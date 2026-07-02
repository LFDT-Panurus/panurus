/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package signers

import (
	"database/sql"
	"fmt"

	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/postgres"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
	scommon "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	fscPostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	fscSqlite "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Stores groups the two stores needed by the signers command.
type Stores struct {
	Identity *sqlcommon.IdentityStore
	Token    *sqlcommon.TokenStore
}

// Close closes both underlying store connections.
func (s *Stores) Close() error {
	var errs []error
	if err := s.Identity.Close(); err != nil {
		errs = append(errs, fmt.Errorf("identity store close: %w", err))
	}
	if err := s.Token.Close(); err != nil {
		errs = append(errs, fmt.Errorf("token store close: %w", err))
	}
	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}

	return nil
}

// NewStores opens the database described by cfg and returns an IdentityStore
// and a TokenStore pointing at the existing schema (no CREATE TABLE is issued).
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

	identityStore, err := sqlcommon.NewNoCacheIdentityStore(
		dbs.ReadDB, dbs.WriteDB,
		tableNames,
		sqlite.NewConditionInterpreter(),
		&fscSqlite.ErrorMapper{},
	)
	if err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("create sqlite identity store: %w", err)
	}

	tokenStore, err := sqlite.NewTokenStore(dbs, tableNames)
	if err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("create sqlite token store: %w", err)
	}

	return &Stores{Identity: identityStore, Token: tokenStore}, nil
}

func newPostgresStores(dataSource string, tableNames sqlcommon.TableNames) (*Stores, error) {
	db, err := sql.Open("pgx", dataSource)
	if err != nil {
		return nil, fmt.Errorf("open postgres db: %w", err)
	}

	dbs := &scommon.RWDB{ReadDB: db, WriteDB: db}

	identityStore, err := sqlcommon.NewNoCacheIdentityStore(
		dbs.ReadDB, dbs.WriteDB,
		tableNames,
		postgres.NewConditionInterpreter(),
		&fscPostgres.ErrorMapper{},
	)
	if err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("create postgres identity store: %w", err)
	}

	// For the postgres token store we use the base common constructor directly
	// (no notifier needed for read-only diagnostic use).
	tokenStore, err := sqlcommon.NewTokenStoreWithNotifier(
		dbs.ReadDB, dbs.WriteDB,
		tableNames,
		postgres.NewConditionInterpreter(),
		nil,
	)
	if err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("create postgres token store: %w", err)
	}

	return &Stores{Identity: identityStore, Token: tokenStore}, nil
}
