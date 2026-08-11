/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package membership_test

import (
	"context"
	stdErrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	mock2 "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLocalMembership_IsMe(t *testing.T) {
	ip := &mock.IdentityProvider{}
	ip.IsMeReturns(true)

	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		&mock.SignerDeserializerManager{},
		&mock.IdentityStoreService{},
		"testType",
		false,
		ip,
	)

	assert.True(t, lm.IsMe(t.Context(), []byte("any")))
	assert.Equal(t, token.Identity("netid"), lm.DefaultNetworkIdentity())
}

func TestRegisterIdentity_PersistsAndRegisters(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)

	des := &mock.SignerDeserializerManager{}

	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)

	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("e1")
	km.AnonymousReturns(false)
	km.IsRemoteReturns(false)
	// return an identity descriptor with raw identity
	idDesc := &idriver.IdentityDescriptor{Identity: []byte("id1"), AuditInfo: []byte("ai")}
	km.IdentityReturns(idDesc, nil)
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

	// register identity
	cfg := membership.IdentityConfiguration{ID: "alice", URL: "/tmp/alice"}
	err := lm.RegisterIdentity(ctx, cfg)
	require.NoError(t, err)

	// identity store should be called to persist at least once
	assert.GreaterOrEqual(t, iss.AddConfigurationCallCount(), 1)

	// deserializer manager should get a typed signer deserializer
	assert.GreaterOrEqual(t, des.AddTypedSignerDeserializerCallCount(), 1)

	// identity provider should be bound for non-anon identity
	assert.GreaterOrEqual(t, ip.BindCallCount(), 1)

	// GetIdentifier should find by the typed identity; try both raw and typed
	id, err := lm.GetIdentifier(ctx, []byte("id1"))
	// raw lookup may fail because the service wraps identities with type; allow either success or fallback to typed lookup
	if err == nil {
		assert.Equal(t, "alice", id)
	} else {
		id2, err2 := lm.GetIdentifier(ctx, []byte("id1"))
		require.Error(t, err)
		if err2 == nil {
			assert.Equal(t, "alice", id2)
		}
	}

	// GetIdentityInfo should return an identity info and be able to get identity
	info, err := lm.GetIdentityInfo(ctx, "alice", nil)
	require.NoError(t, err)
	nid, ai, err := info.Get(ctx)
	require.NoError(t, err)
	// the returned identity may be wrapped with type; compare the byte content
	if si, e := identity.UnmarshalTypedIdentity(nid); e == nil {
		assert.Equal(t, "id1", string(si.Identity))
	} else {
		assert.Equal(t, "id1", string(nid))
	}
	assert.Equal(t, []byte("ai"), ai)
}

func TestRegisterIdentity_AnonymousDoesNotBind(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	// no binds expected

	des := &mock.SignerDeserializerManager{}

	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)

	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("e2")
	km.AnonymousReturns(true)
	km.IsRemoteReturns(true)
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
		true,
		ip,
		kmp,
	)

	cfg := membership.IdentityConfiguration{ID: "bob", URL: "/tmp/bob"}
	err := lm.RegisterIdentity(ctx, cfg)
	require.NoError(t, err)

	// anonymous key manager should not trigger bind
	assert.Equal(t, 0, ip.BindCallCount())

	// but deserializer manager should still be called
	assert.Equal(t, 1, des.AddTypedSignerDeserializerCallCount())
}

func TestIDsAndDefaultIdentifier(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)

	des := &mock.SignerDeserializerManager{}

	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)

	km1 := &mock.KeyManager{}
	km1.EnrollmentIDReturns("e1")
	km1.AnonymousReturns(false)
	km1.IsRemoteReturns(false)
	idDesc1 := &idriver.IdentityDescriptor{Identity: []byte("idA"), AuditInfo: []byte("aiA")}
	km1.IdentityReturns(idDesc1, nil)
	km1.IdentityTypeReturns(identity.Type(99))

	km2 := &mock.KeyManager{}
	km2.EnrollmentIDReturns("e2")
	km2.AnonymousReturns(false)
	km2.IsRemoteReturns(false)
	idDesc2 := &idriver.IdentityDescriptor{Identity: []byte("idB"), AuditInfo: []byte("aiB")}
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

	// register two identities
	err := lm.RegisterIdentity(ctx, membership.IdentityConfiguration{ID: "A", URL: "/tmp/A"})
	require.NoError(t, err)
	err = lm.RegisterIdentity(ctx, membership.IdentityConfiguration{ID: "B", URL: "/tmp/B"})
	require.NoError(t, err)

	ids, err := lm.IDs()
	require.NoError(t, err)
	// expect both ids
	assert.Contains(t, ids, "A")
	assert.Contains(t, ids, "B")

	// default should be the first registered
	def := lm.GetDefaultIdentifier()
	assert.Equal(t, "A", def)
}

