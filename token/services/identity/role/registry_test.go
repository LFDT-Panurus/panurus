/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	mock2 "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	imock "github.com/LFDT-Panurus/panurus/token/services/identity/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	mmock "github.com/LFDT-Panurus/panurus/token/services/identity/membership/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper to create a registry with fakes
func newRegistryWithFakes() (*role.Registry, *imock.WalletStoreService, *imock.Role, *mock.WalletFactory) {
	storage := &imock.WalletStoreService{}
	r := &imock.Role{}
	wf := &mock.WalletFactory{}
	reg := role.NewRegistry(logging.MustGetLogger("role_test"), r, storage, wf)
	// ensure a non-nil logger to avoid panics in methods that log
	return reg, storage, r, wf
}

// --- tests ---

func TestRegisterWallet_AddsToCache(t *testing.T) {
	reg, _, _, _ := newRegistryWithFakes()
	ctx := t.Context()
	w := &mock2.Wallet{}
	w.IDReturns("w1")
	require.NoError(t, reg.RegisterWallet(ctx, "w1", w))

	reg.WalletMu.RLock()
	defer reg.WalletMu.RUnlock()
	req, ok := reg.Wallets["w1"]
	require.True(t, ok)
	require.Equal(t, "w1", req.ID())
}

func TestLookup_ReturnsCachedWalletByWalletID(t *testing.T) {
	reg, _, role, _ := newRegistryWithFakes()
	ctx := t.Context()
	w := &mock2.Wallet{}
	w.IDReturns("cached")
	reg.WalletMu.Lock()
	reg.Wallets["w1"] = w
	reg.WalletMu.Unlock()

	role.MapToIdentityReturns([]byte("id1"), "w1", nil)

	wallet, idInfo, wID, err := reg.Lookup(ctx, []byte("id1"))
	require.NoError(t, err)
	require.Equal(t, "w1", wID)
	require.Nil(t, idInfo)
	require.Equal(t, "cached", wallet.ID())
}

func TestLookup_FallbackToViewIdentityFound(t *testing.T) {
	reg, storage, role, _ := newRegistryWithFakes()
	ctx := t.Context()
	w := &mock2.Wallet{}
	w.IDReturns("cached2")
	reg.WalletMu.Lock()
	reg.Wallets["w2"] = w
	reg.WalletMu.Unlock()

	role.MapToIdentityReturns(nil, "", errors.New("no mapping"))
	// GetWalletID should return the wallet id for the passed identity
	storage.GetWalletIDReturns("w2", nil)

	wallet, idInfo, wID, err := reg.Lookup(ctx, []byte("id2"))
	require.NoError(t, err)
	require.Equal(t, "w2", wID)
	require.Nil(t, idInfo)
	require.Equal(t, "cached2", wallet.ID())
}

func TestLookup_ReturnsIdentityInfoWhenWalletMissing(t *testing.T) {
	reg, _, role, _ := newRegistryWithFakes()
	ctx := t.Context()

	role.MapToIdentityReturns([]byte("id3"), "w3", nil)
	role.GetIdentityInfoReturns(&mockIdentityInfo{id: "id3"}, nil)

	wallet, idInfo, wID, err := reg.Lookup(ctx, []byte("id3"))
	require.NoError(t, err)
	require.Nil(t, wallet)
	require.NotNil(t, idInfo)
	require.Equal(t, "w3", wID)
}

func TestLookup_NoWalletInfo_Error(t *testing.T) {
	reg, _, role, _ := newRegistryWithFakes()
	ctx := t.Context()

	role.MapToIdentityReturns(nil, "", errors.New("not found"))
	// no view identity and no storage mapping -> error expected
	_, _, _, err := reg.Lookup(ctx, struct{ X int }{1})
	require.Error(t, err)
}

func TestBindIdentityAndContainsAndMetadataAndGetWalletID(t *testing.T) {
	reg, storage, _, _ := newRegistryWithFakes()
	ctx := t.Context()

	storage.StoreIdentityReturns(nil)
	require.NoError(t, reg.BindIdentity(ctx, []byte("id"), "e", "w", map[string]string{"a": "b"}, "conf-1"))
	// ContainsIdentity delegates
	storage.IdentityExistsReturns(true)
	require.True(t, reg.ContainsIdentity(ctx, []byte("id"), "w"))

	// GetIdentityMetadata
	meta := map[string]string{}
	raw, _ := json.Marshal(map[string]string{"k": "v"})
	storage.LoadMetaReturns(raw, nil)
	require.NoError(t, reg.GetIdentityMetadata(ctx, []byte("id"), "w", &meta))
	require.Equal(t, "v", meta["k"])

	// GetWalletID when storage returns value
	storage.GetWalletIDReturns("w", nil)
	wid, err := reg.GetWalletID(ctx, []byte("id"))
	require.NoError(t, err)
	require.Equal(t, "w", wid)

	// GetWalletID when storage returns error -> suppressed to empty string
	storage.GetWalletIDReturns("", errors.New("boom"))
	wid2, err2 := reg.GetWalletID(ctx, []byte("id"))
	require.NoError(t, err2)
	require.Empty(t, wid2)
}

