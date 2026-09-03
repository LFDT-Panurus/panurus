/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package checks_test

import (
	"errors"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	dbcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/checks"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/checks/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testTMSID = token.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"}

func testConfig() checks.Config {
	return checks.Config{
		Enabled:      true,
		ScanInterval: 100 * time.Millisecond,
		Timeout:      50 * time.Millisecond,
		BatchSize:    10,
	}
}

func newTestManager(t *testing.T, storage *mock.Storage, checker *mock.Checker, cfg checks.Config) *checks.Manager {
	t.Helper()

	return checks.NewManager(logging.MustGetLogger(), storage, checker, checks.NewMetrics(nil), cfg, checks.RoleOwner, testTMSID)
}

func TestNewManager(t *testing.T) {
	manager := newTestManager(t, &mock.Storage{}, &mock.Checker{}, testConfig())
	require.NotNil(t, manager)
}

// A single lock id shared by every TMS on a node was the bug: whichever TMS's
// sweep ran first would win the advisory lock and every other TMS would find it
// already held, forever. Two TMSes sharing a persistence configuration are told
// apart only by network, channel and namespace, so the lock id has to be derived
// from those, not from a node-wide constant.
func TestManager_LockIDDistinctPerTMS(t *testing.T) {
	leadership := &mock.Leadership{}
	leadership.CloseReturns(nil)

	storageA := &mock.Storage{}
	storageA.AcquireLeadershipReturns(leadership, true, nil)
	managerA := checks.NewManager(logging.MustGetLogger(), storageA, &mock.Checker{}, checks.NewMetrics(nil), testConfig(), checks.RoleOwner,
		token.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"})
	require.NoError(t, managerA.RunSweep(t.Context()))
	require.Equal(t, 1, storageA.AcquireLeadershipCallCount())
	_, lockIDA := storageA.AcquireLeadershipArgsForCall(0)

	storageB := &mock.Storage{}
	storageB.AcquireLeadershipReturns(leadership, true, nil)
	managerB := checks.NewManager(logging.MustGetLogger(), storageB, &mock.Checker{}, checks.NewMetrics(nil), testConfig(), checks.RoleOwner,
		token.TMSID{Network: "n1", Channel: "c1", Namespace: "ns2"})
	require.NoError(t, managerB.RunSweep(t.Context()))
	require.Equal(t, 1, storageB.AcquireLeadershipCallCount())
	_, lockIDB := storageB.AcquireLeadershipArgsForCall(0)

	assert.NotEqual(t, lockIDA, lockIDB, "two TMSes must not derive the same checks lock id")

	// The owner and auditor sweeps over the same TMS are also separate stores and
	// must not contend for each other's lock either.
	storageC := &mock.Storage{}
	storageC.AcquireLeadershipReturns(leadership, true, nil)
	managerC := checks.NewManager(logging.MustGetLogger(), storageC, &mock.Checker{}, checks.NewMetrics(nil), testConfig(), checks.RoleAuditor,
		token.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"})
	require.NoError(t, managerC.RunSweep(t.Context()))
	require.Equal(t, 1, storageC.AcquireLeadershipCallCount())
	_, lockIDC := storageC.AcquireLeadershipArgsForCall(0)

	assert.NotEqual(t, lockIDA, lockIDC, "the owner and auditor sweeps over the same TMS must not derive the same checks lock id")
}

func TestManager_DisabledConfig(t *testing.T) {
	storage := &mock.Storage{}
	checker := &mock.Checker{}
	cfg := testConfig()
	cfg.Enabled = false
	manager := newTestManager(t, storage, checker, cfg)

	require.NoError(t, manager.Start())
	require.NoError(t, manager.Stop())

	assert.Equal(t, 0, storage.AcquireLeadershipCallCount())
	assert.Equal(t, 0, checker.CheckCallCount())
}

func TestManager_Start_InvalidConfig(t *testing.T) {
	base := testConfig()
	cases := map[string]checks.Config{
		"scan interval":      {Enabled: true, ScanInterval: 0, Timeout: base.Timeout, BatchSize: base.BatchSize},
		"timeout":            {Enabled: true, ScanInterval: base.ScanInterval, Timeout: 0, BatchSize: base.BatchSize},
		"batch size":         {Enabled: true, ScanInterval: base.ScanInterval, Timeout: base.Timeout, BatchSize: 0},
		"transaction window": {Enabled: true, ScanInterval: base.ScanInterval, Timeout: base.Timeout, BatchSize: base.BatchSize, TransactionWindow: -time.Second},
		"timeout exceeds scan interval": {
			Enabled: true, ScanInterval: base.Timeout, Timeout: base.Timeout + time.Millisecond, BatchSize: base.BatchSize,
		},
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			manager := newTestManager(t, &mock.Storage{}, &mock.Checker{}, cfg)
			require.Error(t, manager.Start())
		})
	}
}

