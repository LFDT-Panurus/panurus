/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests ristretto.go which implements a high-performance cache using Ristretto v2.
// Tests cover basic operations, concurrency, singleflight deduplication, and error handling.
package cache

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddAndGet(t *testing.T) {
	t.Parallel()
	c, err := NewDefaultRistrettoCache[string]()
	require.NoError(t, err)

	key := "pineapple"
	value := "test-value"

	_, found := c.Get(key)
	require.False(t, found)

	c.Add(key, value)
	retrieved, found := c.Get(key)
	require.True(t, found)
	require.Equal(t, value, retrieved)
}

func TestDelete(t *testing.T) {
	t.Parallel()
	c, err := NewDefaultRistrettoCache[int]()
	require.NoError(t, err)

	key := "pineapple"
	value := 123

	c.Add(key, value)
	c.Delete(key)

	_, found := c.Get(key)
	require.False(t, found)
}

func TestGetOrLoad(t *testing.T) {
	t.Parallel()
	c, err := NewDefaultRistrettoCache[string]()
	require.NoError(t, err)

	key := "pineapple"
	expectedValue := "loaded-value"
	loaderCalls := 0

	loader := func() (string, error) {
		loaderCalls++

		return expectedValue, nil
	}

	// 1. First call: should trigger the loader, return the value, and not report a cache hit.
	val, found, err := c.GetOrLoad(key, loader)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, expectedValue, val)
	require.Equal(t, 1, loaderCalls)

	// 2. Second call: should NOT trigger the loader, return the value, and report a cache hit.
	val, found, err = c.GetOrLoad(key, loader)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, expectedValue, val)
	require.Equal(t, 1, loaderCalls)
}

func TestGetOrLoadError(t *testing.T) {
	t.Parallel()
	c, err := NewDefaultRistrettoCache[string]()
	require.NoError(t, err)

	key := "pineapple"
	loaderErr := errors.New("database is down")
	loader := func() (string, error) {
		return "", loaderErr
	}
	// The loader error should be returned.
	_, _, err = c.GetOrLoad(key, loader)
	require.Equal(t, loaderErr, err)

	// The key should not have been added to the cache.
	_, found := c.Get(key)
	require.False(t, found)
}

func TestGetOrLoadConcurrency(t *testing.T) {
	t.Parallel()
	c, err := NewDefaultRistrettoCache[int]()
	require.NoError(t, err)

	key := "pineapple"
	expectedValue := 42
	var loaderCalls atomic.Int32

	loader := func() (int, error) {
		loaderCalls.Add(1)
		// Simulate a slow data source.
		time.Sleep(100 * time.Millisecond)

		return expectedValue, nil
	}

	numGoroutines := 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for range numGoroutines {
		go func() {
			defer wg.Done()
			val, _, loadErr := c.GetOrLoad(key, loader)
			assert.NoError(t, loadErr)
			assert.Equal(t, expectedValue, val)
		}()
	}

	wg.Wait()

	// the loader should have been called exactly once still.
	require.Equal(t, 1, int(loaderCalls.Load()))
}