func TestWalletIDs_MergesRoleAndStorage(t *testing.T) {
	reg, storage, role, _ := newRegistryWithFakes()
	role.IdentityIDsReturns([]string{"r1"}, nil)
	storage.GetWalletIDsReturns([]string{"s1", "r1"}, nil)

	ids, err := reg.WalletIDs(t.Context())
	require.NoError(t, err)
	// must contain both unique ids
	require.Contains(t, ids, "r1")
	require.Contains(t, ids, "s1")
}

func TestWalletByID_CreatesWalletUsingFactory(t *testing.T) {
	reg, _, role, wf := newRegistryWithFakes()
	ctx := t.Context()
	// make Lookup return an idInfo and wallet id
	role.MapToIdentityReturns([]byte("id4"), "w4", nil)
	role.GetIdentityInfoReturns(&mockIdentityInfo{id: "id4"}, nil)
	created := &mock2.Wallet{}
	created.IDReturns("w4")
	wf.NewWalletReturns(created, nil)

	w, err := reg.WalletByID(ctx, 0, []byte("id4"))
	require.NoError(t, err)
	require.Equal(t, 1, wf.NewWalletCallCount())
	require.Equal(t, "w4", w.ID())

	w, err = reg.WalletByID(ctx, 0, "id4")
	require.NoError(t, err)
	require.Equal(t, 1, wf.NewWalletCallCount())
	require.Equal(t, "w4", w.ID())

	w, err = reg.WalletByID(ctx, 0, "w4")
	require.NoError(t, err)
	require.Equal(t, 1, wf.NewWalletCallCount())
	require.Equal(t, "w4", w.ID())
}

func TestWalletByID_CreatesWalletUsingFactory2(t *testing.T) {
	reg, _, role, wf := newRegistryWithFakes()
	ctx := t.Context()
	// make Lookup return an idInfo and wallet id
	role.MapToIdentityReturns([]byte("id4"), "w4", nil)
	role.GetIdentityInfoReturns(&mockIdentityInfo{id: "id4"}, nil)
	created := &mock2.Wallet{}
	created.IDReturns("w4")
	wf.NewWalletReturns(created, nil)

	w, err := reg.WalletByID(ctx, 0, "w4")
	require.NoError(t, err)
	require.Equal(t, 1, wf.NewWalletCallCount())
	require.Equal(t, "w4", w.ID())

	w, err = reg.WalletByID(ctx, 0, "id4")
	require.NoError(t, err)
	require.Equal(t, 1, wf.NewWalletCallCount())
	require.Equal(t, "w4", w.ID())

	w, err = reg.WalletByID(ctx, 0, "w4")
	require.NoError(t, err)
	require.Equal(t, 1, wf.NewWalletCallCount())
	require.Equal(t, "w4", w.ID())
}

func TestWalletByID_ConcurrentCreation(t *testing.T) {
	reg, _, r, wf := newRegistryWithFakes()
	ctx := t.Context()
	r.MapToIdentityReturns([]byte("idc"), "wc", nil)
	r.GetIdentityInfoReturns(&mockIdentityInfo{id: "idc"}, nil)

	// make NewWallet block until allowed to proceed to simulate concurrent callers
	start := make(chan struct{})
	created := &mock2.Wallet{}
	created.IDReturns("wc")
	wf.NewWalletStub = func(ctx context.Context, id string, role idriver.IdentityRoleType, wr role.IdentitySupport, info idriver.IdentityInfo) (driver.Wallet, error) {
		<-start

		return created, nil
	}

	var wg sync.WaitGroup
	res := make([]driver.Wallet, 5)
	errs := make([]error, 5)
	for i := range 5 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w, err := reg.WalletByID(ctx, 0, []byte("idc"))
			res[i] = w
			errs[i] = err
		}(i)
	}

	// let goroutines start and block inside NewWallet
	time.Sleep(50 * time.Millisecond)
	close(start)
	wg.Wait()

	for i := range 5 {
		require.NoError(t, errs[i])
		require.Equal(t, "wc", res[i].ID())
	}

	// concurrent callers for the same wallet id share a single factory call
	require.Equal(t, 1, wf.NewWalletCallCount())

	// ensure only one was actually registered in the map
	reg.WalletMu.RLock()
	defer reg.WalletMu.RUnlock()
	count := 0
	for k := range reg.Wallets {
		if k == "wc" {
			count++
		}
	}
	require.Equal(t, 1, count)
}