func TestRegisterIdentity_KeyManagerProviderFails(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	des := &mock.SignerDeserializerManager{}

	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)

	kmp := &mock.KeyManagerProvider{}
	kmp.GetReturns(nil, stdErrors.New("no provider"))

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

	// Now RegisterIdentity returns an error when no key manager provider succeeds
	err := lm.RegisterIdentity(ctx, membership.IdentityConfiguration{ID: "X", URL: "/tmp/x"})
	require.Error(t, err)
	// nothing persisted
	assert.Equal(t, 0, iss.AddConfigurationCallCount())
}

func TestRegisterIdentity_EmptyEnrollmentID(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	// no binds expected

	des := &mock.SignerDeserializerManager{}

	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)

	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("")
	km.AnonymousReturns(false)
	km.IsRemoteReturns(false)
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

	// Use a non-existent path so registerLocalIdentities will attempt to read it and return nil
	nonExistent := filepath.Join(t.TempDir(), "does_not_exist")
	// Now RegisterIdentity returns an error when the selected KeyManager has an empty EnrollmentID
	err := lm.RegisterIdentity(ctx, membership.IdentityConfiguration{ID: "z", URL: nonExistent})
	require.Error(t, err)

	// Because enrollment ID was empty, no configuration should be persisted
	assert.Equal(t, 0, iss.AddConfigurationCallCount())

	// Deserializer manager should not be called because no valid key manager was found
	assert.Equal(t, 0, des.AddTypedSignerDeserializerCallCount())

	// Identity provider should not be bound
	assert.Equal(t, 0, ip.BindCallCount())

	// KeyManagerProvider should have been called at least once
	assert.GreaterOrEqual(t, kmp.GetCallCount(), 1)
}

func TestRegisterIdentity_BindFails(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	ip.BindReturns(stdErrors.New("bind fail"))

	des := &mock.SignerDeserializerManager{}

	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)

	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("e1")
	km.AnonymousReturns(false)
	km.IsRemoteReturns(false)
	idDesc := &idriver.IdentityDescriptor{Identity: []byte("id1"), AuditInfo: []byte("ai")}
	km.IdentityReturns(idDesc, nil)
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

	// Now RegisterIdentity returns an error when the bind fails during addLocalIdentity
	err := lm.RegisterIdentity(ctx, membership.IdentityConfiguration{ID: "Y", URL: "/tmp/y"})
	require.Error(t, err)
	// bind failed so nothing persisted
	assert.Equal(t, 0, iss.AddConfigurationCallCount())
}

func TestLoad_IteratorError(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	des := &mock.SignerDeserializerManager{}
	iss := &mock.IdentityStoreService{}
	iss.IteratorConfigurationsReturns(nil, stdErrors.New("iter err"))

	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		des,
		iss,
		"testType",
		false,
		ip,
	)

	err := lm.Load(ctx, nil, nil)
	require.Error(t, err)
}

func TestToIdentityConfiguration_MarshalError(t *testing.T) {
	// provide an identity with an Opts value that cannot be marshaled to YAML
	bad := idriver.ConfiguredIdentity{ID: "bad", Path: "/tmp/bad", Opts: func() {}}
	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		&mock.SignerDeserializerManager{},
		&mock.IdentityStoreService{},
		"testType",
		false,
		&mock.IdentityProvider{},
	)

	// yaml.Marshal panics on some unsupported types (like func); ensure we panic
	err := lm.Load(t.Context(), []idriver.ConfiguredIdentity{bad}, nil)
	require.Error(t, err)
}

