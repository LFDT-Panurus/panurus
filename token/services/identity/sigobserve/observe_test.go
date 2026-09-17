/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sigobserve_test

import (
	"context"
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorder is an Observer that keeps every event it is handed.
type recorder struct {
	mu     sync.Mutex
	events []sigobserve.Event
}

func (r *recorder) Observe(_ context.Context, e sigobserve.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) all() []sigobserve.Event {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]sigobserve.Event(nil), r.events...)
}

func (r *recorder) one(t *testing.T) sigobserve.Event {
	t.Helper()
	events := r.all()
	require.Len(t, events, 1)

	return events[0]
}

func TestNopDropsEvents(t *testing.T) {
	// The contract is only that it does not panic and reports nothing: there is nothing to
	// observe about a dropped event.
	sigobserve.Nop.Observe(t.Context(), sigobserve.Event{Op: sigobserve.OpSign})
}

func TestObserverFunc(t *testing.T) {
	var got sigobserve.Event
	var f sigobserve.Observer = sigobserve.ObserverFunc(func(_ context.Context, e sigobserve.Event) { got = e })

	f.Observe(t.Context(), sigobserve.Event{Op: sigobserve.OpVerify, Principal: "hash"})
	assert.Equal(t, sigobserve.OpVerify, got.Op)
	assert.Equal(t, "hash", got.Principal)
}

func TestMulti(t *testing.T) {
	t.Run("no effective observer collapses to Nop", func(t *testing.T) {
		assert.Equal(t, sigobserve.Nop, sigobserve.Multi())
		assert.Equal(t, sigobserve.Nop, sigobserve.Multi(nil, nil))
		assert.Equal(t, sigobserve.Nop, sigobserve.Multi(nil, sigobserve.Nop))
	})

	t.Run("a single effective observer is returned unwrapped", func(t *testing.T) {
		r := &recorder{}
		assert.Same(t, r, sigobserve.Multi(nil, r, sigobserve.Nop))
	})

	t.Run("fans out in order", func(t *testing.T) {
		var order []string
		first := sigobserve.ObserverFunc(func(context.Context, sigobserve.Event) { order = append(order, "first") })
		second := sigobserve.ObserverFunc(func(context.Context, sigobserve.Event) { order = append(order, "second") })

		sigobserve.Multi(first, nil, second).Observe(t.Context(), sigobserve.Event{})
		assert.Equal(t, []string{"first", "second"}, order)
	})
}

func TestTimerDone(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		r := &recorder{}
		sigobserve.Start(r, sigobserve.OpGetSigner, "hash", sigobserve.RoleOwner).Done(t.Context(), nil)

		e := r.one(t)
		assert.Equal(t, sigobserve.OpGetSigner, e.Op)
		assert.Equal(t, "hash", e.Principal)
		assert.Equal(t, sigobserve.RoleOwner, e.Role)
		assert.Equal(t, sigobserve.OutcomeOK, e.Outcome)
		require.NoError(t, e.Err)
		assert.Positive(t, e.Duration)
	})

	t.Run("failure", func(t *testing.T) {
		r := &recorder{}
		expected := errors.New("boom")
		sigobserve.Start(r, sigobserve.OpBind, "hash", sigobserve.RoleUnknown).Done(t.Context(), expected)

		e := r.one(t)
		assert.Equal(t, sigobserve.OutcomeError, e.Outcome)
		assert.Equal(t, expected, e.Err)
	})
}

func TestTimerDoneVerifyMapsErrorsToInvalid(t *testing.T) {
	r := &recorder{}
	expected := errors.New("signature mismatch")
	sigobserve.Start(r, sigobserve.OpVerify, "hash", sigobserve.RoleIssuer).DoneVerify(t.Context(), expected)
	sigobserve.Start(r, sigobserve.OpVerify, "hash", sigobserve.RoleIssuer).DoneVerify(t.Context(), nil)

	events := r.all()
	require.Len(t, events, 2)
	assert.Equal(t, sigobserve.OutcomeInvalid, events[0].Outcome, "a rejected signature is invalid, not an error")
	assert.Equal(t, expected, events[0].Err)
	assert.Equal(t, sigobserve.OutcomeOK, events[1].Outcome)
}

func TestTimerDoneThrottled(t *testing.T) {
	r := &recorder{}
	expected := errors.New("denied")
	sigobserve.Start(r, sigobserve.OpOwnerVerifier, "hash", sigobserve.RoleOwner).DoneThrottled(t.Context(), expected)

	e := r.one(t)
	assert.Equal(t, sigobserve.OutcomeThrottled, e.Outcome)
	assert.Equal(t, expected, e.Err)
}

func TestTimerDoneResolution(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		err      error
		outcome  sigobserve.Outcome
		cacheHit bool
	}{
		{name: "cache hit", path: sigobserve.PathCache, outcome: sigobserve.OutcomeOK, cacheHit: true},
		{name: "routed", path: sigobserve.PathRouted, outcome: sigobserve.OutcomeOK},
		{name: "fallback", path: sigobserve.PathFallback, outcome: sigobserve.OutcomeOK},
		{name: "failed", path: sigobserve.PathFallback, err: errors.New("no signer"), outcome: sigobserve.OutcomeError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := &recorder{}
			sigobserve.Start(r, sigobserve.OpGetSigner, "hash", sigobserve.RoleUnknown).
				DoneResolution(t.Context(), test.path, test.err)

			e := r.one(t)
			assert.Equal(t, test.path, e.Path)
			assert.Equal(t, test.outcome, e.Outcome)
			assert.True(t, e.CacheChecked, "a resolution always consults the cache")
			assert.Equal(t, test.cacheHit, e.CacheHit)
		})
	}
}

func TestStartToleratesANilObserver(t *testing.T) {
	sigobserve.Start(nil, sigobserve.OpSign, "hash", sigobserve.RoleUnknown).Done(t.Context(), nil)
}

// TestTimerAllocatesNothing guards the claim that instrumenting a hot path with a Timer is free
// when the observer drops the event: a regression here would put an allocation on every Sign.
func TestTimerAllocatesNothing(t *testing.T) {
	ctx := t.Context()
	allocs := testing.AllocsPerRun(100, func() {
		sigobserve.Start(sigobserve.Nop, sigobserve.OpSign, "hash", sigobserve.RoleUnknown).Done(ctx, nil)
	})
	assert.InDelta(t, 0.0, allocs, 0, "a Timer reporting to Nop should not allocate")
}
