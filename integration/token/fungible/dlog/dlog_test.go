/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dlog

import (
	integration2 "github.com/LFDT-Panurus/panurus/integration"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	"github.com/LFDT-Panurus/panurus/integration/token/common"
	"github.com/LFDT-Panurus/panurus/integration/token/common/sdk/fdlog"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/topology"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	nodepkg "github.com/hyperledger-labs/fabric-smart-client/pkg/node"
	. "github.com/onsi/ginkgo/v2"
)

const None = 0
const (
	Aries = 1 << iota
	AuditorAsIssuer
	NoAuditor
	HSM
	WebEnabled
	WithEndorsers
	OwnOrgEndorsers
)

// namespacePolicyEitherOrg lets the peers of either organization endorse the token
// namespace on their own: a node that selects only its own organization needs a policy
// that organization can satisfy, which the default unanimity is not.
const namespacePolicyEitherOrg = "OR ('Org1MSP.member','Org2MSP.member')"

var _ = Describe("EndToEnd", func() {
	for _, t := range integration2.AllTestTypes {
		// TestAll and the small specs that need the same topology share one network, see
		// token2.ShareNetwork. The order is what makes this safe:
		//   - TestAll (T1) runs first, on a fresh network, so its assertions on the whole
		//     transaction history and on the issuer's 241 USD quota hold unchanged; it
		//     checks that it is first.
		//   - Every later spec issues and moves its own token type (WithTokenType), so the
		//     balances TestAll leaves behind are invisible to it, and it does not count
		//     against the USD quota, which applies to the exact type "USD" only. T4 and T5
		//     use different types because both assert absolute balances of the same wallets.
		//   - T17 authorizes the issuer for redeem, as TestAll already did (a KVS
		//     overwrite, so it changes nothing), and also newIssuer, which no later spec
		//     redeems with. It runs before T2.1 and T2 because it relies on newIssuer not
		//     being in the public parameters, which T2.1 and T2 add.
		//   - T3 revokes bob for good, so it runs after T4 and T5, which move tokens to bob.
		//   - T2.1 and T2 change the public parameters, so they run last. T2.1 rebuilds
		//     from the genesis parameters, so it drops TestAll's issuer.ExtraId, which no
		//     later spec uses.
		// Aries alone and Aries|WebEnabled give the same network but for the FSC web server
		// that T5 needs; the other specs still talk to the nodes over gRPC. Each label still
		// runs on its own network when filtered alone.
		Describe("T1 Fungible with Auditor ne Issuer and extras", Ordered, ContinueOnFailure, t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries|WebEnabled, t.ReplicationFactor, "", "alice", "bob", "charlie")
			token2.ShareNetwork(ts)
			It("succeeded", Label("T1"), func() { fungible.TestAll(ts.II, "auditor", nil, true, selector) })
			It("Test redeem rejected with unauthorized issuer", Label("T17"), func() {
				fungible.TestRedeemUnauthorizedIssuer(ts.II, selector, "default", fungible.WithTokenType("USDt17"))
			})
			It("Test Remote Wallet (GRPC)", Label("T4"), func() {
				fungible.TestRemoteOwnerWallet(ts.II, "auditor", selector, false, fungible.WithTokenType("USDt4"))
			})
			It("Test Remote Wallet (WebSocket)", Label("T5"), func() {
				fungible.TestRemoteOwnerWallet(ts.II, "auditor", selector, true, fungible.WithTokenType("USDt5"))
			})
			It("Test Identity Revocation", Label("T3"), func() {
				fungible.TestRevokeIdentity(ts.II, "auditor", selector, fungible.WithTokenType("USDt3"))
			})
			It("Update public params (append new auditor and issuer)", Label("T2.1"), func() {
				fungible.TestPublicParamsUpdate(
					ts.II,
					"newAuditor",
					fungible.PrepareUpdatedPublicParams(ts.II, "newAuditor", "newIssuer", "default", true),
					"default",
					false,
					selector,
					true,
					fungible.WithTokenType("USDt21"),
				)
			})
			It("Update public params (new auditor and issuer)", Label("T2"), func() {
				fungible.TestPublicParamsUpdate(
					ts.II,
					"newAuditor",
					fungible.PrepareUpdatedPublicParams(ts.II, "newAuditor", "newIssuer", "default", false),
					"default",
					false,
					selector,
					false,
					fungible.WithTokenType("USDt2"),
				)
			})
		})

		// TestAll (T6) and the public parameters update (T7) share one network, see
		// token2.ShareNetwork: TestAll runs first, on a fresh network, and T7 uses its own
		// token type, so TestAll's balances and the USD quota do not affect it, and it
		// changes the public parameters, so it runs last. T7 rebuilds from the genesis
		// parameters, so it drops TestAll's issuer.ExtraId, which no later spec uses.
		Describe("Fungible with Auditor = Issuer", Ordered, ContinueOnFailure, t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries|AuditorAsIssuer, t.ReplicationFactor, "", "alice", "bob", "charlie")
			token2.ShareNetwork(ts)
			It("succeeded", Label("T6"), func() { fungible.TestAll(ts.II, "issuer", nil, true, selector) })
			It("Update public params", Label("T7"), func() {
				fungible.TestPublicParamsUpdate(
					ts.II,
					"newIssuer",
					fungible.PrepareUpdatedPublicParams(ts.II, "newIssuer", "newIssuer", "default", false),
					"default",
					true,
					selector,
					false,
					fungible.WithTokenType("USDt7"),
				)
			})
		})

		Describe("Fungible with Auditor ne Issuer + Fabric CA", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, None, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("succeeded", Label("T8"), func() { fungible.TestAll(ts.II, "auditor", nil, false, selector) })
		})

		Describe("Malicious Transactions", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries|NoAuditor, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("Malicious Transactions", Label("T9"), func() { fungible.TestMaliciousTransactions(ts.II, selector) })
		})

		Describe("Fungible with Auditor ne Issuer and Endorsers", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries|WithEndorsers, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("succeeded", Label("T10"), func() { fungible.TestAll(ts.II, "auditor", nil, true, selector) })
		})

		// Multisig and Redeem to yourself share one network, see token2.ShareNetwork. Both
		// leave the network healthy, and their assertions do not overlap: Multisig moves
		// USD among alice, bob, charlie and manager, Redeem only among the issuer's own
		// owner wallet, and together they issue 220 USD, within the issuer's 241 USD quota.
		// Redeem expects the auditor not to have authorized any redeem issuer yet, which
		// Multisig does not do. Each label still runs on its own network when filtered alone.
		Describe("Multisig and Redeem to yourself", Ordered, ContinueOnFailure, t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries, t.ReplicationFactor, "", "alice", "bob", "charlie")
			token2.ShareNetwork(ts)
			It("Multisig succeeded", Label("T12"), func() { fungible.TestMultiSig(ts.II, selector) })
			It("Test redeem", Label("T13"), func() { fungible.TestRedeem(ts.II, selector, "default") })
		})

		Describe("PolicyIdentity", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("OR and AND succeeded", Label("T14"), func() {
				fungible.TestPolicyOR(ts.II, selector)
				fungible.TestPolicyAND(ts.II, selector)
			})
		})
	}

	// Endorser selection is orthogonal to the P2P communication type, and TestAll is the
	// most expensive spec in this suite, so this runs on one infrastructure type rather
	// than the loop over all of them.
	Describe("Fungible with endorsement pinned to each node's own organization", integration2.WebSocketNoReplication.Label, func() {
		ts, selector := newTestSuite(integration2.WebSocketNoReplication.CommType, Aries|OwnOrgEndorsers, integration2.WebSocketNoReplication.ReplicationFactor, "", "alice", "bob", "charlie")
		BeforeEach(ts.Setup)
		AfterEach(ts.TearDown)
		It("succeeded", Label("T15"), func() {
			// The selection is resolved per node: issuer is an Org1 node, alice an Org2
			// one. Asserting it here, rather than only running the flow, is what shows the
			// selection narrows the endorsers — under namespacePolicyEitherOrg the flow
			// succeeds whether or not it is honoured.
			fungible.CheckEndorserSelection(ts.II, []string{"Org1MSP"}, []string{"Org2MSP"}, selector.Get("issuer"))
			fungible.CheckEndorserSelection(ts.II, []string{"Org2MSP"}, []string{"Org1MSP"}, selector.Get("alice"))
			fungible.TestAll(ts.II, "auditor", nil, true, selector)
		})
	})

	for _, tokenSelector := range integration2.TokenSelectors {
		Describe("TokenSelector Test", integration2.WebSocketNoReplication.Label, Label(tokenSelector), func() {
			ts, replicaSelector := newTestSuite(integration2.WebSocketNoReplication.CommType, Aries, integration2.WebSocketNoReplication.ReplicationFactor, tokenSelector, "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("succeeded", Label("T11"), func() { fungible.TestSelector(ts.II, "auditor", replicaSelector) })
		})
	}
})