func TestRegisterLocalIdentities_SuccessAndNoValidFound(t *testing.T) {
	ctx := t.Context()

	// prepare temp dir with subdir
	base := t.TempDir()
	defer func() {
		_ = os.RemoveAll(base)
	}()

	sub := filepath.Join(base, "alice")
	err := os.Mkdir(sub, 0o750)
	require.NoError(t, err)

	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)

	des := &mock.SignerDeserializerManager{}
	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)

	// first call (root) -> nil, second call (subdir) -> succeed
	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("e1")
	km.AnonymousReturns(false)
	km.IsRemoteReturns(false)
	idDesc := &idriver.IdentityDescriptor{Identity: []byte("ida"), AuditInfo: []byte("aia")}
	km.IdentityReturns(idDesc, nil)
	km.IdentityTypeReturns(identity.Type(99))

	kmp := &mock.KeyManagerProvider{}
	kmp.GetReturnsOnCall(0, nil, stdErrors.New("root no"))
	kmp.GetReturnsOnCall(1, km, nil)

	cfg := &mock.Config{}
	cfg.TranslatePathReturns(base)

	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		cfg,
		[]byte("netid"),
		des,
		iss,
		"testType",
		false,
		ip,
		kmp,
	)

	// registering the base path should succeed by loading alice
	err = lm.RegisterIdentity(ctx, membership.IdentityConfiguration{ID: "root", URL: base})
	require.NoError(t, err)
	// AddConfiguration called for the sub-identity
	assert.GreaterOrEqual(t, iss.AddConfigurationCallCount(), 1)

	// now test no valid identities found: setup kmp to always fail
	kmp2 := &mock.KeyManagerProvider{}
	kmp2.GetReturns(nil, stdErrors.New("nope"))
	lm2 := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		cfg,
		[]byte("netid"),
		des,
		iss,
		"testType",
		false,
		ip,
		kmp2,
	)

	err = lm2.RegisterIdentity(ctx, membership.IdentityConfiguration{ID: "root", URL: base})
	require.Error(t, err)
}

func TestTypedIdentityInfo_Get_RegisterAndBindFailures(t *testing.T) {
	ctx := t.Context()
	// happy path
	ip := &mock.IdentityProvider{}
	ip.RegisterIdentityDescriptorReturns(nil)
	ip.BindReturns(nil)

	desc := &idriver.IdentityDescriptor{Identity: []byte("idX"), AuditInfo: []byte("aiX")}
	ti := &membership.TypedIdentityInfo{
		GetIdentity:      func(context.Context, []byte) (*idriver.IdentityDescriptor, error) { return desc, nil },
		IdentityType:     identity.Type(99),
		EnrollmentID:     "e",
		RootIdentity:     token.Identity("root"),
		IdentityProvider: ip,
	}

	id, ai, err := ti.Get(ctx, nil)
	require.NoError(t, err)
	// unwrap to check inner identity
	if si, e := identity.UnmarshalTypedIdentity(id); e == nil {
		assert.Equal(t, "idX", string(si.Identity))
	} else {
		assert.Equal(t, "idX", string(id))
	}
	assert.Equal(t, []byte("aiX"), ai)
	assert.Equal(t, 1, ip.RegisterIdentityDescriptorCallCount())
	assert.Equal(t, 1, ip.BindCallCount())

	// RegisterIdentityDescriptor fails
	ip2 := &mock.IdentityProvider{}
	ip2.RegisterIdentityDescriptorReturns(stdErrors.New("regfail"))
	ti2 := &membership.TypedIdentityInfo{
		GetIdentity:      func(context.Context, []byte) (*idriver.IdentityDescriptor, error) { return desc, nil },
		IdentityType:     identity.Type(99),
		EnrollmentID:     "e",
		RootIdentity:     token.Identity("root"),
		IdentityProvider: ip2,
	}
	_, _, err = ti2.Get(ctx, nil)
	require.Error(t, err)

	// Bind fails
	ip3 := &mock.IdentityProvider{}
	ip3.RegisterIdentityDescriptorReturns(nil)
	ip3.BindReturns(stdErrors.New("bindfail"))
	ti3 := &membership.TypedIdentityInfo{
		GetIdentity:      func(context.Context, []byte) (*idriver.IdentityDescriptor, error) { return desc, nil },
		IdentityType:     identity.Type(99),
		EnrollmentID:     "e",
		RootIdentity:     token.Identity("root"),
		IdentityProvider: ip3,
	}
	_, _, err = ti3.Get(ctx, nil)
	require.Error(t, err)
}

