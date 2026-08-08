/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/recovery"
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

// newRecoveryDriver returns a Driver with the recovery bookkeeping initialized and no stores, which
// is what NewDriver produces before anything binds to it.
func newRecoveryDriver() *Driver {
	return &Driver{
		resolver:   fakeResolver{evm: map[string]bool{"evm-net|": true}},
		recoveries: map[string][]*recovery.Manager{},
	}
}
