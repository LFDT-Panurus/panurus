/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"testing"

	view2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/view"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubIdentityProvider is the slice of FSC's identity provider the membership uses.
type stubIdentityProvider struct {
	def view2.Identity
}

func (s *stubIdentityProvider) DefaultIdentity() view2.Identity { return s.def }

func (s *stubIdentityProvider) Identity(string) view2.Identity { return s.def }

// TestLocalMembershipDelegates checks the driver reports the node's real identity rather than one of
// its own. The token drivers read DefaultIdentity while building a TMS, so a wrong answer here is a
// startup failure rather than a subtle one.
func TestLocalMembershipDelegates(t *testing.T) {
	want := view2.Identity("node-identity")
	m := newLocalMembership(&stubIdentityProvider{def: want})
	require.NotNil(t, m)

	assert.Equal(t, want, m.DefaultIdentity())

	anonymous, err := m.AnonymousIdentity()
	require.NoError(t, err)
	assert.Equal(t, want, anonymous,
		"EVM has no network-layer anonymity source, so this is the default identity by design")
}

// TestLocalMembershipWithoutProvider checks a network built without an identity provider keeps
// reporting nil, rather than a membership that would answer with empty identities.
func TestLocalMembershipWithoutProvider(t *testing.T) {
	assert.Nil(t, newLocalMembership(nil))
}

// TestNetworkExposesMembership checks the adapter reaches the driver contract.
func TestNetworkExposesMembership(t *testing.T) {
	c := validConfig()
	c.applyDefaults()
	require.NoError(t, c.Validate())

	want := view2.Identity("node-identity")
	n, err := NewNetwork("evm-net", &mock.EVMClient{}, []NamespaceConfig{{Namespace: "token", Config: c}}, nil,
		newLocalMembership(&stubIdentityProvider{def: want}))
	require.NoError(t, err)

	membership := n.LocalMembership()
	require.NotNil(t, membership, "a nil membership panics the token drivers during TMS construction")
	assert.Equal(t, want, membership.DefaultIdentity())
}
