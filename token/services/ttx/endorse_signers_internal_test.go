/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// answers "me" for every identity, so the tests see the full required set
type everyoneIsMe struct{}

func (everyoneIsMe) IsMe(context.Context, token.Identity) (bool, error) { return true, nil }

func issueAction(issuer token.Identity, extraSigners ...token.Identity) *driver.ActionMetadataEntry {
	extra := make([]driver.AuditableIdentity, len(extraSigners))
	for i, signer := range extraSigners {
		extra[i] = driver.AuditableIdentity{Identity: signer}
	}

	return &driver.ActionMetadataEntry{
		IssueMetadata: &driver.IssueMetadata{
			Issuer:       driver.AuditableIdentity{Identity: issuer},
			ExtraSigners: extra,
		},
	}
}

func transferAction(senders ...token.Identity) *driver.ActionMetadataEntry {
	auditable := make([]*driver.AuditableIdentity, len(senders))
	for i, sender := range senders {
		auditable[i] = &driver.AuditableIdentity{Identity: sender}
	}

	return &driver.ActionMetadataEntry{
		TransferMetadata: &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{{Senders: auditable}},
		},
	}
}

func requestWithActions(actions ...*driver.ActionMetadataEntry) *token.Request {
	req := token.NewRequest(nil, "an_anchor")
	for i, action := range actions {
		action.ActionID = uint32(i)
	}
	req.Metadata.Actions = actions

	return req
}

func mustWrapMultisig(t *testing.T, ids ...token.Identity) token.Identity {
	t.Helper()
	id, err := multisig.WrapIdentities(ids...)
	require.NoError(t, err)

	return id
}

// skips WrapIdentities' validation, standing in for an identity received from a peer
func mustWrapRawMultisig(t *testing.T, ids ...token.Identity) token.Identity {
	t.Helper()
	inner, err := (&multisig.MultiIdentity{Identities: ids}).Bytes()
	require.NoError(t, err)
	raw, err := (&identity.TypedIdentity{Type: multisig.Multisig, Identity: inner}).Bytes()
	require.NoError(t, err)

	return raw
}

// Regression test for #2557: the responder must expect no more requests than the initiator
// sends, which dedups per group (issues, transfers, each composite occurrence).
func TestExtractRequiredSignersDedup(t *testing.T) {
	alice := token.Identity("alice")
	bob := token.Identity("bob")

	carol := token.Identity("carol")

	aliceAndBob, err := multisig.WrapIdentities(alice, bob)
	require.NoError(t, err)
	aliceOrBob, err := boolpolicy.WrapPolicyIdentity("$0 OR $1", alice, bob)
	require.NoError(t, err)

	tests := []struct {
		name     string
		request  *token.Request
		expected []token.Identity
	}{
		{
			name:     "same sender in two transfers is expected once",
			request:  requestWithActions(transferAction(alice), transferAction(alice)),
			expected: []token.Identity{alice},
		},
		{
			name:     "same sender twice in one transfer is expected once",
			request:  requestWithActions(transferAction(alice, alice)),
			expected: []token.Identity{alice},
		},
		{
			name:     "same issuer in two issues is expected once",
			request:  requestWithActions(issueAction(alice), issueAction(alice)),
			expected: []token.Identity{alice},
		},
		{
			name:     "issuer repeated as an extra signer is expected once",
			request:  requestWithActions(issueAction(alice, alice)),
			expected: []token.Identity{alice},
		},
		{
			name:     "identity signing an issue and a transfer is expected twice",
			request:  requestWithActions(issueAction(alice), transferAction(alice)),
			expected: []token.Identity{alice, alice},
		},
		{
			name:     "distinct senders are all expected",
			request:  requestWithActions(transferAction(alice), transferAction(bob)),
			expected: []token.Identity{alice, bob},
		},
		{
			name:     "multisig components are expected once per occurrence",
			request:  requestWithActions(transferAction(aliceAndBob), transferAction(aliceAndBob)),
			expected: []token.Identity{alice, bob, alice, bob},
		},
		{
			name:     "policy components are expected once per occurrence",
			request:  requestWithActions(transferAction(aliceOrBob), transferAction(aliceOrBob)),
			expected: []token.Identity{alice, bob, alice, bob},
		},
		{
			// Unwrap does not re-check for duplicates, so a peer's identity can repeat one
			name:     "duplicate components of one multisig identity are expected once",
			request:  requestWithActions(transferAction(mustWrapRawMultisig(t, alice, alice))),
			expected: []token.Identity{alice},
		},
		{
			name:     "nested composite identities expand to their plain signers",
			request:  requestWithActions(transferAction(mustWrapMultisig(t, aliceAndBob, carol))),
			expected: []token.Identity{alice, bob, carol},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			signers, err := extractRequiredSigners(t.Context(), everyoneIsMe{}, test.request)
			require.NoError(t, err)
			assert.Equal(t, test.expected, signers)
		})
	}
}

// Identities come off the wire, so expansion must not recurse until the stack runs out.
func TestExtractRequiredSignersBoundsNesting(t *testing.T) {
	id := token.Identity("alice")
	for range driver.DefaultResourceLimits().MaxIdentityDepth + 1 {
		id = mustWrapMultisig(t, id)
	}

	_, err := extractRequiredSigners(t.Context(), everyoneIsMe{}, requestWithActions(transferAction(id)))
	require.ErrorIs(t, err, driver.ErrIdentityNestingTooDeep)
}
