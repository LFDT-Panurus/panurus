/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"

	driver3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	fscPostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
)

// configProvider defines the interface for retrieving database configuration.
type configProvider interface {
	// GetOpts returns the Postgres configuration for the given persistence name and parameters.
	GetOpts(name driver2.PersistenceName, params ...string) (*fscPostgres.Config, error)
}

// Driver implements the token storage driver for Postgres.
type Driver struct {
	cp               configProvider
	tableNamesConfig common3.TableNamesConfig

	// Lazy providers for various store types to ensure they are initialized only when needed.
	TokenLock lazy.Provider[fscPostgres.Config, *TokenLockStore]
	Wallet    lazy.Provider[fscPostgres.Config, *WalletStore]
	Identity  lazy.Provider[fscPostgres.Config, *IdentityStore]
	Token     lazy.Provider[fscPostgres.Config, *TokenStore]
	AuditTx   lazy.Provider[fscPostgres.Config, *AuditTransactionStore]
	OwnerTx   lazy.Provider[fscPostgres.Config, *TransactionStore]
	Endorser  lazy.Provider[fscPostgres.Config, *EndorserStore]
	KeyStore  lazy.Provider[fscPostgres.Config, *KeystoreStore]
}

// NewNamedDriver returns a NamedDriver for Postgres.
func NewNamedDriver(config driver3.Config, dbProvider fscPostgres.DbProvider) driver3.NamedDriver {
	return driver3.NamedDriver{
		Name:   fscPostgres.Persistence,
		Driver: NewDriverWithDbProvider(config, dbProvider),
	}
}

// NewDriver returns a new Driver for Postgres using the default database provider.
func NewDriver(config driver3.Config) *Driver {
	return NewDriverWithDbProvider(config, fscPostgres.NewDbProvider())
}

// NewDriverWithDbProvider returns a new Driver for Postgres using the given database provider.
func NewDriverWithDbProvider(config driver3.Config, dbProvider fscPostgres.DbProvider) *Driver {
	tableNamesConfig, err := common3.LoadTableNamesConfig(config)
	if err != nil {
		logger.Warnf("failed to load table name overrides: %v — using defaults", err)
	}

	d := &Driver{
		cp:               fscPostgres.NewConfigProvider(common.NewConfig(config)),
		tableNamesConfig: tableNamesConfig,
	}

	d.TokenLock = newProviderWithKeyMapper(dbProvider, NewTokenLockStore, "tokenlock", tableNamesConfig)
	d.Identity = newIdentityStoreProvider(dbProvider, tableNamesConfig)
	d.Wallet = newWalletStoreProvider(d, dbProvider, tableNamesConfig)
	d.Token = newTokenStoreProvider(dbProvider, tableNamesConfig)
	d.AuditTx = newProviderWithKeyMapper(dbProvider, NewAuditTransactionStore, "audittx", tableNamesConfig)
	d.OwnerTx = newTransactionStoreProvider(dbProvider, tableNamesConfig)
	d.Endorser = newEndorserStoreProvider(dbProvider, tableNamesConfig)
	d.KeyStore = newProviderWithKeyMapper(dbProvider, NewKeystoreStore, "keystore", tableNamesConfig)

	return d
}

// newTokenStoreProvider returns a lazy provider for TokenStore.
func newTokenStoreProvider(dbProvider fscPostgres.DbProvider, tableNamesConfig common3.TableNamesConfig) lazy.Provider[fscPostgres.Config, *TokenStore] {
	return lazy.NewProviderWithKeyMapper(key, func(o fscPostgres.Config) (*TokenStore, error) {
		opts := fscPostgres.Opts{
			DataSource:      o.DataSource,
			MaxOpenConns:    o.MaxOpenConns,
			MaxIdleConns:    *o.MaxIdleConns,
			MaxIdleTime:     *o.MaxIdleTime,
			TablePrefix:     o.TablePrefix,
			TableNameParams: o.TableNameParams,
			Tracing:         o.Tracing,
		}
		dbs, err := dbProvider.Get(opts)
		if err != nil {
			return nil, err
		}
		tableNames, err := common3.GetTableNamesWithOverrides(o.TablePrefix, tableNamesConfig, o.TableNameParams...)
		if err != nil {
			return nil, err
		}

		// notifier
		notifier, err := NewTokenNotifier(dbs, tableNames, o.DataSource)
		if err != nil {
			return nil, err
		}
		if o.SkipCreateTable {
			notifier.skipSchemaManagement()
		}

		// db
		p, err := NewTokenStoreWithNotifier(dbs, tableNames, notifier)
		if err != nil {
			return nil, err
		}
		if !o.SkipCreateTable {
			if err := p.CreateSchema(); err != nil {
				return nil, err
			}
		}

		return p, nil
	})
}

