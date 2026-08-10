/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/recovery"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
)

func testTMSID() token2.TMSID {
	return token2.TMSID{Network: "evm-net", Channel: "", Namespace: "token"}
}

// TestStartRecoveryWithoutStores covers a node wired without the stores recovery sweeps.
//
// Recovery is a safety net for transactions left Pending by a previous run. A node that has nowhere
// to sweep still works, so this is a downgrade rather than a failure: returning an error here would
// take out a whole network over a facility that node may never need.
func TestStartRecoveryWithoutStores(t *testing.T) {
	d := newRecoveryDriver()

	require.NoError(t, d.startRecovery(testTMSID(), nil))
	assert.Empty(t, d.recoveries, "nothing may be recorded as running")
}

// TestStartRecoveryIsIdempotent is the reason the map exists at all.
//
// Connect is called once per namespace, but a network can be built more than once over a node's
// life, and every build would otherwise start a second pair of sweeps over the same store. Two
// managers sweeping one store claim each other's transactions: each sees rows the other has already
// leased, and recovery turns into contention over exactly the transactions it exists to rescue.
func TestStartRecoveryIsIdempotent(t *testing.T) {
	d := newRecoveryDriver()
	tmsID := testTMSID()

	// Stand in for a first start that already happened. The sweeps themselves need the full store
	// stack, which the integration suite supplies; what is pinned here is that a second call sees
	// the first and declines rather than starting alongside it.
	d.recoveries[tmsID.String()] = nil

	require.NoError(t, d.startRecovery(tmsID, nil))
	assert.Len(t, d.recoveries, 1, "a second start must not add a second set of sweeps")
}

// TestStopAllToleratesNothingToStop covers the unwind path when a TMS fails part-way through
// starting: whatever already started has to be stopped, and the set may be empty.
func TestStopAllToleratesNothingToStop(t *testing.T) {
	assert.NotPanics(t, func() { stopAll(nil) })
	assert.NotPanics(t, func() { stopAll([]*recovery.Manager{}) })
}

// TestRecoveryConfigFallsBackToDefaults pins the downgrade path. Recovery settings are optional, and
// a TMS that carries none - or carries one that cannot be read - gets the defaults rather than
// blocking the network from coming up without recovery.
func TestRecoveryConfigFallsBackToDefaults(t *testing.T) {
	// fakeResolver's ConfigurationFor always reports no configuration, which is the case every
	// deployment that has not opted into custom recovery settings hits.
	d := &Driver{resolver: fakeResolver{evm: map[string]bool{"evm-net|": true}}}

	cfg := d.recoveryConfig(testTMSID())
	assert.Equal(t, recovery.DefaultConfig(), cfg)
	assert.True(t, cfg.Enabled, "recovery is on unless a deployment turns it off")
	assert.Positive(t, cfg.TTL, "a zero TTL would sweep transactions that are merely in flight")
	assert.Positive(t, cfg.ScanInterval)
	assert.Less(t, cfg.ScanInterval, 5*time.Minute, "a sweep this rare would not rescue anything usefully")
}

// TestSettledNetworkResolvesAnAbsentAnchor is the fix for a rejected transaction being swept forever.
//
// A revert writes nothing to the chain, so an anchor that never appears is all a rejection ever looks
// like. Reporting that as Unknown made the shared recovery handler treat it as transient and look
// again on the next sweep, so the record stayed Pending, the holding it reserved was never released,
// and the sweep repeated every few seconds indefinitely.
func TestSettledNetworkResolvesAnAbsentAnchor(t *testing.T) {
	const timeout = time.Minute

	// settled builds the wrapper over a node whose eth_call returns raw, and a store that claims the
	// transaction was written age ago.
	settled := func(t *testing.T, raw []byte, callErr error, age time.Duration) (settledNetwork, string) {
		t.Helper()
		evm := &mock.EVMClient{}
		evm.ChainIDReturns(bigInt(testChainID), nil)
		evm.CallReturns(raw, callErr)
		n := testNetwork(t, evm, nil)

		return settledNetwork{
			Network: n,
			store:   &agedStore{age: age},
			timeout: timeout,
		}, n.ComputeTxID(&driver.TxID{Creator: []byte("creator")})
	}

	// An all-zero return is the contract reporting no token request hash for the anchor, which is what
	// a reverted apply leaves behind.
	absent := make([]byte, 32)

	t.Run("an anchor absent past the timeout is invalid", func(t *testing.T) {
		n, txID := settled(t, absent, nil, 2*timeout)

		status, _, message, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Invalid, status, "the sweep must reach a verdict rather than repeat forever")
		assert.NotEmpty(t, message, "the record should say why it was closed")
	})

	// The patient half, and the reason the age is read rather than assumed: a transaction younger than
	// the timeout may still be on its way, and condemning it would delete a transfer that then lands.
	t.Run("an anchor absent within the timeout stays unknown", func(t *testing.T) {
		n, txID := settled(t, absent, nil, timeout/2)

		status, _, _, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Unknown, status, "a transaction still in flight must be left alone")
	})

	// Only Unknown is reinterpreted. A committed transaction still recovers as Valid however old it
	// is, and its token request hash has to survive the wrapper or the handler cannot verify it.
	t.Run("a committed anchor is untouched", func(t *testing.T) {
		hash := make([]byte, 32)
		hash[31] = 0x7C
		n, txID := settled(t, hash, nil, 2*timeout)

		status, got, _, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Valid, status)
		assert.Equal(t, hash, got, "the token request hash must reach the handler intact")
	})

	// A node that could not be reached has established nothing about the transaction, so it stays a
	// failure to be retried rather than becoming a verdict.
	t.Run("an unreachable node is not a verdict", func(t *testing.T) {
		n, txID := settled(t, nil, errors.New("connection refused"), 2*timeout)

		_, _, _, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.Error(t, err)
	})

	// A store that cannot say how old the row is leaves the transaction in the sweep. Condemning it on
	// the strength of a failed database read would be the one irreversible move available here.
	t.Run("an unreadable store leaves it for the next sweep", func(t *testing.T) {
		n, txID := settled(t, absent, nil, 2*timeout)
		n.store = &agedStore{err: errors.New("store is down")}

		status, _, _, err := n.GetTransactionStatus(t.Context(), "token", txID)
		require.NoError(t, err)
		assert.Equal(t, driver.Unknown, status)
	})
}

// agedStore is a recoveryStore that only answers the age question, which is all settledNetwork asks
// of it. Every other method is present to satisfy the interface and panics if the code under test
// starts depending on it.
type agedStore struct {
	recoveryStore
	age time.Duration
	err error
}

func (s *agedStore) Transactions(
	_ context.Context,
	_ dbdriver.QueryTransactionsParams,
	_ cdriver.Pagination,
) (*cdriver.PageIterator[*dbdriver.TransactionRecord], error) {
	if s.err != nil {
		return nil, s.err
	}

	return &cdriver.PageIterator[*dbdriver.TransactionRecord]{
		Items: iterators.Slice([]*dbdriver.TransactionRecord{{Timestamp: time.Now().Add(-s.age)}}),
	}, nil
}

// newRecoveryDriver returns a Driver with the recovery bookkeeping initialized and no stores, which
// is what NewDriver produces before anything binds to it.
func newRecoveryDriver() *Driver {
	return &Driver{
		resolver:   fakeResolver{evm: map[string]bool{"evm-net|": true}},
		recoveries: map[string][]*recovery.Manager{},
	}
}
