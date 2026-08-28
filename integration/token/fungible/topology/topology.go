/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package topology

import (
	"fmt"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
	fabric2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	"github.com/LFDT-Panurus/panurus/integration/token/common"
	auditor2 "github.com/LFDT-Panurus/panurus/integration/token/fungible/sdk/auditor"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/sdk/endorser"
	issuer2 "github.com/LFDT-Panurus/panurus/integration/token/fungible/sdk/issuer"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/sdk/party"
	endorsementfsc "github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabricx"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/support/libp2p"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/monitoring"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/tracing"
)

func Topology(opts common.Opts) []api.Topology {
	orgs := opts.Orgs
	if len(orgs) == 0 {
		orgs = []string{"Org1", "Org2"}
	}

	backendTopology, backendChannel := setupBackendTopology(opts, orgs)

	// FSC
	fscTopology := fsc.NewTopology()
	fscTopology.P2PCommunicationType = opts.CommType
	fscTopology.WebEnabled = opts.WebEnabled
	if opts.Monitoring {
		fscTopology.EnablePrometheusMetrics()
		fscTopology.EnableTracing(tracing.File)
	}
	fscTopology.SetLogging(token2.RunnerDebug(opts.FSCLogSpec), "")

	auditor, issuer := setupNodes(fscTopology, opts)
	endorserIDs := setupEndorsers(fscTopology, opts, orgs)

	tokenTopology := token.NewTopology()
	tokenTopology.TokenSelector = opts.TokenSelector
	tms := tokenTopology.AddTMS(fscTopology.ListNodes(), backendTopology, backendChannel, opts.DefaultTMSOpts.TokenSDKDriver)
	tms.SetNamespace("token_chaincode")
	common.SetDefaultParams(tms, opts.DefaultTMSOpts)
	if !opts.DefaultTMSOpts.Aries {
		// Enable Fabric-CA
		fabric2.WithFabricCA(tms)
	}
	if opts.FSCBasedEndorsement {
		fabric2.WithFSCEndorsers(tms, endorserIDs...)
		if len(opts.FSCEndorsementPolicyType) > 0 {
			fabric2.WithFSCEndorsementPolicyType(tms, opts.FSCEndorsementPolicyType)
		}
		if len(opts.NamespacePolicy) > 0 {
			fabric2.WithNamespacePolicy(tms, opts.NamespacePolicy)
		}
	}
	if opts.FSCEndorsementPolicyType == endorsementfsc.NamespacePolicy {
		fabric2.SetOrgs(tms, orgs...)
	} else {
		fabric2.SetOrgs(tms, "Org1")
	}
	nodeList := fscTopology.ListNodes()
	fscTopology.SetBootstrapNode(fscTopology.AddNodeByName("lib-p2p-bootstrap-node"))

	if !opts.NoAuditor {
		tms.AddAuditor(auditor)
	}
	tms.AddIssuer(issuer)
	tms.AddIssuerByID("issuer.id1")

	setupSDKs(fscTopology, opts, endorserIDs)
	addExtraTMSs(tokenTopology, opts, nodeList, backendTopology, backendChannel, auditor, issuer)

	if opts.Monitoring {
		monitoringTopology := monitoring.NewTopology()
		// monitoringTopology.EnableHyperledgerExplorer()
		monitoringTopology.EnablePrometheusGrafana()

		return []api.Topology{
			backendTopology, tokenTopology, fscTopology,
			monitoringTopology,
		}
	}

	return []api.Topology{backendTopology, tokenTopology, fscTopology}
}

// setupBackendTopology creates and configures the network backend (Fabric or
// Fabric-X) topology selected by opts.Backend.
func setupBackendTopology(opts common.Opts, orgs []string) (api.Topology, string) {
	switch opts.Backend {
	case "fabric":
		fabricTopology := fabric.NewDefaultTopology()
		fabricTopology.EnableIdemix()
		fabricTopology.AddOrganizationsByName(orgs...)
		fabricTopology.SetNamespaceApproverOrgs(orgs[0])

		return fabricTopology, fabricTopology.Channels[0].Name
	case "fabricx":
		fabricTopology := fabricx.NewDefaultTopology()
		fabricTopology.EnableIdemix()
		fabricTopology.AddOrganizationsByName(orgs...)
		fabricTopology.SetNamespaceApproverOrgs(orgs[0])

		return fabricTopology, fabricTopology.Channels[0].Name
	default:
		panic("unknown backend: " + opts.Backend)
	}
}

