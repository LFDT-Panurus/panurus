/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/LFDT-Panurus/panurus/token/driver"
	drvmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idmock "github.com/LFDT-Panurus/panurus/token/services/identity/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvider_RegisterRecipientData(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, nil)

	data := &driver.RecipientData{
		Identity:               driver.Identity("an_id"),
		AuditInfo:              []byte("audit"),
		TokenMetadata:          []byte("meta"),
		TokenMetadataAuditInfo: []byte("meta_audit"),
	}

	storage.StoreIdentityDataReturns(nil)

	err := p.RegisterRecipientData(t.Context(), data)
	require.NoError(t, err)

	// assert storage called once with expected args
	_, id, ai, tm, tmai := storage.StoreIdentityDataArgsForCall(0)
	assert.Equal(t, []byte("an_id"), id)
	assert.Equal(t, []byte("audit"), ai)
	assert.Equal(t, []byte("meta"), tm)
	assert.Equal(t, []byte("meta_audit"), tmai)
}

func TestProvider_RegisterSigner_And_IsMe(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, nil)

	id := driver.Identity("signer_id")
	signer := &drvmock.Signer{}
	verifier := &drvmock.Verifier{}

	// storage.RegisterIdentityDescriptor should be invoked
	storage.RegisterIdentityDescriptorReturns(nil)

	err := p.RegisterSigner(t.Context(), id, signer, verifier, []byte("si"), false)
	require.NoError(t, err)

	// storage called
	require.Equal(t, 1, storage.RegisterIdentityDescriptorCallCount())

	// provider should now consider this identity as "me"
	isMe, err := p.IsMe(t.Context(), id)
	require.NoError(t, err)
	assert.True(t, isMe)
}

// TestProvider_IsMe_StorageErrorIsPropagated is a regression test for issue #2066: a storage
// failure while checking ownership of an uncached identity must be surfaced, not silently
// flattened into a "not mine" answer.
func TestProvider_IsMe_StorageErrorIsPropagated(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, nil)

	// The identity is not warm in the cache, so areMe must consult storage, which fails.
	storage.GetExistingSignerInfoReturns(nil, errors.New("boom"))

	id := driver.Identity("owned_but_uncached")

	isMe, err := p.IsMe(t.Context(), id)
	require.Error(t, err, "a storage failure must be propagated, not reported as a confident 'not mine'")
	assert.False(t, isMe, "the boolean must not be trusted when an error is returned")

	// On a storage failure AreMe returns a nil slice alongside the error, so callers cannot
	// mistake "couldn't check" for "confirmed not mine".
	me, err := p.AreMe(t.Context(), id)
	require.Error(t, err)
	assert.Nil(t, me)
}

// TestProvider_AreMe_StorageErrorIsPropagated verifies the fix for issue #2066: when some
// identities are cache hits and the storage lookup for the rest fails, AreMe must surface the
// error so the caller does not treat the cache-only partial slice as authoritative. On error the
// returned slice is nil — the cache-only partial is deliberately not handed back, so a caller
// cannot mistake "couldn't check" for "confirmed not mine".
func TestProvider_AreMe_StorageErrorIsPropagated(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, nil)

	// Warm the cache for `cached` by registering a signer for it.
	cached := driver.Identity("cached")
	storage.RegisterIdentityDescriptorReturns(nil)
	require.NoError(t, p.RegisterSigner(t.Context(), cached, &drvmock.Signer{}, &drvmock.Verifier{}, nil, false))

	// The storage lookup for the remaining (uncached) identity fails.
	storage.GetExistingSignerInfoReturns(nil, errors.New("boom"))

	me, err := p.AreMe(t.Context(), cached, driver.Identity("uncached"))
	require.Error(t, err, "a storage failure must be propagated so the cache-only partial is not trusted")
	// On error the slice is nil: the cache-only partial (the cached hit) is deliberately not
	// returned, so a caller cannot treat it as an authoritative answer.
	assert.Nil(t, me)
}

