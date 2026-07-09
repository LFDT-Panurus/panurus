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
	require.NoError(t, reg.BindIdentity(ctx, []byte("id"), "e", "w", map[string]string{"a": "b"}))
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
