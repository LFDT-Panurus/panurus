/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"context"
	"sync"
)

// Backend interface for key-value storage
type Backend interface {
	Put(ctx context.Context, id string, value any) error
	Get(ctx context.Context, id string, entry any) error
	Delete(ctx context.Context, id string) error
	Close() error
}

// KeyValuePair stores tracking info
type KeyValuePair struct {
	Key   string
	Value any
	Error string
}

// TrackedKVS wraps a Backend and tracks operations
type TrackedKVS struct {
	Backend    Backend
	PutCounter int
	GetCounter int
	PutHistory []KeyValuePair
	GetHistory []KeyValuePair

	mutex sync.RWMutex
}

func NewTrackedMemory() *TrackedKVS {
	backend, err := NewInMemory()
	if err != nil {
		panic(err)
	}

	return &TrackedKVS{
		Backend:    backend,
		PutHistory: []KeyValuePair{},
		GetHistory: []KeyValuePair{},
	}
}

func NewTrackedMemoryFrom(backend Backend) *TrackedKVS {
	return &TrackedKVS{
		Backend:    backend,
		PutHistory: []KeyValuePair{},
		GetHistory: []KeyValuePair{},
	}
}

func (f *TrackedKVS) Put(id string, entry any) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	err := f.Backend.Put(context.Background(), id, entry)
	f.PutCounter++
	f.PutHistory = append(f.PutHistory, KeyValuePair{Key: id, Value: entry, Error: ""})

	return err
}

func (f *TrackedKVS) Get(id string, entry any) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	f.GetCounter++

	errorMsg := ""
	var e any

	err := f.Backend.Get(context.Background(), id, entry)
	if err != nil {
		errorMsg = err.Error()
	} else {
		e = entry
	}

	f.GetHistory = append(f.GetHistory, KeyValuePair{Key: id, Value: e, Error: errorMsg})

	return err
}

func (f *TrackedKVS) Delete(id string) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	return f.Backend.Delete(context.Background(), id)
}

func (f *TrackedKVS) Close() error {
	return f.Backend.Close()
}