// TestProvider_AreMe_DistinguishesAbortFromStorageFailure checks that AreMe tells a
// caller-driven abort ("we gave up": a cancelled/deadline-exceeded context) apart from a
// genuine backend failure ("storage is broken"). Both return a nil slice, but the context
// error must stay errors.Is-checkable so a cancellation is not misread as a broken backend.
func TestProvider_AreMe_DistinguishesAbortFromStorageFailure(t *testing.T) {
	newProvider := func() (*identity.Provider, *idmock.Storage) {
		storage := &idmock.Storage{}

		return identity.NewProvider(
			logging.MustGetLogger(), storage,
			&idmock.Deserializer{}, &idmock.NetworkBinderService{}, &idmock.EnrollmentIDUnmarshaler{}, nil,
		), storage
	}
	id := driver.Identity("uncached")

	t.Run("context cancellation is 'we gave up'", func(t *testing.T) {
		p, storage := newProvider()
		storage.GetExistingSignerInfoReturns(nil, context.Canceled)

		me, err := p.AreMe(t.Context(), id)
		require.Error(t, err)
		assert.Nil(t, me)
		require.ErrorIs(t, err, context.Canceled, "context cancellation must stay checkable")
		assert.Contains(t, err.Error(), "gave up")
	})

	t.Run("storage failure is 'storage is broken'", func(t *testing.T) {
		p, storage := newProvider()
		storage.GetExistingSignerInfoReturns(nil, errors.New("connection reset"))

		me, err := p.AreMe(t.Context(), id)
		require.Error(t, err)
		assert.Nil(t, me)
		require.NotErrorIs(t, err, context.Canceled)
		assert.Contains(t, err.Error(), "storage")
	})
}

func TestProvider_GetSigner_Deserializable(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, nil)

	id := driver.Identity("an_identity")
	expected := &drvmock.Signer{}

	// deserializer should return the signer
	des.DeserializeSignerReturns(expected, nil)
	storage.StoreSignerInfoReturns(nil)

	s, err := p.GetSigner(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, expected, s)

	// ensure StoreSignerInfo was invoked to persist signer info
	require.Equal(t, 1, storage.StoreSignerInfoCallCount())
}

func TestProvider_GetSigner_TypedIdentityFallback(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, nil)

	// create a typed identity wrapping an inner identity
	inner := driver.Identity("inner")
	ti := identity.TypedIdentity{Type: identity.Type(5), Identity: inner}
	outerBytes, err := ti.Bytes()
	require.NoError(t, err)
	outer := driver.Identity(outerBytes)

	expected := &drvmock.Signer{}

	// Deserializer should fail for outer identity but succeed for inner
	des.DeserializeSignerReturnsOnCall(0, nil, errors.New("not deserializable"))
	des.DeserializeSignerReturnsOnCall(1, expected, nil)
	storage.StoreSignerInfoReturns(nil)

	s, err := p.GetSigner(t.Context(), outer)
	require.NoError(t, err)
	assert.Equal(t, expected, s)
	// persisted
	require.Equal(t, 2, storage.StoreSignerInfoCallCount())
}

func TestProvider_Bind(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, nil)

	longTerm := driver.Identity("lt")
	e1 := driver.Identity("e1")
	e2 := driver.Identity("e2")

	nbs.BindReturns(nil)

	err := p.Bind(t.Context(), longTerm, e1, e2)
	require.NoError(t, err)

	// bind should be called twice (for e1 and e2)
	require.Equal(t, 2, nbs.BindCallCount())
}

