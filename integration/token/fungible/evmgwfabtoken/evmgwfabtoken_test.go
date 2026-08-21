/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evmgwfabtoken

import (
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

// This suite is the plain-token counterpart of the zkatdlog EVM gateway suite: the same shared test
// bodies, the same fabric-x-evm gateway backend, a different token driver.
//
// Running both is worth the duplication. The two token drivers put very different work through the
// same gateway node, so a failure that shows up under one and not the other narrows down where the
// problem is without any extra instrumentation.
var _ = Describe("EndToEnd", func() {
	Describe("Fungible with Auditor ne Issuer", func() {
		ts, selector := newTestSuite(fsc.LibP2P, 0, "alice", "bob", "charlie")
		BeforeEach(ts.Setup)
		AfterEach(ts.TearDown)
		It("succeeded", Label("T1"), func() {
			fungible.TestAll(ts.II, "auditor", nil, true, selector)
		})
	})
})

func newTestSuite(commType fsc.P2PCommunicationType, factor int, names ...string) (*integration.TestSuite, *token2.ReplicaSelector) {
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
		// Adds endorser nodes marked with the token-level endorser role the EVM handler reads.
		FSCBasedEndorsement: true,
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