func TestManager_StartTwice(t *testing.T) {
	storage := &mock.Storage{}
	checker := &mock.Checker{}
	leadership := &mock.Leadership{}
	storage.AcquireLeadershipReturns(leadership, false, nil)
	manager := newTestManager(t, storage, checker, testConfig())

	require.NoError(t, manager.Start())
	defer func() { _ = manager.Stop() }()

	err := manager.Start()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already started")
}

func TestManager_StopWithoutStart(t *testing.T) {
	manager := newTestManager(t, &mock.Storage{}, &mock.Checker{}, testConfig())
	require.NoError(t, manager.Stop())
}

func TestManager_RunSweep_NotLeader(t *testing.T) {
	storage := &mock.Storage{}
	checker := &mock.Checker{}
	storage.AcquireLeadershipReturns(nil, false, nil)
	manager := newTestManager(t, storage, checker, testConfig())

	require.NoError(t, manager.RunSweep(t.Context()))

	assert.Equal(t, 0, checker.CheckCallCount())
	assert.Equal(t, 0, storage.UpsertFindingsCallCount())
}

func TestManager_RunSweep_AcquireLeadershipError(t *testing.T) {
	storage := &mock.Storage{}
	checker := &mock.Checker{}
	storage.AcquireLeadershipReturns(nil, false, errors.New("lock unavailable"))
	manager := newTestManager(t, storage, checker, testConfig())

	err := manager.RunSweep(t.Context())
	require.Error(t, err)
	assert.Equal(t, 0, checker.CheckCallCount())
}

func TestManager_RunSweep_ReleasesLeadershipOnCheckError(t *testing.T) {
	storage := &mock.Storage{}
	checker := &mock.Checker{}
	leadership := &mock.Leadership{}
	leadership.CloseReturns(nil)
	storage.AcquireLeadershipReturns(leadership, true, nil)
	checker.CheckReturns(nil, errors.New("ledger unreachable"))
	manager := newTestManager(t, storage, checker, testConfig())

	err := manager.RunSweep(t.Context())
	require.Error(t, err)
	assert.Equal(t, 1, leadership.CloseCallCount())
	assert.Equal(t, 0, storage.UpsertFindingsCallCount())
}

func TestManager_RunSweep_RecordsAndResolvesFindings(t *testing.T) {
	storage := &mock.Storage{}
	checker := &mock.Checker{}
	leadership := &mock.Leadership{}
	leadership.CloseReturns(nil)
	storage.AcquireLeadershipReturns(leadership, true, nil)
	storage.ResolveFindingsNotSeenSinceReturns(3, nil)

	findings := []dbcommon.Finding{
		{Checker: "Check A", Code: dbcommon.CodeTxStatusMismatch, Severity: dbcommon.SeverityCritical, TxID: "tx1"},
		{Checker: "Check B", Code: dbcommon.CodeTokenMissingOnLedger, Severity: dbcommon.SeverityWarning, TxID: "tx2"},
	}
	checker.CheckReturns(findings, nil)
	checker.ResolvableCheckersReturns([]string{"Check A", "Check B"})

	manager := newTestManager(t, storage, checker, testConfig())
	require.NoError(t, manager.RunSweep(t.Context()))

	require.Equal(t, 1, storage.UpsertFindingsCallCount())
	_, records, _ := storage.UpsertFindingsArgsForCall(0)
	require.Len(t, records, 2)
	assert.Equal(t, findings[0].Key(), records[0].Key)
	assert.Equal(t, "tx1", records[0].TxID)
	assert.Equal(t, int(dbcommon.SeverityCritical), records[0].Severity)

	require.Equal(t, 1, storage.ResolveFindingsNotSeenSinceCallCount())
	_, resolvable, _ := storage.ResolveFindingsNotSeenSinceArgsForCall(0)
	assert.ElementsMatch(t, []string{"Check A", "Check B"}, resolvable)

	assert.Equal(t, 1, leadership.CloseCallCount())
}