// setupNodes creates the standard fungible-token network nodes (issuer, newIssuer,
// auditor, newAuditor, alice, bob, charlie, manager) and returns the auditor and
// issuer nodes, the only two referenced again once the topology is set up.
func setupNodes(fscTopology *fsc.Topology, opts common.Opts) (auditor, issuer *node.Node) {
	issuer = fscTopology.AddNodeByName("issuer").AddOptions(
		fabric.WithOrganization("Org1"),
		fabric.WithAnonymousIdentity(),
		token.WithDefaultIssuerIdentity(opts.HSM),
		token.WithIssuerIdentity("issuer.id1", opts.HSM),
		token.WithDefaultOwnerIdentity(),
		token.WithOwnerIdentity("issuer.owner"),
	)
	if opts.HSM {
		issuer.AddOptions(fabric.WithDefaultIdentityByHSM())
	}
	issuer.AddOptions(opts.ReplicationOpts.For("issuer")...)

	newIssuer := fscTopology.AddNodeByName("newIssuer").AddOptions(
		fabric.WithOrganization("Org1"),
		fabric.WithAnonymousIdentity(),
		token.WithDefaultIssuerIdentity(opts.HSM),
		token.WithIssuerIdentity("newIssuer.id1", opts.HSM),
		token.WithDefaultOwnerIdentity(),
		token.WithOwnerIdentity("newIssuer.owner"),
	)
	newIssuer.AddOptions(opts.ReplicationOpts.For("newIssuer")...)

	if opts.AuditorAsIssuer {
		issuer.AddOptions(
			token.WithAuditorIdentity(opts.HSM),
			fsc.WithAlias("auditor"),
		)
		auditor = issuer
		newIssuer.AddOptions(
			token.WithAuditorIdentity(opts.HSM),
			fsc.WithAlias("auditor"),
		)
	} else {
		auditor = fscTopology.AddNodeByName("auditor").AddOptions(
			fabric.WithOrganization("Org1"),
			fabric.WithAnonymousIdentity(),
			token.WithAuditorIdentity(opts.HSM),
		)
		auditor.AddOptions(opts.ReplicationOpts.For("auditor")...)
	}
	newAuditor := fscTopology.AddNodeByName("newAuditor").AddOptions(
		fabric.WithOrganization("Org1"),
		token.WithAuditorIdentity(opts.HSM),
	)
	newAuditor.AddOptions(opts.ReplicationOpts.For("newAuditor")...)

	alice := fscTopology.AddNodeByName("alice").AddOptions(
		fabric.WithOrganization("Org2"),
		fabric.WithAnonymousIdentity(),
		token.WithOwnerIdentity("alice.id1"),
		token.WithRemoteOwnerIdentity("alice_remote"),
		token.WithRemoteOwnerIdentity("alice_remote_2"),
	)
	alice.AddOptions(opts.ReplicationOpts.For("alice")...)

	bob := fscTopology.AddNodeByName("bob").AddOptions(
		fabric.WithOrganization("Org2"),
		fabric.WithAnonymousIdentity(),
		token.WithDefaultOwnerIdentity(),
		token.WithOwnerIdentity("bob.id1"),
		token.WithRemoteOwnerIdentity("bob_remote"),
	)
	bob.AddOptions(opts.ReplicationOpts.For("bob")...)

	charlie := fscTopology.AddNodeByName("charlie").AddOptions(
		fabric.WithOrganization("Org2"),
		fabric.WithAnonymousIdentity(),
		token.WithDefaultOwnerIdentity(),
		token.WithOwnerIdentity("charlie.id1"),
	)
	charlie.AddOptions(opts.ReplicationOpts.For("charlie")...)

	manager := fscTopology.AddNodeByName("manager").AddOptions(
		fabric.WithOrganization("Org2"),
		fabric.WithAnonymousIdentity(),
		token.WithDefaultOwnerIdentity(),
		token.WithOwnerIdentity("manager.id1"),
		token.WithOwnerIdentity("manager.id2"),
		token.WithOwnerIdentity("manager.id3"),
	)
	manager.AddOptions(opts.ReplicationOpts.For("manager")...)

	return auditor, issuer
}