// newIdentityStoreProvider returns a lazy provider for IdentityStore.
func newIdentityStoreProvider(dbProvider fscPostgres.DbProvider, tableNamesConfig common3.TableNamesConfig) lazy.Provider[fscPostgres.Config, *IdentityStore] {
	return lazy.NewProviderWithKeyMapper(key, func(o fscPostgres.Config) (*IdentityStore, error) {
		opts := fscPostgres.Opts{
			DataSource:      o.DataSource,
			MaxOpenConns:    o.MaxOpenConns,
			MaxIdleConns:    *o.MaxIdleConns,
			MaxIdleTime:     *o.MaxIdleTime,
			TablePrefix:     o.TablePrefix,
			TableNameParams: o.TableNameParams,
			Tracing:         o.Tracing,
		}
		dbs, err := dbProvider.Get(opts)
		if err != nil {
			return nil, err
		}
		tableNames, err := common3.GetTableNamesWithOverrides(o.TablePrefix, tableNamesConfig, o.TableNameParams...)
		if err != nil {
			return nil, err
		}

		// Create identity store with notifier (includes advisory lock)
		p, err := NewIdentityStore(dbs, tableNames, o.DataSource)
		if err != nil {
			return nil, err
		}
		if o.SkipCreateTable {
			p.notifier.skipSchemaManagement()
		}

		if !o.SkipCreateTable {
			if err := p.CreateSchema(); err != nil {
				return nil, err
			}
		}

		return p, nil
	})
}

// newTransactionStoreProvider returns a lazy provider for TransactionStore with notifier support.
func newTransactionStoreProvider(dbProvider fscPostgres.DbProvider, tableNamesConfig common3.TableNamesConfig) lazy.Provider[fscPostgres.Config, *TransactionStore] {
	return lazy.NewProviderWithKeyMapper(key, func(o fscPostgres.Config) (*TransactionStore, error) {
		opts := fscPostgres.Opts{
			DataSource:      o.DataSource,
			MaxOpenConns:    o.MaxOpenConns,
			MaxIdleConns:    *o.MaxIdleConns,
			MaxIdleTime:     *o.MaxIdleTime,
			TablePrefix:     o.TablePrefix,
			TableNameParams: o.TableNameParams,
			Tracing:         o.Tracing,
		}
		dbs, err := dbProvider.Get(opts)
		if err != nil {
			return nil, err
		}
		tableNames, err := common3.GetTableNamesWithOverrides(o.TablePrefix, tableNamesConfig, o.TableNameParams...)
		if err != nil {
			return nil, err
		}

		// notifier
		notifier, err := NewTransactionNotifier(dbs, tableNames, o.DataSource)
		if err != nil {
			return nil, err
		}
		if o.SkipCreateTable {
			notifier.skipSchemaManagement()
		}

		// db
		p, err := NewTransactionStoreWithNotifier(dbs, tableNames, notifier)
		if err != nil {
			return nil, err
		}
		if !o.SkipCreateTable {
			if err := p.CreateSchema(); err != nil {
				return nil, err
			}
		}

		return p, nil
	})
}