func TestManager_RunSweep_CheckFailedKeepsThatCheckersFindingsOpen(t *testing.T) {
	storage := &mock.Storage{}
	checker := &mock.Checker{}
	leadership := &mock.Leadership{}
	leadership.CloseReturns(nil)
	storage.AcquireLeadershipReturns(leadership, true, nil)
	storage.ResolveFindingsNotSeenSinceReturns(0, nil)

	findings := []dbcommon.Finding{
		{Checker: "Check A", Code: dbcommon.CodeCheckFailed, Severity: dbcommon.SeverityWarning, Message: "ledger unreachable"},
		{Checker: "Check B", Code: dbcommon.CodeTokenMissingOnLedger, Severity: dbcommon.SeverityWarning, TxID: "tx2"},
	}
	checker.CheckReturns(findings, nil)
	checker.ResolvableCheckersReturns([]string{"Check A", "Check B"})

	manager := newTestManager(t, storage, checker, testConfig())
	require.NoError(t, manager.RunSweep(t.Context()))

	require.Equal(t, 1, storage.ResolveFindingsNotSeenSinceCallCount())
	_, resolvable, _ := storage.ResolveFindingsNotSeenSinceArgsForCall(0)
	assert.Equal(t, []string{"Check B"}, resolvable)
}

// A checker can report a finding that is inconclusive without going through
// CodeCheckFailed, e.g. CodeTxStatusUnavailable for a single ledger query that
// errored while the rest of the checker's pass succeeded. That checker must
// stay excluded from resolution too, or a ledger outage on one transaction
// would close a critical finding recorded for that same transaction earlier.
func TestManager_RunSweep_InconclusiveFindingKeepsThatCheckersFindingsOpen(t *testing.T) {
	storage := &mock.Storage{}
	checker := &mock.Checker{}
	leadership := &mock.Leadership{}
	leadership.CloseReturns(nil)
	storage.AcquireLeadershipReturns(leadership, true, nil)
	storage.ResolveFindingsNotSeenSinceReturns(0, nil)

	findings := []dbcommon.Finding{
		{Checker: "Check A", Code: dbcommon.CodeTxStatusUnavailable, Severity: dbcommon.SeverityInfo, TxID: "tx1"},
		{Checker: "Check B", Code: dbcommon.CodeTokenMissingOnLedger, Severity: dbcommon.SeverityWarning, TxID: "tx2"},
	}
	checker.CheckReturns(findings, nil)
	checker.ResolvableCheckersReturns([]string{"Check A", "Check B"})

	manager := newTestManager(t, storage, checker, testConfig())
	require.NoError(t, manager.RunSweep(t.Context()))

	require.Equal(t, 1, storage.ResolveFindingsNotSeenSinceCallCount())
	_, resolvable, _ := storage.ResolveFindingsNotSeenSinceArgsForCall(0)
	assert.Equal(t, []string{"Check B"}, resolvable)
}

