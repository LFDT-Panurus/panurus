/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package recovery_test

import (
	"strings"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	tokenconfig "github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/config/mocks"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	recovery2 "github.com/LFDT-Panurus/panurus/token/services/storage/services/recovery"
	mock2 "github.com/LFDT-Panurus/panurus/token/services/storage/services/recovery/mock"
	fscconfig "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An operator upgrading from a release that still had services.network.fabric.recovery.advisoryLockID
// keeps that key in their config. The lock id is now derived per TMS and the field is gone, so the
// key must simply be ignored rather than failing the load.
func TestLoadConfig_IgnoresStaleAdvisoryLockID(t *testing.T) {
	cp, err := fscconfig.NewProvider("./testdata/stalecfg")
	require.NoError(t, err)

	cfg := tokenconfig.NewConfiguration(cp, "n1c1ns1", driver.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"})

	loaded, err := recovery2.LoadConfig(cfg)
	require.NoError(t, err, "a stale advisoryLockID key must not fail the load")

	assert.True(t, loaded.Enabled)
	assert.Equal(t, 45*time.Second, loaded.TTL, "the rest of the block must still be applied")
}

func newTestTMSConfig(isSet func(key string) bool, unmarshal func(key string, rawVal any) error) *tokenconfig.Configuration {
	cp := &mocks.Provider{}
	cp.IsSetStub = isSet
	cp.UnmarshalKeyStub = unmarshal

	return tokenconfig.NewConfiguration(cp, "tms1", driver.TMSID{Network: "net", Channel: "ch", Namespace: "ns"})
}

// TestLoadConfig_ClampsDefaultTransactionTimeoutBelowConfiguredLeaseDuration
// is the regression test for the round-2 startup failure: an operator who
// only configures leaseDuration, never touching transactionTimeout, used to
// end up with the 10s package default paired against their smaller
// leaseDuration. validateConfig at the time rejected that combination at
// Start(), refusing to start a node whose config was valid before
// transactionTimeout existed at all. Round 7 removed that specific
// validateConfig check (see warnIfLeaseMayExpireMidSweep), but the clamp
// itself is still worth keeping: leaving an operator at a default that dwarfs
// a lease they explicitly configured smaller is confusing regardless of
// whether Start() still hard-fails over it. Uses a leaseDuration large enough
// that half of it still clears minTransactionTimeout, so the clamp lands
// strictly below the lease; see
// TestLoadConfig_ClampFloorsAtMinTransactionTimeoutForSmallLeaseDuration for
// the case where it cannot.
func TestLoadConfig_ClampsDefaultTransactionTimeoutBelowConfiguredLeaseDuration(t *testing.T) {
	tmsCfg := newTestTMSConfig(
		func(key string) bool {
			// The recovery block and leaseDuration are present; transactionTimeout
			// is never mentioned, matching an operator who never set it.
			return strings.HasSuffix(key, recovery2.ConfigKeyRecovery) ||
				strings.HasSuffix(key, recovery2.ConfigKeyRecovery+".leaseDuration")
		},
		func(_ string, rawVal any) error {
			cfg, ok := rawVal.(*recovery2.Config)
			require.True(t, ok)
			// Enabled is applied unconditionally in LoadConfig (not IsSet-gated
			// like the other fields here), so it has to be set explicitly to
			// match a real operator's recovery block. Left at the Go zero value,
			// Start() returns before validateConfig ever runs and this test
			// passes without exercising the clamp at all.
			cfg.Enabled = true
			cfg.LeaseDuration = 60 * time.Second

			return nil
		},
	)

	result, err := recovery2.LoadConfig(tmsCfg)
	require.NoError(t, err)

	assert.Equal(t, 60*time.Second, result.LeaseDuration)
	assert.Positive(t, result.TransactionTimeout, "clamping must not silently disable the deadline")
	assert.Less(t, result.TransactionTimeout, result.LeaseDuration,
		"an operator who never touched transactionTimeout must not end up with a default disproportionate to the leaseDuration they configured")

	// The actual regression: Start() must succeed against the LoadConfig
	// output for an operator who only set leaseDuration.
	m := recovery2.NewManager(logging.MustGetLogger(), &mock2.Storage{}, &mock2.Handler{}, result)
	require.NoError(t, m.Start())
	require.NoError(t, m.Stop())
}

// TestLoadConfig_ClampFloorsAtMinTransactionTimeoutForSmallLeaseDuration
// covers a leaseDuration small enough that half of it undercuts
// minTransactionTimeout: the clamp must floor at minTransactionTimeout rather
// than at leaseDuration/2, even though that leaves the result at or above the
// leaseDuration the operator configured. round 8 made that combination a
// Warn at Start() (see warnIfLeaseMayExpireMidSweep /
// warnIfLeaseMayExpireBetweenSweeps), not a LoadConfig failure: landing an
// operator on a default below the 10s floor would silently trade one startup
// regression (round 6, this file's own history above) for another.
func TestLoadConfig_ClampFloorsAtMinTransactionTimeoutForSmallLeaseDuration(t *testing.T) {
	tmsCfg := newTestTMSConfig(
		func(key string) bool {
			return strings.HasSuffix(key, recovery2.ConfigKeyRecovery) ||
				strings.HasSuffix(key, recovery2.ConfigKeyRecovery+".leaseDuration")
		},
		func(_ string, rawVal any) error {
			cfg, ok := rawVal.(*recovery2.Config)
			require.True(t, ok)
			cfg.Enabled = true
			cfg.LeaseDuration = 5 * time.Second

			return nil
		},
	)

	result, err := recovery2.LoadConfig(tmsCfg)
	require.NoError(t, err)

	assert.Equal(t, 5*time.Second, result.LeaseDuration)
	assert.Equal(t, 10*time.Second, result.TransactionTimeout,
		"a leaseDuration/2 below the 10s floor must clamp up to the floor, not down to a value the LoadConfig floor check would otherwise have to reject")

	// The actual regression this guards against: Start() must still succeed,
	// even though the clamped default now exceeds the configured leaseDuration.
	m := recovery2.NewManager(logging.MustGetLogger(), &mock2.Storage{}, &mock2.Handler{}, result)
	require.NoError(t, m.Start())
	require.NoError(t, m.Stop())
}

// TestLoadConfig_ClampFloorsAboveZeroForTinyLeaseDuration guards the edge of
// the same clamp: result.LeaseDuration/2 truncates to 0 for a leaseDuration
// below 2ns, and 0 means "unbounded" everywhere else in this package (see the
// IsSet gate above and callHandler), so a naive clamp would land an operator
// in that opt-out by accident instead of a bounded, if absurdly tight,
// deadline. Superseded in practice by the minTransactionTimeout floor (see
// TestLoadConfig_ClampFloorsAtMinTransactionTimeoutForSmallLeaseDuration), but
// still worth asserting directly at this extreme.
func TestLoadConfig_ClampFloorsAboveZeroForTinyLeaseDuration(t *testing.T) {
	tmsCfg := newTestTMSConfig(
		func(key string) bool {
			return strings.HasSuffix(key, recovery2.ConfigKeyRecovery) ||
				strings.HasSuffix(key, recovery2.ConfigKeyRecovery+".leaseDuration")
		},
		func(_ string, rawVal any) error {
			cfg, ok := rawVal.(*recovery2.Config)
			require.True(t, ok)
			cfg.Enabled = true
			cfg.LeaseDuration = time.Nanosecond

			return nil
		},
	)

	result, err := recovery2.LoadConfig(tmsCfg)
	require.NoError(t, err)

	assert.Equal(t, time.Nanosecond, result.LeaseDuration)
	assert.Positive(t, result.TransactionTimeout,
		"the clamp must not truncate to 0, which means unbounded rather than a tight deadline")
}

// TestLoadConfig_TransactionTimeoutZeroSurvivesAsUnbounded guards the other
// direction of the same gate: an operator who explicitly opts out with
// transactionTimeout: 0 must not have that silently overridden back to the
// package default.
func TestLoadConfig_TransactionTimeoutZeroSurvivesAsUnbounded(t *testing.T) {
	tmsCfg := newTestTMSConfig(
		func(key string) bool {
			return strings.HasSuffix(key, recovery2.ConfigKeyRecovery) ||
				strings.HasSuffix(key, recovery2.ConfigKeyRecovery+".transactionTimeout")
		},
		func(_ string, rawVal any) error {
			cfg, ok := rawVal.(*recovery2.Config)
			require.True(t, ok)
			cfg.TransactionTimeout = 0 // explicit opt-out

			return nil
		},
	)

	result, err := recovery2.LoadConfig(tmsCfg)
	require.NoError(t, err)

	assert.Zero(t, result.TransactionTimeout,
		"an explicit transactionTimeout: 0 must survive as the documented unbounded opt-out, not fall back to the package default")
	assert.Equal(t, 5*time.Minute, result.LeaseDuration, "leaseDuration left unset should keep its default")
}

// TestLoadConfig_RejectsTransactionTimeoutBelowFloor covers round 8 review's
// ask on the 10s default: an operator's explicit transactionTimeout below the
// floor is rejected at LoadConfig, rather than silently accepted only to then
// abandon recoveries that were about to succeed on a merely slow ledger
// round-trip. Enforced in LoadConfig, not validateConfig, so tests elsewhere
// in this package remain free to construct a Config directly with a
// millisecond-scale TransactionTimeout to exercise timeout handling quickly.
func TestLoadConfig_RejectsTransactionTimeoutBelowFloor(t *testing.T) {
	tmsCfg := newTestTMSConfig(
		func(key string) bool {
			return strings.HasSuffix(key, recovery2.ConfigKeyRecovery) ||
				strings.HasSuffix(key, recovery2.ConfigKeyRecovery+".transactionTimeout")
		},
		func(_ string, rawVal any) error {
			cfg, ok := rawVal.(*recovery2.Config)
			require.True(t, ok)
			cfg.TransactionTimeout = 5 * time.Second

			return nil
		},
	)

	_, err := recovery2.LoadConfig(tmsCfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "10s")
}

func TestManager_StartValidatesConfig(t *testing.T) {
	base := func() recovery2.Config {
		return recovery2.Config{
			Enabled:            true,
			TTL:                time.Second,
			ScanInterval:       time.Second,
			BatchSize:          1,
			WorkerCount:        1,
			LeaseDuration:      time.Second,
			TransactionTimeout: 100 * time.Millisecond,
			InstanceID:         "test-instance",
		}
	}

	tests := []struct {
		name          string
		mutate        func(*recovery2.Config)
		wantErrSubstr string
	}{
		{"non-positive TTL", func(c *recovery2.Config) { c.TTL = 0 }, "invalid recovery TTL"},
		{"non-positive scan interval", func(c *recovery2.Config) { c.ScanInterval = 0 }, "invalid recovery scan interval"},
		{"non-positive batch size", func(c *recovery2.Config) { c.BatchSize = 0 }, "invalid recovery batch size"},
		{"non-positive worker count", func(c *recovery2.Config) { c.WorkerCount = 0 }, "invalid recovery worker count"},
		{"non-positive lease duration", func(c *recovery2.Config) { c.LeaseDuration = 0 }, "invalid recovery lease duration"},
		{"negative transaction timeout", func(c *recovery2.Config) { c.TransactionTimeout = -time.Millisecond }, "invalid recovery transaction timeout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			tt.mutate(&cfg)

			m := recovery2.NewManager(logging.MustGetLogger(), &mock2.Storage{}, &mock2.Handler{}, cfg)
			err := m.Start()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErrSubstr)
		})
	}
}

