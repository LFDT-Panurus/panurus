/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity_test

import (
	"context"
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	drvmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idmock "github.com/LFDT-Panurus/panurus/token/services/identity/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eventRecorder collects the events an instrumented component reports.
type eventRecorder struct {
	mu     sync.Mutex
	events []sigobserve.Event
}

func (r *eventRecorder) Observe(_ context.Context, e sigobserve.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *eventRecorder) all() []sigobserve.Event {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]sigobserve.Event(nil), r.events...)
}

// byOp returns the events reported for op.
func (r *eventRecorder) byOp(op sigobserve.Op) []sigobserve.Event {
	out := make([]sigobserve.Event, 0, 1)
	for _, e := range r.all() {
		if e.Op == op {
			out = append(out, e)
		}
	}

	return out
}

// oneOf returns the single event reported for op.
func (r *eventRecorder) oneOf(t *testing.T, op sigobserve.Op) sigobserve.Event {
	t.Helper()
	events := r.byOp(op)
	require.Len(t, events, 1, "expected exactly one [%s] event", op)

	return events[0]
}

// observedProvider is a Provider wired to a recorder, with its collaborators exposed.
type observedProvider struct {
	provider *identity.Provider
	storage  *idmock.Storage
	des      *idmock.Deserializer
	binder   *idmock.NetworkBinderService
	events   *eventRecorder
}

func newObservedProvider(t *testing.T) *observedProvider {
	t.Helper()

	o := &observedProvider{
		storage: &idmock.Storage{},
		des:     &idmock.Deserializer{},
		binder:  &idmock.NetworkBinderService{},
		events:  &eventRecorder{},
	}
	o.provider = identity.NewProvider(logging.MustGetLogger(), o.storage, o.des, o.binder, &idmock.EnrollmentIDUnmarshaler{}, nil)
	o.provider.SetObserver(o.events)

	return o
}

func TestProviderObservesGetSigner(t *testing.T) {
	o := newObservedProvider(t)
	signer := &drvmock.Signer{}
	signer.SignReturns([]byte("sigma"), nil)
	o.des.DeserializeSignerReturns(signer, nil)

	id := driver.Identity("an_identity")
	resolved, err := o.provider.GetSigner(t.Context(), id)
	require.NoError(t, err)

	event := o.events.oneOf(t, sigobserve.OpGetSigner)
	assert.Equal(t, id.UniqueID(), event.Principal, "a principal is named by identity hash")
	assert.Equal(t, sigobserve.OutcomeOK, event.Outcome)
	assert.Equal(t, sigobserve.PathFallback, event.Path)
	assert.True(t, event.CacheChecked)
	assert.False(t, event.CacheHit)

	// The resolved signer is instrumented too, so the signatures it produces are attributed.
	_, err = resolved.Sign([]byte("message"))
	require.NoError(t, err)
	signEvent := o.events.oneOf(t, sigobserve.OpSign)
	assert.Equal(t, id.UniqueID(), signEvent.Principal)
	assert.Equal(t, sigobserve.OutcomeOK, signEvent.Outcome)
}

func TestProviderObservesGetSignerCacheHit(t *testing.T) {
	o := newObservedProvider(t)
	o.des.DeserializeSignerReturns(&drvmock.Signer{}, nil)

	id := driver.Identity("an_identity")
	_, err := o.provider.GetSigner(t.Context(), id)
	require.NoError(t, err)
	_, err = o.provider.GetSigner(t.Context(), id)
	require.NoError(t, err)

	events := o.events.byOp(sigobserve.OpGetSigner)
	require.Len(t, events, 2)
	assert.Equal(t, sigobserve.PathFallback, events[0].Path)
	assert.False(t, events[0].CacheHit)
	assert.Equal(t, sigobserve.PathCache, events[1].Path)
	assert.True(t, events[1].CacheHit)
}

func TestProviderObservesGetSignerFailure(t *testing.T) {
	o := newObservedProvider(t)
	o.des.DeserializeSignerReturns(nil, errors.New("no signer"))

	_, err := o.provider.GetSigner(t.Context(), driver.Identity("an_identity"))
	require.Error(t, err)

	event := o.events.oneOf(t, sigobserve.OpGetSigner)
	assert.Equal(t, sigobserve.OutcomeError, event.Outcome)
	require.Error(t, event.Err)
	assert.Empty(t, o.events.byOp(sigobserve.OpSign), "a failed resolution hands out no signer to instrument")
}

func TestProviderObservesRegisterSigner(t *testing.T) {
	o := newObservedProvider(t)
	id := driver.Identity("signer_id")

	require.NoError(t, o.provider.RegisterSigner(t.Context(), id, &drvmock.Signer{}, &drvmock.Verifier{}, nil, false))

	registerEvent := o.events.oneOf(t, sigobserve.OpRegisterSigner)
	assert.Equal(t, id.UniqueID(), registerEvent.Principal)
	assert.Equal(t, sigobserve.OutcomeOK, registerEvent.Outcome)

	// RegisterSigner delegates to a descriptor registration, and the two are reported separately
	// so that a direct descriptor registration stays distinguishable.
	descriptorEvent := o.events.oneOf(t, sigobserve.OpRegisterIdentityDescriptor)
	assert.Equal(t, id.UniqueID(), descriptorEvent.Principal)
}

