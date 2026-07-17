/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dlogx

import (
	integration2 "github.com/LFDT-Panurus/panurus/integration"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	"github.com/LFDT-Panurus/panurus/integration/token/common"
	"github.com/LFDT-Panurus/panurus/integration/token/common/sdk/fxdlog"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/topology"
	endorsementfsc "github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabricx"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/node"
	. "github.com/onsi/ginkgo/v2"
)

// namespacePolicy2of3 requires signatures from 2 out of the 3 orgs (Org1, Org2, Org3) to
// endorse the token namespace, exercising the "namespace" FSC endorsement policy type.
const namespacePolicy2of3 = "OutOf(2, 'Org1MSP.member', 'Org2MSP.member', 'Org3MSP.member')"

var namespacePolicyOrgs = []string{"Org1", "Org2", "Org3"}

const None = 0
const (
	Aries = 1 << iota
	AuditorAsIssuer
	NoAuditor
	HSM
	WebEnabled
	WithEndorsers
	WithNamespacePolicy
)

var _ = Describe("EndToEnd", func() {

	for _, t := range integration2.AllTestTypes {
		Describe("T1 Fungible with Auditor ne Issuer and Endorsers", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries|WithEndorsers, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("succeeded", Label("T1"), func() {
				fungible.TestAll(ts.II, "auditor", nil, true, selector)
			})
		})

		Describe("Extras with websockets", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries|WithEndorsers|WebEnabled, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("Update public params (new auditor and issuer)", Label("T2"), func() {
				fungible.TestPublicParamsUpdate(
					ts.II,
					"newAuditor",
					fungible.PrepareUpdatedPublicParams(ts.II, "newAuditor", "newIssuer", "default", false),
					"default",
					false,
					selector,
					false,
				)
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
				)
			})
			It("Test Identity Revocation", Label("T3"), func() { fungible.TestRevokeIdentity(ts.II, "auditor", selector) })
			It("Test Remote Wallet (GRPC)", Label("T4"), func() { fungible.TestRemoteOwnerWallet(ts.II, "auditor", selector, false) })
			It("Test Remote Wallet (WebSocket)", Label("T5"), func() { fungible.TestRemoteOwnerWallet(ts.II, "auditor", selector, true) })
		})

		Describe("Fungible with Auditor = Issuer", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries|WithEndorsers|AuditorAsIssuer, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
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
				)
			})
		})

		Describe("Malicious Transactions", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries|WithEndorsers|NoAuditor, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("Malicious Transactions", Label("T9"), func() { fungible.TestMaliciousTransactions(ts.II, selector) })
		})

		Describe("Multisig", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries|WithEndorsers, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("succeeded", Label("T12"), func() { fungible.TestMultiSig(ts.II, selector) })
		})

		Describe("PolicyIdentity", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries|WithEndorsers, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("OR and AND succeeded", Label("T14", "T15"), func() {
				fungible.TestPolicyOR(ts.II, selector)
				fungible.TestPolicyAND(ts.II, selector)
			})
		})

		Describe("Redeem to yourself", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries|WithEndorsers, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("Test redeem", Label("T13"), func() { fungible.TestRedeem(ts.II, selector, "default") })
		})

		Describe("T16 Fungible with a 2-of-3 namespace endorsement policy", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries|WithEndorsers|WithNamespacePolicy, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("succeeded", Label("T16"), func() {
				fungible.TestAll(ts.II, "auditor", nil, true, selector)
			})
		})
	}

	for _, tokenSelector := range integration2.TokenSelectors {
		Describe("TokenSelector Test", integration2.WebSocketNoReplication.Label, Label(tokenSelector), func() {
			ts, replicaSelector := newTestSuite(integration2.WebSocketNoReplication.CommType, Aries|WithEndorsers, integration2.WebSocketNoReplication.ReplicationFactor, tokenSelector, "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("succeeded", Label("T11"), func() { fungible.TestSelector(ts.II, "auditor", replicaSelector) })
		})
	}
})

func newTestSuite(commType fsc.P2PCommunicationType, mask int, factor int, tokenSelector string, names ...string) (*integration.TestSuite, *token2.ReplicaSelector) {
	opts, selector := token2.NewReplicationOptions(factor, names...)
	tmsOpts := common.Opts{
		Backend:  fabricx.PlatformName, // select fabricx platform for NWO
		CommType: commType,
		DefaultTMSOpts: common.TMSOpts{
			TokenSDKDriver: zkatdlognoghv1.DriverIdentifier,
			Aries:          mask&Aries > 0,
		},
		NoAuditor:           mask&NoAuditor > 0,
		AuditorAsIssuer:     mask&AuditorAsIssuer > 0,
		HSM:                 mask&HSM > 0,
		WebEnabled:          mask&WebEnabled > 0,
		SDKs:                []node.SDK{&fxdlog.SDK{}}, // add fabricx SDK
		Monitoring:          false,
		ReplicationOpts:     opts,
		FSCBasedEndorsement: mask&WithEndorsers > 0,
		FSCLogSpec:          "info",
		TokenSelector:       tokenSelector,
	}
	if mask&WithNamespacePolicy > 0 {
		tmsOpts.Orgs = namespacePolicyOrgs
		tmsOpts.FSCEndorsementPolicyType = endorsementfsc.NamespacePolicy
		tmsOpts.NamespacePolicy = namespacePolicy2of3
	}

	ts := integration.NewTestSuite(func() (*integration.Infrastructure, error) {
		i, err := integration.New(StartPortDlog(), "./testdata", topology.Topology(tmsOpts)...)
		i.DeleteOnStart = true
		i.DeleteOnStop = false
		if integration.WithRaceDetection {
			i.EnableRaceDetector()
		}
		i.RegisterPlatformFactory(fabricx.NewPlatformFactory())
		i.RegisterPlatformFactory(token.NewPlatformFactory(i))
		i.Generate()

		return i, err
	})

	return ts, selector
}
