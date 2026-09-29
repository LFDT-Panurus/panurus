/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp_test

import (
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testTMSID = token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

func TestVersionKeeperReadsLedgerVersion(t *testing.T) {
	reader := &mock.SetupVersionReader{}
	reader.FetchSetupHashVersionReturns(7, true, nil)

	vk := pp.NewVersionKeeper(testTMSID, reader)

	// the first read initializes the keeper from the ledger, it does not
	// assume the public parameters were never updated
	v, err := vk.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, uint64(7), v)

	network, channel, namespace := reader.FetchSetupHashVersionArgsForCall(0)
	assert.Equal(t, testTMSID.Network, network)
	assert.Equal(t, testTMSID.Channel, channel)
	assert.Equal(t, testTMSID.Namespace, namespace)

	// once synchronized, reads are served from the cache
	for range 5 {
		v, err = vk.GetVersion()
		require.NoError(t, err)
		assert.Equal(t, uint64(7), v)
	}
	assert.Equal(t, 1, reader.FetchSetupHashVersionCallCount())
}

func TestVersionKeeperUpdateIsAbsolute(t *testing.T) {
	reader := &mock.SetupVersionReader{}
	reader.FetchSetupHashVersionReturns(3, true, nil)

	vk := pp.NewVersionKeeper(testTMSID, reader)

	v, err := vk.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, uint64(3), v)

	// an update re-reads the ledger, it does not increment a local counter:
	// notifications that carry no version change leave the version alone...
	require.NoError(t, vk.UpdateVersion())
	v, err = vk.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, uint64(3), v)

	// ... and a version that jumps by more than one is tracked exactly
	reader.FetchSetupHashVersionReturns(9, true, nil)
	require.NoError(t, vk.UpdateVersion())
	v, err = vk.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, uint64(9), v)
}

func TestVersionKeeperRetriesAfterFailure(t *testing.T) {
	reader := &mock.SetupVersionReader{}
	reader.FetchSetupHashVersionReturnsOnCall(0, 0, false, errors.New("ledger unreachable"))
	reader.FetchSetupHashVersionReturnsOnCall(1, 4, true, nil)

	vk := pp.NewVersionKeeper(testTMSID, reader)

	// a failed read surfaces as an error instead of a silently wrong version
	_, err := vk.GetVersion()
	require.ErrorContains(t, err, "ledger unreachable")

	// and leaves the keeper unsynchronized, so the next read retries
	v, err := vk.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, uint64(4), v)
	assert.Equal(t, 2, reader.FetchSetupHashVersionCallCount())
}

func TestVersionKeeperUpdateFailureKeepsLastVersion(t *testing.T) {
	reader := &mock.SetupVersionReader{}
	reader.FetchSetupHashVersionReturnsOnCall(0, 2, true, nil)
	reader.FetchSetupHashVersionReturnsOnCall(1, 0, false, errors.New("ledger unreachable"))
	reader.FetchSetupHashVersionReturnsOnCall(2, 5, true, nil)

	vk := pp.NewVersionKeeper(testTMSID, reader)

	v, err := vk.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, uint64(2), v)

	require.ErrorContains(t, vk.UpdateVersion(), "ledger unreachable")

	// the next read recovers the real version
	v, err = vk.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, uint64(5), v)
}

func TestVersionKeeperPublicParamsNotDeployedYet(t *testing.T) {
	reader := &mock.SetupVersionReader{}
	reader.FetchSetupHashVersionReturnsOnCall(0, 0, false, nil)
	reader.FetchSetupHashVersionReturnsOnCall(1, 0, true, nil)

	vk := pp.NewVersionKeeper(testTMSID, reader)

	// a missing setup key is not an error: zero is the version the public
	// parameters will be written at
	v, err := vk.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, uint64(0), v)

	// but the keeper stays unsynchronized, so it picks the key up once it lands
	v, err = vk.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, uint64(0), v)
	assert.Equal(t, 2, reader.FetchSetupHashVersionCallCount())

	// now that the key exists, reads are cached again
	v, err = vk.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, uint64(0), v)
	assert.Equal(t, 2, reader.FetchSetupHashVersionCallCount())
}

func TestVersionKeeperNotFoundKeepsLastKnownVersion(t *testing.T) {
	reader := &mock.SetupVersionReader{}
	reader.FetchSetupHashVersionReturnsOnCall(0, 6, true, nil)
	reader.FetchSetupHashVersionReturnsOnCall(1, 0, false, nil)
	reader.FetchSetupHashVersionReturnsOnCall(2, 0, false, nil)

	vk := pp.NewVersionKeeper(testTMSID, reader)

	v, err := vk.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, uint64(6), v)

	// the key going missing must not drop the keeper back to version zero,
	// the last version read from the ledger stays the best known answer
	require.NoError(t, vk.UpdateVersion())
	v, err = vk.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, uint64(6), v)
}

func TestVersionKeeperConcurrentAccess(t *testing.T) {
	reader := &mock.SetupVersionReader{}
	reader.FetchSetupHashVersionReturns(11, true, nil)

	vk := pp.NewVersionKeeper(testTMSID, reader)

	const routines = 32
	versions := make(chan uint64, routines)
	errs := make(chan error, 2*routines)

	var wg sync.WaitGroup
	for range routines {
		wg.Add(2)
		go func() {
			defer wg.Done()
			v, err := vk.GetVersion()
			versions <- v
			errs <- err
		}()
		go func() {
			defer wg.Done()
			errs <- vk.UpdateVersion()
		}()
	}
	wg.Wait()
	close(versions)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	for v := range versions {
		assert.Equal(t, uint64(11), v)
	}

	v, err := vk.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, uint64(11), v)
}

func TestVersionKeeperProvider(t *testing.T) {
	reader := &mock.SetupVersionReader{}
	reader.FetchSetupHashVersionReturns(2, true, nil)

	p := pp.NewVersionKeeperProvider(reader)

	vk, err := p.Get(testTMSID)
	require.NoError(t, err)
	require.NotNil(t, vk)

	// the provider does not contact the ledger, the keeper does
	assert.Equal(t, 0, reader.FetchSetupHashVersionCallCount())

	v, err := vk.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, uint64(2), v)

	// keepers are cached per TMS
	same, err := p.Get(testTMSID)
	require.NoError(t, err)
	assert.Same(t, vk, same)

	other, err := p.Get(token.TMSID{Network: "net", Channel: "ch", Namespace: "other"})
	require.NoError(t, err)
	assert.NotSame(t, vk, other)
}
