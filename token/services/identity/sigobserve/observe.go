/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package sigobserve carries the event vocabulary of the Signer and Verifier services: one
// Event per completed operation, and an Observer that consumes them.
//
// The package deliberately depends on nothing but the driver types. Metrics, audit logging
// and throttle escalation are all Observers living in their own packages, so instrumenting a
// call site never drags a metrics provider or a policy engine into it, and package token can
// import this vocabulary without an import cycle.
//
// Observers run inline on the signing and verification hot paths. Implementations must be
// safe for concurrent use and must not block.
package sigobserve

import (
	"context"
	"time"
)

// Op identifies an instrumented Signer or Verifier service operation.
type Op string

const (
	// OpGetSigner is a signer resolution (identity.Provider.GetSigner).
	OpGetSigner Op = "get_signer"
	// OpRegisterSigner is the registration of a signer/verifier pair for an identity.
	OpRegisterSigner Op = "register_signer"
	// OpRegisterIdentityDescriptor is the registration of a full identity descriptor.
	OpRegisterIdentityDescriptor Op = "register_identity_descriptor"
	// OpIsMe is an "is this identity mine" lookup (IsMe/AreMe).
	OpIsMe Op = "is_me"
	// OpGetAuditInfo is an audit-info lookup for an identity.
	OpGetAuditInfo Op = "get_audit_info"
	// OpBind is the binding of ephemeral identities to a long-term one.
	OpBind Op = "bind"
	// OpOwnerVerifier is the resolution of an owner's verifier.
	OpOwnerVerifier Op = "owner_verifier"
	// OpIssuerVerifier is the resolution of an issuer's verifier.
	OpIssuerVerifier Op = "issuer_verifier"
	// OpAuditorVerifier is the resolution of an auditor's verifier.
	OpAuditorVerifier Op = "auditor_verifier"
	// OpSign is an invocation of a resolved signer.
	OpSign Op = "sign"
	// OpVerify is an invocation of a resolved verifier.
	OpVerify Op = "verify"
	// OpEscalation is a change of a principal's throttle level. It reports policy state, not
	// a service call, so it carries no duration.
	OpEscalation Op = "escalation"
)

// Role is the role an identity plays in a token transaction, when the instrumented
// operation is specific to one.
type Role string

const (
	// RoleOwner is a token owner.
	RoleOwner Role = "owner"
	// RoleIssuer is a token issuer.
	RoleIssuer Role = "issuer"
	// RoleAuditor is an auditor.
	RoleAuditor Role = "auditor"
	// RoleUnknown is used for operations that are not tied to a single role.
	RoleUnknown Role = "unknown"
)

// Outcome is how an instrumented operation ended.
type Outcome string

const (
	// OutcomeOK is a successful operation.
	OutcomeOK Outcome = "ok"
	// OutcomeError is an operation that failed for reasons other than an invalid signature:
	// a missing signer, a storage error, a malformed identity.
	OutcomeError Outcome = "error"
	// OutcomeInvalid is a verification that did not succeed. It does not separate a forged
	// signature from a malformed input - a Verifier reports both as a plain error - which is
	// exactly why it is the signal to watch: a principal driving this counter up is either
	// broken or probing.
	OutcomeInvalid Outcome = "invalid"
	// OutcomeThrottled is an operation denied by policy before it ran.
	OutcomeThrottled Outcome = "throttled"
)

// Resolution paths reported by signer resolution, describing how the signer was obtained.
const (
	// PathCache is a hit in the signer cache.
	PathCache = "cache"
	// PathRouted is a conf_id-pinned SignerRouter hit.
	PathRouted = "routed"
	// PathFallback is the linear-scan probing deserializer.
	PathFallback = "fallback"
)

// Event describes one completed Signer or Verifier service operation.
type Event struct {
	// Op is the operation performed.
	Op Op
	// Principal identifies the identity the operation was performed for. It is an identity
	// hash (driver.Identity.UniqueID()), never raw identity bytes: it is stable, it is
	// already the signer cache key, and it keeps identity material out of logs.
	Principal string
	// Role is the identity's role, or RoleUnknown when the operation spans roles.
	Role Role
	// Outcome is how the operation ended.
	Outcome Outcome
	// Path reports how a signer resolution was satisfied (PathCache, PathRouted,
	// PathFallback). It is empty for operations that resolve nothing.
	Path string
	// CacheChecked reports whether this operation consulted the signer cache, and hence
	// whether CacheHit carries a meaning.
	CacheChecked bool
	// CacheHit reports whether the consulted cache had the entry.
	CacheHit bool
	// Duration is the wall-clock time the operation took. It is zero for OpEscalation.
	Duration time.Duration
	// Level is the throttle level a principal moved to, for OpEscalation events.
	Level string
	// Reason explains an OpEscalation event ("rate", "error_rate", "invalid_signature_rate",
	// "quiet_period").
	Reason string
	// Err is the error the operation failed with, if any.
	Err error
}

