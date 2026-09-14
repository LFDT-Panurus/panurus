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

// TestPublicParamsRetriesATornRead is the fix for a real bug: the bytes and the version were read as
// two separate calls with nothing bracketing them, so an update landing in between produced an
// internally inconsistent pair. The contract would reject it (it checks both fields together), so the
// practical cost was a doomed, gas-spending transaction rather than data corruption, but it was a
// deterministic gap in this function alone, closeable without touching the contract.
func TestPublicParamsRetriesATornRead(t *testing.T) {
	tokenState, err := client.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	require.NoError(t, err)

	// The version read returns 0, 1, 1, 1: the first attempt's bracket (before=0, after=1) straddles
	// an update and must be retried; the second attempt's (before=1, after=1) does not.
	versionReads := []uint64{0, 1, 1, 1}
	calls := 0
	evmClient := &mock.EVMClient{}
	evmClient.CallStub = func(_ context.Context, _ client.Address, data []byte, _ string) ([]byte, error) {
		switch string(data) {
		case string(abi.MethodID("getPublicParameters()")):
			return abiBytesFor([]byte("params")), nil
		case string(abi.MethodID("getPublicParamsVersion()")):
			v := versionReads[calls]
			calls++

			return abiUint64For(v), nil
		}

		return nil, nil
	}

	provider := NewChainProvider(evmClient, tokenState, "latest")
	raw, version, err := provider.PublicParams(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "params", string(raw))
	assert.EqualValues(t, 1, version, "the torn first attempt must be discarded, not returned")
	assert.Equal(t, 4, calls, "a torn bracket must be retried, not accepted")
}

// TestPublicParamsGivesUpAfterRepeatedTears checks the retry is bounded: a chain whose version never
// settles between two reads must not spin PublicParams forever.
func TestPublicParamsGivesUpAfterRepeatedTears(t *testing.T) {
	tokenState, err := client.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	require.NoError(t, err)

	var version uint64
	evmClient := &mock.EVMClient{}
	evmClient.CallStub = func(_ context.Context, _ client.Address, data []byte, _ string) ([]byte, error) {
		switch string(data) {
		case string(abi.MethodID("getPublicParameters()")):
			return abiBytesFor([]byte("params")), nil
		case string(abi.MethodID("getPublicParamsVersion()")):
			version++

			return abiUint64For(version), nil
		}

		return nil, nil
	}

	provider := NewChainProvider(evmClient, tokenState, "latest")
	_, _, err = provider.PublicParams(context.Background())
	require.Error(t, err)
}

// TestPublicParamsReadsAtTheGivenBlockTag is the regression test for F2: a provider constructed with
// blockTag "latest" must see an update the instant it lands, not only once it finalizes.
// TokenState.applyStateDelta enforces publicParamsVersion/publicParamsHash against its current (head)
// storage, so an endorser's ChainProvider reading at "finalized" would keep signing the pre-update
// value for the whole finalization lag, and every one of its endorsements would revert
// StalePublicParams until the update finalized. driver.go now constructs the endorsement path's
// ChainProvider with client.BlockTagLatest for exactly this reason; this test pins the underlying
// mechanism (ChainProvider honours whichever tag it is given) so that choice cannot regress silently.
func TestPublicParamsReadsAtTheGivenBlockTag(t *testing.T) {
	tokenState, err := client.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	require.NoError(t, err)

	// The chain has just accepted an update: "finalized" still reports the old pair, "latest" already
	// reports the new one.
	byTag := map[string]struct {
		raw     string
		version uint64
	}{
		"finalized": {"params-v1", 1},
		"latest":    {"params-v2", 2},
	}
	evmClient := &mock.EVMClient{}
	evmClient.CallStub = func(_ context.Context, _ client.Address, data []byte, tag string) ([]byte, error) {
		tc := byTag[tag]
		switch string(data) {
		case string(abi.MethodID("getPublicParameters()")):
			return abiBytesFor([]byte(tc.raw)), nil
		case string(abi.MethodID("getPublicParamsVersion()")):
			return abiUint64For(tc.version), nil
		}

		return nil, nil
	}

	latestProvider := NewChainProvider(evmClient, tokenState, "latest")
	raw, version, err := latestProvider.PublicParams(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "params-v2", string(raw), "a provider reading at latest must see the update immediately")
	assert.EqualValues(t, 2, version)

	finalizedProvider := NewChainProvider(evmClient, tokenState, "finalized")
	raw, version, err = finalizedProvider.PublicParams(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "params-v1", string(raw), "a provider reading at finalized must not see the update yet")
	assert.EqualValues(t, 1, version)
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
