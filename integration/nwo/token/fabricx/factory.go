/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabricx

import (
	"fmt"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric"
	tokentopology "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/views/fabricx/tmsdeploy"
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

		n.AddNamespaceWithUnanimity(tms.Namespace, orgs...)
	case *fabricx.Topology:
		orgs := fabric.GetOrgs(tms)
		gomega.Expect(orgs).ToNot(gomega.BeEmpty(), "missing orgs for tms [%s:%s:%s:%s:%s]", tms.Network, tms.Channel, tms.Namespace, tms.Driver, tms.Alias)

		n.AddNamespaceWithUnanimity(tms.Namespace, orgs...)
	default:
		panic(fmt.Sprintf("unknown backend network type %T", n))
	}
}

func (b *Backend) UpdatePublicParams(tms *tokentopology.TMS, ppRaw []byte) {
	endorsers := fabric.Endorsers(tms)
	if len(endorsers) == 0 {
		panic("no endorsers found")
	}
	_, err := b.ClientProvider.Client(endorsers[0]).CallView("TMSDeploy", common2.JSONMarshall(
		&tmsdeploy.Deploy{
			Network:         tms.Network,
			Channel:         tms.Channel,
			Namespace:       tms.Namespace,
			PublicParamsRaw: ppRaw,
		},
	))
	if err != nil {
		panic("failed updating pps: " + err.Error())
	}
}