// Observer consumes operation events.
type Observer interface {
	// Observe records a completed operation. It must not block, must tolerate being called
	// concurrently, and must not retain the Event's Err beyond the call.
	Observe(ctx context.Context, e Event)
}

// Gate decides whether an operation on behalf of a principal may proceed. It is declared here,
// next to the event vocabulary, so that a policy implementation and the client-facing service
// that consults it can agree on the contract without depending on each other.
//
// Implementations must be safe for concurrent use and must not block.
type Gate interface {
	// Allow reports whether op on behalf of principal (an identity hash) may proceed,
	// returning nil when it may.
	Allow(ctx context.Context, principal string, op Op) error
}

// ObserverFunc adapts a function to the Observer interface.
type ObserverFunc func(ctx context.Context, e Event)

// Observe calls f.
func (f ObserverFunc) Observe(ctx context.Context, e Event) { f(ctx, e) }

// nopObserver drops every event.
type nopObserver struct{}

// Observe does nothing.
func (nopObserver) Observe(context.Context, Event) {}

// Nop is an Observer that drops every event. It is what a caller with no instrumentation
// configured should use, so call sites never have to nil-check.
var Nop Observer = nopObserver{}

// multiObserver fans an event out to several observers.
type multiObserver []Observer

// Observe forwards e to every observer in order.
func (m multiObserver) Observe(ctx context.Context, e Event) {
	for _, o := range m {
		o.Observe(ctx, e)
	}
}

// Multi returns an Observer that forwards every event to all of the passed observers, in
// order. Nil observers are dropped; the result of Multi with no effective observer is Nop,
// and with exactly one it is that observer itself, so the common cases cost no extra
// indirection.
func Multi(observers ...Observer) Observer {
	effective := make([]Observer, 0, len(observers))
	for _, o := range observers {
		if o == nil || o == Nop {
			continue
		}
		effective = append(effective, o)
	}

	switch len(effective) {
	case 0:
		return Nop
	case 1:
		return effective[0]
	default:
		return multiObserver(effective)
	}
}

// Timer measures one operation and reports it to an Observer when it ends. It is a value
// type holding no heap state, so instrumenting a hot path with it allocates nothing.
//
// Typical use:
//
//	t := sigobserve.Start(o, sigobserve.OpSign, principal, role)
//	sigma, err := signer.Sign(message)
//	t.Done(ctx, err)
type Timer struct {
	observer  Observer
	op        Op
	principal string
	role      Role
	start     time.Time
}

// Start begins measuring an operation. A nil observer is treated as Nop.
func Start(o Observer, op Op, principal string, role Role) Timer {
	if o == nil {
		o = Nop
	}

	return Timer{observer: o, op: op, principal: principal, role: role, start: time.Now()}
}

// Done reports the operation as OutcomeOK when err is nil and OutcomeError otherwise.
func (t Timer) Done(ctx context.Context, err error) {
	t.emit(ctx, Event{Outcome: outcomeOf(err), Err: err})
}

// DoneVerify reports a verification, mapping a non-nil err to OutcomeInvalid rather than
// OutcomeError: a Verifier that returns an error has rejected the signature, and that
// rejection is the security signal callers watch.
func (t Timer) DoneVerify(ctx context.Context, err error) {
	outcome := OutcomeOK
	if err != nil {
		outcome = OutcomeInvalid
	}
	t.emit(ctx, Event{Outcome: outcome, Err: err})
}

// DoneThrottled reports the operation as denied by policy, with the error returned to the
// caller.
func (t Timer) DoneThrottled(ctx context.Context, err error) {
	t.emit(ctx, Event{Outcome: OutcomeThrottled, Err: err})
}

// DoneResolution reports a signer resolution, adding the path it took and whether the cache
// was consulted and hit.
func (t Timer) DoneResolution(ctx context.Context, path string, err error) {
	t.emit(ctx, Event{
		Outcome:      outcomeOf(err),
		Path:         path,
		CacheChecked: true,
		CacheHit:     err == nil && path == PathCache,
		Err:          err,
	})
}

// emit fills in the fields the Timer owns and hands the event to the observer.
func (t Timer) emit(ctx context.Context, e Event) {
	e.Op = t.op
	e.Principal = t.principal
	e.Role = t.role
	e.Duration = time.Since(t.start)
	t.observer.Observe(ctx, e)
}

// outcomeOf maps an error to the outcome of a non-verification operation.
func outcomeOf(err error) Outcome {
	if err != nil {
		return OutcomeError
	}

	return OutcomeOK
}
