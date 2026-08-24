/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evmgwfabtoken

import (
	integration2 "github.com/LFDT-Panurus/panurus/integration"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
	tevm "github.com/LFDT-Panurus/panurus/integration/nwo/token/evm"
	gfabtokenv1 "github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/fabtokenv1"
	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	"github.com/LFDT-Panurus/panurus/integration/token/common"
	"github.com/LFDT-Panurus/panurus/integration/token/common/sdk/evmfabtoken"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/topology"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/node"
	. "github.com/onsi/ginkgo/v2"
)

// Topology variations, combined as a bit mask so a Describe can ask for one without a wall of
// boolean arguments. The naming follows the Fabric suites so the two read the same way.
const (
	AuditorAsIssuer = 1 << iota
	NoAuditor
	WebEnabled
)

// This suite is the plain-token counterpart of the zkatdlog EVM gateway suite: the same shared test
// bodies, the same fabric-x-evm gateway backend, a different token driver.
//
// Running both is worth the duplication. The two token drivers put very different work through the
// same gateway node, so a failure that shows up under one and not the other narrows down where the
// problem is without any extra instrumentation.
//
// Each Describe gets its own network, so the specs are grouped by the topology they need rather than
// by what they assert. Labels match the Fabric suites, which lets a single case be singled out with
// GINKGO_TEST_OPTS="--label-filter=T9" instead of paying for the whole suite.
var _ = Describe("EndToEnd", func() {
	Describe("Fungible with Auditor ne Issuer", func() {
		ts, selector := newTestSuite(fsc.LibP2P, 0, 0, "", "alice", "bob", "charlie")
		BeforeEach(ts.Setup)
		AfterEach(ts.TearDown)
		It("succeeded", Label("T1"), func() {
			fungible.TestAll(ts.II, "auditor", nil, true, selector)
		})
	})

	// A public-parameters update is the one token-level operation that reaches into the EVM driver's
	// own machinery rather than just riding over it: the new parameters go on chain, and every node's
	// watcher has to notice the version move and apply them. Both shapes are worth running, because
	// replacing the identities and appending to them produce different parameter bytes.
	Describe("Update public parameters", func() {
		ts, selector := newTestSuite(fsc.LibP2P, WebEnabled, 0, "", "alice", "bob", "charlie")
		BeforeEach(ts.Setup)
		AfterEach(ts.TearDown)
		It("replaces the auditor and the issuer", Label("T2"), func() {
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
		// The append-style update is declared in the zkatdlog suites only, matching the Fabric
		// fabtoken suite, which likewise updates by replacement and never by append.
	})

	Describe("Fungible with Auditor = Issuer", func() {
		ts, selector := newTestSuite(fsc.LibP2P, AuditorAsIssuer, 0, "", "alice", "bob", "charlie")
		BeforeEach(ts.Setup)
		AfterEach(ts.TearDown)
		It("succeeded", Label("T6"), func() {
			fungible.TestAll(ts.II, "issuer", nil, true, selector)
		})
	})

	// The rejection path is worth as much as the happy path here. A malicious request has to be
	// refused, and refused in a way the driver reports as permanent, so the caller stops rather than
	// retrying a transaction the chain will never accept.
	Describe("Malicious transactions", func() {
		ts, selector := newTestSuite(fsc.LibP2P, NoAuditor, 0, "", "alice", "bob", "charlie")
		BeforeEach(ts.Setup)
		AfterEach(ts.TearDown)
		It("are rejected", Label("T9"), func() {
			fungible.TestMaliciousTransactions(ts.II, selector)
		})
	})

	// Redeem burns tokens, which is a state delta shape none of the other specs produce: it deletes
	// without adding. The translator has to carry it as faithfully as it carries an issue or a
	// transfer.
	Describe("Redeem to yourself", func() {
		ts, selector := newTestSuite(fsc.LibP2P, 0, 0, "", "alice", "bob", "charlie")
		BeforeEach(ts.Setup)
		AfterEach(ts.TearDown)
		It("Test redeem", Label("T13"), func() {
			fungible.TestRedeem(ts.II, selector, "default")
		})
	})

	// No Multisig spec: it is a zkatdlog-driver feature, and no fabtoken suite in the tree exercises
	// it on any backend, so it is declared in the zkatdlog EVM suites only.
	//
	// No PolicyIdentity spec either, for a stronger reason: T14 is commented out of the CI matrix for
	// both dlog-fabric and fabricx-dlog (.github/workflows/tests.yml), so it is not green anywhere.
	// See the zkatdlog EVM suite for what it trips over.

	// The selector tests drive several transactions at once from one node, which is the only spec
	// that puts concurrent submissions through a single submitting account. That makes it the one
	// that exercises the nonce manager's serialisation against a real chain rather than a mock.
	for _, tokenSelector := range integration2.TokenSelectors {
		Describe("TokenSelector", Label(tokenSelector), func() {
			ts, replicaSelector := newTestSuite(fsc.LibP2P, 0, 0, tokenSelector, "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("succeeded", Label("T11"), func() {
				fungible.TestSelector(ts.II, "auditor", replicaSelector)
			})
		})
	}
})

func newTestSuite(
	commType fsc.P2PCommunicationType,
	mask int,
	factor int,
	tokenSelector string,
	names ...string,
) (*integration.TestSuite, *token2.ReplicaSelector) {
	opts, selector := token2.NewReplicationOptions(factor, names...)
	tmsOpts := common.Opts{
		Backend:  tevm.GatewayTopologyName,
		CommType: commType,
		DefaultTMSOpts: common.TMSOpts{
			TokenSDKDriver: gfabtokenv1.DriverIdentifier,
			Aries:          true,
		},
		SDKs:            []node.SDK{&evmfabtoken.SDK{}},
		ReplicationOpts: opts,
		FSCLogSpec:      "info",
		// This adds the endorser nodes to the topology and marks them with the token-level endorser
		// role, which is what the EVM handler reads when it provisions endorser identities. The
		// endorsement policy itself still lives in the evm configuration rather than in Fabric's
		// endorser machinery.
		FSCBasedEndorsement: true,
		AuditorAsIssuer:     mask&AuditorAsIssuer > 0,
		NoAuditor:           mask&NoAuditor > 0,
		WebEnabled:          mask&WebEnabled > 0,
		TokenSelector:       tokenSelector,
	}

	ts := integration.NewTestSuite(func() (*integration.Infrastructure, error) {
		i, err := integration.New(StartPortEVMGatewayFabToken(), "./testdata", topology.Topology(tmsOpts)...)
		if err != nil {
			return nil, err
		}
		i.DeleteOnStart = true
		i.DeleteOnStop = false
		if integration.WithRaceDetection {
			i.EnableRaceDetector()
		}
		i.RegisterPlatformFactory(tevm.NewGatewayPlatformFactory())
		i.RegisterPlatformFactory(token.NewPlatformFactory(i))
		i.Generate()

		return i, nil
	})

	return ts, selector
}