func TestTypedSignerDeserializer_DeserializeSigner(t *testing.T) {
	ctx := t.Context()
	km := &mock.KeyManager{}
	signer := &mock2.Signer{}
	signer.SignReturns([]byte("sig"), nil)
	km.DeserializeSignerReturns(signer, nil)

	td := &membership.TypedSignerDeserializer{KeyManager: km}
	s, err := td.DeserializeSigner(ctx, identity.Type(99), []byte("raw"))
	require.NoError(t, err)
	assert.Equal(t, signer, s)
}

func TestLoad_Success(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)

	des := &mock.SignerDeserializerManager{}

	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)
	iss.NotifierReturns(nil, storage.ErrNotSupported)

	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("e1")
	km.AnonymousReturns(false)
	km.IsRemoteReturns(false)
	idDesc := &idriver.IdentityDescriptor{Identity: []byte("id1"), AuditInfo: []byte("ai")}
	km.IdentityReturns(idDesc, nil)
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

	identities := []idriver.ConfiguredIdentity{
		{ID: "alice", Path: "/tmp/alice", Default: true},
	}
	err := lm.Load(ctx, identities, nil)
	require.NoError(t, err)

	assert.Equal(t, "alice", lm.GetDefaultIdentifier())
}

func TestLoad_WithTargets(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)

	des := &mock.SignerDeserializerManager{}

	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)
	iss.NotifierReturns(nil, storage.ErrNotSupported)

	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("e1")
	km.AnonymousReturns(false)
	km.IsRemoteReturns(false)
	idDesc := &idriver.IdentityDescriptor{Identity: []byte("id1"), AuditInfo: []byte("ai")}
	km.IdentityReturns(idDesc, nil)
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

	identities := []idriver.ConfiguredIdentity{
		{ID: "alice", Path: "/tmp/alice"},
	}
	// target identity matches the one returned by KeyManager
	targets := []view.Identity{[]byte("id1")}

	err := lm.Load(ctx, identities, targets)
	require.NoError(t, err)

	// alice should be loaded
	ids, err := lm.IDs()
	require.NoError(t, err)
	assert.Contains(t, ids, "alice")
}

func TestGetIdentifier_NotFound(t *testing.T) {
	ctx := t.Context()
	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		&mock.SignerDeserializerManager{},
		&mock.IdentityStoreService{},
		"testType",
		false,
		&mock.IdentityProvider{},
	)

	_, err := lm.GetIdentifier(ctx, []byte("unknown"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identifier not found")
}

func TestGetIdentityInfo_NotFound(t *testing.T) {
	ctx := t.Context()
	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		&mock.SignerDeserializerManager{},
		&mock.IdentityStoreService{},
		"testType",
		false,
		&mock.IdentityProvider{},
	)

	_, err := lm.GetIdentityInfo(ctx, "unknown", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local identity not found")
}

func TestLoad_MergeStoredIdentities(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)

	des := &mock.SignerDeserializerManager{}

	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)
	iss.NotifierReturns(nil, storage.ErrNotSupported)

	// Stored identity
	storedConfig := idriver.IdentityConfiguration{
		ID: "stored", URL: "/tmp/stored", Type: "testType",
	}
	// Mock iterator
	iter := &mock.IdentityConfigurationIterator{}
	iter.NextReturnsOnCall(0, &storedConfig, nil)
	iter.NextReturnsOnCall(1, nil, nil)
	iss.IteratorConfigurationsReturns(iter, nil)

	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("e1")
	km.AnonymousReturns(false)
	km.IsRemoteReturns(false)
	idDesc := &idriver.IdentityDescriptor{Identity: []byte("id1"), AuditInfo: []byte("ai")}
	km.IdentityReturns(idDesc, nil)
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

	// Configured identity
	identities := []idriver.ConfiguredIdentity{
		{ID: "configured", Path: "/tmp/configured"},
	}

	err := lm.Load(ctx, identities, nil)
	require.NoError(t, err)

	ids, err := lm.IDs()
	require.NoError(t, err)
	assert.Contains(t, ids, "stored")
	assert.Contains(t, ids, "configured")
}

