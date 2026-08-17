/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabricx

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric"
	tokentopology "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/views/ppsetup"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	fabrictopology "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric/topology"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabricx"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/onsi/gomega"
)

const (
	// issuerClientID is the name of the FSC node whose view sets up the public parameters.
	issuerClientID = "issuer"
	// setupPublicParamsView is the view invoked on the issuer to set up the public parameters.
	setupPublicParamsView = "SetupPublicParams"
	// setupPublicParamsTimeout is the timeout passed to the SetupPublicParams view.
	setupPublicParamsTimeout = 2 * time.Minute
	// defaultClientRetries is how many times we look for a ready issuer client before giving up.
	defaultClientRetries = 60
	// defaultClientRetryDelay is how long we wait between two issuer client lookups.
	defaultClientRetryDelay = 1 * time.Second
	// defaultInstallDelay is how long InstallPublicParams waits before recording the public
	// parameters, giving the backend network time to settle before the FSC nodes are started.
	defaultInstallDelay = 10 * time.Second
)

var logger = logging.MustGetLogger()

type ClientProvider interface {
	Client(string) api.GRPCClient
}

// Backend installs and updates the token public parameters of a fabricx network by
// invoking the SetupPublicParams view on the issuer FSC node.
//
// InstallPendingPublicParams and UpdatePublicParams wait for the issuer client to become ready
// and report any failure as an error rather than a panic. InstallPublicParams only records the
// public parameters, because the issuer node does not exist yet when it runs; the outcome of
// their deferred installation is retrieved with WaitForPublicParams. A genuine runtime panic in
// a dependency, such as unsynchronised concurrent map access in the client provider, is left to
// propagate to the caller (a Ginkgo Setup), which reports it with the panic site intact.
type Backend struct {
	ClientProvider ClientProvider

	// ClientRetries is how many times to look for a ready issuer client before giving up.
	// Zero means defaultClientRetries.
	ClientRetries int
	// ClientRetryDelay is how long to wait between two issuer client lookups.
	// Zero means defaultClientRetryDelay.
	ClientRetryDelay time.Duration
	// InstallDelay is how long InstallPublicParams waits before recording the public parameters.
	// Zero means defaultInstallDelay.
	InstallDelay time.Duration

	mutex sync.Mutex
	// pending collects the public parameters to be installed once the FSC nodes are up.
	pending  []pendingPublicParams
	installs map[string]*installTask
}

// pendingPublicParams holds the public parameters of a TMS whose installation has been deferred.
type pendingPublicParams struct {
	tms   *tokentopology.TMS
	ppRaw []byte
	task  *installTask
}

// installTask tracks the outcome of one deferred public-params installation. err is
// written before done is closed and only read after done is closed, so it needs no lock.
type installTask struct {
	done chan struct{}
	once sync.Once
	err  error
}

func newInstallTask() *installTask {
	return &installTask{done: make(chan struct{})}
}

// complete records the outcome of the installation and unblocks any waiter. Only the
// first call has an effect, so a panic recovered after an error was already recorded
// does not overwrite the original cause.
func (t *installTask) complete(err error) {
	t.once.Do(func() {
		t.err = err
		close(t.done)
	})
}

// result returns the recorded outcome. It must only be called once done is closed.
func (t *installTask) result() error {
	return t.err
}

func (b *Backend) PrepareNamespace(tms *tokentopology.TMS) {
	switch n := tms.BackendTopology.(type) {
	case *fabrictopology.Topology:
		orgs := fabric.GetOrgs(tms)
		gomega.Expect(orgs).ToNot(gomega.BeEmpty(), "missing orgs for tms [%s:%s:%s:%s:%s]", tms.Network, tms.Channel, tms.Namespace, tms.Driver, tms.Alias)

		addNamespace(n, tms, orgs...)
	case *fabricx.Topology:
		orgs := fabric.GetOrgs(tms)
		gomega.Expect(orgs).ToNot(gomega.BeEmpty(), "missing orgs for tms [%s:%s:%s:%s:%s]", tms.Network, tms.Channel, tms.Namespace, tms.Driver, tms.Alias)

		addNamespace(n.Topology, tms, orgs...)
	default:
		gomega.Expect(false).To(gomega.BeTrue(), "unknown backend network type %T", n)
	}
}