func TestProvider_EnrollmentIDHelpers(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, nil)

	id := driver.Identity("who")
	audit := []byte("audit")

	eidu.GetEnrollmentIDReturns("e-id", nil)
	v, err := p.GetEnrollmentID(t.Context(), id, audit)
	require.NoError(t, err)
	assert.Equal(t, "e-id", v)

	eidu.GetRevocationHandlerReturns("rh", nil)
	rh, err := p.GetRevocationHandler(t.Context(), id, audit)
	require.NoError(t, err)
	assert.Equal(t, "rh", rh)

	eidu.GetEIDAndRHReturns("e2", "rh2", nil)
	eid, erh, err := p.GetEIDAndRH(t.Context(), id, audit)
	require.NoError(t, err)
	assert.Equal(t, "e2", eid)
	assert.Equal(t, "rh2", erh)
}

func TestProvider_RollbackPartialRecipientRegistration(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, nil)

	id := driver.Identity("recipient_id")
	require.NoError(t, p.RegisterRecipientIdentity(t.Context(), id))

	var rb identity.RecipientRegistrationRollback = p
	rb.RollbackPartialRecipientRegistration(t.Context(), id)
}

func TestProvider_GetAuditInfo(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, nil)

	id := driver.Identity("who")
	audit := []byte("audit-data")
	storage.GetAuditInfoReturns(audit, nil)

	ai, err := p.GetAuditInfo(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, audit, ai)
	require.Equal(t, 1, storage.GetAuditInfoCallCount())
}

// storeNilSignerEntry plants a nil entry for id in the unexported signer cache via reflection.
func storeNilSignerEntry(t *testing.T, p *identity.Provider, id driver.Identity) {
	t.Helper()
	field := reflect.ValueOf(p).Elem().FieldByName("signers")
	field = reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	field.MethodByName("Add").Call([]reflect.Value{
		reflect.ValueOf(id.UniqueID()),
		reflect.ValueOf((*identity.SignerEntry)(nil)),
	})
}

// TestProvider_GetSigner_TombstonedCacheEntryIsMiss checks a nil cache entry is a miss (#2065).
func TestProvider_GetSigner_TombstonedCacheEntryIsMiss(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, nil)

	id := driver.Identity("an_identity")
	expected := &drvmock.Signer{}
	des.DeserializeSignerReturns(expected, nil)
	storage.StoreSignerInfoReturns(nil)

	// first resolution caches the signer under id
	s, err := p.GetSigner(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, expected, s)

	storeNilSignerEntry(t, p, id)

	// must not panic; the tombstone is treated as a miss and the signer is re-resolved
	s, err = p.GetSigner(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, expected, s)
}

// TestProvider_GetSigner_NilSignerReturnsError checks the defense-in-depth guard: when resolution
// yields a nil signer without an error (e.g. a deserializer that returns (nil, nil)), GetSigner
// returns an error instead of propagating a nil signer that would panic on the caller's first Sign.
func TestProvider_GetSigner_NilSignerReturnsError(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, nil)

	// deserializer succeeds but hands back a nil signer
	des.DeserializeSignerReturns(nil, nil)
	storage.StoreSignerInfoReturns(nil)

	s, err := p.GetSigner(t.Context(), driver.Identity("an_identity"))
	require.Error(t, err)
	assert.Nil(t, s)
}

// fakeSignerDeserializer is a minimal idriver.SignerDeserializer for router registration in
// TestProvider_GetSigner_Metrics's "routed" case; it does not implement ProbeFreeSignerDeserializer,
// so SignerRouter.Resolve exercises its ordinary (non-probe-free) branch.
type fakeSignerDeserializer struct {
	signer driver.Signer
}

func (f fakeSignerDeserializer) DeserializeSigner(_ context.Context, _ []byte) (driver.Signer, error) {
	return f.signer, nil
}

