/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark

import (
	"strconv"

	integration2 "github.com/LFDT-Panurus/panurus/integration"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/fabtokenv1"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatsnarkv1"
	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	"github.com/LFDT-Panurus/panurus/integration/token/common"
	"github.com/LFDT-Panurus/panurus/integration/token/common/sdk/fzkatsnark"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/topology"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabricx"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/node"
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
)

var _ = Describe("EndToEnd", func() {
	for _, t := range integration2.LibP2PNoReplicationOnly {
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

		// Without Aries, the owner identities are issued by the token platform's idemix CA (BN254).
		Describe("Fungible with Auditor ne Issuer + Fabric CA", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, WithEndorsers, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("succeeded", Label("T8"), func() { fungible.TestAll(ts.II, "auditor", nil, false, selector) })
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
			It("OR and AND succeeded", Label("T14"), func() {
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

		// fabtoken 64 bits -> ZKAT-SNARK 32 bits: the fabtoken tokens are not spendable in place and
		// must be upgraded through the issuer (burn and re-issue).
		Describe("Tokens Upgrade with Auditor ne Issuer", t.Label, func() {
			ts, selector := newUpgradeTestSuite(t.CommType, 64, t.ReplicationFactor, "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("succeeded", Label("T17"), func() {
				fungible.TestTokensUpgradeWithAlias(ts.II, "auditor", nil, selector, upgradeTMSAlias)
			})
		})

		// fabtoken 32 bits -> ZKAT-SNARK 32 bits: the fabtoken tokens are spendable in place.
		Describe("Tokens Local Upgrade with Auditor ne Issuer", t.Label, func() {
			ts, selector := newUpgradeTestSuite(t.CommType, 32, t.ReplicationFactor, "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("succeeded", Label("T18"), func() {
				fungible.TestLocalTokensUpgradeWithAlias(ts.II, "auditor", nil, selector, upgradeTMSAlias)
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

// upgradeTMSAlias is the alias of the transient ZKAT-SNARK TMS the upgrade scenarios switch to.
const upgradeTMSAlias = "zkatsnark-32bits"

// publicParamsInstaller installs the public parameters that the token platform recorded while the
// network was starting.
type publicParamsInstaller interface {
	InstallPendingPublicParams()
}

// testSuite starts the network as the embedded suite does, and then installs the public parameters
// of the fabricx backend, which requires the FSC nodes to be up.
type testSuite struct {
	*integration.TestSuite
	installer publicParamsInstaller
}

// Setup starts the network and installs the public parameters.
func (s *testSuite) Setup() {
	s.TestSuite.Setup()
	s.installer.InstallPendingPublicParams()
}

// newTestSuite returns a suite running a ZKAT-SNARK network on fabricx, configured by mask.
func newTestSuite(commType fsc.P2PCommunicationType, mask int, factor int, tokenSelector string, names ...string) (*testSuite, *token2.ReplicaSelector) {
	opts, selector := token2.NewReplicationOptions(factor, names...)
	tmsOpts := common.Opts{
		Backend:  fabricx.PlatformName, // select fabricx platform for NWO
		CommType: commType,
		DefaultTMSOpts: common.TMSOpts{
			TokenSDKDriver: zkatsnarkv1.DriverIdentifier,
			Aries:          mask&Aries > 0,
		},
		NoAuditor:           mask&NoAuditor > 0,
		AuditorAsIssuer:     mask&AuditorAsIssuer > 0,
		HSM:                 mask&HSM > 0,
		WebEnabled:          mask&WebEnabled > 0,
		SDKs:                []node.SDK{&fzkatsnark.SDK{}}, // add fabricx SDK
		Monitoring:          false,
		ReplicationOpts:     opts,
		FSCBasedEndorsement: mask&WithEndorsers > 0,
		FSCLogSpec:          "info",
		TokenSelector:       tokenSelector,
	}

	return newSuite(tmsOpts), selector
}

// newUpgradeTestSuite returns a suite running on fabricx a network that starts with fabtoken public
// parameters of the passed precision, plus a transient 32-bit ZKAT-SNARK TMS (alias upgradeTMSAlias)
// whose public parameters the upgrade tests install later.
func newUpgradeTestSuite(commType fsc.P2PCommunicationType, fabtokenPrecision int, factor int, names ...string) (*testSuite, *token2.ReplicaSelector) {
	opts, selector := token2.NewReplicationOptions(factor, names...)
	tmsOpts := common.Opts{
		Backend:  fabricx.PlatformName, // select fabricx platform for NWO
		CommType: commType,
		DefaultTMSOpts: common.TMSOpts{
			TokenSDKDriver:      fabtokenv1.DriverIdentifier,
			PublicParamsGenArgs: []string{strconv.Itoa(fabtokenPrecision)},
		},
		ExtraTMSs: []common.TMSOpts{
			{
				Alias:               upgradeTMSAlias,
				TokenSDKDriver:      zkatsnarkv1.DriverIdentifier,
				PublicParamsGenArgs: []string{"32"},
				Aries:               true,
			},
		},
		SDKs:                []node.SDK{&fzkatsnark.SDK{}}, // fabricx SDK with the ZKAT-SNARK and fabtoken drivers
		ReplicationOpts:     opts,
		FSCBasedEndorsement: true,
		FSCLogSpec:          "info",
	}

	return newSuite(tmsOpts), selector
}

// newSuite returns a suite whose infrastructure runs the topology described by tmsOpts on fabricx.
func newSuite(tmsOpts common.Opts) *testSuite {
	ts := &testSuite{}
	ts.TestSuite = integration.NewTestSuite(func() (*integration.Infrastructure, error) {
		i, err := integration.New(StartPortZkatsnark(), "./testdata", topology.Topology(tmsOpts)...)
		i.DeleteOnStart = true
		i.DeleteOnStop = false
		if integration.WithRaceDetection {
			i.EnableRaceDetector()
		}
		i.RegisterPlatformFactory(fabricx.NewPlatformFactory())
		tokenPlatformFactory := token.NewPlatformFactory(i)
		i.RegisterPlatformFactory(tokenPlatformFactory)
		i.Generate()
		ts.installer = tokenPlatformFactory

		return i, err
	})

	return ts
}
