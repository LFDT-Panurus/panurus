/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/
package uniqueness

import (
	"context"
	"sync"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// MemoryKVS is an in-memory implementation of the KVS interface.
type MemoryKVS struct {
	mutex sync.RWMutex
	store map[string][]byte
}

// NewMemoryKVS creates a new in-memory KVS.
func NewMemoryKVS() *MemoryKVS {
	return &MemoryKVS{
		store: make(map[string][]byte),
	}
}

// Get retrieves the value for the given key. A missing key is reported as ErrKeyNotFound.
func (m *MemoryKVS) Get(_ context.Context, k string, v any) error {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	val, ok := m.store[k]
	if !ok {
		return errors.Wrapf(ErrKeyNotFound, "key %s not found", k)
	}
	ptr, ok := v.(*[]byte)
	if !ok {
		return errors.Errorf("value must be *[]byte")
	}
	*ptr = val

	return nil
}

// Put stores the value for the given key.
func (m *MemoryKVS) Put(_ context.Context, k string, v any) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	val, ok := v.([]byte)
	if !ok {
		return errors.Errorf("value must be []byte")
	}
	m.store[k] = val

	return nil
}

// NewMemoryService returns a uniqueness service backed by an in-memory KVS.
func NewMemoryService() *Service {
	return &Service{kvs: NewMemoryKVS()}
}
