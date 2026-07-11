/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"strings"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/cache/secondcache"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	fscSqlite "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"

	driver3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

var logger = logging.MustGetLogger()

type configProvider interface {
	GetOpts(name driver2.PersistenceName, params ...string) (*fscSqlite.Config, error)
}

type Driver struct {
	cp               configProvider
	tableNamesConfig common2.TableNamesConfig

	TokenLock lazy.Provider[fscSqlite.Config, *TokenLockStore]
	Wallet    lazy.Provider[fscSqlite.Config, *WalletStore]
	Identity  lazy.Provider[fscSqlite.Config, *IdentityStore]
	Token     lazy.Provider[fscSqlite.Config, *TokenStore]
	AuditTx   lazy.Provider[fscSqlite.Config, *AuditTransactionStore]
	OwnerTx   lazy.Provider[fscSqlite.Config, *OwnerTransactionStore]
	Endorser  lazy.Provider[fscSqlite.Config, *EndorserStore]
	KeyStore  lazy.Provider[fscSqlite.Config, *KeystoreStore]
}

func NewNamedDriver(config driver3.Config, dbProvider fscSqlite.DbProvider) driver3.NamedDriver {
	return driver3.NamedDriver{
		Name:   fscSqlite.Persistence,
		Driver: NewDriverWithDbProvider(config, dbProvider),
	}
}

func NewDriver(config driver3.Config) *Driver {
	return NewDriverWithDbProvider(config, fscSqlite.NewDbProvider())
}

func NewDriverWithDbProvider(config driver3.Config, dbProvider fscSqlite.DbProvider) *Driver {
	tableNamesConfig, err := common2.LoadTableNamesConfig(config)
	if err != nil {
		logger.Warnf("failed to load table name overrides: %v — using defaults", err)
	}

	d := &Driver{
		cp:               fscSqlite.NewConfigProvider(common.NewConfig(config)),
		tableNamesConfig: tableNamesConfig,
	}

	d.TokenLock = newProviderWithKeyMapper(dbProvider, NewTokenLockStore, tableNamesConfig)
	d.Identity = newIdentityStoreProvider(dbProvider, tableNamesConfig)
	d.Wallet = newWalletStoreProvider(d, dbProvider, tableNamesConfig)
	d.Token = newProviderWithKeyMapper(dbProvider, NewTokenStore, tableNamesConfig)
	d.AuditTx = newProviderWithKeyMapper(dbProvider, NewAuditTransactionStore, tableNamesConfig)
	d.OwnerTx = newProviderWithKeyMapper(dbProvider, NewTransactionStore, tableNamesConfig)
	d.Endorser = newProviderWithKeyMapper(dbProvider, NewEndorserStore, tableNamesConfig)
	d.KeyStore = newProviderWithKeyMapper(dbProvider, NewKeystoreStore, tableNamesConfig)

	return d
}

func newIdentityStoreProvider(dbProvider fscSqlite.DbProvider, tableNamesConfig common2.TableNamesConfig) lazy.Provider[fscSqlite.Config, *IdentityStore] {
	return lazy.NewProviderWithKeyMapper(key, func(o fscSqlite.Config) (*IdentityStore, error) {
		opts := fscSqlite.Opts{
			DataSource:      o.DataSource,
			SkipPragmas:     o.SkipPragmas,
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
		tableNames, err := common2.GetTableNamesWithOverrides(o.TablePrefix, tableNamesConfig, o.TableNameParams...)
		if err != nil {
			return nil, err
		}

		p, err := common2.NewIdentityStoreWithNotifier(
			dbs.ReadDB,
			dbs.WriteDB,
			tableNames,
			secondcache.NewTyped[bool](5000),
			secondcache.NewTyped[[]byte](5000),
			NewConditionInterpreter(),
			&fscSqlite.ErrorMapper{},
			nil,
		)
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
func newWalletStoreProvider(d *Driver, dbProvider fscSqlite.DbProvider, tableNamesConfig common2.TableNamesConfig) lazy.Provider[fscSqlite.Config, *WalletStore] {
	return lazy.NewProviderWithKeyMapper(key, func(o fscSqlite.Config) (*WalletStore, error) {
		// Ensure IdentityConfigurations exists before creating Wallets' schema.
		if _, err := d.Identity.Get(o); err != nil {
			return nil, err
		}

		opts := fscSqlite.Opts{
			DataSource:      o.DataSource,
			SkipPragmas:     o.SkipPragmas,
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
		tableNames, err := common2.GetTableNamesWithOverrides(o.TablePrefix, tableNamesConfig, o.TableNameParams...)
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

func (d *Driver) NewTokenLock(name driver2.PersistenceName, params ...string) (driver3.TokenLockStore, error) {
	opts, err := d.cp.GetOpts(name, params...)
	if err != nil {
		return nil, err
	}

	return d.TokenLock.Get(*opts)
}

func (d *Driver) NewWallet(name driver2.PersistenceName, params ...string) (driver3.WalletStore, error) {
	opts, err := d.cp.GetOpts(name, params...)
	if err != nil {
		return nil, err
	}

	return d.Wallet.Get(*opts)
}

func (d *Driver) NewIdentity(name driver2.PersistenceName, params ...string) (driver3.IdentityStore, error) {
	opts, err := d.cp.GetOpts(name, params...)
	if err != nil {
		return nil, err
	}

	return d.Identity.Get(*opts)
}

func (d *Driver) NewKeyStore(name driver2.PersistenceName, params ...string) (driver3.KeyStore, error) {
	opts, err := d.cp.GetOpts(name, params...)
	if err != nil {
		return nil, err
	}

	return d.KeyStore.Get(*opts)
}

func (d *Driver) NewToken(name driver2.PersistenceName, params ...string) (driver3.TokenStore, error) {
	opts, err := d.cp.GetOpts(name, params...)
	if err != nil {
		return nil, err
	}

	return d.Token.Get(*opts)
}

func (d *Driver) NewAuditTransaction(name driver2.PersistenceName, params ...string) (driver3.AuditTransactionStore, error) {
	opts, err := d.cp.GetOpts(name, append(params, "aud")...)
	if err != nil {
		return nil, err
	}

	return d.AuditTx.Get(*opts)
}

func (d *Driver) NewOwnerTransaction(name driver2.PersistenceName, params ...string) (driver3.TokenTransactionStore, error) {
	opts, err := d.cp.GetOpts(name, params...)
	if err != nil {
		return nil, err
	}

	return d.OwnerTx.Get(*opts)
}

func (d *Driver) NewEndorser(name driver2.PersistenceName, params ...string) (driver3.EndorserStore, error) {
	opts, err := d.cp.GetOpts(name, params...)
	if err != nil {
		return nil, err
	}

	return d.Endorser.Get(*opts)
}

func newProviderWithKeyMapper[V common.DBObject](dbProvider fscSqlite.DbProvider, constructor common2.PersistenceConstructor[V], tableNamesConfig common2.TableNamesConfig) lazy.Provider[fscSqlite.Config, V] {
	return lazy.NewProviderWithKeyMapper(key, func(o fscSqlite.Config) (V, error) {
		opts := fscSqlite.Opts{
			DataSource:      o.DataSource,
			SkipPragmas:     o.SkipPragmas,
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
		tableNames, err := common2.GetTableNamesWithOverrides(o.TablePrefix, tableNamesConfig, o.TableNameParams...)
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

func key(k fscSqlite.Config) string {
	return "sqlite" + k.DataSource + k.TablePrefix + strings.Join(k.TableNameParams, "_")
}
