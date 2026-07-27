/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
)

func addr(low byte) client.Address {
	var a client.Address
	a[client.AddressLength-1] = low

	return a
}

func endorser(idName string, low byte) Endorser {
	return Endorser{Identity: view.Identity(idName), Address: addr(low)}
}

func TestRegistryResolvesBothDirections(t *testing.T) {
	reg, err := NewRegistry([]Endorser{endorser("alice", 1), endorser("bob", 2)})
	require.NoError(t, err)
	require.Equal(t, 2, reg.Len())

	id, ok := reg.IdentityOf(addr(1))
	require.True(t, ok)
	assert.Equal(t, view.Identity("alice"), id)
	assert.True(t, reg.IsEndorser(addr(2)))

	_, ok = reg.IdentityOf(addr(9))
	assert.False(t, ok, "unknown address is not an endorser")
	assert.False(t, reg.IsEndorser(addr(9)))
}

func TestRegistryExposesSetsAsCopies(t *testing.T) {
	reg, err := NewRegistry([]Endorser{endorser("alice", 1), endorser("bob", 2)})
	require.NoError(t, err)

	ids := reg.Identities()
	addrs := reg.Addresses()
	require.Len(t, ids, 2)
	require.Len(t, addrs, 2)

	// mutating the returned slices must not affect the registry
	ids[0] = view.Identity("tampered")
	addrs[0] = addr(0xFF)
	again, _ := reg.IdentityOf(addr(1))
	assert.Equal(t, view.Identity("alice"), again, "registry must be immutable to callers")
}

func TestRegistryRejectsMalformedSets(t *testing.T) {
	cases := map[string][]Endorser{
		"empty set":          {},
		"missing identity":   {{Identity: nil, Address: addr(1)}},
		"zero address":       {{Identity: view.Identity("alice"), Address: client.Address{}}},
		"duplicate address":  {endorser("alice", 1), endorser("bob", 1)},
		"duplicate identity": {endorser("alice", 1), endorser("alice", 2)},
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewRegistry(set)
			require.Error(t, err)
		})
	}
}
