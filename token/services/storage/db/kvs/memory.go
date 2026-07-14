/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils"
	mem "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/memory"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
)

func NewInMemory() (KVS, error) {
	k, err := kvs.New(utils.MustGet(mem.NewDriver().NewKVS("")), "", kvs.DefaultCacheSize)
	if err != nil {
		return nil, err
	}

	return &fscKVS{KVS: k}, nil
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
}

func (k *fscKVS) Close() error {
	k.Stop()

	return nil
}
