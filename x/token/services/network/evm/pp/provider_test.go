/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/abi"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
)

// newProviderHarness returns a provider reading a chainState a test can move underneath it, the way
// an endorsed setup delta does.
func newProviderHarness(t *testing.T, state *chainState) *ChainProvider {
	t.Helper()
	tokenState, err := client.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	require.NoError(t, err)

	evmClient := &mock.EVMClient{}
	evmClient.CallStub = func(_ context.Context, _ client.Address, data []byte, _ string) ([]byte, error) {
		raw, version := state.get()
		switch string(data) {
		case string(abi.MethodID("getPublicParameters()")):
			return abiBytesFor(raw), nil
		case string(abi.MethodID("getPublicParamsVersion()")):
			return abiUint64For(version), nil
		}

		return nil, nil
	}

	return NewChainProvider(evmClient, tokenState, "latest")
}

// TestPublicParamsFollowsAnUpdate is the invariant the type claims and the reason nothing here is
// cached: the bytes and the version must describe the same on-chain state.
//
// Caching the version broke this in a way that no single read could show. The parameters were read
// fresh and the version was not, so after an update every delta carried the new parameters under the
// old version, the contract rejected each one with StalePublicParams, and the failure surfaced as an
// unexplained revert during gas estimation rather than as anything about parameters.
func TestPublicParamsFollowsAnUpdate(t *testing.T) {
	state := &chainState{}
	state.set("params-v0", 0)
	provider := newProviderHarness(t, state)

	raw, version, err := provider.PublicParams(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "params-v0", string(raw))
	assert.EqualValues(t, 0, version)

	// An endorsed setup delta lands. Nobody tells the provider.
	state.set("params-v1", 1)

	raw, version, err = provider.PublicParams(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "params-v1", string(raw), "the parameters must be the ones on chain now")
	assert.EqualValues(t, 1, version, "the version must be the one those parameters are stored at")
}

// TestPublicParamsIsConsistentAcrossRepeatedReads checks the pair stays matched over several updates,
// since a version that lagged by one would still look right on the first read after each change.
func TestPublicParamsIsConsistentAcrossRepeatedReads(t *testing.T) {
	state := &chainState{}
	provider := newProviderHarness(t, state)

	for _, tc := range []struct {
		raw     string
		version uint64
	}{
		{"params-v0", 0},
		{"params-v1", 1},
		{"params-v2", 2},
		{"params-v3", 3},
	} {
		state.set(tc.raw, tc.version)

		raw, version, err := provider.PublicParams(context.Background())
		require.NoError(t, err)
		assert.Equal(t, tc.raw, string(raw))
		assert.Equal(t, tc.version, version)
	}
}