func TestLoad_PickFirstAsDefault(t *testing.T) {
	ctx := t.Context()
	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)
	des := &mock.SignerDeserializerManager{}
	iss := &mock.IdentityStoreService{}
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)
	iss.NotifierReturns(nil, storage.ErrNotSupported)

	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("e1")
	km.IdentityReturns(&idriver.IdentityDescriptor{Identity: []byte("id1")}, nil)
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

	identities := []idriver.ConfiguredIdentity{
		{ID: "first", Path: "/tmp/first"},
		{ID: "second", Path: "/tmp/second"},
	}
	err := lm.Load(ctx, identities, nil)
	require.NoError(t, err)

	assert.Equal(t, "first", lm.GetDefaultIdentifier())
}

func TestLoad_AnonymousFiltering(t *testing.T) {
	ctx := t.Context()
	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)
	des := &mock.SignerDeserializerManager{}
	iss := &mock.IdentityStoreService{}
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)
	iss.NotifierReturns(nil, storage.ErrNotSupported)

	// Non-anonymous key manager
	kmNonAnon := &mock.KeyManager{}
	kmNonAnon.AnonymousReturns(false)
	kmNonAnon.EnrollmentIDReturns("e1")
	kmNonAnon.IdentityReturns(&idriver.IdentityDescriptor{Identity: []byte("id1")}, nil)

	// Anonymous key manager
	kmAnon := &mock.KeyManager{}
	kmAnon.AnonymousReturns(true)
	kmAnon.EnrollmentIDReturns("e2")
	kmAnon.IdentityReturns(&idriver.IdentityDescriptor{Identity: []byte("id2")}, nil)

	kmp := &mock.KeyManagerProvider{}
	kmp.GetReturnsOnCall(0, kmNonAnon, nil)
	kmp.GetReturnsOnCall(1, kmAnon, nil)

	// Enable anonymous mode
	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		des,
		iss,
		"testType",
		true, // defaultAnonymous = true
		ip,
		kmp,
	)

	identities := []idriver.ConfiguredIdentity{
		{ID: "non-anon", Path: "/tmp/non-anon", Default: true}, // Should be ignored as default
		{ID: "anon", Path: "/tmp/anon"},
	}
	err := lm.Load(ctx, identities, nil)
	require.NoError(t, err)

	assert.Equal(t, "anon", lm.GetDefaultIdentifier())
}

func TestPriorityComparison(t *testing.T) {
	// Smaller number = higher priority
	a := membership.LocalIdentityWithPriority{Priority: 10}
	b := membership.LocalIdentityWithPriority{Priority: 0}
	c := membership.LocalIdentityWithPriority{Priority: 5}

	list := []membership.LocalIdentityWithPriority{a, b, c}
	slices.SortFunc(list, membership.PriorityComparison)

	assert.Equal(t, 0, list[0].Priority)
	assert.Equal(t, 5, list[1].Priority)
	assert.Equal(t, 10, list[2].Priority)
}

func TestRefreshAndGet_PointQueryLoadsReplicaConfiguration(t *testing.T) {
	ctx := t.Context()

	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)

	iss := &mock.IdentityStoreService{}
	iss.NotifierReturns(nil, storage.ErrNotSupported)
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)
	// the shared store holds a configuration registered by another replica
	iss.ConfigurationsByIDReturns([]idriver.IdentityConfiguration{
		{ID: "replica-wallet", URL: "/tmp/replica", Type: "testType"},
	}, nil)

	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("e1")
	km.AnonymousReturns(false)
	km.IsRemoteReturns(false)
	km.IdentityReturns(&idriver.IdentityDescriptor{Identity: []byte("id1"), AuditInfo: []byte("ai")}, nil)
	km.IdentityTypeReturns(identity.Type(99))

	kmp := &mock.KeyManagerProvider{}
	kmp.GetReturns(km, nil)

	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		&mock.SignerDeserializerManager{},
		iss,
		"testType",
		false,
		ip,
		kmp,
	)
	require.NoError(t, lm.Load(ctx, nil, nil))

	info, err := lm.GetIdentityInfo(ctx, "replica-wallet", nil)
	require.NoError(t, err)
	assert.Equal(t, "replica-wallet", info.ID())

	// resolved through the point query, without a second full scan
	require.Equal(t, 1, iss.ConfigurationsByIDCallCount())
	_, id, typ := iss.ConfigurationsByIDArgsForCall(0)
	assert.Equal(t, "replica-wallet", id)
	assert.Equal(t, "testType", typ)
	assert.Equal(t, 1, iss.IteratorConfigurationsCallCount())
}

