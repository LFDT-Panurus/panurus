/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ErrIdentityNestingTooDeep is returned when a composite identity nests more deeply than
// ResourceLimits.MaxIdentityDepth allows. Callers that need to distinguish a malformed identity
// from an over-nested one can test for it with errors.Is.
var ErrIdentityNestingTooDeep = errors.New("composite identity nesting is too deep")

// ErrTooManyIdentityComponents is returned when a composite identity carries more component
// identities than ResourceLimits.MaxIdentityComponents allows.
var ErrTooManyIdentityComponents = errors.New("composite identity has too many components")

// identityNestingKey is the private context key under which the identity-nesting budget is
// carried. It is a distinct unexported type so that no other package can collide with it.
type identityNestingKey struct{}

// identityNesting is the budget for one descent through a composite identity: the limits in
// force, plus how many levels the current path has already consumed.
type identityNesting struct {
	depth         int
	maxDepth      int
	maxComponents int
}

// WithIdentityNestingLimits returns a copy of ctx carrying the bounds that
// EnterCompositeIdentity and MaxIdentityComponentsFrom enforce while deserializing composite
// identities. Non-positive values are replaced by the corresponding defaults, so a partially
// specified ResourceLimits can never silently disable a bound.
//
// Validators should seed this from the ResourceLimits they were built with, so that the bound a
// peer applies to an untrusted owner identity is the configured one. Any descent whose context
// was never seeded still gets the defaults - see EnterCompositeIdentity.
func WithIdentityNestingLimits(ctx context.Context, maxDepth, maxComponents int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	d := DefaultResourceLimits()
	if maxDepth <= 0 {
		maxDepth = d.MaxIdentityDepth
	}
	if maxComponents <= 0 {
		maxComponents = d.MaxIdentityComponents
	}

	return context.WithValue(ctx, identityNestingKey{}, identityNesting{
		maxDepth:      maxDepth,
		maxComponents: maxComponents,
	})
}

// nestingFrom returns the nesting budget carried by ctx, falling back to a fresh budget built
// from the default limits when ctx carries none.
//
// Defaulting rather than treating an unseeded context as unbounded is deliberate. Composite
// identity deserialization is reachable from paths that have no ResourceLimits to seed - wallets
// resolving a recipient, an auditor inspecting a request, tests - and the failure mode of a
// seeding site that is added later and forgotten should be a bound that still holds, not one that
// is silently switched off. Legitimate identities nest 2-3 levels, well inside the defaults.
func nestingFrom(ctx context.Context) identityNesting {
	if ctx != nil {
		if n, ok := ctx.Value(identityNestingKey{}).(identityNesting); ok {
			return n
		}
	}
	d := DefaultResourceLimits()

	return identityNesting{maxDepth: d.MaxIdentityDepth, maxComponents: d.MaxIdentityComponents}
}

// EnterCompositeIdentity accounts for one level of composite-identity nesting and returns the
// context to use for the components one level down. It returns an error wrapping
// ErrIdentityNestingTooDeep once the path has consumed MaxIdentityDepth levels.
//
// Call it at the top of every step that recurses into a component identity, and pass the
// *returned* context to the components - passing the original one down leaves the counter at zero
// and the bound unenforced. Because the count rides in the context, it is per-path: sibling
// components each start from their parent's depth rather than sharing a running total. That is the
// right semantics for a depth bound, and it is why the fan-out bound below is needed too.
func EnterCompositeIdentity(ctx context.Context) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	n := nestingFrom(ctx)
	if n.depth >= n.maxDepth {
		return nil, errors.Wrapf(ErrIdentityNestingTooDeep, "nesting exceeds the maximum depth of %d", n.maxDepth)
	}
	n.depth++

	return context.WithValue(ctx, identityNestingKey{}, n), nil
}

// MaxIdentityComponentsFrom returns the maximum number of component identities a single composite
// identity may carry under the limits in force for ctx.
func MaxIdentityComponentsFrom(ctx context.Context) int {
	return nestingFrom(ctx).maxComponents
}
