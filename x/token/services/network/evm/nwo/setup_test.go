/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nwo

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math/big"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/abi"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
)

const (
	testChainID = 31337
	currentPP   = "public-parameters-v0"
	newPP       = "public-parameters-v1"
	currentVer  = uint64(3)
)

// testKey returns a distinct, valid 32-byte private-key scalar.
func testKey(low byte) []byte {
	k := make([]byte, 32)
	k[31] = low

	return k
}

// abiBytes encodes a byte string the way an ABI `returns (bytes)` result arrives: an offset word, a
// length word, then the payload padded to a whole number of words.
func abiBytes(b []byte) []byte {
	head := make([]byte, 32)
	head[31] = 0x20
	length := make([]byte, 32)
	binary.BigEndian.PutUint64(length[24:], uint64(len(b)))
	padded := make([]byte, (len(b)+31)/32*32)
	copy(padded, b)

	return append(append(head, length...), padded...)
}

func abiUint64(v uint64) []byte {
	out := make([]byte, 32)
	binary.BigEndian.PutUint64(out[24:], v)

	return out
}

// newSetupHarness wires an updater over a mock node that reports currentPP at currentVer, accepts a
// broadcast, and mines it successfully.
func newSetupHarness(t *testing.T, keys [][]byte, threshold uint) (*SetupUpdater, *mock.EVMClient) {
	t.Helper()
	tokenState, err := client.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	require.NoError(t, err)

	evmClient := &mock.EVMClient{}
	evmClient.CallStub = func(_ context.Context, _ client.Address, data []byte, _ string) ([]byte, error) {
		switch string(data) {
		case string(abi.MethodID("getPublicParameters()")):
			return abiBytes([]byte(currentPP)), nil
		case string(abi.MethodID("getPublicParamsVersion()")):
			return abiUint64(currentVer), nil
		}

		return nil, nil
	}
	evmClient.EstimateGasReturns(200_000, nil)
	evmClient.SuggestGasFeesReturns(client.GasFees{
		MaxFeePerGas:         big.NewInt(1_000_000_000),
		MaxPriorityFeePerGas: big.NewInt(1),
	}, nil)
	evmClient.PendingNonceAtReturns(0, nil)
	evmClient.SendRawTransactionReturns(client.Hash{0x01}, nil)
	evmClient.GetTransactionReceiptReturns(&client.Receipt{Status: 1}, nil)

	submitter, err := evm.NewSubmitter(
		evmClient, secp256k1.PrivKeyFromBytes(testKey(0x77)), tokenState, big.NewInt(testChainID), evm.GasConfig{},
	)
	require.NoError(t, err)

	updater, err := NewSetupUpdater(SetupUpdaterConfig{
		Client:       evmClient,
		TokenState:   tokenState,
		ChainID:      big.NewInt(testChainID),
		BlockTag:     "latest",
		EndorserKeys: keys,
		Threshold:    threshold,
		Submitter:    submitter,
	})
	require.NoError(t, err)

	return updater, evmClient
}

// TestBuildDeltaNamesTheVersionItSupersedes is the property the contract enforces: a setup delta must
// assert the parameters it was built against, and the contract rejects it if they have moved on. The
// version is therefore read from the chain rather than assumed.
func TestBuildDeltaNamesTheVersionItSupersedes(t *testing.T) {
	updater, _ := newSetupHarness(t, [][]byte{testKey(1)}, 1)

	delta, err := updater.buildDelta(context.Background(), []byte(newPP))
	require.NoError(t, err)

	assert.True(t, delta.IsSetup)
	assert.Equal(t, []byte(newPP), delta.SetupParameters)
	assert.Equal(t, currentVer, delta.PublicParamsVersion)
	assert.Equal(t, sha256.Sum256([]byte(currentPP)), delta.PublicParamsHash)
	// a setup delta carries nothing else, and the contract reverts if it does
	assert.Empty(t, delta.SpentRefs)
	assert.Empty(t, delta.Outputs)
	assert.Empty(t, delta.MetadataKeys)
}

