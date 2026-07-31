/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"sync"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
)

// TestNonceRecoversFromChain is the restart story: the manager holds no persisted state, so the first
// allocation after a start re-derives the sequence from the node's pending nonce.
func TestNonceRecoversFromChain(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(42, nil)
	n := NewNonceManager(evm, client.Address{})

	got, err := n.Next(t.Context())
	require.NoError(t, err)
	assert.Equal(t, uint64(42), got, "the first nonce must come from the chain, not from zero")
}

// TestNonceIsSequentialWithoutRefetching checks the manager does not ask the node for every
// transaction: doing so would hand the same nonce to two callers racing between the query and the
// send.
func TestNonceIsSequentialWithoutRefetching(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(7, nil)
	n := NewNonceManager(evm, client.Address{})

	for want := uint64(7); want < 12; want++ {
		got, err := n.Next(t.Context())
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	assert.Equal(t, 1, evm.PendingNonceAtCallCount(), "the node is consulted once, not per transaction")
}

// TestNonceResetReRecovers checks the failed-broadcast path: after a reset the sequence is
// re-derived from the node, which is authoritative about what it has actually seen.
func TestNonceResetReRecovers(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturnsOnCall(0, 5, nil)
	evm.PendingNonceAtReturnsOnCall(1, 9, nil)
	n := NewNonceManager(evm, client.Address{})

	first, err := n.Next(t.Context())
	require.NoError(t, err)
	assert.Equal(t, uint64(5), first)

	n.Reset()

	second, err := n.Next(t.Context())
	require.NoError(t, err)
	assert.Equal(t, uint64(9), second, "after a reset the nonce must come from the node again")
	assert.Equal(t, 2, evm.PendingNonceAtCallCount())
}

func TestNonceSurfacesRecoveryFailure(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(0, errors.New("node unavailable"))
	n := NewNonceManager(evm, client.Address{})

	_, err := n.Next(t.Context())
	require.Error(t, err)

	_, initialized := n.Cached()
	assert.False(t, initialized, "a failed recovery must not leave the manager initialized")
}

// TestNonceConcurrentAllocationIsUnique is the property Ethereum requires: every concurrent caller
// must get a distinct nonce, or all but one transaction is rejected.
func TestNonceConcurrentAllocationIsUnique(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(100, nil)
	n := NewNonceManager(evm, client.Address{})

	const callers = 50
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		got = make(map[uint64]struct{}, callers)
	)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			nonce, err := n.Next(t.Context())
			if err != nil {
				return
			}
			mu.Lock()
			got[nonce] = struct{}{}
			mu.Unlock()
		}()
	}
	wg.Wait()

	assert.Len(t, got, callers, "every concurrent caller must receive a distinct nonce")
}
