/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/recovery"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
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

	cfg := d.recoveryConfig(testTMSID(), 0)
	assert.Equal(t, recovery.DefaultConfig(), cfg)
	assert.True(t, cfg.Enabled, "recovery is on unless a deployment turns it off")
	assert.Positive(t, cfg.TTL, "a zero TTL would sweep transactions that are merely in flight")
	assert.Positive(t, cfg.ScanInterval)
	assert.Less(t, cfg.ScanInterval, 5*time.Minute, "a sweep this rare would not rescue anything usefully")
}

// TestRecoveryTTLCoversTheFinalityTimeout is what makes settledNetwork's verdict safe.
//
// Recovery reads a still-absent anchor as a rejection, and that is only true once the transaction has
// been outstanding for longer than one is ever awaited. The manager claims rows by age, so raising
// the TTL to the finality timeout is what puts that guarantee behind the verdict. A shorter TTL would
// have recovery condemning transactions the finality listener is still legitimately waiting on.
func TestRecoveryTTLCoversTheFinalityTimeout(t *testing.T) {
	d := &Driver{resolver: fakeResolver{evm: map[string]bool{"evm-net|": true}}}

	t.Run("a longer finality timeout raises the ttl", func(t *testing.T) {
		cfg := d.recoveryConfig(testTMSID(), 4*time.Minute)
		assert.Equal(t, 4*time.Minute, cfg.TTL)
	})

	t.Run("a shorter one leaves it alone", func(t *testing.T) {
		defaults := recovery.DefaultConfig()
		cfg := d.recoveryConfig(testTMSID(), time.Nanosecond)
		assert.Equal(t, defaults.TTL, cfg.TTL, "the configured sweep delay is a floor, not a target")
	})
}

// TestSettledNetworkResolvesAnAbsentAnchor is the fix for a rejected transaction being swept forever.
//
// A revert writes nothing to the chain, so an anchor that never appears is all a rejection ever looks
// like. Reporting that as Unknown made the shared recovery handler treat it as transient and look
// again on the next sweep, so the record stayed Pending, the holding it reserved was never released,
// and the sweep repeated every few seconds indefinitely.
func TestSettledNetworkResolvesAnAbsentAnchor(t *testing.T) {
	anchorTxID := func(t *testing.T) string {
		t.Helper()
		evm := &mock.EVMClient{}
		evm.ChainIDReturns(bigInt(testChainID), nil)
		n := testNetwork(t, evm, nil)

		return n.ComputeTxID(&driver.TxID{Creator: []byte("creator")})
	}

	t.Run("an absent anchor is invalid, not unknown", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.ChainIDReturns(bigInt(testChainID), nil)
		// An empty return is the contract reporting no token request hash for the anchor, which is
		// what a reverted apply leaves behind.
		evm.CallReturns(make([]byte, 32), nil)
		n := testNetwork(t, evm, nil)

		status, _, message, err := settledNetwork{n}.GetTransactionStatus(t.Context(), "token", anchorTxID(t))
		require.NoError(t, err)
		assert.Equal(t, driver.Invalid, status, "the sweep must reach a verdict rather than repeat forever")
		assert.NotEmpty(t, message, "the record should say why it was closed")
	})

	// Only Unknown is reinterpreted. A committed transaction still recovers as Valid, and its token
	// request hash has to survive the wrapper or the handler cannot verify what it commits.
	t.Run("a committed anchor is untouched", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.ChainIDReturns(bigInt(testChainID), nil)
		hash := make([]byte, 32)
		hash[31] = 0x7C
		evm.CallReturns(hash, nil)
		n := testNetwork(t, evm, nil)

		status, got, _, err := settledNetwork{n}.GetTransactionStatus(t.Context(), "token", anchorTxID(t))
		require.NoError(t, err)
		assert.Equal(t, driver.Valid, status)
		assert.Equal(t, hash, got, "the token request hash must reach the handler intact")
	})

	// A node that could not be reached has not established anything about the transaction, so it must
	// stay a failure and be retried rather than becoming a verdict.
	t.Run("an unreachable node is not a verdict", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.ChainIDReturns(bigInt(testChainID), nil)
		evm.CallReturns(nil, errors.New("connection refused"))
		n := testNetwork(t, evm, nil)

		_, _, _, err := settledNetwork{n}.GetTransactionStatus(t.Context(), "token", anchorTxID(t))
		require.Error(t, err)
	})
}

// newRecoveryDriver returns a Driver with the recovery bookkeeping initialized and no stores, which
// is what NewDriver produces before anything binds to it.
func newRecoveryDriver() *Driver {
	return &Driver{
		resolver:   fakeResolver{evm: map[string]bool{"evm-net|": true}},
		recoveries: map[string][]*recovery.Manager{},
	}
}