// TestWalletByID_FactoryReentersRegistry checks that WalletFactory.NewWallet is invoked
// without the registry lock held: the factory receives the registry itself as
// IdentitySupport, so it may legitimately call back into it while the wallet is being
// built. sync.RWMutex is not reentrant, so if WalletByID held WalletMu across the call,
// this would deadlock forever (hence the timeout, a deadlock has no natural termination).
func TestWalletByID_FactoryReentersRegistry(t *testing.T) {
	reg, _, r, wf := newRegistryWithFakes()
	ctx := t.Context()
	r.MapToIdentityReturns([]byte("idre"), "wre", nil)
	r.GetIdentityInfoReturns(&mockIdentityInfo{id: "idre"}, nil)

	created := &mock2.Wallet{}
	created.IDReturns("wre")
	wf.NewWalletStub = func(ctx context.Context, id string, roleType idriver.IdentityRoleType, wr role.IdentitySupport, info idriver.IdentityInfo) (driver.Wallet, error) {
		// simulate a factory that registers the wallet it is building
		if err := reg.RegisterWallet(ctx, id, created); err != nil {
			return nil, err
		}

		return created, nil
	}

	type result struct {
		w   driver.Wallet
		err error
	}
	done := make(chan result, 1)
	go func() {
		w, err := reg.WalletByID(ctx, 0, []byte("idre"))
		done <- result{w: w, err: err}
	}()

	select {
	case res := <-done:
		require.NoError(t, res.err)
		require.Equal(t, "wre", res.w.ID())
	case <-time.After(10 * time.Second):
		t.Fatal("WalletByID did not return: the registry lock is held across WalletFactory.NewWallet")
	}

	require.Equal(t, 1, wf.NewWalletCallCount())
}

// TestWalletByID_DistinctWalletsAreCreatedConcurrently checks that creating wallets with
// different identifiers is not serialized behind the registry write lock: both factory
// calls must be able to be in flight at the same time.
func TestWalletByID_DistinctWalletsAreCreatedConcurrently(t *testing.T) {
	reg, _, r, wf := newRegistryWithFakes()
	ctx := t.Context()
	r.MapToIdentityStub = func(_ context.Context, id driver.WalletLookupID) (driver.Identity, string, error) {
		identity, ok := toIdentityBytes(id)
		require.True(t, ok)

		return identity, "w-" + string(identity), nil
	}
	r.GetIdentityInfoStub = func(_ context.Context, wID string) (idriver.IdentityInfo, error) {
		return &mockIdentityInfo{id: wID}, nil
	}

	// both factory calls must be inside NewWallet at the same time for this to unblock
	var inFlight atomic.Int32
	release := make(chan struct{})
	wf.NewWalletStub = func(_ context.Context, id string, _ idriver.IdentityRoleType, _ role.IdentitySupport, _ idriver.IdentityInfo) (driver.Wallet, error) {
		if inFlight.Add(1) == 2 {
			close(release)
		}
		select {
		case <-release:
		case <-time.After(10 * time.Second):
			return nil, errors.New("wallet creation is serialized: only one NewWallet call at a time")
		}
		w := &mock2.Wallet{}
		w.IDReturns(id)

		return w, nil
	}

	var wg sync.WaitGroup
	res := make([]driver.Wallet, 2)
	errs := make([]error, 2)
	ids := []string{"ida", "idb"}
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res[i], errs[i] = reg.WalletByID(ctx, 0, []byte(ids[i]))
		}(i)
	}
	wg.Wait()

	for i := range ids {
		require.NoError(t, errs[i])
		require.Equal(t, "w-"+ids[i], res[i].ID())
	}
	require.Equal(t, 2, wf.NewWalletCallCount())
}

// toIdentityBytes is the test-side counterpart of the registry's identity conversion.
func toIdentityBytes(id driver.WalletLookupID) (driver.Identity, bool) {
	switch v := id.(type) {
	case driver.Identity:
		return v, true
	case []byte:
		return v, true
	default:
		return nil, false
	}
}

