/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabricx

import (
	"time"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric"
	tokentopology "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/views/ppsetup"
	"github.com/bytedance/gopkg/util/logger"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	common2 "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common"
	fabrictopology "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric/topology"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabricx"
	"github.com/onsi/gomega"
)

type ClientProvider interface {
	Client(string) api.GRPCClient
}

type Backend struct {
	ClientProvider ClientProvider

	// pending collects the public parameters to be installed once the FSC nodes are up.
	pending []pendingPublicParams
}

// pendingPublicParams holds the public parameters of a TMS whose installation has been deferred.
type pendingPublicParams struct {
	tms   *tokentopology.TMS
	ppRaw []byte
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
func (b *Backend) InstallPublicParams(tms *tokentopology.TMS, ppRaw []byte) {
	// give the backend network time to settle before the FSC nodes are started
	time.Sleep(10 * time.Second)

	b.pending = append(b.pending, pendingPublicParams{tms: tms, ppRaw: ppRaw})
}

// InstallPendingPublicParams installs the public parameters recorded by InstallPublicParams.
// It must be called from the goroutine that started the network, once all FSC nodes are up.
func (b *Backend) InstallPendingPublicParams() {
	pending := b.pending
	b.pending = nil

	for _, p := range pending {
		logger.Infof("installing public params on [%s:%s:%s:%s]...", p.tms.Network, p.tms.Channel, p.tms.Namespace, p.tms.Driver)
		gomega.Expect(b.ClientProvider.Client("issuer")).ToNot(gomega.BeNil(), "no view client for the issuer, is the network started?")
		b.UpdatePublicParams(p.tms, p.ppRaw)
		logger.Infof("installing public params on [%s:%s:%s:%s]...done", p.tms.Network, p.tms.Channel, p.tms.Namespace, p.tms.Driver)
	}
}

func (b *Backend) UpdatePublicParams(tms *tokentopology.TMS, ppRaw []byte) {
	_, err := b.ClientProvider.Client("issuer").CallView("SetupPublicParams", common2.JSONMarshall(
		&ppsetup.SetupPublicParams{
			Network:         tms.Network,
			Channel:         tms.Channel,
			Namespace:       tms.Namespace,
			PublicParamsRaw: ppRaw,
			Timeout:         2 * time.Minute,
		},
	))
	if err != nil {
		panic("failed updating pps: " + err.Error())
	}
}