// TestProvider_GetSigner_Metrics proves GetSigner reports SignerResolutions/GetSignerDuration
// under the correct "outcome"/"path" label for each of the three ways a signer can be obtained:
// a cache hit, a SignerRouter-routed hit, and the fallback deserializer.
func TestProvider_GetSigner_Metrics(t *testing.T) {
	t.Run("fallback", func(t *testing.T) {
		storage := &idmock.Storage{}
		des := &idmock.Deserializer{}
		nbs := &idmock.NetworkBinderService{}
		eidu := &idmock.EnrollmentIDUnmarshaler{}
		provider := newFakeMetricsProvider()

		p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, identity.NewMetrics(provider))

		expected := &drvmock.Signer{}
		des.DeserializeSignerReturns(expected, nil)
		storage.StoreSignerInfoReturns(nil)

		s, err := p.GetSigner(t.Context(), driver.Identity("an_identity"))
		require.NoError(t, err)
		assert.Equal(t, expected, s)

		assert.Equal(t, 1, provider.counterAddCount("identity_signer_resolutions_total", "outcome", "fallback"))
		assert.Equal(t, 1, provider.histogramObserveCount("identity_get_signer_duration_seconds", "path", "fallback"))
	})

	t.Run("cache", func(t *testing.T) {
		storage := &idmock.Storage{}
		des := &idmock.Deserializer{}
		nbs := &idmock.NetworkBinderService{}
		eidu := &idmock.EnrollmentIDUnmarshaler{}
		provider := newFakeMetricsProvider()

		p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, identity.NewMetrics(provider))

		expected := &drvmock.Signer{}
		des.DeserializeSignerReturns(expected, nil)
		storage.StoreSignerInfoReturns(nil)

		id := driver.Identity("an_identity")
		_, err := p.GetSigner(t.Context(), id)
		require.NoError(t, err)

		s, err := p.GetSigner(t.Context(), id)
		require.NoError(t, err)
		assert.Equal(t, expected, s)

		assert.Equal(t, 1, provider.counterAddCount("identity_signer_resolutions_total", "outcome", "cache"))
		assert.Equal(t, 1, provider.histogramObserveCount("identity_get_signer_duration_seconds", "path", "cache"))
	})

	t.Run("routed", func(t *testing.T) {
		storage := &idmock.Storage{}
		des := &idmock.Deserializer{}
		nbs := &idmock.NetworkBinderService{}
		eidu := &idmock.EnrollmentIDUnmarshaler{}
		provider := newFakeMetricsProvider()

		identityMetrics := identity.NewMetrics(provider)
		p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, identityMetrics)

		expected := &drvmock.Signer{}
		router := identity.NewSignerRouter(identityMetrics)
		router.Register("conf-id-1", fakeSignerDeserializer{signer: expected})
		resolver := &idmock.ConfIDResolver{}
		resolver.GetConfIDReturns("conf-id-1", nil)
		router.SetConfIDResolver(resolver)
		p.SetSignerRouter(router)

		wrapped, err := identity.WrapWithType(driver.IdemixIdentityType, []byte("raw"))
		require.NoError(t, err)
		storage.StoreSignerInfoReturns(nil)

		s, err := p.GetSigner(t.Context(), wrapped)
		require.NoError(t, err)
		assert.Equal(t, expected, s)

		assert.Equal(t, 1, provider.counterAddCount("identity_signer_resolutions_total", "outcome", "routed"))
		assert.Equal(t, 1, provider.histogramObserveCount("identity_get_signer_duration_seconds", "path", "routed"))
	})
}