func TestLookup_WithUnknownType_Error(t *testing.T) {
	reg, _, r, _ := newRegistryWithFakes()
	r.MapToIdentityReturns(nil, "", errors.New("fail"))
	_, _, _, err := reg.Lookup(t.Context(), struct{ X int }{1})
	require.Error(t, err)
}

// TestLookup_ByIdentityBytesResolvesViaSharedStores pins the cross-replica
// resolution chain for a lookup by raw identity bytes: the wallet store maps
// the identity to its wallet id, and the identity store point query loads that
// configuration by name — no full store scan and no reliance on notifications.
func TestLookup_ByIdentityBytesResolvesViaSharedStores(t *testing.T) {
	ctx := t.Context()

	// identity store: empty at Load, later holds "alice" registered by another
	// replica; the point query answers by exact id+type only
	iss := &mmock.IdentityStoreService{}
	iss.NotifierReturns(nil, storage.ErrNotSupported)
	iss.IteratorConfigurationsReturns(&mmock.IdentityConfigurationIterator{}, nil)
	aliceConfig := idriver.IdentityConfiguration{ID: "alice", URL: "/tmp/alice", Type: "testType"}
	iss.ConfigurationsByIDStub = func(_ context.Context, id, typ string) ([]idriver.IdentityConfiguration, error) {
		if id == "alice" && typ == "testType" {
			return []idriver.IdentityConfiguration{aliceConfig}, nil
		}

		return nil, nil
	}

	km := &mmock.KeyManager{}
	km.EnrollmentIDReturns("e1")
	km.AnonymousReturns(false)
	km.IdentityReturns(&idriver.IdentityDescriptor{Identity: []byte("alice-long-term-id"), AuditInfo: []byte("ai")}, nil)
	km.IdentityTypeReturns(identity.Type(99))
	kmp := &mmock.KeyManagerProvider{}
	kmp.GetReturns(km, nil)

	ip := &mmock.IdentityProvider{}
	ip.BindReturns(nil)

	lm := membership.NewLocalMembership(
		logging.MustGetLogger("role_test"),
		&mmock.Config{},
		[]byte("netid"),
		&mmock.SignerDeserializerManager{},
		iss,
		"testType",
		false,
		ip,
		kmp,
	)
	require.NoError(t, lm.Load(ctx, nil, nil))

	r := role.NewRole(logging.MustGetLogger("role_test"), idriver.OwnerRole, "network", []byte("node-id"), lm)

	// wallet store: holds the identity->wallet binding written by the replica
	// that created the wallet
	walletStore := &imock.WalletStoreService{}
	walletStore.GetWalletIDReturns("alice", nil)

	reg := role.NewRegistry(logging.MustGetLogger("role_test"), r, walletStore, &mock.WalletFactory{})

	// raw identity bytes, unknown to this process and not valid UTF-8
	rawIdentity := []byte{0xff, 0xfe, 0x01, 0x02}
	wallet, idInfo, wID, err := reg.Lookup(ctx, rawIdentity)
	require.NoError(t, err)
	require.Nil(t, wallet)
	require.NotNil(t, idInfo)
	require.Equal(t, "alice", wID)
	require.Equal(t, "alice", idInfo.ID())

	// the configuration is now loaded locally
	ids, err := lm.IDs()
	require.NoError(t, err)
	require.Contains(t, ids, "alice")

	// resolved through the point query, without a second full scan
	require.Equal(t, 1, iss.IteratorConfigurationsCallCount())
}

// closableWallet is a wallet that records whether it has been closed.
type closableWallet struct {
	*mock2.Wallet
	closed atomic.Int32
}

func (w *closableWallet) Close() { w.closed.Add(1) }

// TestDoneClosesRegisteredWallets checks Done releases the wallets held by the
// registry, so that background goroutines they own (such as the recipient data
// provisioning of an anonymous owner wallet) are terminated instead of living for the
// lifetime of the process.
func TestDoneClosesRegisteredWallets(t *testing.T) {
	reg, _, r, _ := newRegistryWithFakes()
	ctx := t.Context()

	closable := &closableWallet{Wallet: &mock2.Wallet{}}
	closable.IDReturns("w1")
	require.NoError(t, reg.RegisterWallet(ctx, "w1", closable))

	// A wallet with no resources to release must simply be skipped.
	plain := &mock2.Wallet{}
	plain.IDReturns("w2")
	require.NoError(t, reg.RegisterWallet(ctx, "w2", plain))

	require.NoError(t, reg.Done())

	assert.Equal(t, int32(1), closable.closed.Load(), "the wallet was not closed exactly once")
	assert.Equal(t, 1, r.DoneCallCount(), "the role was not released")

	reg.WalletMu.RLock()
	defer reg.WalletMu.RUnlock()
	assert.Empty(t, reg.Wallets, "the wallet cache was not dropped")
}

