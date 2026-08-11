/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A context that was never seeded must still be bounded, by the defaults. Composite identity
// deserialization is reachable from paths that have no ResourceLimits to seed, and the failure mode
// of a seeding site that is added later and forgotten must be a bound that still holds.
func TestEnterCompositeIdentity_UnseededContextUsesDefaults(t *testing.T) {
	maxDepth := DefaultResourceLimits().MaxIdentityDepth
	require.Positive(t, maxDepth)

	ctx := context.Background()
	for i := range maxDepth {
		next, err := EnterCompositeIdentity(ctx)
		require.NoError(t, err, "descent to depth %d should be allowed", i+1)
		//nolint:fatcontext // deriving one context per level is the mechanism under test: each
		// iteration stands in for one step down a nested composite identity, which is exactly how
		// the deserializers descend.
		ctx = next
	}

	_, err := EnterCompositeIdentity(ctx)
	require.ErrorIs(t, err, ErrIdentityNestingTooDeep)
}

func TestEnterCompositeIdentity_NilContextUsesDefaults(t *testing.T) {
	//nolint:staticcheck // exercising the nil-context guard deliberately
	ctx, err := EnterCompositeIdentity(nil)
	require.NoError(t, err)
	require.NotNil(t, ctx)
}

func TestEnterCompositeIdentity_HonoursConfiguredDepth(t *testing.T) {
	ctx := WithIdentityNestingLimits(context.Background(), 2, 4)

	ctx, err := EnterCompositeIdentity(ctx)
	require.NoError(t, err)
	ctx, err = EnterCompositeIdentity(ctx)
	require.NoError(t, err)

	_, err = EnterCompositeIdentity(ctx)
	require.ErrorIs(t, err, ErrIdentityNestingTooDeep)
	assert.Contains(t, err.Error(), "maximum depth of 2")
}

// The depth counter rides in the context, so it is per-path: two sibling components each descend
// from their parent's depth rather than sharing a running total. This is the property that makes
// the separate fan-out bound necessary, so pin it explicitly.
func TestEnterCompositeIdentity_DepthIsPerPathNotGlobal(t *testing.T) {
	root := WithIdentityNestingLimits(context.Background(), 2, 4)

	parent, err := EnterCompositeIdentity(root)
	require.NoError(t, err)

	// both siblings descend one level from the same parent, and both are allowed
	firstChild, err := EnterCompositeIdentity(parent)
	require.NoError(t, err)
	secondChild, err := EnterCompositeIdentity(parent)
	require.NoError(t, err)

	// each has now consumed the full budget along its own path
	_, err = EnterCompositeIdentity(firstChild)
	require.ErrorIs(t, err, ErrIdentityNestingTooDeep)
	_, err = EnterCompositeIdentity(secondChild)
	require.ErrorIs(t, err, ErrIdentityNestingTooDeep)
}

// A non-positive limit must not be readable as "unlimited": zero would make the very first descent
// fail, and a negative value would make every descent succeed.
func TestWithIdentityNestingLimits_NonPositiveFallsBackToDefaults(t *testing.T) {
	d := DefaultResourceLimits()

	for _, tc := range []struct {
		name                    string
		maxDepth, maxComponents int
	}{
		{"zero", 0, 0},
		{"negative", -1, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithIdentityNestingLimits(context.Background(), tc.maxDepth, tc.maxComponents)

			assert.Equal(t, d.MaxIdentityComponents, MaxIdentityComponentsFrom(ctx))

			_, err := EnterCompositeIdentity(ctx)
			require.NoError(t, err, "the first descent must be allowed")
		})
	}
}

func TestMaxIdentityComponentsFrom(t *testing.T) {
	assert.Equal(t, DefaultResourceLimits().MaxIdentityComponents, MaxIdentityComponentsFrom(context.Background()))
	//nolint:staticcheck // exercising the nil-context guard deliberately
	assert.Equal(t, DefaultResourceLimits().MaxIdentityComponents, MaxIdentityComponentsFrom(nil))
	assert.Equal(t, 7, MaxIdentityComponentsFrom(WithIdentityNestingLimits(context.Background(), 3, 7)))
}

// The component cap must survive a descent, so that a nested composite identity is held to the same
// fan-out bound as the outermost one.
func TestMaxIdentityComponentsFrom_SurvivesDescent(t *testing.T) {
	ctx, err := EnterCompositeIdentity(WithIdentityNestingLimits(context.Background(), 4, 7))
	require.NoError(t, err)
	ctx, err = EnterCompositeIdentity(ctx)
	require.NoError(t, err)

	assert.Equal(t, 7, MaxIdentityComponentsFrom(ctx))
}

// Callers distinguish an over-nested identity from a malformed one with errors.Is, so the sentinel
// has to survive the wrapping the deserializers add on the way out.
func TestErrIdentityNestingTooDeep_SurvivesWrapping(t *testing.T) {
	ctx := WithIdentityNestingLimits(context.Background(), 1, 4)
	ctx, err := EnterCompositeIdentity(ctx)
	require.NoError(t, err)

	_, err = EnterCompositeIdentity(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, errors.Wrap(err, "cannot deserialize multisig identity"), ErrIdentityNestingTooDeep)
}
