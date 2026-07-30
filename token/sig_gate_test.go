/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// denyingGate refuses every operation, recording what it was asked about.
type denyingGate struct {
	mu    sync.Mutex
	calls []sigobserve.Op
	last  string
}

func (g *denyingGate) Allow(_ context.Context, principal string, op sigobserve.Op) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, op)
	g.last = principal

	return errors.Wrapf(SignatureThrottled, "operation [%s] denied", op)
}

func (g *denyingGate) ops() []sigobserve.Op {
	g.mu.Lock()
	defer g.mu.Unlock()

	return append([]sigobserve.Op(nil), g.calls...)
}

// allowingGate permits every operation, recording the operations it was consulted about.
type allowingGate struct {
	mu    sync.Mutex
	calls []sigobserve.Op
}

func (g *allowingGate) Allow(_ context.Context, _ string, op sigobserve.Op) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, op)

	return nil
}

func (g *allowingGate) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()

	return len(g.calls)
}

// gateRecorder collects the events the signature service reports.
type gateRecorder struct {
	mu     sync.Mutex
	events []sigobserve.Event
}

func (r *gateRecorder) Observe(_ context.Context, e sigobserve.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *gateRecorder) all() []sigobserve.Event {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]sigobserve.Event(nil), r.events...)
}

// gatedService is a SignatureService fronted by gate, with its collaborators exposed.
type gatedService struct {
	service      *SignatureService
	deserializer *mock.Deserializer
	provider     *mock.IdentityProvider
	events       *gateRecorder
}

func newGatedService(gate SignatureGate) *gatedService {
	g := &gatedService{
		deserializer: &mock.Deserializer{},
		provider:     &mock.IdentityProvider{},
		events:       &gateRecorder{},
	}
	g.service = NewSignatureService(g.deserializer, g.provider,
		WithSignatureObserver(g.events), WithSignatureGate(gate))

	return g
}

// TestSignatureServiceDeniesEveryGatedOperation walks the whole client-facing surface, because a
// gate that covers only some of it leaves the rest of the surface as the way around the policy.
//
// AuditorVerifier and GetSigner are intentionally absent: they are not gated because the
// identities they use come from trusted fixed sources (public parameters and the node's own
// long-term identity, respectively). Gating them would make DefaultRate a hard TPS ceiling on
// normal transaction processing rather than a per-counterparty abuse limit. See
// TestSignatureServiceTrustedOperationsAreNotGated.
func TestSignatureServiceDeniesEveryGatedOperation(t *testing.T) {
	id := Identity("an_identity")
	tests := []struct {
		name string
		call func(s *SignatureService) error
		op   sigobserve.Op
		role sigobserve.Role
	}{
		{
			name: "OwnerVerifier",
			call: func(s *SignatureService) error {
				_, err := s.OwnerVerifier(t.Context(), id)

				return err
			},
			op:   sigobserve.OpOwnerVerifier,
			role: sigobserve.RoleOwner,
		},
		{
			name: "IssuerVerifier",
			call: func(s *SignatureService) error {
				_, err := s.IssuerVerifier(t.Context(), id)

				return err
			},
			op:   sigobserve.OpIssuerVerifier,
			role: sigobserve.RoleIssuer,
		},
		{
			name: "RegisterSigner",
			call: func(s *SignatureService) error {
				return s.RegisterSigner(t.Context(), id, &mock.Signer{}, &mock.Verifier{})
			},
			op:   sigobserve.OpRegisterSigner,
			role: sigobserve.RoleUnknown,
		},
		{
			name: "RegisterEphemeralSigner",
			call: func(s *SignatureService) error {
				return s.RegisterEphemeralSigner(t.Context(), id, &mock.Signer{}, &mock.Verifier{})
			},
			op:   sigobserve.OpRegisterSigner,
			role: sigobserve.RoleUnknown,
		},
		{
			name: "GetAuditInfo",
			call: func(s *SignatureService) error {
				_, err := s.GetAuditInfo(t.Context(), id)

				return err
			},
			op:   sigobserve.OpGetAuditInfo,
			role: sigobserve.RoleUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gate := &denyingGate{}
			g := newGatedService(gate)

			err := test.call(g.service)
			require.Error(t, err)
			require.ErrorIs(t, err, SignatureThrottled, "a denial must be distinguishable from a failure")

			assert.Equal(t, []sigobserve.Op{test.op}, gate.ops())
			assert.Equal(t, id.UniqueID(), gate.last, "the gate meters identity hashes, never raw identities")

			// The denial happens before the work, so nothing downstream is touched.
			assert.Empty(t, g.deserializer.Invocations(), "a denied operation must not reach the deserializer")
			assert.Empty(t, g.provider.Invocations(), "a denied operation must not reach the identity provider")

			events := g.events.all()
			require.Len(t, events, 1)
			assert.Equal(t, test.op, events[0].Op)
			assert.Equal(t, test.role, events[0].Role)
			assert.Equal(t, id.UniqueID(), events[0].Principal)
			assert.Equal(t, sigobserve.OutcomeThrottled, events[0].Outcome)
			assert.ErrorIs(t, events[0].Err, SignatureThrottled)
		})
	}
}

