/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package membership_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalMembership_Discovery(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)

	des := &mock.SignerDeserializerManager{}

	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)
	iss.NotifierReturns(nil, storage.ErrNotSupported)

	km1 := &mock.KeyManager{}
	km1.EnrollmentIDReturns("e1")
	km1.AnonymousReturns(false)
	km1.IsRemoteReturns(false)
	idDesc1 := &idriver.IdentityDescriptor{Identity: []byte("id1"), AuditInfo: []byte("ai1")}
	km1.IdentityReturns(idDesc1, nil)
	km1.IdentityTypeReturns(identity.Type(99))

	km2 := &mock.KeyManager{}
	km2.EnrollmentIDReturns("e2")
	km2.AnonymousReturns(false)
	km2.IsRemoteReturns(false)
	idDesc2 := &idriver.IdentityDescriptor{Identity: []byte("id2"), AuditInfo: []byte("ai2")}
	km2.IdentityReturns(idDesc2, nil)
	km2.IdentityTypeReturns(identity.Type(99))

	kmp := &mock.KeyManagerProvider{}
	kmp.GetReturnsOnCall(0, km1, nil)
	kmp.GetReturnsOnCall(1, km2, nil)

	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		des,
		iss,
		"testType",
		false,
		ip,
		kmp,
	)

	// Initially empty Load
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)

	err := lm.Load(ctx, nil, nil)
	require.NoError(t, err)

	ids, _ := lm.IDs()
	assert.Empty(t, ids)

	// Mock DB update
	newConfig := idriver.IdentityConfiguration{ID: "new", URL: "/tmp/new", Type: "testType"}
	iss.ConfigurationsByIDReturns([]idriver.IdentityConfiguration{newConfig}, nil)

	// Discovery via GetIdentityInfo
	info, err := lm.GetIdentityInfo(ctx, "new", nil)
	require.NoError(t, err)
	assert.NotNil(t, info)

	ids, _ = lm.IDs()
	assert.Contains(t, ids, "new")

	// discovered through a point query, without a second full scan
	assert.Equal(t, 1, iss.ConfigurationsByIDCallCount())
	assert.Equal(t, 1, iss.IteratorConfigurationsCallCount())
}

func TestLocalMembership_DefaultOverride(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)

	des := &mock.SignerDeserializerManager{}

	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)
	iss.NotifierReturns(nil, storage.ErrNotSupported)

	km1 := &mock.KeyManager{}
	km1.EnrollmentIDReturns("e1")
	km1.IdentityReturns(&idriver.IdentityDescriptor{Identity: []byte("id1")}, nil)
	km1.IdentityTypeReturns(identity.Type(99))

	km2 := &mock.KeyManager{}
	km2.EnrollmentIDReturns("e2")
	km2.IdentityReturns(&idriver.IdentityDescriptor{Identity: []byte("id2")}, nil)
	km2.IdentityTypeReturns(identity.Type(99))

	kmp := &mock.KeyManagerProvider{}
	kmp.GetReturnsOnCall(0, km1, nil)
	kmp.GetReturnsOnCall(1, km2, nil)

	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		des,
		iss,
		"testType",
		false,
		ip,
		kmp,
	)

	// Load with one identity as default
	identities := []idriver.ConfiguredIdentity{{ID: "id1", Path: "/tmp/id1", Default: true}}
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)
	err := lm.Load(ctx, identities, nil)
	require.NoError(t, err)
	assert.Equal(t, "id1", lm.GetDefaultIdentifier())

	// Discovery of a new identity (it was previously default: true, but now we don't support it)
	newConfig := idriver.IdentityConfiguration{ID: "id2", URL: "/tmp/id2", Type: "testType"}
	iss.ConfigurationsByIDReturns([]idriver.IdentityConfiguration{newConfig}, nil)

	_, err = lm.GetIdentityInfo(ctx, "id2", nil)
	require.NoError(t, err)
	// Default should NOT change
	assert.Equal(t, "id1", lm.GetDefaultIdentifier())
}

func TestLocalMembership_DoubleCheckedLocking(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	des := &mock.SignerDeserializerManager{}
	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)
	iss.NotifierReturns(nil, storage.ErrNotSupported)

	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("e1")
	km.IdentityReturns(&idriver.IdentityDescriptor{Identity: []byte("id1")}, nil)
	km.IdentityTypeReturns(identity.Type(99))

	kmp := &mock.KeyManagerProvider{}
	kmp.GetReturns(km, nil)

	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		des,
		iss,
		"testType",
		false,
		ip,
		kmp,
	)

	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)
	_ = lm.Load(ctx, nil, nil)

	newConfig := idriver.IdentityConfiguration{ID: "new", URL: "/tmp/new", Type: "testType"}

	// We want to ensure that the configuration is registered only once even if
	// multiple goroutines discover it concurrently
	iss.ConfigurationsByIDReturns([]idriver.IdentityConfiguration{newConfig}, nil)

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			_, _ = lm.GetIdentityInfo(ctx, "new", nil)
		})
	}
	wg.Wait()

	assert.Equal(t, 1, kmp.GetCallCount())
	// no full rescan beyond the initial Load
	assert.Equal(t, 1, iss.IteratorConfigurationsCallCount())
}