func newTestSuite(commType fsc.P2PCommunicationType, mask int, factor int, tokenSelector string, names ...string) (*token2.TestSuite, *token2.ReplicaSelector) {
	opts, selector := token2.NewReplicationOptions(factor, names...)
	tmsOpts := common.Opts{
		Backend:             "fabric",
		CommType:            commType,
		DefaultTMSOpts:      common.TMSOpts{TokenSDKDriver: zkatdlognoghv1.DriverIdentifier, Aries: mask&Aries > 0},
		NoAuditor:           mask&NoAuditor > 0,
		AuditorAsIssuer:     mask&AuditorAsIssuer > 0,
		HSM:                 mask&HSM > 0,
		WebEnabled:          mask&WebEnabled > 0,
		SDKs:                []nodepkg.SDK{&fdlog.SDK{}},
		Monitoring:          false,
		ReplicationOpts:     opts,
		FSCBasedEndorsement: mask&WithEndorsers > 0,
		FSCLogSpec:          "info",
		TokenSelector:       tokenSelector,
	}
	if mask&OwnOrgEndorsers > 0 {
		// Both organizations host the token chaincode, so that restricting a node to its
		// own organization actually narrows the set of endorsers discovery would return.
		tmsOpts.NamespacePolicy = namespacePolicyEitherOrg
		tmsOpts.EndorsersFromOwnOrg = true
	}
	ts := token2.NewTestSuite(StartPortDlog, topology.Topology(tmsOpts))

	return ts, selector
}
