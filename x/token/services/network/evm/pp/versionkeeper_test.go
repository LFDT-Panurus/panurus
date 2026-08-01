/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp

import (
	"context"
	"encoding/binary"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/abi"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
)

// uint64Return encodes v as the ABI return value of a uint64 method.
func uint64Return(v uint64) []byte {
	out := make([]byte, 32)
	binary.BigEndian.PutUint64(out[24:], v)

	return out
}

func testAddress(low byte) client.Address {
	var a client.Address
	a[client.AddressLength-1] = low

	return a
}

func TestSyncReadsAndCaches(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.CallReturns(uint64Return(3), nil)

	k := NewVersionKeeper(evm, testAddress(0xAA), "")
	got, err := k.Sync(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint64(3), got)

	cached, ok := k.Cached()
	assert.True(t, ok)
	assert.Equal(t, uint64(3), cached)

	// the call must target the TokenState with the getPublicParamsVersion selector, at the block tag
	require.Equal(t, 1, evm.CallCallCount())
	_, to, data, tag := evm.CallArgsForCall(0)
	assert.Equal(t, testAddress(0xAA), to)
	assert.Equal(t, "finalized", tag, "an empty block tag must default to finalized")
	assert.Equal(t, abi.MethodID("getPublicParamsVersion()"), data)
}

// TestGetVersionSyncsOnceThenServesCache is the property the endorsement path depends on: a steady
// state costs no round trip.
func TestGetVersionSyncsOnceThenServesCache(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.CallReturns(uint64Return(7), nil)
	k := NewVersionKeeper(evm, testAddress(0xAA), "finalized")

	for range 5 {
		got, err := k.GetVersion(context.Background())
		require.NoError(t, err)
		assert.Equal(t, uint64(7), got)
	}
	assert.Equal(t, 1, evm.CallCallCount(), "only the first read may hit the node")
}

// TestInvalidateForcesResync covers the public-parameters update path: after an update the cached
// version is stale by definition, so the next read must go back to the contract.
func TestInvalidateForcesResync(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.CallReturnsOnCall(0, uint64Return(1), nil)
	evm.CallReturnsOnCall(1, uint64Return(2), nil)
	k := NewVersionKeeper(evm, testAddress(0xAA), "finalized")

	first, err := k.GetVersion(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint64(1), first)

	k.Invalidate()
	second, err := k.GetVersion(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint64(2), second, "after an update the keeper must observe the bumped version")
	assert.Equal(t, 2, evm.CallCallCount())
}

func TestCachedBeforeAnySync(t *testing.T) {
	k := NewVersionKeeper(&mock.EVMClient{}, testAddress(0xAA), "finalized")
	version, ok := k.Cached()
	assert.False(t, ok, "the keeper starts uninitialized")
	assert.Zero(t, version)
}

func TestSyncSurfacesErrors(t *testing.T) {
	t.Run("call failure", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.CallReturns(nil, assert.AnError)
		k := NewVersionKeeper(evm, testAddress(0xAA), "finalized")
		_, err := k.Sync(context.Background())
		require.Error(t, err)

		_, ok := k.Cached()
		assert.False(t, ok, "a failed sync must not mark the keeper initialized")
	})

	t.Run("undecodable result", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.CallReturns([]byte{0x01}, nil) // too short for a uint64 word
		k := NewVersionKeeper(evm, testAddress(0xAA), "finalized")
		_, err := k.Sync(context.Background())
		require.Error(t, err)
	})
}

// TestConcurrentAccess exercises the keeper the way the driver uses it: many endorsements reading
// while an update re-syncs. Run under -race, this is what proves the locking is right.
func TestConcurrentAccess(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.CallReturns(uint64Return(5), nil)
	k := NewVersionKeeper(evm, testAddress(0xAA), "finalized")

	// Failures are collected rather than asserted inside the goroutines: a failed assertion there
	// would call FailNow off the test goroutine, which is not allowed.
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	record := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	}

	for range 16 {
		wg.Go(func() {
			_, err := k.GetVersion(context.Background())
			record(err)
			k.Cached()
		})
	}
	for range 4 {
		wg.Go(func() {
			k.Invalidate()
			_, err := k.Sync(context.Background())
			record(err)
		})
	}
	wg.Wait()
	require.Empty(t, errs, "concurrent access must not fail")

	version, ok := k.Cached()
	assert.True(t, ok)
	assert.Equal(t, uint64(5), version)
}
