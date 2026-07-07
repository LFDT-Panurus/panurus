/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeResolver reports a fixed set of (network|channel) pairs as EVM networks.
type fakeResolver struct {
	evm map[string]bool
}

func (f fakeResolver) IsEVMNetwork(network, channel string) bool {
	return f.evm[network+"|"+channel]
}

func TestDriverNewRouting(t *testing.T) {
	d := &Driver{resolver: fakeResolver{evm: map[string]bool{"evm-net|": true}}}

	// An EVM-configured network yields a Network.
	n, err := d.New("evm-net", "")
	require.NoError(t, err)
	require.NotNil(t, n)
	assert.Equal(t, "evm-net", n.Name())
	assert.Equal(t, "", n.Channel())

	// A non-EVM network must error so the provider falls through to the next driver.
	_, err = d.New("fabric-net", "")
	assert.Error(t, err)

	// The right network name but a mismatched channel must also fall through.
	_, err = d.New("evm-net", "other-channel")
	assert.Error(t, err)
}

// TestNetworkStubNotImplemented documents that the skeleton's behavioural methods are wired to the
// interface but not yet implemented, so a mis-registration surfaces as a clear error rather than a
// nil-pointer panic.
func TestNetworkStubNotImplemented(t *testing.T) {
	n := newNetwork("evm-net")

	assert.NotNil(t, n.NewEnvelope())
	err := n.Broadcast(t.Context(), &Envelope{})
	assert.ErrorIs(t, err, errNotImplemented)
	_, err = n.Ledger()
	assert.ErrorIs(t, err, errNotImplemented)
}
