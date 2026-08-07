/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"

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

// TMSIDsFor reports one TMS per configured network, enough for the routing tests: they never exercise
// the public-parameters watcher, which needs a TMS provider the fake does not have.
func (f fakeResolver) TMSIDsFor(network, channel string) []token2.TMSID {
	if !f.IsEVMNetwork(network, channel) {
		return nil
	}

	return []token2.TMSID{{Network: network, Channel: channel, Namespace: "token"}}
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

// TestNetworkSurfaceIsWired checks the methods that used to be stubs now answer through the finality
// manager rather than returning a not-implemented error.
func TestNetworkSurfaceIsWired(t *testing.T) {
	evm := &mock.EVMClient{}
	// getTokenRequestHash returns the zero hash: the anchor has not been applied.
	evm.CallReturns(make([]byte, 32), nil)
	n := testNetwork(t, evm, nil)

	assert.NotNil(t, n.NewEnvelope())

	ledger, err := n.Ledger()
	require.NoError(t, err)
	require.NotNil(t, ledger)

	// An anchor the chain has never seen is Unknown: never the invalid zero code, and never Invalid,
	// because a reverted apply is indistinguishable from one still pending (design 7.4).
	status, hash, _, err := n.GetTransactionStatus(t.Context(), "token", anchorHex(0x01))
	require.NoError(t, err)
	assert.Equal(t, driver.Unknown, status)
	assert.Nil(t, hash)

	code, err := ledger.Status(anchorHex(0x01))
	require.NoError(t, err)
	assert.Equal(t, driver.Unknown, code)
}

// TestNetworkRejectsMalformedTransactionID checks the anchor-shaped identifier is validated rather
// than silently producing a wrong on-chain lookup.
func TestNetworkRejectsMalformedTransactionID(t *testing.T) {
	n := testNetwork(t, nil, nil)

	_, _, _, err := n.GetTransactionStatus(t.Context(), "token", "not-a-valid-anchor")
	require.Error(t, err)

	err = n.AddFinalityListener("token", "not-a-valid-anchor", nil)
	require.Error(t, err)
}
