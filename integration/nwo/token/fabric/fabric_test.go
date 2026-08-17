/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"testing"
	"time"

	topology2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBackend is a fabric.Backend whose UpdatePublicParams/InstallPublicParams outcomes are
// driven by the test. It records how UpdatePublicParams was called so that a test can prove
// whether NetworkHandler reached the backend at all.
type fakeBackend struct {
	updateErr    error
	installErr   error
	updateCalls  int
	lastUpdatePP []byte
}

func (b *fakeBackend) PrepareNamespace(*topology2.TMS) {}

func (b *fakeBackend) UpdatePublicParams(_ *topology2.TMS, raw []byte) error {
	b.updateCalls++
	b.lastUpdatePP = raw

	return b.updateErr
}

func (b *fakeBackend) InstallPublicParams(*topology2.TMS, []byte) error {
	return b.installErr
}

// fakeWatcherBackend is a fakeBackend that also implements PublicParamsInstallWatcher, so that
// NetworkHandler drains its deferred installation. It records the timeout it was asked to wait
// with, so that a test can prove which bound NetworkHandler passed down.
type fakeWatcherBackend struct {
	fakeBackend
	has            bool
	waitErr        error
	waitCalls      int
	lastWaitFor    time.Duration
	installTimeout time.Duration
}

func (b *fakeWatcherBackend) HasPublicParamsInstall(*topology2.TMS) bool { return b.has }

func (b *fakeWatcherBackend) WaitForPublicParams(_ *topology2.TMS, timeout time.Duration) error {
	b.waitCalls++
	b.lastWaitFor = timeout

	return b.waitErr
}

func (b *fakeWatcherBackend) PublicParamsInstallTimeout() time.Duration { return b.installTimeout }

func newTMS() *topology2.TMS {
	return &topology2.TMS{
		Network:   "testnet",
		Channel:   "testchannel",
		Namespace: "tns",
		Driver:    "fabtoken",
	}
}

// newHandler returns a NetworkHandler wired to backend only. The methods under test do not touch
// the embedded common NetworkHandler, so it is left zero-valued.
func newHandler(backend Backend) *NetworkHandler {
	return &NetworkHandler{Entries: map[string]*Entry{}, Backend: backend}
}

