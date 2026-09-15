/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file implements a high-performance cache using Ristretto v2.
// It provides thread-safe caching with singleflight to prevent duplicate loads.
package cache

import (
	"github.com/dgraph-io/ristretto/v2"
	"golang.org/x/sync/singleflight"
)

const (
	// ZeroCost with this ristretto uses the Cost function defined in its configuration
	ZeroCost = 0

	// Default Ristretto configuration values
	DefaultNumCounters = 1e6 // 1 million
	DefaultMaxCost     = 1e8 // 100 million
	DefaultBufferItems = 64
)

// Config is a shortcut for the ristretto Configuration.
type Config[K ristretto.Key, V any] = ristretto.Config[K, V]

// ristrettoCache is our implementation using Ristretto v2.
type ristrettoCache[T any] struct {
	cache *ristretto.Cache[string, T]
	sfg   singleflight.Group
}

// NewRistrettoCache creates and returns a new ristretto-based cache implementation.
func NewRistrettoCache[T any](config *ristretto.Config[string, T]) (*ristrettoCache[T], error) {
	rCache, err := ristretto.NewCache[string, T](config)
	if err != nil {
		return nil, err
	}

	return &ristrettoCache[T]{
		cache: rCache,
	}, nil
}

func NewDefaultRistrettoCache[T any]() (*ristrettoCache[T], error) {
	return NewRistrettoCacheWithSize[T](DefaultMaxCost)
}

func NewRistrettoCacheWithSize[T any](maxCost int64) (*ristrettoCache[T], error) {
	return NewRistrettoCache[T](&ristretto.Config[string, T]{
		NumCounters: DefaultNumCounters,
		MaxCost:     maxCost,
		BufferItems: DefaultBufferItems,
		Cost: func(value T) int64 {
			return 1
		},
	})
}

func (c *ristrettoCache[T]) Get(key string) (T, bool) {
	return c.cache.Get(key)
}

func (c *ristrettoCache[T]) Add(key string, value T) {
	c.cache.Set(key, value, ZeroCost)
}

func (c *ristrettoCache[T]) Delete(key string) {
	c.cache.Del(key)
}

func (c *ristrettoCache[T]) Clear() {
	c.cache.Clear()
}

// Wait blocks until all buffered writes and deletes are applied to the cache.
// Useful for testing and deterministic cache state assertions.
func (c *ristrettoCache[T]) Wait() {
	c.cache.Wait()
}

func (c *ristrettoCache[T]) GetOrLoad(key string, loader func() (T, error)) (T, bool, error) {
	var zero T

	if value, found := c.Get(key); found {
		return value, true, nil
	}

	// If not found, use singleflight to prevent thundering herds.
	res, err, _ := c.sfg.Do(key, func() (any, error) {
		newValue, loadErr := loader()
		if loadErr != nil {
			return nil, loadErr
		}
		c.Add(key, newValue)

		return newValue, nil
	})
	if err != nil {
		return zero, false, err
	}

	return res.(T), false, nil
}