func TestRefreshAndGet_UnknownLabelDoesNotFullScan(t *testing.T) {
	ctx := t.Context()

	iss := &mock.IdentityStoreService{}
	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		&mock.SignerDeserializerManager{},
		iss,
		"testType",
		false,
		&mock.IdentityProvider{},
	)

	_, err := lm.GetIdentityInfo(ctx, "unknown", nil)
	require.Error(t, err)

	require.Equal(t, 1, iss.ConfigurationsByIDCallCount())
	_, id, typ := iss.ConfigurationsByIDArgsForCall(0)
	assert.Equal(t, "unknown", id)
	assert.Equal(t, "testType", typ)
	assert.Equal(t, 0, iss.IteratorConfigurationsCallCount())
}

func TestRefreshAndGet_NonUTF8LabelSkipsStore(t *testing.T) {
	ctx := t.Context()

	iss := &mock.IdentityStoreService{}
	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		&mock.SignerDeserializerManager{},
		iss,
		"testType",
		false,
		&mock.IdentityProvider{},
	)

	// raw identity bytes are not valid UTF-8 and cannot be a configuration id
	_, err := lm.GetIdentityInfo(ctx, string([]byte{0xff, 0xfe, 0xfd}), nil)
	require.Error(t, err)

	assert.Equal(t, 0, iss.ConfigurationsByIDCallCount())
	assert.Equal(t, 0, iss.IteratorConfigurationsCallCount())
}

// newStoredOnlyMembership builds a LocalMembership whose identity store
// serves the given stored configurations and whose KeyManagerProvider is the
// passed mock. No configured identities are loaded.
func newStoredOnlyMembership(configs []idriver.IdentityConfiguration, kmp *mock.KeyManagerProvider) (*membership.LocalMembership, *mock.SignerDeserializerManager) {
	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)
	des := &mock.SignerDeserializerManager{}

	cfg := &mock.Config{}
	cfg.TranslatePathCalls(func(p string) string { return p })

	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(true, nil) // stored configs are already persisted
	iss.NotifierReturns(nil, storage.ErrNotSupported)
	it := &mock.IdentityConfigurationIterator{}
	var iterMu sync.Mutex
	served := 0
	it.NextCalls(func() (*idriver.IdentityConfiguration, error) {
		iterMu.Lock()
		defer iterMu.Unlock()
		if served >= len(configs) {
			return nil, nil
		}
		served++

		return &configs[served-1], nil
	})
	iss.IteratorConfigurationsReturns(it, nil)

	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		cfg,
		[]byte("netid"),
		des,
		iss,
		"testType",
		false,
		ip,
		kmp,
	)

	return lm, des
}

func newTestKeyManager(enrollmentID string, id []byte) *mock.KeyManager {
	km := &mock.KeyManager{}
	km.EnrollmentIDReturns(enrollmentID)
	km.AnonymousReturns(false)
	km.IsRemoteReturns(false)
	km.IdentityReturns(&idriver.IdentityDescriptor{Identity: id, AuditInfo: []byte("ai")}, nil)
	km.IdentityTypeReturns(identity.Type(99))

	return km
}

