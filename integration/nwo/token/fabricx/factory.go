/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabricx

import (
	"fmt"
	"time"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric"
	tokentopology "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/views/ppsetup"
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
		panic(fmt.Sprintf("unknown backend network type %T", n))
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

func (b *Backend) UpdatePublicParams(tms *tokentopology.TMS, ppRaw []byte) {
	endorsers := fabric.Endorsers(tms)
	if len(endorsers) == 0 {
		panic("no endorsers found")
	}
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
