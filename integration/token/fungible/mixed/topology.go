/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package mixed

import (
	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
	fabric2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric"
	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	"github.com/LFDT-Panurus/panurus/integration/token/common"
	auditor3 "github.com/LFDT-Panurus/panurus/integration/token/fungible/sdk/auditor"
	issuer3 "github.com/LFDT-Panurus/panurus/integration/token/fungible/sdk/issuer"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/sdk/party"
	"github.com/LFDT-Panurus/panurus/token/core"
	fabtokenv1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	dlognoghv1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/support/libp2p"
)

var (
	DLogDriver     = string(core.DriverIdentifier(dlognoghv1.DLogNoGHDriverName, dlognoghv1.ProtocolV1))
	FabtokenDriver = string(core.DriverIdentifier(fabtokenv1.FabTokenDriverName, fabtokenv1.ProtocolV1))
)

const (
	// DLogNamespace is the default name for the namespace where the token chaincode is deployed when using the dlog driver
	// #nosec G101 no passwords here
	DLogNamespace = "dlog_token_chaincode"
	// FabTokenNamespace is the default name for the namespace where the token chaincode is deployed when using the fabtoken driver
	// #nosec G101  no passwords here
	FabTokenNamespace = "fabtoken_token_chaincode"
)

func Topology(opts common.Opts) []api.Topology {
	fabricTopology := fabric.NewDefaultTopology()
	fabricTopology.EnableIdemix()
	fabricTopology.AddOrganizationsByName("Org1", "Org2", "Org3")
	fabricTopology.SetNamespaceApproverOrgsOR("Org1", "Org2")
	backendNetwork := fabricTopology
	backendChannel := fabricTopology.Channels[0].Name

	// FSC
	fscTopology := fsc.NewTopology()
	fscTopology.P2PCommunicationType = opts.CommType
	fscTopology.SetLogging(token2.RunnerDebug(opts.FSCLogSpec), "")

	issuer := fscTopology.NewTemplate("issuer")
	issuer1 := fscTopology.AddNodeFromTemplate("issuer1", issuer).
		AddOptions(
			fabric.WithOrganization("Org1"),
			fabric.WithAnonymousIdentity(),
			token.WithDefaultIssuerIdentity(false),
		).
		AddOptions(opts.ReplicationOpts.For("issuer1")...)
	issuer2 := fscTopology.AddNodeFromTemplate("issuer2", issuer).
		AddOptions(
			fabric.WithOrganization("Org2"),
			fabric.WithAnonymousIdentity(),
			token.WithDefaultIssuerIdentity(false),
		).
		AddOptions(opts.ReplicationOpts.For("issuer2")...)

	auditor := fscTopology.NewTemplate("auditor")
	auditor1 := fscTopology.AddNodeFromTemplate("auditor1", auditor).
		AddOptions(
			fabric.WithOrganization("Org1"),
			fabric.WithAnonymousIdentity(),
			token.WithAuditorIdentity(false),
		).
		AddOptions(opts.ReplicationOpts.For("auditor1")...)
	auditor2 := fscTopology.AddNodeFromTemplate("auditor2", auditor).
		AddOptions(
			fabric.WithOrganization("Org2"),
			fabric.WithAnonymousIdentity(),
			token.WithAuditorIdentity(false),
		).
		AddOptions(opts.ReplicationOpts.For("auditor2")...)

	alice := fscTopology.AddNodeByName("alice").AddOptions(
		fabric.WithOrganization("Org1"),
		fabric.WithAnonymousIdentity(),
		token.WithOwnerIdentity("alice"),
	).AddOptions(opts.ReplicationOpts.For("alice")...)

	bob := fscTopology.AddNodeByName("bob").AddOptions(
		fabric.WithOrganization("Org2"),
		fabric.WithAnonymousIdentity(),
		token.WithOwnerIdentity("bob"),
	).AddOptions(opts.ReplicationOpts.For("bob")...)

	// Token topology
	tokenTopology := token.NewTopology()

	// we have two TMS, one with the dlog driver and one with the fabtoken driver
	dlogTms := tokenTopology.AddTMS([]*node.Node{issuer1, auditor1, alice, bob}, backendNetwork, backendChannel, DLogDriver)
	dlogTms.SetNamespace(DLogNamespace)
	// max token value is 2^16 - 1 = 65535
	dlogTms.SetTokenGenPublicParams("16")
	fabric2.SetOrgs(dlogTms, "Org1")

	fabTokenTms := tokenTopology.AddTMS([]*node.Node{issuer2, auditor2, alice, bob}, backendNetwork, backendChannel, FabtokenDriver)
	fabTokenTms.SetNamespace(FabTokenNamespace)
	fabTokenTms.SetTokenGenPublicParams("16")
	fabric2.SetOrgs(fabTokenTms, "Org2")

	dlogTms.AddAuditor(auditor1)
	dlogTms.AddIssuer(issuer1)
	fabTokenTms.AddAuditor(auditor2)
	fabTokenTms.AddIssuer(issuer2)

	// FSC topology
	fscTopology.SetBootstrapNode(fscTopology.AddNodeByName("lib-p2p-bootstrap-node"))

	// set Panuruss
	// auditors
	for _, node := range fscTopology.ListNodes("auditor1", "auditor2") {
		node.AddSDKWithBase(opts.SDKs[0], &auditor3.SDK{})
	}

	// issuers
	for _, node := range fscTopology.ListNodes("issuer1", "issuer2") {
		node.AddSDKWithBase(opts.SDKs[0], &issuer3.SDK{})
	}

	// parties
	for _, node := range fscTopology.ListNodes("alice", "bob") {
		node.AddSDKWithBase(opts.SDKs[0], &party.SDK{})
	}

	// additional nodes that are backend specific
	fscTopology.ListNodes("lib-p2p-bootstrap-node")[0].AddSDK(&libp2p.SDK{})

	// add the rest of Panuruss
	for i := 1; i < len(opts.SDKs); i++ {
		fscTopology.AddSDK(opts.SDKs[i])
	}

	return []api.Topology{backendNetwork, tokenTopology, fscTopology}
}