// newWalletStoreProvider returns a lazy provider for WalletStore. Wallets carries a hard
// FOREIGN KEY to IdentityConfigurations (conf_id), so its schema must be created after the
// Identity store's — lazy providers otherwise offer no ordering guarantee across stores.
func newWalletStoreProvider(d *Driver, dbProvider fscPostgres.DbProvider, tableNamesConfig common3.TableNamesConfig) lazy.Provider[fscPostgres.Config, *WalletStore] {
	return lazy.NewProviderWithKeyMapper(key, func(o fscPostgres.Config) (*WalletStore, error) {
		// Ensure IdentityConfigurations exists before creating Wallets' schema.
		if _, err := d.Identity.Get(o); err != nil {
			return nil, err
		}

		opts := fscPostgres.Opts{
			DataSource:      o.DataSource,
			MaxOpenConns:    o.MaxOpenConns,
			MaxIdleConns:    *o.MaxIdleConns,
			MaxIdleTime:     *o.MaxIdleTime,
			TablePrefix:     o.TablePrefix,
			TableNameParams: o.TableNameParams,
			Tracing:         o.Tracing,
		}
		dbs, err := dbProvider.Get(opts)
		if err != nil {
			return nil, err
		}
		tableNames, err := common3.GetTableNamesWithOverrides(o.TablePrefix, tableNamesConfig, o.TableNameParams...)
		if err != nil {
			return nil, err
		}
		p, err := NewWalletStore(dbs, tableNames)
		if err != nil {
			return nil, err
		}
		if !o.SkipCreateTable {
			if err := p.CreateSchema(); err != nil {
				return nil, err
			}
		}

		return p, nil
	})
}

// NewTokenLock returns a new TokenLockStore.
func (d *Driver) NewTokenLock(name driver2.PersistenceName, params ...string) (driver3.TokenLockStore, error) {
	opts, err := d.cp.GetOpts(name, params...)
	if err != nil {
		return nil, err
	}

	return d.TokenLock.Get(*opts)
}

// NewWallet returns a new WalletStore.
func (d *Driver) NewWallet(name driver2.PersistenceName, params ...string) (driver3.WalletStore, error) {
	opts, err := d.cp.GetOpts(name, params...)
	if err != nil {
		return nil, err
	}

	return d.Wallet.Get(*opts)
}

// NewIdentity returns a new IdentityStore.
func (d *Driver) NewIdentity(name driver2.PersistenceName, params ...string) (driver3.IdentityStore, error) {
	opts, err := d.cp.GetOpts(name, params...)
	if err != nil {
		return nil, err
	}

	return d.Identity.Get(*opts)
}

// NewKeyStore returns a new KeyStoreStore.
func (d *Driver) NewKeyStore(name driver2.PersistenceName, params ...string) (driver3.KeyStore, error) {
	opts, err := d.cp.GetOpts(name, params...)
	if err != nil {
		return nil, err
	}

	return d.KeyStore.Get(*opts)
}

// NewToken returns a new TokenStore.
func (d *Driver) NewToken(name driver2.PersistenceName, params ...string) (driver3.TokenStore, error) {
	opts, err := d.cp.GetOpts(name, params...)
	if err != nil {
		return nil, err
	}

	return d.Token.Get(*opts)
}

// NewAuditTransaction returns a new AuditTransactionStore.
func (d *Driver) NewAuditTransaction(name driver2.PersistenceName, params ...string) (driver3.AuditTransactionStore, error) {
	opts, err := d.cp.GetOpts(name, append(params, "aud")...)
	if err != nil {
		return nil, err
	}

	return d.AuditTx.Get(*opts)
}

// NewOwnerTransaction returns a new TokenTransactionStore.
func (d *Driver) NewOwnerTransaction(name driver2.PersistenceName, params ...string) (driver3.TokenTransactionStore, error) {
	opts, err := d.cp.GetOpts(name, params...)
	if err != nil {
		return nil, err
	}

	return d.OwnerTx.Get(*opts)
}

// NewEndorser returns a new EndorserStore.
func (d *Driver) NewEndorser(name driver2.PersistenceName, params ...string) (driver3.EndorserStore, error) {
	opts, err := d.cp.GetOpts(name, params...)
	if err != nil {
		return nil, err
	}

	return d.Endorser.Get(*opts)
}