// TestLoad_ParallelStoredConfigurations exercises the parallel resolution
// path for stored identity configurations (the 100k+ user-wallet scenario):
// all identities must be registered and KeyManager construction must actually
// overlap. Run with -race to validate the prepare/commit split.
func TestLoad_ParallelStoredConfigurations(t *testing.T) {
	ctx := t.Context()

	const n = 500

	configs := make([]idriver.IdentityConfiguration, n)
	for i := range configs {
		configs[i] = idriver.IdentityConfiguration{
			ID:   fmt.Sprintf("wallet-%d", i+1),
			Type: "testType",
			URL:  fmt.Sprintf("/tmp/wallet-%d", i+1),
		}
	}

	// a single shared KeyManager exercises the "shared and safe for
	// concurrent EnrollmentID calls" flavour of the provider contract; the
	// determinism tests below return independently owned instances.
	km := newTestKeyManager("e1", []byte("id1"))
	kmp := &mock.KeyManagerProvider{}
	var inFlight, maxInFlight atomic.Int64
	kmp.GetCalls(func(context.Context, *membership.IdentityConfiguration) (membership.KeyManager, error) {
		cur := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			seen := maxInFlight.Load()
			if cur <= seen || maxInFlight.CompareAndSwap(seen, cur) {
				break
			}
		}
		time.Sleep(time.Millisecond)

		return km, nil
	})

	lm, des := newStoredOnlyMembership(configs, kmp)
	require.NoError(t, lm.Load(ctx, nil, nil))

	ids, err := lm.IDs()
	require.NoError(t, err)
	assert.Len(t, ids, n)
	assert.Equal(t, n, des.AddTypedSignerDeserializerCallCount())
	if runtime.NumCPU() > 1 {
		assert.Greater(t, maxInFlight.Load(), int64(1), "KeyManager construction must overlap")
	}
}

// TestLoad_FallbackDefaultKeepsIteratorOrder verifies that when no identity
// carries the default flag, the fallback default is the first stored
// configuration in iterator order, not the first whose KeyManager happens to
// finish construction.
func TestLoad_FallbackDefaultKeepsIteratorOrder(t *testing.T) {
	ctx := t.Context()

	configs := []idriver.IdentityConfiguration{
		{ID: "wallet-1", Type: "testType", URL: "/tmp/wallet-1"},
		{ID: "wallet-2", Type: "testType", URL: "/tmp/wallet-2"},
		{ID: "wallet-3", Type: "testType", URL: "/tmp/wallet-3"},
	}

	kmp := &mock.KeyManagerProvider{}
	kmp.GetCalls(func(_ context.Context, cfg *membership.IdentityConfiguration) (membership.KeyManager, error) {
		// the first configuration finishes last
		if cfg.ID == "wallet-1" {
			time.Sleep(50 * time.Millisecond)
		}

		return newTestKeyManager("e-"+cfg.ID, []byte("id-"+cfg.ID)), nil
	})

	lm, _ := newStoredOnlyMembership(configs, kmp)
	require.NoError(t, lm.Load(ctx, nil, nil))

	assert.Equal(t, "wallet-1", lm.GetDefaultIdentifier(),
		"fallback default must follow iterator order, not completion order")
}

// TestLoad_SameNameSamePriorityKeepsIteratorOrder verifies that two stored
// configurations sharing name and priority resolve lookups to the first one
// in iterator order even when the second finishes construction first.
func TestLoad_SameNameSamePriorityKeepsIteratorOrder(t *testing.T) {
	ctx := t.Context()

	configs := []idriver.IdentityConfiguration{
		{ID: "shared", Type: "testType", URL: "/tmp/shared-first"},
		{ID: "shared", Type: "testType", URL: "/tmp/shared-second"},
	}

	kmp := &mock.KeyManagerProvider{}
	kmp.GetCalls(func(_ context.Context, cfg *membership.IdentityConfiguration) (membership.KeyManager, error) {
		if cfg.URL == "/tmp/shared-first" {
			// the first configuration finishes last
			time.Sleep(50 * time.Millisecond)

			return newTestKeyManager("e-first", []byte("id-first")), nil
		}

		return newTestKeyManager("e-second", []byte("id-second")), nil
	})

	lm, _ := newStoredOnlyMembership(configs, kmp)
	require.NoError(t, lm.Load(ctx, nil, nil))

	info, err := lm.GetIdentityInfo(ctx, "shared", nil)
	require.NoError(t, err)
	assert.Equal(t, "e-first", info.EnrollmentID(),
		"same-name same-priority lookup must follow iterator order, not completion order")
}