func TestLocalMembership_Notifier(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	des := &mock.SignerDeserializerManager{}
	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)

	notifier := &mock.IdentityConfigurationNotifier{}
	iss.NotifierReturns(notifier, nil)

	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("e1")
	km.IdentityReturns(&idriver.IdentityDescriptor{Identity: []byte("id1")}, nil)
	km.IdentityTypeReturns(identity.Type(99))

	kmp := &mock.KeyManagerProvider{}
	kmp.GetReturns(km, nil)

	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		des,
		iss,
		"testType",
		false,
		ip,
		kmp,
	)

	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)

	var mu sync.Mutex
	var subCallback func(idriver.Operation, idriver.IdentityConfigurationRecord)
	notifier.SubscribeStub = func(callback func(idriver.Operation, idriver.IdentityConfigurationRecord)) error {
		mu.Lock()
		defer mu.Unlock()
		subCallback = callback

		return nil
	}

	err := lm.Load(ctx, nil, nil)
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()

		return subCallback != nil
	}, 2*time.Second, 100*time.Millisecond)

	newConfig := idriver.IdentityConfiguration{ID: "new", URL: "/tmp/new", Type: "testType"}
	iss.GetConfigurationReturns(&newConfig, nil)

	// Simulate notification
	mu.Lock()
	callback := subCallback
	mu.Unlock()
	callback(idriver.Insert, idriver.IdentityConfigurationRecord{
		ID:   "new",
		Type: "testType",
		URL:  "/tmp/new",
	})

	// Wait for background processing (though handleConfig is called synchronously in the callback in my implementation,
	// but the callback itself is called from the notifier which might be background).
	// In my lm.go, the callback is executed by the notifier loop.

	assert.Eventually(t, func() bool {
		ids, _ := lm.IDs()

		return slices.Contains(ids, "new")
	}, 2*time.Second, 100*time.Millisecond)
}

func TestLocalMembership_NotificationReadsStoreOutsideLock(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	des := &mock.SignerDeserializerManager{}
	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)

	notifier := &mock.IdentityConfigurationNotifier{}
	iss.NotifierReturns(notifier, nil)

	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("e1")
	km.IdentityReturns(&idriver.IdentityDescriptor{Identity: []byte("id1")}, nil)
	km.IdentityTypeReturns(identity.Type(99))

	kmp := &mock.KeyManagerProvider{}
	kmp.GetReturns(km, nil)

	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		des,
		iss,
		"testType",
		false,
		ip,
		kmp,
	)

	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)

	var mu sync.Mutex
	var subCallback func(idriver.Operation, idriver.IdentityConfigurationRecord)
	notifier.SubscribeStub = func(callback func(idriver.Operation, idriver.IdentityConfigurationRecord)) error {
		mu.Lock()
		defer mu.Unlock()
		subCallback = callback

		return nil
	}

	require.NoError(t, lm.Load(ctx, nil, nil))

	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()

		return subCallback != nil
	}, 2*time.Second, 100*time.Millisecond)

	newConfig := idriver.IdentityConfiguration{ID: "new", URL: "/tmp/new", Type: "testType"}
	iss.GetConfigurationStub = func(context.Context, string, string, string) (*idriver.IdentityConfiguration, error) {
		// re-enter a read path: this deadlocks if the notification handler
		// held the write lock across the store read
		_, _ = lm.IDs()

		return &newConfig, nil
	}

	mu.Lock()
	callback := subCallback
	mu.Unlock()
	callback(idriver.Insert, idriver.IdentityConfigurationRecord{
		ID:   "new",
		Type: "testType",
		URL:  "/tmp/new",
	})

	assert.Eventually(t, func() bool {
		ids, _ := lm.IDs()

		return slices.Contains(ids, "new")
	}, 2*time.Second, 100*time.Millisecond)
}

func TestLocalMembership_Close(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	des := &mock.SignerDeserializerManager{}
	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)
	iss.NotifierReturns(nil, storage.ErrNotSupported)

	kmp := &mock.KeyManagerProvider{}

	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		des,
		iss,
		"testType",
		false,
		ip,
		kmp,
	)

	err := lm.Load(ctx, nil, nil)
	require.NoError(t, err)

	// Close the membership
	lm.Close()

	// Should be safe to call multiple times
	lm.Close()
}
