/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file adapts fabric-smart-client's generic second-chance cache.Map to the
// Get/Add/Delete/GetOrLoad shape this package's cache consumers expect, the same shape the
// fabric-smart-client secondcache package exposed before it was removed upstream.
package cache

import (
	fsccache "github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/cache"
	"golang.org/x/sync/singleflight"
)

// SecondChanceCache adapts fabric-smart-client's cache.Map to the Get/Add/Delete/GetOrLoad shape
// used by this package's consumers.
type SecondChanceCache[T any] struct {
	m   fsccache.Map[string, T]
	sfg singleflight.Group
}

// NewSecondChanceCache returns a cache holding at most size entries, evicting buffer of them per
// victim scan once full. See fabric-smart-client's cache.NewSecondChanceCache for the eviction
// policy.
func NewSecondChanceCache[T any](size, buffer int) *SecondChanceCache[T] {
	return &SecondChanceCache[T]{m: fsccache.NewSecondChanceCache[string, T](size, buffer, nil)}
}

func (c *SecondChanceCache[T]) Get(key string) (T, bool) {
	return c.m.Get(key)
}

func (c *SecondChanceCache[T]) Add(key string, value T) {
	c.m.Put(key, value)
}

func (c *SecondChanceCache[T]) Delete(key string) {
	c.m.Delete(key)
}

// GetOrLoad returns the cached value for key, or loads it via loader on a miss. Concurrent misses
// for the same key are collapsed via singleflight so loader runs once.
func (c *SecondChanceCache[T]) GetOrLoad(key string, loader func() (T, error)) (T, bool, error) {
	if value, ok := c.Get(key); ok {
		return value, true, nil
	}

	res, err, _ := c.sfg.Do(key, func() (any, error) {
		value, loadErr := loader()
		if loadErr != nil {
			return nil, loadErr
		}
		c.Add(key, value)

		return value, nil
	})
	if err != nil {
		var zero T

		return zero, false, err
	}

	return res.(T), false, nil
}
