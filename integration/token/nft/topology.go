/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nft

import (
	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
	fabric2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric"
	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	"github.com/LFDT-Panurus/panurus/integration/token/common"
	views2 "github.com/LFDT-Panurus/panurus/integration/token/common/views"
	"github.com/LFDT-Panurus/panurus/integration/token/nft/views"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
)

func Topology(opts common.Opts) []api.Topology {
	if opts.Backend != "fabric" {
		panic("unknown backend: " + opts.Backend)
	}

	fabricTopology := fabric.NewDefaultTopology()
	fabricTopology.EnableIdemix()
	fabricTopology.AddOrganizationsByName("Org1", "Org2")
	fabricTopology.SetNamespaceApproverOrgs("Org1")
	backendChannel := fabricTopology.Channels[0].Name

	// FSC
	fscTopology := fsc.NewTopology()
	fscTopology.SetLogging(token2.RunnerDebug(opts.FSCLogSpec), "")
	fscTopology.P2PCommunicationType = opts.CommType

	fscTopology.AddNodeByName("issuer").
		AddOptions(
			fabric.WithOrganization("Org1"),
			fabric.WithAnonymousIdentity(),
			token.WithDefaultIssuerIdentity(false),
		).
		AddOptions(opts.ReplicationOpts.For("issuer")...).
		RegisterViewFactory("issue", &views.IssueHouseViewFactory{}).
		RegisterViewFactory("TxFinality", &views2.TxFinalityViewFactory{})

	auditor := fscTopology.AddNodeByName("auditor").
		AddOptions(
			fabric.WithOrganization("Org1"),
			fabric.WithAnonymousIdentity(),
			token.WithAuditorIdentity(false),
		).
		AddOptions(opts.ReplicationOpts.For("auditor")...).
		RegisterViewFactory("registerAuditor", &views.RegisterAuditorViewFactory{}).
		RegisterViewFactory("TxFinality", &views2.TxFinalityViewFactory{})

	fscTopology.AddNodeByName("alice").AddOptions(
		fabric.WithOrganization("Org2"),
		fabric.WithAnonymousIdentity(),
		token.WithOwnerIdentity("alice.id1"),
	).
		AddOptions(opts.ReplicationOpts.For("alice")...).
		RegisterResponder(&views.AcceptIssuedHouseView{}, &views.IssueHouseView{}).
		RegisterResponder(&views.AcceptTransferHouseView{}, &views.TransferHouseView{}).
		RegisterViewFactory("transfer", &views.TransferHouseViewFactory{}).
		RegisterViewFactory("queryHouse", &views.GetHouseViewFactory{}).
		RegisterViewFactory("TxFinality", &views2.TxFinalityViewFactory{})

	fscTopology.AddNodeByName("bob").
		AddOptions(
			fabric.WithOrganization("Org2"),
			fabric.WithAnonymousIdentity(),
			token.WithDefaultOwnerIdentity(),
			token.WithOwnerIdentity("bob.id1"),
		).
		AddOptions(opts.ReplicationOpts.For("bob")...).
		RegisterResponder(&views.AcceptIssuedHouseView{}, &views.IssueHouseView{}).
		RegisterResponder(&views.AcceptTransferHouseView{}, &views.TransferHouseView{}).
		RegisterViewFactory("transfer", &views.TransferHouseViewFactory{}).
		RegisterViewFactory("queryHouse", &views.GetHouseViewFactory{}).
		RegisterViewFactory("TxFinality", &views2.TxFinalityViewFactory{})

	tokenTopology := token.NewTopology()
	tms := tokenTopology.AddTMS(fscTopology.ListNodes(), fabricTopology, backendChannel, opts.DefaultTMSOpts.TokenSDKDriver)
	common.SetDefaultParams(tms, opts.DefaultTMSOpts)
	fabric2.SetOrgs(tms, "Org1")

	tms.AddAuditor(auditor)
	tms.AddIssuerByID("issuer")

	for _, sdk := range opts.SDKs {
		fscTopology.AddSDK(sdk)
	}

	return []api.Topology{fabricTopology, tokenTopology, fscTopology}
}