// TestManager_StartAcceptsZeroTransactionTimeoutAsUnbounded verifies the
// exemption: TransactionTimeout == 0 is valid at any LeaseDuration.
func TestManager_StartAcceptsZeroTransactionTimeoutAsUnbounded(t *testing.T) {
	cfg := recovery2.Config{
		Enabled:            true,
		TTL:                time.Second,
		ScanInterval:       time.Second,
		BatchSize:          1,
		WorkerCount:        1,
		LeaseDuration:      time.Second,
		TransactionTimeout: 0,
		InstanceID:         "test-instance",
	}

	mockDB := &mock2.Storage{}
	mockDB.AcquireRecoveryLeadershipReturns(nil, false, nil)

	m := recovery2.NewManager(logging.MustGetLogger(), mockDB, &mock2.Handler{}, cfg)
	require.NoError(t, m.Start())
	require.NoError(t, m.Stop())
}

// TestManager_WarnsWhenTransactionTimeoutMayExceedLeaseDuringSweep is the
// round-7 regression test for the finding that validateConfig's
// transactionTimeout < leaseDuration check enforced the wrong relationship:
// the docs describe leaseDuration > (batchSize/workerCount) x
// transactionTimeout, which this config violates (10 claims/worker x 200ms =
// 2s worst case, against a 1s lease) despite transactionTimeout (200ms)
// itself being comfortably less than leaseDuration (1s) — the old check would
// have passed this silently. Warn, not a Start() failure: see the doc comment
// on warnIfLeaseMayExpireMidSweep.
func TestManager_WarnsWhenTransactionTimeoutMayExceedLeaseDuringSweep(t *testing.T) {
	logger := &sweepLogger{}
	cfg := recovery2.Config{
		Enabled:            true,
		TTL:                time.Second,
		ScanInterval:       time.Second,
		BatchSize:          10,
		WorkerCount:        1,
		LeaseDuration:      time.Second,
		TransactionTimeout: 200 * time.Millisecond,
		InstanceID:         "test-instance",
	}

	mockDB := &mock2.Storage{}
	mockDB.AcquireRecoveryLeadershipReturns(nil, false, nil)

	m := recovery2.NewManager(logger, mockDB, &mock2.Handler{}, cfg)
	require.NoError(t, m.Start())
	require.NoError(t, m.Stop())

	found := false
	for _, msg := range logger.allMessages() {
		if strings.Contains(msg, "worst-case sweep duration") && strings.Contains(msg, "leaseDuration") {
			found = true

			break
		}
	}
	assert.True(t, found, "expected a Warn about worst-case sweep duration vs leaseDuration, got: %v", logger.allMessages())
}

