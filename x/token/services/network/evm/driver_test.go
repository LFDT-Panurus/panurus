/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeResolver reports a fixed set of (network|channel) pairs as EVM networks and yields a minimal
// valid configuration for them.
type fakeResolver struct {
	evm map[string]bool
}

func (f fakeResolver) IsEVMNetwork(network, channel string) bool {
	return f.evm[network+"|"+channel]
}

func (f fakeResolver) ConfigFor(network, channel string) (*Config, error) {
	if !f.IsEVMNetwork(network, channel) {
		return nil, errors.Errorf("no evm configuration for [%s:%s]", network, channel)
	}
	c := validConfig()
	c.applyDefaults()

	return c, c.Validate()
}

func TestDriverNewRouting(t *testing.T) {
	d := &Driver{resolver: fakeResolver{evm: map[string]bool{"evm-net|": true}}}

	// An EVM-configured network yields a Network.
	n, err := d.New("evm-net", "")
	require.NoError(t, err)
	require.NotNil(t, n)
	assert.Equal(t, "evm-net", n.Name())
	assert.Empty(t, n.Channel())

	// A non-EVM network must error so the provider falls through to the next driver.
	_, err = d.New("fabric-net", "")
	require.Error(t, err)

	// The right network name but a mismatched channel must also fall through.
	_, err = d.New("evm-net", "other-channel")
	assert.Error(t, err)
}

// TestNetworkDeferredSurface documents the methods that land with the finality manager: they are
// wired to the interface and return a clear error rather than panicking.
func TestNetworkDeferredSurface(t *testing.T) {
	n := testNetwork(t, nil, nil)

	assert.NotNil(t, n.NewEnvelope())
	_, err := n.Ledger()
	require.ErrorIs(t, err, errNotImplemented)
	err = n.AddFinalityListener("ns", "tx", nil)
	require.ErrorIs(t, err, errNotImplemented)
	status, _, _, err := n.GetTransactionStatus(t.Context(), "ns", "tx")
	require.ErrorIs(t, err, errNotImplemented)
	assert.Equal(t, 4, status, "an unresolvable status must be Unknown (4), never the invalid zero code")
}
