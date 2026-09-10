/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	dmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sigRecorder collects the events the deserializer reports.
type sigRecorder struct {
	mu     sync.Mutex
	events []sigobserve.Event
}

func (r *sigRecorder) Observe(_ context.Context, e sigobserve.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *sigRecorder) all() []sigobserve.Event {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]sigobserve.Event(nil), r.events...)
}

func (r *sigRecorder) one(t *testing.T) sigobserve.Event {
	t.Helper()
	events := r.all()
	require.Len(t, events, 1)

	return events[0]
}

// observedDeserializer is a Deserializer wired to a recorder, with its per-role deserializers
// exposed so a test can drive one of them.
type observedDeserializer struct {
	des     *Deserializer
	owner   *dmock.VerifierDeserializer
	issuer  *dmock.VerifierDeserializer
	auditor *dmock.VerifierDeserializer
	events  *sigRecorder
}

func newObservedDeserializer() *observedDeserializer {
	o := &observedDeserializer{
		owner:   &dmock.VerifierDeserializer{},
		issuer:  &dmock.VerifierDeserializer{},
		auditor: &dmock.VerifierDeserializer{},
		events:  &sigRecorder{},
	}
	o.des = NewDeserializer(o.auditor, o.owner, o.issuer, &dmock.AuditMatcherProvider{}, &dmock.RecipientExtractor{})
	o.des.SetObserver(o.events)

	return o
}

func TestDeserializerObservesVerifierResolution(t *testing.T) {
	id := driver.Identity("an_identity")
	tests := []struct {
		name    string
		resolve func(o *observedDeserializer) (driver.Verifier, error)
		mockOf  func(o *observedDeserializer) *dmock.VerifierDeserializer
		op      sigobserve.Op
		role    sigobserve.Role
	}{
		{
			name:    "owner",
			resolve: func(o *observedDeserializer) (driver.Verifier, error) { return o.des.GetOwnerVerifier(t.Context(), id) },
			mockOf:  func(o *observedDeserializer) *dmock.VerifierDeserializer { return o.owner },
			op:      sigobserve.OpOwnerVerifier,
			role:    sigobserve.RoleOwner,
		},
		{
			name: "issuer",
			resolve: func(o *observedDeserializer) (driver.Verifier, error) {
				return o.des.GetIssuerVerifier(t.Context(), id)
			},
			mockOf: func(o *observedDeserializer) *dmock.VerifierDeserializer { return o.issuer },
			op:     sigobserve.OpIssuerVerifier,
			role:   sigobserve.RoleIssuer,
		},
		{
			name: "auditor",
			resolve: func(o *observedDeserializer) (driver.Verifier, error) {
				return o.des.GetAuditorVerifier(t.Context(), id)
			},
			mockOf: func(o *observedDeserializer) *dmock.VerifierDeserializer { return o.auditor },
			op:     sigobserve.OpAuditorVerifier,
			role:   sigobserve.RoleAuditor,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			o := newObservedDeserializer()
			test.mockOf(o).DeserializeVerifierReturns(&dmock.Verifier{}, nil)

			_, err := test.resolve(o)
			require.NoError(t, err)

			event := o.events.one(t)
			assert.Equal(t, test.op, event.Op)
			assert.Equal(t, test.role, event.Role, "the role is what makes an owner's traffic separable from an issuer's")
			assert.Equal(t, id.UniqueID(), event.Principal)
			assert.Equal(t, sigobserve.OutcomeOK, event.Outcome)
		})
	}
}

func TestDeserializerObservesResolutionFailure(t *testing.T) {
	o := newObservedDeserializer()
	o.owner.DeserializeVerifierReturns(nil, errors.New("unknown identity"))

	verifier, err := o.des.GetOwnerVerifier(t.Context(), driver.Identity("an_identity"))
	require.Error(t, err)
	assert.Nil(t, verifier, "a failed resolution must not hand back an instrumented nil")

	event := o.events.one(t)
	assert.Equal(t, sigobserve.OutcomeError, event.Outcome)
	assert.ErrorContains(t, event.Err, "unknown identity")
}

// TestDeserializerObservesVerificationsWithTheResolvedVerifier covers the point of wrapping the
// verifier: a rejected signature is what an attack looks like, and it happens after the
// resolution the deserializer could otherwise report on its own.
func TestDeserializerObservesVerificationsWithTheResolvedVerifier(t *testing.T) {
	o := newObservedDeserializer()
	verifier := &dmock.Verifier{}
	verifier.VerifyReturns(errors.New("signature mismatch"))
	o.owner.DeserializeVerifierReturns(verifier, nil)

	id := driver.Identity("an_identity")
	resolved, err := o.des.GetOwnerVerifier(t.Context(), id)
	require.NoError(t, err)
	require.Error(t, resolved.Verify([]byte("message"), []byte("sigma")))

	events := o.events.all()
	require.Len(t, events, 2)
	verifyEvent := events[1]
	assert.Equal(t, sigobserve.OpVerify, verifyEvent.Op)
	assert.Equal(t, sigobserve.RoleOwner, verifyEvent.Role)
	assert.Equal(t, id.UniqueID(), verifyEvent.Principal)
	assert.Equal(t, sigobserve.OutcomeInvalid, verifyEvent.Outcome,
		"a rejected signature is not a service failure and must be counted apart from one")
}

// TestDeserializerWithoutObserverIsTransparent pins the default a validator gets: no events, and
// the verifier the underlying deserializer produced, unwrapped.
func TestDeserializerWithoutObserverIsTransparent(t *testing.T) {
	o := newObservedDeserializer()
	expected := &dmock.Verifier{}
	o.owner.DeserializeVerifierReturns(expected, nil)

	o.des.SetObserver(nil)

	resolved, err := o.des.GetOwnerVerifier(t.Context(), driver.Identity("an_identity"))
	require.NoError(t, err)
	assert.Same(t, expected, resolved)
	assert.Empty(t, o.events.all())
}
