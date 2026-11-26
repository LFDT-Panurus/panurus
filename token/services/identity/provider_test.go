/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	drvmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
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

	// a registered signer is only recorded in storage, which is what IsMe reads
	storage.GetExistingSignerInfoReturns([]string{id.UniqueID()}, nil)

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

	// areMe has to consult storage for this identity, and storage fails.
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
// identities are answered from memory and the storage lookup for the rest fails, AreMe must
// surface the error so the caller does not treat the partial slice as authoritative. On error the
// returned slice is nil — the partial is deliberately not handed back, so a caller cannot mistake
// "couldn't check" for "confirmed not mine".
func TestProvider_AreMe_StorageErrorIsPropagated(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, nil)

	// An ephemeral signer is answered from memory, without storage.
	inMemory := driver.Identity("ephemeral")
	require.NoError(t, p.RegisterSigner(t.Context(), inMemory, &drvmock.Signer{}, &drvmock.Verifier{}, nil, true))

	// The storage lookup for the remaining identity fails.
	storage.GetExistingSignerInfoReturns(nil, errors.New("boom"))

	me, err := p.AreMe(t.Context(), inMemory, driver.Identity("not_in_memory"))
	require.Error(t, err, "a storage failure must be propagated so the partial answer is not trusted")
	// On error the slice is nil: the in-memory partial is deliberately not returned, so a caller
	// cannot treat it as an authoritative answer.
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
// an in-memory ephemeral signer, a SignerRouter-routed hit, and the fallback deserializer.
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

	t.Run("ephemeral", func(t *testing.T) {
		storage := &idmock.Storage{}
		des := &idmock.Deserializer{}
		nbs := &idmock.NetworkBinderService{}
		eidu := &idmock.EnrollmentIDUnmarshaler{}
		provider := newFakeMetricsProvider()

		p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu, identity.NewMetrics(provider))

		expected := &drvmock.Signer{}
		des.DeserializeSignerReturns(nil, errors.New("not deserializable"))

		id := driver.Identity("an_identity")
		require.NoError(t, p.RegisterSigner(t.Context(), id, expected, &drvmock.Verifier{}, nil, true))

		s, err := p.GetSigner(t.Context(), id)
		require.NoError(t, err)
		assert.Equal(t, expected, s)

		assert.Equal(t, 1, provider.counterAddCount("identity_signer_resolutions_total", "outcome", "ephemeral"))
		assert.Equal(t, 1, provider.histogramObserveCount("identity_get_signer_duration_seconds", "path", "ephemeral"))
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

		assert.Equal(t, 1, storage.StoreSignerInfoCallCount())
		assert.Equal(t, 1, provider.counterAddCount("identity_signer_resolutions_total", "outcome", "routed"))
		assert.Equal(t, 1, provider.histogramObserveCount("identity_get_signer_duration_seconds", "path", "routed"))
	})
}

// TestProvider_GetSigner_PersistsOnce pins the signer info row to one write per identity: signing
// resolves the same identity repeatedly, and the write is a database round trip.
func TestProvider_GetSigner_PersistsOnce(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	des.DeserializeSignerReturns(&drvmock.Signer{}, nil)

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, &idmock.NetworkBinderService{}, &idmock.EnrollmentIDUnmarshaler{}, nil)

	id := driver.Identity("an_identity")
	_, err := p.GetSigner(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, 1, storage.StoreSignerInfoCallCount())

	// the row is there now, which the store reports from its own signer info cache
	storage.SignerInfoExistsReturns(true, nil)

	_, err = p.GetSigner(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, 1, storage.StoreSignerInfoCallCount(), "the row must be written once, not per GetSigner")
}

// TestProvider_GetSigner_Ephemeral is a regression test for #2814: an ephemeral signer is never
// persisted, so memory is the only place GetSigner and IsMe can find it.
func TestProvider_GetSigner_Ephemeral(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	des.DeserializeSignerReturns(nil, errors.New("not deserializable"))
	p := identity.NewProvider(logging.MustGetLogger(), storage, des,
		&idmock.NetworkBinderService{}, &idmock.EnrollmentIDUnmarshaler{}, nil)

	ephemeralID := driver.Identity("ephemeral_id")
	ephemeralSigner := &drvmock.Signer{}
	require.NoError(t, p.RegisterSigner(t.Context(), ephemeralID, ephemeralSigner, &drvmock.Verifier{}, nil, true))
	require.Equal(t, 0, storage.RegisterIdentityDescriptorCallCount(), "an ephemeral signer is not persisted")

	signer, err := p.GetSigner(t.Context(), ephemeralID)
	require.NoError(t, err)
	assert.Same(t, ephemeralSigner, signer)

	isMe, err := p.IsMe(t.Context(), ephemeralID)
	require.NoError(t, err)
	assert.True(t, isMe)
}

// TestProvider_GetSigner_EphemeralAlias resolves an ephemeral descriptor under both of its keys.
func TestProvider_GetSigner_EphemeralAlias(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	des.DeserializeSignerReturns(nil, errors.New("not deserializable"))
	p := identity.NewProvider(logging.MustGetLogger(), storage, des,
		&idmock.NetworkBinderService{}, &idmock.EnrollmentIDUnmarshaler{}, nil)

	id, alias := driver.Identity("ephemeral_id"), driver.Identity("ephemeral_alias")
	ephemeralSigner := &drvmock.Signer{}
	require.NoError(t, p.RegisterIdentityDescriptor(t.Context(), &idriver.IdentityDescriptor{
		Identity:  id,
		Signer:    ephemeralSigner,
		Ephemeral: true,
	}, alias))
	require.Equal(t, 0, storage.RegisterIdentityDescriptorCallCount())

	for _, key := range []driver.Identity{id, alias} {
		signer, err := p.GetSigner(t.Context(), key)
		require.NoError(t, err, "no signer for [%s]", key)
		assert.Same(t, ephemeralSigner, signer)
	}
}

// TestProvider_EphemeralSigners_Concurrent covers the ephemeral store's lock under -race.
func TestProvider_EphemeralSigners_Concurrent(t *testing.T) {
	des := &idmock.Deserializer{}
	des.DeserializeSignerReturns(nil, errors.New("not deserializable"))
	p := identity.NewProvider(logging.MustGetLogger(), &idmock.Storage{}, des,
		&idmock.NetworkBinderService{}, &idmock.EnrollmentIDUnmarshaler{}, nil)

	// errors come back to the test goroutine: require.* may not be called from another one
	const n = 50
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			id := driver.Identity(fmt.Appendf(nil, "concurrent-%d", i))
			signer := &drvmock.Signer{}
			if err := p.RegisterSigner(t.Context(), id, signer, nil, nil, true); err != nil {
				errs <- err

				return
			}
			switch got, err := p.GetSigner(t.Context(), id); {
			case err != nil:
				errs <- err
			case got != driver.Signer(signer):
				errs <- errors.New("wrong signer for " + id.String())
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}