// TestWalletByID_CancelledCallerDoesNotFailOthers checks that a caller abandoning its
// wallet lookup does not take unrelated callers down with it. Creation is coalesced with
// singleflight, which hands the winning goroutine's error to everyone who joined the same
// flight, and the winner builds the wallet with its own context: without care, one
// cancelled transaction makes every concurrent transaction resolving the same wallet fail
// with a context error that has nothing to do with it.
func TestWalletByID_CancelledCallerDoesNotFailOthers(t *testing.T) {
	reg, _, r, wf := newRegistryWithFakes()
	r.GetIdentityInfoReturns(&mockIdentityInfo{id: "idx"}, nil)

	// The second caller is only in the flight this test cares about once its lookup has
	// run, so the first caller is cancelled after that point.
	var lookups atomic.Int32
	secondLookedUp := make(chan struct{})
	r.MapToIdentityStub = func(_ context.Context, _ driver.WalletLookupID) (driver.Identity, string, error) {
		if lookups.Add(1) == 2 {
			close(secondLookedUp)
		}

		return []byte("idx"), "wx", nil
	}

	// The first creation blocks until it is released, and reports its own context, the way
	// pseudonym generation and storage access do.
	inFactory := make(chan struct{})
	release := make(chan struct{})
	var factoryCalls atomic.Int32
	wf.NewWalletStub = func(ctx context.Context, id string, _ idriver.IdentityRoleType, _ role.IdentitySupport, _ idriver.IdentityInfo) (driver.Wallet, error) {
		if factoryCalls.Add(1) == 1 {
			close(inFactory)
			<-release
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		w := &mock2.Wallet{}
		w.IDReturns(id)

		return w, nil
	}

	type result struct {
		w   driver.Wallet
		err error
	}

	// The caller that gives up: it wins the flight, then its context is cancelled.
	cancelledCtx, cancel := context.WithCancel(t.Context())
	first := make(chan result, 1)
	go func() {
		w, err := reg.WalletByID(cancelledCtx, 0, []byte("idx"))
		first <- result{w: w, err: err}
	}()
	<-inFactory

	// The healthy caller: it joins the in-flight creation with a context of its own.
	second := make(chan result, 1)
	go func() {
		w, err := reg.WalletByID(t.Context(), 0, []byte("idx"))
		second <- result{w: w, err: err}
	}()
	<-secondLookedUp

	cancel()
	close(release)

	select {
	case res := <-second:
		require.NoError(t, res.err,
			"a healthy caller inherited the cancellation of another caller sharing the creation")
		require.NotNil(t, res.w)
		require.Equal(t, "wx", res.w.ID())
	case <-time.After(10 * time.Second):
		t.Fatal("the healthy caller never returned")
	}

	select {
	case res := <-first:
		require.Error(t, res.err, "the cancelled caller should report its own cancellation")
		require.ErrorIs(t, res.err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled caller never returned")
	}
}

// TestWalletByID_CancelledCallerStopsWaiting checks that a caller waiting on someone
// else's creation honours its own context: singleflight itself is not context-aware, so
// without the explicit wait the caller is pinned to the winner's construction and cannot
// meet its own deadline.
func TestWalletByID_CancelledCallerStopsWaiting(t *testing.T) {
	reg, _, r, wf := newRegistryWithFakes()
	r.MapToIdentityReturns([]byte("idw"), "ww", nil)
	r.GetIdentityInfoReturns(&mockIdentityInfo{id: "idw"}, nil)

	inFactory := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	wf.NewWalletStub = func(_ context.Context, id string, _ idriver.IdentityRoleType, _ role.IdentitySupport, _ idriver.IdentityInfo) (driver.Wallet, error) {
		close(inFactory)
		<-release
		w := &mock2.Wallet{}
		w.IDReturns(id)

		return w, nil
	}

	go func() {
		_, _ = reg.WalletByID(t.Context(), 0, []byte("idw"))
	}()
	<-inFactory

	// This caller joins a creation that never finishes, and must still return when its own
	// context is cancelled.
	ctx, cancel := context.WithCancel(t.Context())
	joined := make(chan error, 1)
	go func() {
		_, err := reg.WalletByID(ctx, 0, []byte("idw"))
		joined <- err
	}()
	cancel()

	select {
	case err := <-joined:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("a caller waiting on another caller's creation ignored its own context")
	}
}

// TestWalletByID_NilCachedWalletIsRebuilt checks that an entry holding a nil wallet is
// treated as absent, the way the fast path of WalletByID already treats it. RegisterWallet
// accepts a nil wallet, so such an entry is reachable, and handing it back as a wallet
// panics the caller instead of returning it a wallet it can use.
func TestWalletByID_NilCachedWalletIsRebuilt(t *testing.T) {
	reg, _, r, wf := newRegistryWithFakes()
	ctx := t.Context()
	require.NoError(t, reg.RegisterWallet(ctx, "wnil", nil))
	r.MapToIdentityReturns([]byte("idnil"), "wnil", nil)
	r.GetIdentityInfoReturns(&mockIdentityInfo{id: "idnil"}, nil)

	created := &mock2.Wallet{}
	created.IDReturns("wnil")
	wf.NewWalletReturns(created, nil)

	w, err := reg.WalletByID(ctx, 0, []byte("idnil"))
	require.NoError(t, err)
	require.NotNil(t, w)
	require.Equal(t, "wnil", w.ID())
	require.Equal(t, 1, wf.NewWalletCallCount())
}

// TestWalletByID_FactoryReturningNoWalletIsAnError checks that a factory returning
// (nil, nil) is reported rather than cached and handed on as a wallet.
func TestWalletByID_FactoryReturningNoWalletIsAnError(t *testing.T) {
	reg, _, r, wf := newRegistryWithFakes()
	ctx := t.Context()
	r.MapToIdentityReturns([]byte("idnone"), "wnone", nil)
	r.GetIdentityInfoReturns(&mockIdentityInfo{id: "idnone"}, nil)
	wf.NewWalletReturns(nil, nil)

	w, err := reg.WalletByID(ctx, 0, []byte("idnone"))
	require.Error(t, err)
	require.Nil(t, w)

	reg.WalletMu.RLock()
	defer reg.WalletMu.RUnlock()
	assert.Empty(t, reg.Wallets, "a missing wallet was cached")
}

// TestWalletByID_CreationDuringDoneReleasesTheWallet checks the window opened by building
// wallets outside WalletMu: Done can drop the cache while a creation is in flight. The
// wallet that creation produces must not land in the cache Done has just cleared, because
// nothing would ever close it - and for an anonymous owner wallet that leaves a recipient
// data provisioning goroutine running for the lifetime of the process, which is exactly
// what Done exists to prevent.
func TestWalletByID_CreationDuringDoneReleasesTheWallet(t *testing.T) {
	reg, _, r, wf := newRegistryWithFakes()
	ctx := t.Context()
	r.MapToIdentityReturns([]byte("idd"), "wd", nil)
	r.GetIdentityInfoReturns(&mockIdentityInfo{id: "idd"}, nil)

	closable := &closableWallet{Wallet: &mock2.Wallet{}}
	closable.IDReturns("wd")

	inFactory := make(chan struct{})
	release := make(chan struct{})
	wf.NewWalletStub = func(_ context.Context, _ string, _ idriver.IdentityRoleType, _ role.IdentitySupport, _ idriver.IdentityInfo) (driver.Wallet, error) {
		close(inFactory)
		<-release

		return closable, nil
	}

	type result struct {
		w   driver.Wallet
		err error
	}
	done := make(chan result, 1)
	go func() {
		w, err := reg.WalletByID(ctx, 0, []byte("idd"))
		done <- result{w: w, err: err}
	}()

	// The creation is in flight and holds no lock, so shutdown can overtake it.
	<-inFactory
	require.NoError(t, reg.Done())
	close(release)

	select {
	case res := <-done:
		require.Error(t, res.err, "a wallet created after Done was handed to a caller")
		require.Nil(t, res.w)
	case <-time.After(10 * time.Second):
		t.Fatal("WalletByID never returned")
	}

	assert.Equal(t, int32(1), closable.closed.Load(),
		"the wallet built while the registry was shutting down was not released")

	reg.WalletMu.RLock()
	defer reg.WalletMu.RUnlock()
	assert.Empty(t, reg.Wallets, "the wallet cache was repopulated after Done")
}