// addNamespace deploys the token namespace with either the custom policy configured via
// fabric.WithNamespacePolicy, or the default unanimity policy over orgs when unset.
func addNamespace(n *fabrictopology.Topology, tms *tokentopology.TMS, orgs ...string) {
	policy := fabric.GetNamespacePolicy(tms)
	if len(policy) == 0 {
		n.AddNamespaceWithUnanimity(tms.Namespace, orgs...)

		return
	}

	var peers []string
	for _, org := range orgs {
		for _, peer := range n.Peers {
			if peer.Organization == org {
				peers = append(peers, peer.Name)
			}
		}
	}
	n.AddNamespace(tms.Namespace, policy, peers...)
}

// InstallPublicParams records the public parameters of the passed TMS for installation.
// The installation cannot happen here: this runs in the token platform's post-run hook, which
// executes before the FSC nodes are started, hence the issuer's view client does not exist yet.
// Waiting for it in a background goroutine is not an option either, because the NWO context that
// hands out the view clients is not safe for concurrent use while the FSC platform populates it.
// The recorded public parameters are installed by InstallPendingPublicParams.
//
// The returned error only reports the absence of a client provider. The outcome of the
// installation itself is reported by InstallPendingPublicParams, and obtained afterwards with
// WaitForPublicParams; it is never raised as a panic.
//
// Recording the public parameters for the same TMS more than once is idempotent: the first
// recording is kept and later ones are ignored. Two TMSs that collide on their ID (e.g. the
// same network, channel, namespace and driver), or a re-run of the network bring-up, therefore
// do not fail the run, and the first outcome stays observable.
func (b *Backend) InstallPublicParams(tms *tokentopology.TMS, ppRaw []byte) error {
	if b.ClientProvider == nil {
		return errors.Errorf("no client provider available, cannot install public params on [%s]", tms.ID())
	}

	// give the backend network time to settle before the FSC nodes are started
	time.Sleep(b.installDelay())

	b.recordPending(tms, ppRaw)

	return nil
}

