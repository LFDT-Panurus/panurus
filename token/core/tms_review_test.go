/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package core_test

import (
	"crypto/sha256"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	tokenmock "github.com/LFDT-Panurus/panurus/token/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reviewNormalizer leaves the explicit TMS coordinates unchanged.
type reviewNormalizer struct{}

func (reviewNormalizer) Normalize(opts *token.ServiceOptions) (*token.ServiceOptions, error) {
	return opts, nil
}

// reviewContext exercises the real core and management providers together, mocking only
// driver construction and the vault. Different parameter hashes identify the wrapped TMS.
type reviewContext struct {
	core        *ppFallbackContext
	management  *token.ManagementServiceProvider
	id          token.TMSID
	running     []byte
	replacement []byte
	updated     *drivermock.TokenManagerService
	original    *token.ManagementService
}

func newReviewContext(t *testing.T) *reviewContext {
	t.Helper()
	c := newPPFallbackContext(t)
	r := &reviewContext{
		core:        c,
		id:          token.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"},
		running:     c.pubParams(t, "ok-running"),
		replacement: c.pubParams(t, "ok-replacement"),
		updated:     &drivermock.TokenManagerService{},
	}
	for _, entry := range []struct {
		service *drivermock.TokenManagerService
		params  []byte
	}{{c.tms, r.running}, {r.updated, r.replacement}} {
		digest := sha256.Sum256(entry.params)
		ppm := &drivermock.PublicParamsManager{}
		ppm.PublicParamsHashReturns(digest[:])
		entry.service.PublicParamsManagerReturns(ppm)
	}
	vault := &tokenmock.VaultProvider{}
	vault.VaultReturns(&drivermock.Vault{}, nil)
	r.management = token.NewManagementServiceProvider(c.provider, reviewNormalizer{}, vault, nil, nil)
	var err error
	r.original, err = r.management.GetManagementService(token.WithTMSID(r.id), token.WithPublicParameter(r.running))
	require.NoError(t, err)
	c.driver.NewTokenServiceReturns(r.updated, nil)

	return r
}

func (r *reviewContext) get() (*token.ManagementService, error) {
	return r.management.GetManagementService(token.WithTMSID(r.id))
}

func requireWrappedParams(t *testing.T, ms *token.ManagementService, params []byte) {
	t.Helper()
	require.NotNil(t, ms)
	digest := sha256.Sum256(params)
	assert.Equal(t, digest[:], []byte(ms.PublicParametersManager().PublicParamsHash()))
}

// TestTMSReviewUnloadFailureKeepsReplacement checks the core cache independently of the
// outer cache: Done can fail after replacement, and that error must not restore the old TMS.
func TestTMSReviewUnloadFailureKeepsReplacement(t *testing.T) {
	r := newReviewContext(t)
	unloadErr := errors.New("wallet unload failed")
	r.core.tms.DoneReturns(unloadErr)

	err := r.core.provider.Update(newPPFallbackOpts(r.core, r.replacement))
	require.ErrorIs(t, err, unloadErr)
	assert.Contains(t, err.Error(), "failed to unload token service")
	current, err := r.core.provider.GetTokenManagerService(newPPFallbackOpts(r.core, nil))
	require.NoError(t, err)
	assert.Same(t, r.updated, current)
	assert.Equal(t, 1, r.core.tms.DoneCallCount())
	assert.Equal(t, 2, r.core.driver.NewTokenServiceCallCount())
}

// TestTMSReviewManagementCacheAfterUnloadFailure reproduces Akram's second comment:
// an error after the core swap must not leave the old management wrapper cached indefinitely.
func TestTMSReviewManagementCacheAfterUnloadFailure(t *testing.T) {
	r := newReviewContext(t)
	unloadErr := errors.New("wallet unload failed")
	r.core.tms.DoneReturns(unloadErr)

	err := r.management.Update(r.id, r.replacement)
	require.ErrorIs(t, err, unloadErr)
	assert.Contains(t, err.Error(), "failed updating tms")
	current, err := r.core.provider.GetTokenManagerService(newPPFallbackOpts(r.core, nil))
	require.NoError(t, err)
	require.Same(t, r.updated, current, "the update error occurs after the core swap")
	ms, err := r.get()
	require.NoError(t, err)
	assert.NotSame(t, r.original, ms, "the unloaded TMS's wrapper must be evicted")
	requireWrappedParams(t, ms, r.replacement)
	again, err := r.get()
	require.NoError(t, err)
	assert.Same(t, ms, again, "the fresh wrapper must be cached")
	assert.Equal(t, 2, r.core.driver.NewTokenServiceCallCount(), "wrapping must reuse the replacement")
	assert.Zero(t, r.core.fetcher.FetchCallCount())
}

// TestTMSReviewRejectedUpdateKeepsLiveCoreService checks that early eviction must apply
// only to wrappers: invalid parameters and construction failures leave the old core TMS live.
func TestTMSReviewRejectedUpdateKeepsLiveCoreService(t *testing.T) {
	for _, name := range []string{"empty", "unknown driver", "malformed", "construction failure"} {
		t.Run(name, func(t *testing.T) {
			r := newReviewContext(t)
			params := r.replacement
			var constructionErr error
			switch name {
			case "empty":
				params = nil
			case "unknown driver":
				params = r.core.unknownDriverPubParams(t)
			case "malformed":
				params = r.core.pubParams(t, "malformed")
			case "construction failure":
				constructionErr = errors.New("cannot open wallet store")
				r.core.driver.NewTokenServiceReturns(nil, constructionErr)
			}
			err := r.management.Update(r.id, params)
			require.Error(t, err)
			if constructionErr != nil {
				require.ErrorIs(t, err, constructionErr)
			}
			current, err := r.core.provider.GetTokenManagerService(newPPFallbackOpts(r.core, nil))
			require.NoError(t, err)
			assert.Same(t, r.core.tms, current)
			assert.Zero(t, r.core.tms.DoneCallCount())
			ms, err := r.get()
			require.NoError(t, err)
			requireWrappedParams(t, ms, r.running)
			again, err := r.get()
			require.NoError(t, err)
			assert.Same(t, ms, again)
			builds := 1
			if constructionErr != nil {
				builds++
			}
			assert.Equal(t, builds, r.core.driver.NewTokenServiceCallCount())
			assert.Zero(t, r.core.fetcher.FetchCallCount(), "failed updates and rewrapping must not fetch")
			assert.Zero(t, r.core.storage.PublicParamsCallCount(), "updates must not fall back to storage")
		})
	}
}

// TestTMSReviewIdenticalUpdateReusesCoreService checks that invalidating a wrapper on a
// no-op update neither recreates the driver nor resolves public parameters from the ledger.
func TestTMSReviewIdenticalUpdateReusesCoreService(t *testing.T) {
	r := newReviewContext(t)
	require.NoError(t, r.management.Update(r.id, r.running))
	ms, err := r.get()
	require.NoError(t, err)
	requireWrappedParams(t, ms, r.running)
	assert.NotSame(t, r.original, ms, "the outer cache is invalidated even on a core no-op")
	assert.Equal(t, 1, r.core.driver.NewTokenServiceCallCount())
	assert.Zero(t, r.core.tms.DoneCallCount())
	assert.Zero(t, r.core.fetcher.FetchCallCount())
}

// TestTMSReviewAlreadyHeldWrapperIsNotRefreshed pins the existing lifetime limitation:
// replacing a cache entry does not rewrite a wrapper obtained before the update.
func TestTMSReviewAlreadyHeldWrapperIsNotRefreshed(t *testing.T) {
	r := newReviewContext(t)
	require.NoError(t, r.management.Update(r.id, r.replacement))
	ms, err := r.get()
	require.NoError(t, err)
	requireWrappedParams(t, ms, r.replacement)
	requireWrappedParams(t, r.original, r.running)
	assert.Equal(t, 1, r.core.tms.DoneCallCount(), "the old wrapper does not prevent unload")
}

// TestTMSReviewManagementLookupDuringUnload reproduces Akram's first comment through
// both real providers: the core has swapped, but the cached management wrapper is still old.
func TestTMSReviewManagementLookupDuringUnload(t *testing.T) {
	r := newReviewContext(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	r.core.tms.DoneStub = func() error {
		close(entered)
		<-release

		return nil
	}
	updateErr := make(chan error, 1)
	go func() { updateErr <- r.management.Update(r.id, r.replacement) }()
	t.Cleanup(func() {
		unblock()
		select {
		case err := <-updateErr:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("update did not finish after releasing unload")
		}
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("update did not reach unload")
	}
	current, err := r.core.provider.GetTokenManagerService(newPPFallbackOpts(r.core, nil))
	require.NoError(t, err)
	require.Same(t, r.updated, current)
	type result struct {
		ms  *token.ManagementService
		err error
	}
	results := make(chan result, 1)
	go func() {
		ms, err := r.get()
		results <- result{ms: ms, err: err}
	}()
	// Wait for observable completion or a mutex wait, rather than relying on a sleep
	// to assume that the lookup has run. The expected fix makes it wait for this TMS.
	require.Eventually(t, func() bool {
		if len(results) != 0 {
			return true
		}
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		for g := range strings.SplitSeq(string(buf), "\n\n") {
			if strings.Contains(g, "(*ManagementServiceProvider).managementService") &&
				strings.Contains(g, "(*keyedMutex).lock") && strings.Contains(g, "[sync.") {
				return true
			}
		}

		return false
	}, 5*time.Second, time.Millisecond)
	assert.Empty(t, results, "lookup must wait instead of returning the wrapper being unloaded")
	unblock()
	select {
	case got := <-results:
		require.NoError(t, got.err)
		requireWrappedParams(t, got.ms, r.replacement)
	case <-time.After(5 * time.Second):
		t.Fatal("lookup did not finish after releasing unload")
	}
}

// TestTMSReviewPostInitCanOverlapAcrossKeys checks the callback scheduling change:
// initialization of one TMS does not serialize the callback for another TMS.
func TestTMSReviewPostInitCanOverlapAcrossKeys(t *testing.T) {
	c := newPPFallbackContext(t)
	entered := make(chan string, 2)
	release := make(chan struct{})
	c.provider.SetCallback(func(_ driver.TokenManagerService, _, _, namespace string) error {
		entered <- namespace
		<-release

		return nil
	})
	results := make(chan error, 2)
	t.Cleanup(func() {
		close(release)
		for range 2 {
			select {
			case err := <-results:
				assert.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Error("creation did not finish after releasing callback")
			}
		}
	})
	params := c.pubParams(t, "ok")
	for _, namespace := range []string{"ns1", "ns2"} {
		opts := newPPFallbackOpts(c, params)
		opts.Namespace = namespace
		go func() {
			_, err := c.provider.GetTokenManagerService(opts)
			results <- err
		}()
	}
	var namespaces []string
	for range 2 {
		select {
		case namespace := <-entered:
			namespaces = append(namespaces, namespace)
		case <-time.After(5 * time.Second):
			t.Fatal("another TMS's callback is blocked behind the first callback")
		}
	}
	assert.ElementsMatch(t, []string{"ns1", "ns2"}, namespaces)
}
