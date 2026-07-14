/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
)

type NamedDriver = driver2.NamedDriver[Driver]

type Config = driver.Config

type Driver interface {
	NewTokenLock(driver.PersistenceName, ...string) (TokenLockStore, error)

	NewWallet(driver.PersistenceName, ...string) (WalletStore, error)

	NewIdentity(driver.PersistenceName, ...string) (IdentityStore, error)

	NewToken(driver.PersistenceName, ...string) (TokenStore, error)

	NewAuditTransaction(driver.PersistenceName, ...string) (AuditTransactionStore, error)

	NewOwnerTransaction(driver.PersistenceName, ...string) (TokenTransactionStore, error)

	NewEndorser(driver.PersistenceName, ...string) (EndorserStore, error)

	NewKeyStore(name driver.PersistenceName, params ...string) (KeyStore, error)
}