func TestProviderObservesRegisterSignerFailure(t *testing.T) {
	o := newObservedProvider(t)
	o.storage.RegisterIdentityDescriptorReturns(errors.New("storage down"))

	err := o.provider.RegisterSigner(t.Context(), driver.Identity("signer_id"), &drvmock.Signer{}, &drvmock.Verifier{}, nil, false)
	require.Error(t, err)

	assert.Equal(t, sigobserve.OutcomeError, o.events.oneOf(t, sigobserve.OpRegisterSigner).Outcome)
	assert.Equal(t, sigobserve.OutcomeError, o.events.oneOf(t, sigobserve.OpRegisterIdentityDescriptor).Outcome)
}

func TestProviderObservesBind(t *testing.T) {
	o := newObservedProvider(t)
	longTerm := driver.Identity("long_term")

	require.NoError(t, o.provider.Bind(t.Context(), longTerm, driver.Identity("ephemeral")))

	event := o.events.oneOf(t, sigobserve.OpBind)
	assert.Equal(t, longTerm.UniqueID(), event.Principal, "a binding is attributed to the long-term identity")
	assert.Equal(t, sigobserve.OutcomeOK, event.Outcome)
}

func TestProviderObservesGetAuditInfo(t *testing.T) {
	o := newObservedProvider(t)
	id := driver.Identity("an_identity")
	o.storage.GetAuditInfoReturns(nil, errors.New("storage down"))

	_, err := o.provider.GetAuditInfo(t.Context(), id)
	require.Error(t, err)

	event := o.events.oneOf(t, sigobserve.OpGetAuditInfo)
	assert.Equal(t, id.UniqueID(), event.Principal)
	assert.Equal(t, sigobserve.OutcomeError, event.Outcome)
}

func TestProviderObservesAreMe(t *testing.T) {
	t.Run("a single identity is attributed to itself", func(t *testing.T) {
		o := newObservedProvider(t)
		id := driver.Identity("an_identity")
		o.storage.GetExistingSignerInfoReturns([]string{id.UniqueID()}, nil)

		assert.Len(t, o.provider.AreMe(t.Context(), id), 1)

		event := o.events.oneOf(t, sigobserve.OpIsMe)
		assert.Equal(t, id.UniqueID(), event.Principal)
		assert.Equal(t, sigobserve.OutcomeOK, event.Outcome)
	})

	t.Run("a batch is left unattributed", func(t *testing.T) {
		o := newObservedProvider(t)
		o.storage.GetExistingSignerInfoReturns(nil, nil)

		o.provider.AreMe(t.Context(), driver.Identity("first"), driver.Identity("second"))

		event := o.events.oneOf(t, sigobserve.OpIsMe)
		assert.Empty(t, event.Principal, "charging a batch to one of its members would let identities throttle each other")
	})
}

// TestProviderReportsAreMeStorageFailure covers the contract of a best-effort lookup: the
// identities resolved before the failure are still returned, and the failure is still reported.
func TestProviderReportsAreMeStorageFailure(t *testing.T) {
	o := newObservedProvider(t)
	cached := driver.Identity("cached_identity")
	require.NoError(t, o.provider.RegisterSigner(t.Context(), cached, &drvmock.Signer{}, &drvmock.Verifier{}, nil, true))
	o.storage.GetExistingSignerInfoReturns(nil, errors.New("storage down"))

	result := o.provider.AreMe(t.Context(), cached, driver.Identity("unknown_identity"))
	assert.Equal(t, []string{cached.UniqueID()}, result, "what the cache knew is still returned")

	event := o.events.oneOf(t, sigobserve.OpIsMe)
	assert.Equal(t, sigobserve.OutcomeError, event.Outcome, "a swallowed storage failure is invisible; this one is not")
	assert.ErrorContains(t, event.Err, "failed checking if a signer exists")
}

func TestProviderIsMeReportsOneEvent(t *testing.T) {
	o := newObservedProvider(t)
	id := driver.Identity("an_identity")
	o.storage.GetExistingSignerInfoReturns([]string{id.UniqueID()}, nil)

	assert.True(t, o.provider.IsMe(t.Context(), id))
	assert.Len(t, o.events.byOp(sigobserve.OpIsMe), 1, "IsMe is AreMe of one identity, not two operations")
}

// TestProviderWithoutObserverIsTransparent pins the zero-cost default: with no observer the
// provider hands back the signer it resolved, unwrapped, so nothing about the signing path changes.
func TestProviderWithoutObserverIsTransparent(t *testing.T) {
	o := newObservedProvider(t)
	expected := &drvmock.Signer{}
	o.des.DeserializeSignerReturns(expected, nil)

	o.provider.SetObserver(nil)

	resolved, err := o.provider.GetSigner(t.Context(), driver.Identity("an_identity"))
	require.NoError(t, err)
	assert.Same(t, expected, resolved)
	assert.Empty(t, o.events.all())
}
