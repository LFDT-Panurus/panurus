/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	mem "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/memory"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
)

func NewInMemory() (KVS, error) {
	store := utils.MustGet(mem.NewDriver().NewKVS(""))
	k, err := kvs.New(store, "", kvs.DefaultCacheSize)
	if err != nil {
		return nil, err
	}

	return &fscKVS{KVS: k, store: store}, nil
}

func Keystore(kvs KVS) *kvsAdapter {
	return &kvsAdapter{kvs: kvs}
}

type kvsAdapter struct {
	kvs KVS
}

func (k *kvsAdapter) Put(id string, state any) error {
	return k.kvs.Put(context.Background(), id, state)
}

func (k *kvsAdapter) Get(id string, state any) error {
	return k.kvs.Get(context.Background(), id, state)
}

func (k *kvsAdapter) Close() error {
	return k.kvs.Close()
}

func (k *kvsAdapter) Delete(id string) error {
	return k.kvs.Delete(context.Background(), id)
}

type fscKVS struct {
	*kvs.KVS
	// store is the key-value store backing KVS. It is kept here because kvs.KVS.Stop() only
	// logs a failure to close it, and Close has to report one.
	store driver.KeyValueStore
}

// Close closes the underlying key-value store. It does so directly rather than through
// kvs.KVS.Stop(), which closes the same store but only logs a failure, leaving the caller no way
// to observe it.
func (k *fscKVS) Close() error {
	if err := k.store.Close(); err != nil {
		return errors.Wrap(err, "failed to close the kvs store")
	}

	return nil
}