// TestProvider_AreMe_OrderAndDedup checks input order and dedup across cache and storage hits.
func TestProvider_AreMe_OrderAndDedup(t *testing.T) {
	storage := &idmock.Storage{}
	p := identity.NewProvider(logging.MustGetLogger(), storage,
		&idmock.Deserializer{}, &idmock.NetworkBinderService{}, &idmock.EnrollmentIDUnmarshaler{}, nil)

	a, b, c, d := driver.Identity("a"), driver.Identity("b"), driver.Identity("c"), driver.Identity("d")
	// a, c cached; b, d in storage, answered out of order
	require.NoError(t, p.RegisterSigner(t.Context(), a, &drvmock.Signer{}, nil, nil, true))
	require.NoError(t, p.RegisterSigner(t.Context(), c, &drvmock.Signer{}, nil, nil, true))
	storage.GetExistingSignerInfoReturns([]string{d.UniqueID(), b.UniqueID()}, nil)

	me, err := p.AreMe(t.Context(), d, a, b, a, c, d, driver.Identity("unknown"))
	require.NoError(t, err)
	assert.Equal(t, []string{d.UniqueID(), a.UniqueID(), b.UniqueID(), c.UniqueID()}, me)

	require.Equal(t, 1, storage.GetExistingSignerInfoCallCount())
	_, asked := storage.GetExistingSignerInfoArgsForCall(0)
	assert.Equal(t, []driver.Identity{d, b, driver.Identity("unknown")}, asked)
}

// TestProvider_AreMe_IsMe_CacheHitSkipsStorage checks cache hits make no storage calls.
func TestProvider_AreMe_IsMe_CacheHitSkipsStorage(t *testing.T) {
	storage := &idmock.Storage{}
	p := identity.NewProvider(logging.MustGetLogger(), storage,
		&idmock.Deserializer{}, &idmock.NetworkBinderService{}, &idmock.EnrollmentIDUnmarshaler{}, nil)

	a, b := driver.Identity("a"), driver.Identity("b")
	require.NoError(t, p.RegisterSigner(t.Context(), a, &drvmock.Signer{}, nil, nil, true))
	require.NoError(t, p.RegisterSigner(t.Context(), b, &drvmock.Signer{}, nil, nil, true))

	me, err := p.AreMe(t.Context(), b, a)
	require.NoError(t, err)
	assert.Equal(t, []string{b.UniqueID(), a.UniqueID()}, me)

	isMe, err := p.IsMe(t.Context(), a)
	require.NoError(t, err)
	assert.True(t, isMe)

	assert.Equal(t, 0, storage.GetExistingSignerInfoCallCount())
	assert.Equal(t, 0, storage.SignerInfoExistsCallCount())

	me, err = p.AreMe(t.Context())
	require.NoError(t, err)
	assert.Empty(t, me)
}

// TestProvider_IsMe_StorageAnswers checks IsMe on a cache miss, including error semantics.
func TestProvider_IsMe_StorageAnswers(t *testing.T) {
	newProvider := func() (*identity.Provider, *idmock.Storage) {
		storage := &idmock.Storage{}

		return identity.NewProvider(logging.MustGetLogger(), storage,
			&idmock.Deserializer{}, &idmock.NetworkBinderService{}, &idmock.EnrollmentIDUnmarshaler{}, nil), storage
	}
	id := driver.Identity("uncached")

	t.Run("found", func(t *testing.T) {
		p, storage := newProvider()
		storage.GetExistingSignerInfoReturns([]string{id.UniqueID()}, nil)
		isMe, err := p.IsMe(t.Context(), id)
		require.NoError(t, err)
		assert.True(t, isMe)
		require.Equal(t, 1, storage.GetExistingSignerInfoCallCount())
		_, asked := storage.GetExistingSignerInfoArgsForCall(0)
		assert.Equal(t, []driver.Identity{id}, asked)
	})

	t.Run("not found", func(t *testing.T) {
		p, storage := newProvider()
		storage.GetExistingSignerInfoReturns([]string{}, nil)
		isMe, err := p.IsMe(t.Context(), id)
		require.NoError(t, err)
		assert.False(t, isMe)
	})

	t.Run("context cancellation is 'we gave up'", func(t *testing.T) {
		p, storage := newProvider()
		storage.GetExistingSignerInfoReturns(nil, context.Canceled)
		isMe, err := p.IsMe(t.Context(), id)
		require.ErrorIs(t, err, context.Canceled)
		assert.Contains(t, err.Error(), "gave up")
		assert.False(t, isMe)
	})

	t.Run("storage failure is 'storage is broken'", func(t *testing.T) {
		p, storage := newProvider()
		storage.GetExistingSignerInfoReturns(nil, errors.New("connection reset"))
		isMe, err := p.IsMe(t.Context(), id)
		require.Error(t, err)
		require.NotErrorIs(t, err, context.Canceled)
		assert.Contains(t, err.Error(), "storage")
		assert.False(t, isMe)
	})
}

