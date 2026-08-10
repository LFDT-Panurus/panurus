/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
)

func testAddress(low byte) client.Address {
	var a client.Address
	a[client.AddressLength-1] = low

	return a
}

func logManager(evm client.EVMClient) *Manager {
	return NewManager(evm, &stubState{}, testAddress(0xAA), 0, 5*time.Millisecond, time.Second)
}

// TestStateCommittedTopic pins topic 0 against the value any Ethereum tooling computes
// (cast keccak "StateCommitted(bytes32,bool,string)"). A wrong topic silently matches nothing, so the
// recipient path would time out instead of failing loudly.
func TestStateCommittedTopic(t *testing.T) {
	topic := StateCommittedTopic()
	assert.Equal(t,
		"1ad9559f09f43d5013a1b23fe194a3e8fab5b51c6f7f083b461d3f7cb85dfa61",
		hex.EncodeToString(topic[:]))
}

// TestTxHashByAnchorFindsTheCommit is the recipient path: from an anchor alone, find the transaction
// that applied it. The hash comes from the log's metadata, since a contract cannot know its own
// transaction hash (design §7.4).
func TestTxHashByAnchorFindsTheCommit(t *testing.T) {
	want, err := client.HexToHash("0x853f272fffc6efc284fc16a254decca742d2347e05703e501c59968f78f81ffa")
	require.NoError(t, err)

	evm := &mock.EVMClient{}
	evm.GetLogsReturns([]client.Log{{TxHash: want, BlockNumber: 12}}, nil)

	got, found, err := logManager(evm).TxHashByAnchor(t.Context(), anchor(0x01))
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, want, got)
}

// TestTxHashByAnchorFiltersCorrectly checks the query itself: the right contract, the event topic, and
// the anchor as the indexed second topic. Getting any of these wrong returns nothing rather than
// erroring, so the filter is asserted directly.
func TestTxHashByAnchorFiltersCorrectly(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.GetLogsReturns(nil, nil)

	m := NewManager(evm, &stubState{}, testAddress(0xAA), 100, 5*time.Millisecond, time.Second)
	_, _, err := m.TxHashByAnchor(t.Context(), anchor(0x42))
	require.NoError(t, err)

	require.Equal(t, 1, evm.GetLogsCallCount())
	_, filter := evm.GetLogsArgsForCall(0)

	assert.Equal(t, testAddress(0xAA), filter.Address, "must query the TMS's own TokenState")
	assert.Equal(t, uint64(100), filter.FromBlock, "the configured start block must be honoured")
	assert.Zero(t, filter.ToBlock, "zero means latest, so the search reaches the head")

	require.Len(t, filter.Topics, 2)
	require.Len(t, filter.Topics[0], 1)
	assert.Equal(t, StateCommittedTopic(), filter.Topics[0][0])
	require.Len(t, filter.Topics[1], 1)
	assert.Equal(t, client.Hash(anchor(0x42)), filter.Topics[1][0], "the anchor is the indexed topic")
}

// TestTxHashByAnchorNotFound covers the ordinary case for a recipient still waiting: no event yet.
// It must not be an error, since the transaction may simply not have been applied.
func TestTxHashByAnchorNotFound(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.GetLogsReturns(nil, nil)

	_, found, err := logManager(evm).TxHashByAnchor(t.Context(), anchor(0x01))
	require.NoError(t, err)
	assert.False(t, found)
}

// TestTxHashByAnchorRejectsDuplicates guards the replay invariant: the contract rejects a second delta
// for an anchor, so two commit events for one anchor would mean that guard is broken. Better to fail
// loudly than to pick one arbitrarily.
func TestTxHashByAnchorRejectsDuplicates(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.GetLogsReturns([]client.Log{{BlockNumber: 1}, {BlockNumber: 2}}, nil)

	_, _, err := logManager(evm).TxHashByAnchor(t.Context(), anchor(0x01))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "replay guard")
}

func TestTxHashByAnchorSurfacesQueryFailure(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.GetLogsReturns(nil, errors.New("node unavailable"))

	_, _, err := logManager(evm).TxHashByAnchor(t.Context(), anchor(0x01))
	require.Error(t, err)
}

// TestTxHashByAnchorNeedsTheContract checks the misconfiguration is caught rather than silently
// querying the zero address, which would match nothing and look like "not committed yet".
func TestTxHashByAnchorNeedsTheContract(t *testing.T) {
	evm := &mock.EVMClient{}
	m := NewManager(evm, &stubState{}, client.Address{}, 0, 5*time.Millisecond, time.Second)

	_, _, err := m.TxHashByAnchor(t.Context(), anchor(0x01))
	require.Error(t, err)
	assert.Zero(t, evm.GetLogsCallCount(), "a missing contract address must not reach the node")
}