// TestSignatureServiceTrustedOperationsAreNotGated pins that AuditorVerifier and GetSigner
// bypass the gate entirely. Their principals come from trusted fixed sources — public
// parameters and the node's own identity — so there is no attacker-controlled input to
// defend against, and applying the rate quota would make DefaultRate a hard TPS ceiling on
// the node's own transaction throughput.
func TestSignatureServiceTrustedOperationsAreNotGated(t *testing.T) {
	gate := &denyingGate{}
	g := newGatedService(gate)
	id := Identity("an_identity")

	// Both calls reach the downstream component even though the gate would deny them.
	g.deserializer.GetAuditorVerifierReturns(&mock.Verifier{}, nil)
	g.provider.GetSignerReturns(&mock.Signer{}, nil)

	_, err := g.service.AuditorVerifier(t.Context(), id)
	require.NoError(t, err, "AuditorVerifier must not be gated")

	_, err = g.service.GetSigner(t.Context(), id)
	require.NoError(t, err, "GetSigner must not be gated")

	assert.Empty(t, gate.ops(), "the gate must not be consulted for trusted fixed-identity operations")
	assert.Empty(t, g.events.all(), "no denial events are emitted for ungated operations")
	assert.Equal(t, 1, g.deserializer.GetAuditorVerifierCallCount(), "AuditorVerifier reaches the deserializer")
	assert.Equal(t, 1, g.provider.GetSignerCallCount(), "GetSigner reaches the identity provider")
}

// TestSignatureServiceReportsOnlyDenials pins the no-double-counting rule: the operations
// themselves are instrumented where they run, so this service reports denials and nothing else.
func TestSignatureServiceReportsOnlyDenials(t *testing.T) {
	gate := &allowingGate{}
	g := newGatedService(gate)
	g.deserializer.GetOwnerVerifierReturns(&mock.Verifier{}, nil)

	_, err := g.service.OwnerVerifier(t.Context(), Identity("an_identity"))
	require.NoError(t, err)

	assert.Equal(t, 1, gate.count(), "the gate is still consulted")
	assert.Empty(t, g.events.all(), "an allowed operation is counted by the component that performs it")
}

// TestSignatureServiceLocalQuestionsAreNotGated covers AreMe and IsMe: they answer a question
// about local state and have no way to say "refused", so gating them would turn a denial into the
// wrong answer.
func TestSignatureServiceLocalQuestionsAreNotGated(t *testing.T) {
	gate := &denyingGate{}
	g := newGatedService(gate)
	id := Identity("an_identity")
	g.provider.AreMeReturns([]string{id.UniqueID()})
	g.provider.IsMeReturns(true)

	assert.Equal(t, []string{id.UniqueID()}, g.service.AreMe(t.Context(), id))
	assert.True(t, g.service.IsMe(t.Context(), id))

	assert.Empty(t, gate.ops(), "the gate must not be consulted for a question about local state")
	assert.Empty(t, g.events.all())
}

// TestSignatureServiceWithoutAGateIsUnchanged pins the default: a service built without a policy
// behaves exactly as it did before the gate existed.
func TestSignatureServiceWithoutAGateIsUnchanged(t *testing.T) {
	g := newGatedService(nil)
	expected := &mock.Verifier{}
	g.deserializer.GetOwnerVerifierReturns(expected, nil)

	verifier, err := g.service.OwnerVerifier(t.Context(), Identity("an_identity"))
	require.NoError(t, err)
	assert.Same(t, expected, verifier)
	assert.Empty(t, g.events.all())
}

// TestSignatureServiceGetAuditInfoStopsAtTheFirstDenial guards a batch call: continuing past a
// denial would perform exactly the work the policy refused.
func TestSignatureServiceGetAuditInfoStopsAtTheFirstDenial(t *testing.T) {
	gate := &denyingGate{}
	g := newGatedService(gate)

	_, err := g.service.GetAuditInfo(t.Context(), Identity("first"), Identity("second"))
	require.ErrorIs(t, err, SignatureThrottled)
	assert.Len(t, gate.ops(), 1, "the second identity is never reached")
	assert.Zero(t, g.provider.GetAuditInfoCallCount())
}

// TestSignatureServiceOptionsTolerateNil covers the wiring path where a driver has no
// observability stack to install.
func TestSignatureServiceOptionsTolerateNil(t *testing.T) {
	s := NewSignatureService(&mock.Deserializer{}, &mock.IdentityProvider{},
		WithSignatureObserver(nil), WithSignatureGate(nil))

	assert.NotNil(t, s.observer, "a nil observer must not replace the no-op one")
	assert.Nil(t, s.gate)
	require.NoError(t, s.allow(t.Context(), sigobserve.OpSign, sigobserve.RoleUnknown, Identity("an_identity")))
}