// TestProvider_GetSigner_ConcurrentColdMissResolvesOnce checks concurrent misses share one resolution.
func TestProvider_GetSigner_ConcurrentColdMissResolvesOnce(t *testing.T) {
	const callers = 32

	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	provider := newFakeMetricsProvider()
	p := identity.NewProvider(logging.MustGetLogger(), storage, des,
		&idmock.NetworkBinderService{}, &idmock.EnrollmentIDUnmarshaler{}, identity.NewMetrics(provider))

	inner := driver.Identity("inner")
	outer, err := identity.WrapWithType(identity.Type(5), inner)
	require.NoError(t, err)

	expected := &drvmock.Signer{}
	release := make(chan struct{})
	des.DeserializeSignerCalls(func(_ context.Context, raw []byte) (driver.Signer, error) {
		if !inner.Equal(raw) {
			return nil, errors.New("not deserializable")
		}
		// hold the flight open
		<-release

		return expected, nil
	})
	storage.StoreSignerInfoReturns(nil)

	var wg sync.WaitGroup
	signers := make([]driver.Signer, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Go(func() {
			signers[i], errs[i] = p.GetSigner(t.Context(), outer)
		})
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	for i := range callers {
		require.NoError(t, errs[i])
		assert.Equal(t, expected, signers[i])
	}
	// once per level (outer, inner)
	assert.Equal(t, 2, des.DeserializeSignerCallCount())
	assert.Equal(t, 2, storage.StoreSignerInfoCallCount())
	// metrics are per caller
	total := provider.counterAddCount("identity_signer_resolutions_total", "outcome", "fallback") +
		provider.counterAddCount("identity_signer_resolutions_total", "outcome", "cache")
	assert.Equal(t, callers, total)
}

// TestProvider_GetSigner_JoinedCallerSurvivesWinnersCancellation checks a joined caller retries
// after the winner's cancellation.
func TestProvider_GetSigner_JoinedCallerSurvivesWinnersCancellation(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	p := identity.NewProvider(logging.MustGetLogger(), storage, des,
		&idmock.NetworkBinderService{}, &idmock.EnrollmentIDUnmarshaler{}, nil)
	id := driver.Identity("an_identity")
	expected := &drvmock.Signer{}
	storage.StoreSignerInfoReturns(nil)

	winnerEntered := make(chan struct{})
	var calls atomic.Int32
	des.DeserializeSignerCalls(func(ctx context.Context, _ []byte) (driver.Signer, error) {
		if calls.Add(1) == 1 {
			close(winnerEntered)
			<-ctx.Done()

			return nil, ctx.Err()
		}

		return expected, nil
	})

	winnerCtx, cancelWinner := context.WithCancel(t.Context())
	winnerErr := make(chan error, 1)
	go func() {
		_, err := p.GetSigner(winnerCtx, id)
		winnerErr <- err
	}()
	<-winnerEntered

	joinedDone := make(chan struct{})
	var joinedSigner driver.Signer
	var joinedErr error
	go func() {
		defer close(joinedDone)
		joinedSigner, joinedErr = p.GetSigner(t.Context(), id)
	}()
	time.Sleep(50 * time.Millisecond)
	cancelWinner()

	require.ErrorIs(t, <-winnerErr, context.Canceled)
	<-joinedDone
	require.NoError(t, joinedErr)
	assert.Equal(t, expected, joinedSigner)
}