// TestEndorseProducesADistinctQuorum checks the signatures are what the verifier requires: exactly
// the threshold, from distinct endorsers, over the digest of the delta being submitted. The verifier
// counts distinct signers, so a quorum reusing one key would be rejected on chain.
func TestEndorseProducesADistinctQuorum(t *testing.T) {
	updater, _ := newSetupHarness(t, [][]byte{testKey(1), testKey(2), testKey(3)}, 2)

	delta, err := updater.buildDelta(context.Background(), []byte(newPP))
	require.NoError(t, err)
	endorsements, err := updater.endorse(delta)
	require.NoError(t, err)
	require.Len(t, endorsements, 2)

	digest := eip712.Digest(updater.domain, delta)
	seen := map[string]bool{}
	for _, sig := range endorsements {
		signer, err := eip712.RecoverAddress(digest, sig)
		require.NoError(t, err)
		assert.False(t, seen[signer.Hex()], "the quorum must be distinct endorsers")
		seen[signer.Hex()] = true
	}
}

// TestEndorseUsesEveryEndorserWhenNoThreshold checks an unset threshold means all of them, which is
// what a network that has not narrowed its policy expects.
func TestEndorseUsesEveryEndorserWhenNoThreshold(t *testing.T) {
	updater, _ := newSetupHarness(t, [][]byte{testKey(1), testKey(2), testKey(3)}, 0)

	delta, err := updater.buildDelta(context.Background(), []byte(newPP))
	require.NoError(t, err)
	endorsements, err := updater.endorse(delta)
	require.NoError(t, err)
	assert.Len(t, endorsements, 3)
}

// TestUpdateBroadcastsAndWaits covers the whole path: the update reaches the node and the updater
// only returns once it has been mined.
func TestUpdateBroadcastsAndWaits(t *testing.T) {
	updater, evmClient := newSetupHarness(t, [][]byte{testKey(1), testKey(2)}, 2)

	require.NoError(t, updater.Update(context.Background(), []byte(newPP)))
	assert.Equal(t, 1, evmClient.SendRawTransactionCallCount())
	assert.NotZero(t, evmClient.GetTransactionReceiptCallCount(), "the update must be waited on")
}

// TestUpdateFailsOnRevert checks a reverted update is reported here. A reverted setup leaves the
// parameters untouched with no other trace, so a silent success would surface much later, as nodes
// that never see the new parameters.
func TestUpdateFailsOnRevert(t *testing.T) {
	updater, evmClient := newSetupHarness(t, [][]byte{testKey(1)}, 1)
	evmClient.GetTransactionReceiptReturns(&client.Receipt{Status: 0}, nil)

	err := updater.Update(context.Background(), []byte(newPP))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reverted")
}

// TestUpdateRejectsEmptyParameters checks the update is refused before it is signed: an empty setup
// delta is invalid, and finding that out on chain would cost a real transaction.
func TestUpdateRejectsEmptyParameters(t *testing.T) {
	updater, evmClient := newSetupHarness(t, [][]byte{testKey(1)}, 1)

	require.Error(t, updater.Update(context.Background(), nil))
	assert.Zero(t, evmClient.SendRawTransactionCallCount(), "nothing must be broadcast")
}

func TestNewSetupUpdaterValidatesItsInput(t *testing.T) {
	base := func() SetupUpdaterConfig {
		return SetupUpdaterConfig{
			Client:       &mock.EVMClient{},
			ChainID:      big.NewInt(testChainID),
			EndorserKeys: [][]byte{testKey(1)},
			Submitter:    &evm.Submitter{},
		}
	}

	for name, broken := range map[string]func(*SetupUpdaterConfig){
		"no client":    func(c *SetupUpdaterConfig) { c.Client = nil },
		"no submitter": func(c *SetupUpdaterConfig) { c.Submitter = nil },
		"no chain id":  func(c *SetupUpdaterConfig) { c.ChainID = nil },
		"no endorsers": func(c *SetupUpdaterConfig) { c.EndorserKeys = nil },
		"unusable key": func(c *SetupUpdaterConfig) { c.EndorserKeys = [][]byte{{0x01}} },
	} {
		t.Run(name, func(t *testing.T) {
			config := base()
			broken(&config)
			_, err := NewSetupUpdater(config)
			require.Error(t, err)
		})
	}
}

// TestSetupAnchorIsDistinctPerUpdate checks consecutive updates cannot collide: the contract refuses
// an anchor it has already processed, so a repeated anchor would make the second update fail.
func TestSetupAnchorIsDistinctPerUpdate(t *testing.T) {
	first := setupAnchor([]byte(newPP), 0)
	assert.NotEqual(t, first, setupAnchor([]byte(newPP), 1), "a later version must give a different anchor")
	assert.NotEqual(t, first, setupAnchor([]byte("other"), 0), "different parameters must give a different anchor")
	assert.Equal(t, first, setupAnchor([]byte(newPP), 0), "the derivation must be deterministic")
}