func TestManager_RunSweep_ForwardsEveryFindingToStorage(t *testing.T) {
	storage := &mock.Storage{}
	checker := &mock.Checker{}
	leadership := &mock.Leadership{}
	leadership.CloseReturns(nil)
	storage.AcquireLeadershipReturns(leadership, true, nil)
	storage.ResolveFindingsNotSeenSinceReturns(0, nil)

	findings := []dbcommon.Finding{
		{Checker: "Check A", Code: dbcommon.CodeTokenMissingOnLedger, Severity: dbcommon.SeverityWarning, TxID: "tx1"},
		{Checker: "Check A", Code: dbcommon.CodeTokenMissingOnLedger, Severity: dbcommon.SeverityCritical, TxID: "tx1"},
	}
	checker.CheckReturns(findings, nil)
	checker.ResolvableCheckersReturns([]string{"Check A"})

	manager := newTestManager(t, storage, checker, testConfig())
	require.NoError(t, manager.RunSweep(t.Context()))

	require.Equal(t, 1, storage.UpsertFindingsCallCount())
	_, records, _ := storage.UpsertFindingsArgsForCall(0)
	assert.Len(t, records, 2, "the manager forwards raw findings; deduplication on the same key is the storage layer's job")
}

func TestManager_RunSweep_UpsertFindingsError(t *testing.T) {
	storage := &mock.Storage{}
	checker := &mock.Checker{}
	leadership := &mock.Leadership{}
	leadership.CloseReturns(nil)
	storage.AcquireLeadershipReturns(leadership, true, nil)
	storage.UpsertFindingsReturns(errors.New("db unavailable"))
	checker.CheckReturns([]dbcommon.Finding{{Checker: "Check A", Code: dbcommon.CodeTxStatusMismatch}}, nil)

	manager := newTestManager(t, storage, checker, testConfig())
	err := manager.RunSweep(t.Context())
	require.Error(t, err)
	assert.Equal(t, 0, storage.ResolveFindingsNotSeenSinceCallCount())
}

func TestManager_RunSweep_ResolveFindingsError(t *testing.T) {
	storage := &mock.Storage{}
	checker := &mock.Checker{}
	leadership := &mock.Leadership{}
	leadership.CloseReturns(nil)
	storage.AcquireLeadershipReturns(leadership, true, nil)
	storage.ResolveFindingsNotSeenSinceReturns(0, errors.New("db unavailable"))
	checker.CheckReturns(nil, nil)
	checker.ResolvableCheckersReturns([]string{"Check A"})

	manager := newTestManager(t, storage, checker, testConfig())
	err := manager.RunSweep(t.Context())
	require.Error(t, err)
}

func TestManager_RunSweep_NoFindings(t *testing.T) {
	storage := &mock.Storage{}
	checker := &mock.Checker{}
	leadership := &mock.Leadership{}
	leadership.CloseReturns(nil)
	storage.AcquireLeadershipReturns(leadership, true, nil)
	checker.CheckReturns(nil, nil)
	checker.ResolvableCheckersReturns([]string{"Check A"})

	manager := newTestManager(t, storage, checker, testConfig())
	require.NoError(t, manager.RunSweep(t.Context()))

	// The manager always calls UpsertFindings, empty or not; skipping the no-op
	// insert is the storage layer's call to make, not the manager's.
	require.Equal(t, 1, storage.UpsertFindingsCallCount())
	_, records, _ := storage.UpsertFindingsArgsForCall(0)
	assert.Empty(t, records)

	require.Equal(t, 1, storage.ResolveFindingsNotSeenSinceCallCount())
	_, resolvable, _ := storage.ResolveFindingsNotSeenSinceArgsForCall(0)
	assert.Equal(t, []string{"Check A"}, resolvable)
}

func TestManager_StartStop_SweepsInBackground(t *testing.T) {
	storage := &mock.Storage{}
	checker := &mock.Checker{}
	leadership := &mock.Leadership{}
	leadership.CloseReturns(nil)
	storage.AcquireLeadershipReturns(leadership, true, nil)
	checker.CheckReturns(nil, nil)

	cfg := testConfig()
	cfg.ScanInterval = 20 * time.Millisecond
	cfg.Timeout = 10 * time.Millisecond
	manager := newTestManager(t, storage, checker, cfg)

	require.NoError(t, manager.Start())
	require.Eventually(t, func() bool {
		return checker.CheckCallCount() >= 1
	}, 2*time.Second, 10*time.Millisecond)

	require.NoError(t, manager.Stop())
}
