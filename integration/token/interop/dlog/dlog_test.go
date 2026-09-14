/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dlog_test

import (
	integration2 "github.com/LFDT-Panurus/panurus/integration"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	"github.com/LFDT-Panurus/panurus/integration/token/common"
	"github.com/LFDT-Panurus/panurus/integration/token/common/sdk/fdlog"
	"github.com/LFDT-Panurus/panurus/integration/token/interop"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	nodepkg "github.com/hyperledger-labs/fabric-smart-client/pkg/node"
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("DLog end to end", func() {
	BeforeEach(func() { token.Drivers = append(token.Drivers, zkatdlognoghv1.DriverIdentifier) })

	for _, t := range integration2.AllTestTypes {
		Describe("HTLC Single Fabric Network", t.Label, func() {
			ts, selector := newTestSuiteSingleFabric(t.CommType, t.ReplicationFactor, "alice", "bob")
			AfterEach(ts.TearDown)
			BeforeEach(ts.Setup)
			It("Performed htlc-related basic operations", Label("T1"), func() { interop.TestHTLCSingleNetwork(ts.II, selector) })
		})

		Describe("HTLC Two Fabric Networks", t.Label, func() {
			ts, selector := newTestSuiteTwoFabric(t.CommType, t.ReplicationFactor, "alice", "bob")
			AfterEach(ts.TearDown)
			BeforeEach(ts.Setup)
			It("Performed an htlc based atomic swap", Label("T3"), func() { interop.TestHTLCTwoNetworks(ts.II, selector) })
			It("Performed a fast exchange", Label("T4"), func() { interop.TestFastExchange(ts.II, selector) })
		})

		Describe("HTLC No Cross Claim Two Fabric Networks", t.Label, func() {
			ts, selector := newTestSuiteNoCrossClaimFabric(t.CommType, t.ReplicationFactor, "alice", "bob")
			AfterEach(ts.TearDown)
			BeforeEach(ts.Setup)
			It("Performed an htlc based atomic swap", Label("T5"), func() { interop.TestHTLCNoCrossClaimTwoNetworks(ts.II, selector) })
		})
	}
})

func newTestSuiteSingleFabric(commType fsc.P2PCommunicationType, factor int, names ...string) (*token2.TestSuite, *token2.ReplicaSelector) {
	opts, selector := token2.NewReplicationOptions(factor, names...)
	ts := token2.NewTestSuite(integration2.ZKATDLogInteropHTLC.StartPortForNode, interop.HTLCSingleFabricNetworkTopology(common.Opts{
		CommType:        commType,
		ReplicationOpts: opts,
		DefaultTMSOpts:  common.TMSOpts{TokenSDKDriver: zkatdlognoghv1.DriverIdentifier, Aries: true},
		SDKs:            []nodepkg.SDK{&fdlog.SDK{}},
		FSCLogSpec:      "info",
	}))

	return ts, selector
}

func newTestSuiteTwoFabric(commType fsc.P2PCommunicationType, factor int, names ...string) (*token2.TestSuite, *token2.ReplicaSelector) {
	opts, selector := token2.NewReplicationOptions(factor, names...)
	ts := token2.NewTestSuite(integration2.ZKATDLogInteropHTLCTwoFabricNetworks.StartPortForNode, interop.HTLCTwoFabricNetworksTopology(common.Opts{
		CommType:        commType,
		ReplicationOpts: opts,
		DefaultTMSOpts:  common.TMSOpts{TokenSDKDriver: zkatdlognoghv1.DriverIdentifier, Aries: true},
		SDKs:            []nodepkg.SDK{&fdlog.SDK{}},
		FSCLogSpec:      "info",
	}))

	return ts, selector
}

func newTestSuiteNoCrossClaimFabric(commType fsc.P2PCommunicationType, factor int, names ...string) (*token2.TestSuite, *token2.ReplicaSelector) {
	opts, selector := token2.NewReplicationOptions(factor, names...)
	ts := token2.NewTestSuite(integration2.ZKATDLogInteropHTLCSwapNoCrossTwoFabricNetworks.StartPortForNode, interop.HTLCNoCrossClaimTopology(common.Opts{
		CommType:        commType,
		ReplicationOpts: opts,
		DefaultTMSOpts:  common.TMSOpts{TokenSDKDriver: zkatdlognoghv1.DriverIdentifier, Aries: true},
		SDKs:            []nodepkg.SDK{&fdlog.SDK{}},
		FSCLogSpec:      "info",
	}))

	return ts, selector
}
