/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package memory

import (
	driver3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	sqlite2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	mem "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/memory"
)

type Driver sqlite2.Driver

func NewNamedDriver() driver3.NamedDriver {
	return driver3.NamedDriver{
		Name:   mem.Persistence,
		Driver: NewDriver(),
	}
}

func NewDriver() *Driver {
	return (*Driver)(sqlite2.NewDriver(nil))
}

func (d *Driver) NewTokenLock(_ driver2.PersistenceName, params ...string) (driver3.TokenLockStore, error) {
	return ((*sqlite2.Driver)(d)).TokenLock.Get(mem.Op.GetConfig(params...))
}

func (d *Driver) NewWallet(_ driver2.PersistenceName, params ...string) (driver3.WalletStore, error) {
	return ((*sqlite2.Driver)(d)).Wallet.Get(mem.Op.GetConfig(params...))
}

func (d *Driver) NewIdentity(_ driver2.PersistenceName, params ...string) (driver3.IdentityStore, error) {
	return ((*sqlite2.Driver)(d)).Identity.Get(mem.Op.GetConfig(params...))
}

func (d *Driver) NewKeyStore(_ driver2.PersistenceName, params ...string) (driver3.KeyStore, error) {
	return ((*sqlite2.Driver)(d)).KeyStore.Get(mem.Op.GetConfig(params...))
}

func (d *Driver) NewToken(_ driver2.PersistenceName, params ...string) (driver3.TokenStore, error) {
	return ((*sqlite2.Driver)(d)).Token.Get(mem.Op.GetConfig(params...))
}

func (d *Driver) NewAuditTransaction(_ driver2.PersistenceName, params ...string) (driver3.AuditTransactionStore, error) {
	return ((*sqlite2.Driver)(d)).AuditTx.Get(mem.Op.GetConfig(params...))
}

func (d *Driver) NewOwnerTransaction(_ driver2.PersistenceName, params ...string) (driver3.TokenTransactionStore, error) {
	return ((*sqlite2.Driver)(d)).OwnerTx.Get(mem.Op.GetConfig(params...))
}

func (d *Driver) NewEndorser(_ driver2.PersistenceName, params ...string) (driver3.EndorserStore, error) {
	return ((*sqlite2.Driver)(d)).Endorser.Get(mem.Op.GetConfig(params...))
}
