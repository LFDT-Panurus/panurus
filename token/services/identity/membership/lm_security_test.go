/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package membership_test

import (
	"context"
	"os"
	"os/exec"
	"testing"

	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/stretchr/testify/require"
)

// panicOnConfigurationsByIDStore panics inside ConfigurationsByID to simulate
// any failure reachable while refreshAndGet resolves an unknown label from
// the persistent store (a real store could panic on e.g. a malformed row or
// a driver bug). The point is to observe what happens to the RWMutex in
// getLocalIdentity regardless of what triggers the panic.
type panicOnConfigurationsByIDStore struct{}

func (p *panicOnConfigurationsByIDStore) AddConfiguration(context.Context, idriver.IdentityConfiguration) error {
	return nil
}

func (p *panicOnConfigurationsByIDStore) GetConfiguration(context.Context, string, string, string) (*idriver.IdentityConfiguration, error) {
	return nil, nil
}

func (p *panicOnConfigurationsByIDStore) ConfigurationsByID(context.Context, string, string) ([]idriver.IdentityConfiguration, error) {
	panic("boom: simulated panic inside identityDB.ConfigurationsByID")
}

func (p *panicOnConfigurationsByIDStore) ConfigurationExists(context.Context, string, string, string) (bool, error) {
	return false, nil
}

func (p *panicOnConfigurationsByIDStore) IteratorConfigurations(context.Context, string) (membership.IdentityConfigurationIterator, error) {
	return &mock.IdentityConfigurationIterator{}, nil
}

func (p *panicOnConfigurationsByIDStore) Notifier() (idriver.IdentityConfigurationNotifier, error) {
	return nil, nil
}

const lockCorruptionWorkerEnv = "PANURUS_LM_LOCK_CORRUPTION_WORKER"

// TestGetLocalIdentity_PanicDuringRefreshDoesNotCorruptRWMutex proves that
// getLocalIdentity's RUnlock()/deferred-RLock() pair around refreshAndGet no
// longer corrupts localIdentitiesMutex's reader count when refreshAndGet (or
// anything it transitively calls, such as the identity store) panics: the
// deferred RLock still runs during stack unwind, so the caller's own
// deferred RUnlock (e.g. GetIdentityInfo's) balances correctly. The
// underlying panic still propagates (refreshAndGet's caller does not
// recover), but it must surface as an ordinary panic carrying the original
// message, not as a "fatal error: sync: RUnlock of unlocked RWMutex" runtime
// abort. The scenario is exercised in a subprocess so a regression (a fatal
// error, which cannot be recovered from) is still observable from the test.
func TestGetLocalIdentity_PanicDuringRefreshDoesNotCorruptRWMutex(t *testing.T) {
	if os.Getenv(lockCorruptionWorkerEnv) == "1" {
		runLockCorruptionWorker()

		return
	}

	cmd := exec.Command(os.Args[0], "-test.run", "^TestGetLocalIdentity_PanicDuringRefreshDoesNotCorruptRWMutex$", "-test.v") //nolint:gosec // re-exec of the test binary itself, not attacker-controlled input
	cmd.Env = append(os.Environ(), lockCorruptionWorkerEnv+"=1")
	out, err := cmd.CombinedOutput()
	outStr := string(out)
	t.Logf("subprocess output:\n%s", outStr)

	require.Error(t, err, "the underlying panic still propagates since nothing recovers it")
	require.Contains(t, outStr, "boom: simulated panic inside identityDB.ConfigurationsByID",
		"subprocess crashed but not where expected")
	require.NotContains(t, outStr, "fatal error: sync: RUnlock of unlocked RWMutex",
		"the RWMutex must remain balanced - this fatal error means the fix regressed")
}

// TestGetLocalIdentity_RecoverSurvivesPanicDuringRefresh proves that, with
// the RWMutex no longer corrupted, a top-level recover() - the kind of
// panic-recovery middleware commonly wrapped around per-request handlers
// (e.g. RPC/view handlers) to keep a server alive despite bugs in individual
// request processing - successfully contains the panic and the process
// keeps serving subsequent requests. Before the fix this was impossible: the
// corrupted-RWMutex fatal error bypassed recover() entirely and killed the
// whole process before the handler's deferred recover() ever ran.
func TestGetLocalIdentity_RecoverSurvivesPanicDuringRefresh(t *testing.T) {
	if os.Getenv(lockCorruptionWorkerEnv) == "1" {
		runLockCorruptionRecoverWorker()

		return
	}

	cmd := exec.Command(os.Args[0], "-test.run", "^TestGetLocalIdentity_RecoverSurvivesPanicDuringRefresh$", "-test.v") //nolint:gosec // re-exec of the test binary itself, not attacker-controlled input
	cmd.Env = append(os.Environ(), lockCorruptionWorkerEnv+"=1")
	out, err := cmd.CombinedOutput()
	outStr := string(out)
	t.Logf("subprocess output:\n%s", outStr)

	require.NoError(t, err, "the process should exit cleanly: recover() contains the panic and the mutex stays balanced")
	require.Contains(t, outStr, "recovered from per-request panic",
		"the handler's recover() should catch the panic from request 1")
	require.Contains(t, outStr, "server continues running, handled request 2 successfully",
		"the server should still be able to serve request 2 after recovering from request 1's panic")
}

