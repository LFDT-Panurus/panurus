/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token"
)

// blockingCache is a manager cache whose Get builds and tracks a manager the way the real loader
// does, but only once released, so a test can hold a lookup open while Shutdown runs.
type blockingCache struct {
	entered chan struct{}
	release chan struct{}
	build   func() *Manager
}

func (c *blockingCache) Get(*token.ManagementService) (token.SelectorManager, error) {
	close(c.entered)
	<-c.release

	return c.build(), nil
}

func (c *blockingCache) Peek(*token.ManagementService) (token.SelectorManager, bool) {
	return nil, false
}

func (c *blockingCache) Update(*token.ManagementService) (token.SelectorManager, token.SelectorManager, error) {
	return nil, nil, nil
}

func (c *blockingCache) Delete(*token.ManagementService) (token.SelectorManager, bool) {
	return nil, false
}

func (c *blockingCache) Length() int { return 0 }

// TestShutdownWaitsForALookupInFlight forces the interleaving where Shutdown runs while a lookup is
// inside the cache. Shutdown must wait for it: otherwise the lookup finishes on the cache Shutdown just
// discarded and hands its caller a manager that is never stopped by this Shutdown, or, on a cache hit,
// one it already stopped.
func TestShutdownWaitsForALookupInFlight(t *testing.T) {
	svc := &SelectorService{}
	stopped := false
	built := &Manager{cancel: func() { stopped = true }, cleanerDone: make(chan struct{})}
	close(built.cleanerDone)

	old := &blockingCache{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		build: func() *Manager {
			svc.trackManager(built)

			return built
		},
	}
	svc.managerLazyCache = old
	fresh := &blockingCache{entered: make(chan struct{}), release: make(chan struct{})}
	svc.newCache = func() lazyCache { return fresh }

	lookup := make(chan token.SelectorManager, 1)
	go func() {
		m, err := svc.SelectorManager(&token.ManagementService{})
		assert.NoError(t, err)
		lookup <- m
	}()
	<-old.entered

	shutdownDone := make(chan struct{})
	go func() {
		svc.Shutdown()
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
		t.Fatal("Shutdown replaced the cache while a lookup was still inside the old one")
	case <-time.After(100 * time.Millisecond):
	}

	close(old.release)
	require.Same(t, built, <-lookup)
	<-shutdownDone

	assert.True(t, stopped, "the manager built during the lookup was tracked in time to be stopped")
	assert.Same(t, fresh, svc.managerLazyCache, "later lookups go to the new cache")
}
