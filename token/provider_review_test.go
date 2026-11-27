/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestManagementServiceReviewEvictsBeforeCoreUpdate checks the proposed ordering at
// the provider boundary, including errors and no-ops whose outcome is not yet known.
func TestManagementServiceReviewEvictsBeforeCoreUpdate(t *testing.T) {
	h := &hookTMSProvider{
		get: func(driver.ServiceOptions) (driver.TokenManagerService, error) {
			return newBuildableTMS(), nil
		},
	}
	p := newHookProvider(h)
	id := TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"}
	_, err := p.GetManagementService(WithTMSID(id))
	require.NoError(t, err)
	h.update = func(driver.ServiceOptions) error {
		p.lock.RLock()
		_, cached := p.services[id]
		p.lock.RUnlock()
		assert.False(t, cached, "evict the wrapper before the core can unload its TMS")

		return nil
	}
	require.NoError(t, p.Update(id, []byte("pp")))
}

// TestManagementServiceReviewHungUpdateIsPerTMS checks that an update holding one
// TMS's key lock does not block cached lookups, creation, or updates of other TMSs.
func TestManagementServiceReviewHungUpdateIsPerTMS(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	h := &hookTMSProvider{
		get: func(driver.ServiceOptions) (driver.TokenManagerService, error) {
			return newBuildableTMS(), nil
		},
		update: func(opts driver.ServiceOptions) error {
			if opts.Namespace == "hung" {
				close(entered)
				<-release
			}

			return nil
		},
	}
	p := newHookProvider(h)
	id := func(ns string) TMSID { return TMSID{Network: "n1", Channel: "c1", Namespace: ns} }
	cached, err := p.GetManagementService(WithTMSID(id("cached")))
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- p.Update(id("hung"), []byte("pp")) }()
	t.Cleanup(func() {
		close(release)
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("hung update did not finish after release")
		}
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("update did not start")
	}
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{name: "cached lookup", call: func() error {
			ms, err := p.GetManagementService(WithTMSID(id("cached")))
			assert.Same(t, cached, ms)

			return err
		}},
		{name: "creation", call: func() error {
			_, err := p.GetManagementService(WithTMSID(id("new")))

			return err
		}},
		{name: "update", call: func() error { return p.Update(id("other"), []byte("pp")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := make(chan error, 1)
			go func() { result <- tc.call() }()
			select {
			case err := <-result:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("another TMS is blocked by the hung update")
			}
		})
	}
}

// TestManagementServiceReviewConcurrentLookupsAfterUpdateBuildOnce verifies that
// multiple lookups for a cached TMS wait for its update and share one fresh wrapper.
func TestManagementServiceReviewConcurrentLookupsAfterUpdateBuildOnce(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	old, updated := newBuildableTMS(), newBuildableTMS()
	var replaced atomic.Bool
	var getCalls atomic.Int32
	h := &hookTMSProvider{
		get: func(driver.ServiceOptions) (driver.TokenManagerService, error) {
			getCalls.Add(1)
			if replaced.Load() {
				return updated, nil
			}

			return old, nil
		},
		update: func(driver.ServiceOptions) error {
			close(entered)
			<-release
			replaced.Store(true)

			return nil
		},
	}
	p := newHookProvider(h)
	id := TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"}
	original, err := p.GetManagementService(WithTMSID(id))
	require.NoError(t, err)
	updateErr := make(chan error, 1)
	go func() { updateErr <- p.Update(id, []byte("pp")) }()
	t.Cleanup(func() {
		unblock()
		select {
		case err := <-updateErr:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("update did not finish after release")
		}
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("update did not start")
	}
	type result struct {
		ms  *ManagementService
		err error
	}
	const readers = 3
	results := make(chan result, readers)
	for range readers {
		go func() {
			ms, err := p.GetManagementService(WithTMSID(id))
			results <- result{ms: ms, err: err}
		}()
	}
	// Each reader must either have returned (the regression) or be waiting for the
	// per-key mutex. Observe both states before releasing the update.
	require.Eventually(t, func() bool {
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		waiting := 0
		for g := range strings.SplitSeq(string(buf), "\n\n") {
			if strings.Contains(g, "(*ManagementServiceProvider).managementService") &&
				strings.Contains(g, "(*keyedMutex).lock") && strings.Contains(g, "[sync.") {
				waiting++
			}
		}

		return len(results)+waiting == readers
	}, 5*time.Second, time.Millisecond)
	assert.Empty(t, results, "cached lookups must wait for the update of their TMS")
	unblock()
	var first *ManagementService
	for range readers {
		select {
		case got := <-results:
			require.NoError(t, got.err)
			assert.NotSame(t, original, got.ms, "a lookup must return a fresh wrapper")
			assert.Same(t, updated, got.ms.tms, "the wrapper must reference the updated TMS")
			if first == nil {
				first = got.ms
			} else {
				assert.Same(t, first, got.ms, "waiting readers must share the rebuilt wrapper")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("lookup did not finish after release")
		}
	}
	assert.Equal(t, int32(2), getCalls.Load(), "one initial wrap and one wrap after update")
}