// InstallPendingPublicParams installs the public parameters recorded by InstallPublicParams and
// returns the failures of the installations that did not succeed. It must be called from the
// goroutine that started the network, once all FSC nodes are up. The outcome of every
// installation is also recorded on the backend, so that it can be retrieved later with
// WaitForPublicParams.
func (b *Backend) InstallPendingPublicParams() error {
	var errs []error
	for _, p := range b.takePending() {
		if err := b.installPublicParams(p); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// installPublicParams performs the actual installation of one recorded entry, reporting any
// failure as an error and recording the outcome on the entry's task. A genuine panic is left
// to propagate to the caller (a Ginkgo Setup), which reports it with the panic site intact.
func (b *Backend) installPublicParams(p pendingPublicParams) (err error) {
	tms := p.tms
	// record the outcome on the task even if the installation panics, so that a waiter in
	// WaitForPublicParams is not left blocked while the panic unwinds up to Ginkgo
	defer func() { p.task.complete(err) }()

	logger.Infof("installing public params on [%s]...", tms.ID())
	if err = b.setupPublicParams(tms, p.ppRaw); err != nil {
		logger.Errorf("installing public params on [%s]...failed [%v]", tms.ID(), err)

		return err
	}
	logger.Infof("installing public params on [%s]...done", tms.ID())

	return nil
}

// UpdatePublicParams replaces the public parameters of tms by invoking the SetupPublicParams
// view on the issuer FSC node. It waits for the issuer client to become available, so calling
// it while the issuer node is still starting is a wait rather than a failure, and returns any
// failure as an error.
func (b *Backend) UpdatePublicParams(tms *tokentopology.TMS, ppRaw []byte) error {
	if b.ClientProvider == nil {
		return errors.Errorf("no client provider available, cannot update public params on [%s]", tms.ID())
	}

	logger.Infof("updating public params on [%s]...", tms.ID())
	if err := b.setupPublicParams(tms, ppRaw); err != nil {
		logger.Errorf("updating public params on [%s]...failed [%v]", tms.ID(), err)

		return err
	}
	logger.Infof("updating public params on [%s]...done", tms.ID())

	return nil
}

// HasPublicParamsInstall reports whether InstallPublicParams was ever called for tms.
func (b *Backend) HasPublicParamsInstall(tms *tokentopology.TMS) bool {
	return b.installTaskFor(tms) != nil
}

// WaitForPublicParams blocks until the installation recorded by InstallPublicParams for tms has
// finished, and returns its outcome. It returns an error if the installation did not finish
// within timeout, or if no installation was ever recorded for tms. It can be called repeatedly
// and always reports the same outcome. A zero timeout makes it a non-blocking check: it reports
// a finished installation's outcome, and otherwise returns the timeout error at once.
func (b *Backend) WaitForPublicParams(tms *tokentopology.TMS, timeout time.Duration) error {
	task := b.installTaskFor(tms)
	if task == nil {
		return errors.Errorf("no public params installation was started for [%s]", tms.ID())
	}

	// Prefer an already-recorded outcome, so that a zero timeout is a well-defined non-blocking
	// check rather than a coin toss: with both cases ready, select would pick one at random.
	select {
	case <-task.done:
		return task.result()
	default:
	}

	select {
	case <-task.done:
		return task.result()
	case <-time.After(timeout):
		return errors.Errorf("timeout waiting for the installation of the public params on [%s]", tms.ID())
	}
}

// recordPending defers the installation of ppRaw on tms and registers a task for it. Recording
// twice for the same TMS is idempotent: the first recording is kept and later ones are ignored,
// so that the first outcome cannot be made unobservable by a second recording, and neither a
// TMS-ID collision nor a re-run of the bring-up fails the run.
func (b *Backend) recordPending(tms *tokentopology.TMS, ppRaw []byte) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.installs == nil {
		b.installs = map[string]*installTask{}
	}
	if _, exists := b.installs[tms.ID()]; exists {
		logger.Warnf("public params installation for [%s] was already recorded, keeping the first one", tms.ID())

		return
	}
	task := newInstallTask()
	b.installs[tms.ID()] = task
	b.pending = append(b.pending, pendingPublicParams{tms: tms, ppRaw: ppRaw, task: task})
}

// takePending returns the recorded installations and forgets them, so that a second call to
// InstallPendingPublicParams does not install them again.
func (b *Backend) takePending() []pendingPublicParams {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	pending := b.pending
	b.pending = nil

	return pending
}

// installTaskFor returns the task registered for tms, or nil if there is none.
func (b *Backend) installTaskFor(tms *tokentopology.TMS) *installTask {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	return b.installs[tms.ID()]
}

// setupPublicParams waits for the issuer client to become available, then invokes the
// SetupPublicParams view on it. It returns an error if the client is still not available
// after the configured number of attempts, or if the view fails.
func (b *Backend) setupPublicParams(tms *tokentopology.TMS, ppRaw []byte) error {
	retries, delay := b.clientRetries(), b.clientRetryDelay()
	for i := range retries {
		issuer := b.ClientProvider.Client(issuerClientID)
		if issuer != nil {
			return callSetupPublicParamsView(issuer, tms, ppRaw)
		}

		if i < (retries - 1) {
			logger.Infof("public params setup on [%s]...client [%s] not ready, wait a bit...", tms.ID(), issuerClientID)
			time.Sleep(delay)
		}
	}

	return errors.Errorf("client [%s] not ready after %d attempts, cannot set up the public params on [%s]", issuerClientID, retries, tms.ID())
}

func (b *Backend) clientRetries() int {
	if b.ClientRetries > 0 {
		return b.ClientRetries
	}

	return defaultClientRetries
}

func (b *Backend) clientRetryDelay() time.Duration {
	if b.ClientRetryDelay > 0 {
		return b.ClientRetryDelay
	}

	return defaultClientRetryDelay
}

func (b *Backend) installDelay() time.Duration {
	if b.InstallDelay > 0 {
		return b.InstallDelay
	}

	return defaultInstallDelay
}

func (b *Backend) PublicParamsInstallTimeout() time.Duration {
	return b.installDelay() + time.Duration(b.clientRetries())*b.clientRetryDelay() + setupPublicParamsTimeout + 50*time.Second
}

// callSetupPublicParamsView invokes the SetupPublicParams view on the given client.
func callSetupPublicParamsView(issuer api.GRPCClient, tms *tokentopology.TMS, ppRaw []byte) error {
	// marshalling here rather than via common.JSONMarshall, whose failure mode is an
	// assertion that needs a registered gomega fail handler
	input, err := json.Marshal(&ppsetup.SetupPublicParams{
		Network:         tms.Network,
		Channel:         tms.Channel,
		Namespace:       tms.Namespace,
		PublicParamsRaw: ppRaw,
		Timeout:         setupPublicParamsTimeout,
	})
	if err != nil {
		return errors.Wrapf(err, "failed marshalling the public params setup request for [%s]", tms.ID())
	}

	if _, err := issuer.CallView(setupPublicParamsView, input); err != nil {
		return errors.Wrapf(err, "failed setting up the public params on [%s:%s:%s:%s]", tms.Network, tms.Channel, tms.Namespace, tms.Driver)
	}

	return nil
}