// setupEndorsers creates the endorser nodes used for FSC-based endorsement: one per
// org under the namespace-policy scheme, or a fixed one-to-three node topology
// otherwise. Returns nil when FSC-based endorsement is disabled.
func setupEndorsers(fscTopology *fsc.Topology, opts common.Opts, orgs []string) []string {
	if !opts.FSCBasedEndorsement {
		return nil
	}

	var endorserIDs []string
	if opts.FSCEndorsementPolicyType == endorsementfsc.NamespacePolicy {
		// one endorser per org, so that the namespace endorsement policy can pick a
		// satisfying subset of endorsers across different MSPs
		for i, org := range orgs {
			endorserTemplate := fscTopology.NewTemplate("endorser")
			endorserTemplate.AddOptions(
				fabric.WithOrganization(org),
				fabric2.WithEndorserRole(),
			)
			endorserID := fmt.Sprintf("endorser-%d", i+1)
			fscTopology.AddNodeFromTemplate(endorserID, endorserTemplate).AddOptions(opts.ReplicationOpts.For(endorserID)...)
			endorserIDs = append(endorserIDs, endorserID)
		}

		return endorserIDs
	}

	endorserTemplate := fscTopology.NewTemplate("endorser")
	endorserTemplate.AddOptions(
		fabric.WithOrganization(orgs[0]),
		fabric2.WithEndorserRole(),
	)
	fscTopology.AddNodeFromTemplate("endorser-1", endorserTemplate).AddOptions(opts.ReplicationOpts.For("endorser-1")...)
	endorserIDs = append(endorserIDs, "endorser-1")
	if opts.Backend != "fabricx" {
		fscTopology.AddNodeFromTemplate("endorser-2", endorserTemplate).AddOptions(opts.ReplicationOpts.For("endorser-2")...)
		fscTopology.AddNodeFromTemplate("endorser-3", endorserTemplate).AddOptions(opts.ReplicationOpts.For("endorser-3")...)
		endorserIDs = append(endorserIDs, "endorser-2", "endorser-3")
	}

	return endorserIDs
}

// setupSDKs wires each configured SDK onto its node group (auditors, issuers,
// parties, and endorsers when FSC-based endorsement is enabled) plus the bootstrap
// node's libp2p SDK, then adds any remaining SDKs to the whole topology. A no-op
// when no SDKs are configured.
func setupSDKs(fscTopology *fsc.Topology, opts common.Opts, endorserIDs []string) {
	if len(opts.SDKs) == 0 {
		return
	}

	// business SDKs
	// auditors
	for _, node := range fscTopology.ListNodes("auditor", "newAuditor") {
		node.AddSDKWithBase(opts.SDKs[0], &auditor2.SDK{})
	}

	// issuers
	for _, node := range fscTopology.ListNodes("issuer", "newIssuer") {
		if opts.AuditorAsIssuer {
			node.AddSDKWithBase(opts.SDKs[0], &issuer2.SDK{}, &auditor2.SDK{})
		} else {
			node.AddSDKWithBase(opts.SDKs[0], &issuer2.SDK{})
		}
	}

	// parties
	for _, node := range fscTopology.ListNodes("alice", "bob", "charlie", "manager") {
		node.AddSDKWithBase(opts.SDKs[0], &party.SDK{})
	}

	// endorsers
	if opts.FSCBasedEndorsement {
		for _, node := range fscTopology.ListNodes(endorserIDs...) {
			node.AddSDKWithBase(opts.SDKs[0], &endorser.SDK{})
		}
	}

	fscTopology.ListNodes("lib-p2p-bootstrap-node")[0].AddSDK(&libp2p.SDK{})

	// add the rest of Panuruss
	for i := 1; i < len(opts.SDKs); i++ {
		fscTopology.AddSDK(opts.SDKs[i])
	}
}

// addExtraTMSs adds one TMS per entry in opts.ExtraTMSs, each sharing the base
// backend and node list but on its own transient namespace alias.
func addExtraTMSs(tokenTopology *token.Topology, opts common.Opts, nodeList []*node.Node, backendTopology api.Topology, backendChannel string, auditor, issuer *node.Node) {
	for _, tmsOpts := range opts.ExtraTMSs {
		tms := tokenTopology.AddTMS(nodeList, backendTopology, backendChannel, tmsOpts.TokenSDKDriver)
		tms.Alias = tmsOpts.Alias
		tms.Namespace = "token_chaincode"
		tms.Transient = true
		if tmsOpts.Aries {
			zkatdlognoghv1.WithAries(tms)
		}
		if tmsOpts.CSP {
			zkatdlognoghv1.WithCSP(tms)
		}
		tms.SetTokenGenPublicParams(tmsOpts.PublicParamsGenArgs...)
		if !opts.NoAuditor {
			tms.AddAuditor(auditor)
		}
		tms.AddIssuer(issuer)
		tms.AddIssuerByID("issuer.id1")
	}
}