// TestUpdatePublicParamsFailsFastOnPendingInstallError proves that a failed deferred installation
// surfaces at the NetworkHandler boundary as a gomega assertion carrying the original error, and
// that the backend's own UpdatePublicParams is never reached: issuing an update against public
// params that were never installed would only fail with a less informative error.
func TestUpdatePublicParamsFailsFastOnPendingInstallError(t *testing.T) {
	backend := &fakeWatcherBackend{has: true, waitErr: errors.New("install boom"), installTimeout: 42 * time.Second}
	handler := newHandler(backend)

	gomega.RegisterTestingT(t)
	err := gomega.InterceptGomegaFailure(func() {
		handler.UpdatePublicParams(newTMS(), []byte("pp"))
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "install boom")
	assert.Contains(t, err.Error(), "public params installation for")
	// the update itself must not run once the deferred installation is known to have failed
	assert.Equal(t, 0, backend.updateCalls)
	// the wait used the backend's own bound rather than a guessed one
	assert.Equal(t, 42*time.Second, backend.lastWaitFor)
}

// TestUpdatePublicParamsSucceedsAfterPendingInstall proves that, once the deferred installation
// has finished successfully, the update is forwarded to the backend with the given parameters and
// no assertion is raised.
func TestUpdatePublicParamsSucceedsAfterPendingInstall(t *testing.T) {
	backend := &fakeWatcherBackend{has: true, installTimeout: time.Second}
	handler := newHandler(backend)
	ppRaw := []byte("public-parameters")

	gomega.RegisterTestingT(t)
	err := gomega.InterceptGomegaFailure(func() {
		handler.UpdatePublicParams(newTMS(), ppRaw)
	})

	require.NoError(t, err)
	assert.Equal(t, 1, backend.waitCalls)
	assert.Equal(t, 1, backend.updateCalls)
	assert.Equal(t, ppRaw, backend.lastUpdatePP)
}

// TestUpdatePublicParamsSurfacesBackendError proves that when there is no deferred installation to
// drain, a failure of the backend's own UpdatePublicParams still surfaces as a gomega assertion.
func TestUpdatePublicParamsSurfacesBackendError(t *testing.T) {
	backend := &fakeWatcherBackend{has: false}
	backend.updateErr = errors.New("update boom")
	handler := newHandler(backend)

	gomega.RegisterTestingT(t)
	err := gomega.InterceptGomegaFailure(func() {
		handler.UpdatePublicParams(newTMS(), []byte("pp"))
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "update boom")
	assert.Contains(t, err.Error(), "failed updating public params")
	// with no recorded installation there is nothing to wait for
	assert.Equal(t, 0, backend.waitCalls)
	assert.Equal(t, 1, backend.updateCalls)
}

// TestUpdatePublicParamsNonWatcherBackend proves that a backend that does not defer installations
// is updated directly, without any attempt to drain a pending installation.
func TestUpdatePublicParamsNonWatcherBackend(t *testing.T) {
	backend := &fakeBackend{}
	handler := newHandler(backend)

	gomega.RegisterTestingT(t)
	err := gomega.InterceptGomegaFailure(func() {
		handler.UpdatePublicParams(newTMS(), []byte("pp"))
	})

	require.NoError(t, err)
	assert.Equal(t, 1, backend.updateCalls)
}

// TestCleanupLogsInsteadOfFailingOnInstallError proves that a deferred installation that failed
// after PostRun returned is reported at teardown without raising an assertion (which would mask
// the real test failures), and is drained with a zero timeout so teardown never blocks.
func TestCleanupLogsInsteadOfFailingOnInstallError(t *testing.T) {
	tms := newTMS()
	backend := &fakeWatcherBackend{has: true, waitErr: errors.New("install boom"), installTimeout: time.Minute}
	handler := newHandler(backend)
	handler.Entries[tms.ID()] = &Entry{TMS: tms}

	gomega.RegisterTestingT(t)
	err := gomega.InterceptGomegaFailure(func() {
		handler.Cleanup()
	})

	// Cleanup logs the failure, it does not turn it into a gomega assertion
	require.NoError(t, err)
	assert.Equal(t, 1, backend.waitCalls)
	assert.Equal(t, time.Duration(0), backend.lastWaitFor)
}

// TestPendingInstallErrorNonWatcher proves that a backend that does not track deferred
// installations reports no pending error.
func TestPendingInstallErrorNonWatcher(t *testing.T) {
	handler := newHandler(&fakeBackend{})

	require.NoError(t, handler.pendingInstallError(newTMS(), time.Second))
}

// TestPendingInstallErrorNoRecordedInstall proves that a watcher backend with no recorded
// installation for the TMS reports no pending error and is not waited on.
func TestPendingInstallErrorNoRecordedInstall(t *testing.T) {
	backend := &fakeWatcherBackend{has: false, waitErr: errors.New("should not be consulted")}
	handler := newHandler(backend)

	require.NoError(t, handler.pendingInstallError(newTMS(), time.Second))
	assert.Equal(t, 0, backend.waitCalls)
}

// TestPendingInstallErrorReturnsWatcherOutcome proves that a recorded installation's outcome is
// returned verbatim, and that the requested timeout is passed through to the watcher.
func TestPendingInstallErrorReturnsWatcherOutcome(t *testing.T) {
	backend := &fakeWatcherBackend{has: true, waitErr: errors.New("install boom")}
	handler := newHandler(backend)

	err := handler.pendingInstallError(newTMS(), 7*time.Second)
	require.ErrorContains(t, err, "install boom")
	assert.Equal(t, 7*time.Second, backend.lastWaitFor)
}

// TestPublicParamsInstallTimeout proves that the bound is taken from the backend when it exposes
// one, and is zero for a backend that does not defer installations.
func TestPublicParamsInstallTimeout(t *testing.T) {
	watcher := newHandler(&fakeWatcherBackend{installTimeout: 3 * time.Minute})
	assert.Equal(t, 3*time.Minute, watcher.publicParamsInstallTimeout())

	nonWatcher := newHandler(&fakeBackend{})
	assert.Equal(t, time.Duration(0), nonWatcher.publicParamsInstallTimeout())
}
