/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity

import (
	"github.com/LFDT-Panurus/panurus/token"
	driver2 "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	kvs2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
)

type KVSStorageProvider struct {
	kvs kvs2.KVS
}

func NewKVSStorageProvider(kvs kvs2.KVS) *KVSStorageProvider {
	return &KVSStorageProvider{kvs: kvs}
}

func (s *KVSStorageProvider) WalletStore(tmsID token.TMSID) (driver.WalletStore, error) {
	return kvs2.NewWalletStore(s.kvs, tmsID), nil
}

func (s *KVSStorageProvider) IdentityStore(tmsID token.TMSID) (driver.IdentityStore, error) {
	return kvs2.NewIdentityStore(s.kvs, tmsID), nil
}

func (s *KVSStorageProvider) Keystore(token.TMSID) (driver2.Keystore, error) {
	return kvs2.Keystore(s.kvs), nil
}