// TestManager_WarnsWhenScanIntervalMayExceedLeaseDuration is the round-7
// regression test for the new renewal-by-reclaim design: a stuck
// transaction's claim is kept alive only by this instance's own next
// ClaimPendingTransactions call reclaiming it (see the inFlight field's doc
// comment), so if ScanInterval is not comfortably shorter than
// LeaseDuration, the claim can lapse between sweeps.
func TestManager_WarnsWhenScanIntervalMayExceedLeaseDuration(t *testing.T) {
	logger := &sweepLogger{}
	cfg := recovery2.Config{
		Enabled:            true,
		TTL:                time.Second,
		ScanInterval:       2 * time.Second,
		BatchSize:          1,
		WorkerCount:        1,
		LeaseDuration:      time.Second,
		TransactionTimeout: 100 * time.Millisecond,
		InstanceID:         "test-instance",
	}

	mockDB := &mock2.Storage{}
	mockDB.AcquireRecoveryLeadershipReturns(nil, false, nil)

	m := recovery2.NewManager(logger, mockDB, &mock2.Handler{}, cfg)
	require.NoError(t, m.Start())
	require.NoError(t, m.Stop())

	found := false
	for _, msg := range logger.allMessages() {
		if strings.Contains(msg, "scanInterval") && strings.Contains(msg, "leaseDuration") {
			found = true

			break
		}
	}
	assert.True(t, found, "expected a Warn about scanInterval vs leaseDuration, got: %v", logger.allMessages())
}