func newLockCorruptionMembership() *membership.LocalMembership {
	return membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		&mock.SignerDeserializerManager{},
		&panicOnConfigurationsByIDStore{},
		"testType",
		false,
		&mock.IdentityProvider{},
	)
}

func runLockCorruptionWorker() {
	lm := newLockCorruptionMembership()

	// A single goroutine, single call: "unknown-label" is not present in any
	// in-memory map, forcing getLocalIdentity to take the
	// RUnlock -> refreshAndGet -> RLock path. refreshAndGet's call into
	// identityDB.ConfigurationsByID panics. GetIdentityInfo's own deferred
	// RUnlock then fires during stack unwind, one call too many relative to
	// the RLock count actually held, and the runtime aborts the process.
	_, _ = lm.GetIdentityInfo(context.Background(), "unknown-label", nil)
}

// simulateRequestHandler mimics a typical per-request handler wrapped with
// panic-recovery middleware, the kind found in many RPC/HTTP/view-handler
// frameworks, so that one bad request cannot take down the whole server.
func simulateRequestHandler(lm *membership.LocalMembership, label string) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			println("recovered from per-request panic:", r.(string))
			ok = false
		}
	}()
	_, _ = lm.GetIdentityInfo(context.Background(), label, nil)

	return true
}

func runLockCorruptionRecoverWorker() {
	lm := newLockCorruptionMembership()

	// Request 1: an attacker-influenced label that is not present in any
	// in-memory map, forcing getLocalIdentity's RUnlock -> refreshAndGet ->
	// RLock path; refreshAndGet's call into identityDB.ConfigurationsByID
	// panics. The handler's own recover() should normally shield the server
	// from this, letting it continue serving other requests.
	simulateRequestHandler(lm, "unknown-label")

	// If we get here, request 1's panic was contained as expected and the
	// server is still alive to serve request 2.
	println("server continues running, handled request 2 successfully")
}

// TestGetIdentityInfo_NotFoundLeaksOtherIdentities proves that
// GetIdentityInfo's "not found" error message embeds the full
// localIdentitiesByName map via %v, and LocalIdentity.String() for
// non-anonymous identities formats the resolved enrollment ID into that
// string, and LocalIdentity.String() for non-anonymous identities formats
// the resolved enrollment ID into that string. So any caller that triggers
// a "not found" lookup — e.g. an attacker-supplied wallet label that doesn't
// resolve — would receive an error string containing every other loaded
// identity's enrollment ID. GetIdentityInfo now formats the map through
// logging.Keys, which renders only the map's key set (identity names), not
// its values (the *LocalIdentity structs whose String() leaks the
// enrollment ID) — mirroring the same fix already applied to GetIdentifier.
func TestGetIdentityInfo_NotFoundDoesNotLeakOtherIdentities(t *testing.T) {
	ctx := context.Background()

	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)
	ip.IsMeReturns(false)

	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)
	iss.NotifierReturns(nil, storage.ErrNotSupported)
	// make sure the "unknown" label really is unknown to the persistent store too
	iss.ConfigurationsByIDReturns(nil, nil)

	cfg := &mock.Config{}
	cfg.TranslatePathCalls(func(p string) string { return p })

	const secretEnrollmentID = "super-secret-enrollment-id-alice"
	const secretIdentityBytes = "raw-identity-bytes-that-should-stay-private"

	kmp := &mock.KeyManagerProvider{}
	kmp.GetCalls(func(_ context.Context, idConfig *membership.IdentityConfiguration) (membership.KeyManager, error) {
		km := &mock.KeyManager{}
		km.EnrollmentIDReturns(secretEnrollmentID)
		km.AnonymousReturns(false) // non-anonymous: String() eagerly fetches+formats the identity
		km.IsRemoteReturns(false)
		km.IdentityReturns(&idriver.IdentityDescriptor{
			Identity:  []byte(secretIdentityBytes),
			AuditInfo: []byte("some-audit-info"),
		}, nil)
		km.IdentityTypeReturns(0)

		return km, nil
	})

	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		cfg,
		[]byte("netid"),
		&mock.SignerDeserializerManager{},
		iss,
		"testType",
		false,
		ip,
		kmp,
	)

	// Register a legitimate identity "alice" whose enrollment ID is meant to
	// stay confidential to this node.
	regCfg := membership.IdentityConfiguration{ID: "alice", URL: "/tmp/alice"}
	require.NoError(t, lm.RegisterIdentity(ctx, regCfg))

	// Simulate an attacker-controlled/unrelated party asking for identity
	// info for a label that does not exist, e.g. a bogus wallet lookup id
	// coming over the wire (role.Role.GetIdentityInfo / Registry.Lookup call
	// LocalMembership.GetIdentityInfo with externally influenced labels).
	_, err := lm.GetIdentityInfo(ctx, "does-not-exist", nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), secretEnrollmentID,
		"the 'not found' error must not leak other identities' enrollment IDs")
	require.NotContains(t, err.Error(), secretIdentityBytes,
		"the 'not found' error must not leak other identities' raw identity bytes")
	require.Contains(t, err.Error(), "alice",
		"the error may still reference the known identity name, just not its enrollment id/identity bytes")
}