// newEndorserStoreProvider returns a lazy provider for EndorserStore.
func newEndorserStoreProvider(dbProvider fscPostgres.DbProvider, tableNamesConfig common3.TableNamesConfig) lazy.Provider[fscPostgres.Config, *EndorserStore] {
	return lazy.NewProviderWithKeyMapper(key, func(o fscPostgres.Config) (*EndorserStore, error) {
		opts := fscPostgres.Opts{
			DataSource:      o.DataSource,
			MaxOpenConns:    o.MaxOpenConns,
			MaxIdleConns:    *o.MaxIdleConns,
			MaxIdleTime:     *o.MaxIdleTime,
			TablePrefix:     o.TablePrefix,
			TableNameParams: o.TableNameParams,
			Tracing:         o.Tracing,
		}
		dbs, err := dbProvider.Get(opts)
		if err != nil {
			return nil, err
		}
		tableNames, err := common3.GetTableNamesWithOverrides(o.TablePrefix, tableNamesConfig, o.TableNameParams...)
		if err != nil {
			return nil, err
		}

		// Create endorser store (reuses validation table from transaction store)
		p, err := NewEndorserStore(dbs, tableNames)
		if err != nil {
			return nil, err
		}

		// Create schema if needed
		if !o.SkipCreateTable {
			if err := p.CreateSchema(); err != nil {
				return nil, err
			}
		}

		return p, nil
	})
}

// newProviderWithKeyMapper returns a lazy provider for a DB object using a common constructor.
func newProviderWithKeyMapper[V common.DBObject](dbProvider fscPostgres.DbProvider, constructor common3.PersistenceConstructor[V], storeType string, tableNamesConfig common3.TableNamesConfig) lazy.Provider[fscPostgres.Config, V] {
	return lazy.NewProviderWithKeyMapper(key, func(o fscPostgres.Config) (V, error) {
		opts := fscPostgres.Opts{
			DataSource:      o.DataSource,
			MaxOpenConns:    o.MaxOpenConns,
			MaxIdleConns:    *o.MaxIdleConns,
			MaxIdleTime:     *o.MaxIdleTime,
			TablePrefix:     o.TablePrefix,
			TableNameParams: o.TableNameParams,
			Tracing:         o.Tracing,
		}
		dbs, err := dbProvider.Get(opts)
		if err != nil {
			return utils.Zero[V](), err
		}
		tableNames, err := common3.GetTableNamesWithOverrides(o.TablePrefix, tableNamesConfig, o.TableNameParams...)
		if err != nil {
			return utils.Zero[V](), err
		}
		p, err := constructor(dbs, tableNames)
		if err != nil {
			return utils.Zero[V](), err
		}
		if !o.SkipCreateTable {
			if err := p.CreateSchema(); err != nil {
				return utils.Zero[V](), err
			}
		}

		return p, nil
	})
}

// key returns a unique key for the given Postgres configuration.
func key(k fscPostgres.Config) string {
	return "postgres" + k.DataSource + k.TablePrefix + strings.Join(k.TableNameParams, "_")
}

// createTableLockID generates a deterministic lock ID for a store type.
// Uses SHA256 hash of the store type name, converted to int64.
// This ensures the same store type always gets the same lock ID across replicas.
func createTableLockID(storeType string) int64 {
	h := sha256.Sum256([]byte(storeType))

	return int64(binary.BigEndian.Uint64(h[:])) //nolint:gosec
}

// prefixSchemaWithLock prefixes SQL schema with a PostgreSQL advisory lock statement.
// The lock is transaction-scoped (pg_advisory_xact_lock) and automatically released on commit/rollback.
// This prevents conflicts when multiple replicas attempt to create the same tables simultaneously.
func prefixSchemaWithLock(schema string, lockID int64) string {
	return fmt.Sprintf("SELECT pg_advisory_xact_lock(%d);\n%s", lockID, schema)
}
