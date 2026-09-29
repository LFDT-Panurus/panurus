/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dlog

import (
	"github.com/LFDT-Panurus/panurus/integration"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	"github.com/LFDT-Panurus/panurus/integration/token"
	"github.com/LFDT-Panurus/panurus/integration/token/common"
	"github.com/LFDT-Panurus/panurus/integration/token/common/sdk/fdlog"
	"github.com/LFDT-Panurus/panurus/integration/token/nft"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	nodepkg "github.com/hyperledger-labs/fabric-smart-client/pkg/node"
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("EndToEnd", func() {
	for _, t := range integration.AllTestTypes {
		Describe("NFT", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, t.ReplicationFactor, "alice", "bob")
			AfterEach(ts.TearDown)
			BeforeEach(ts.Setup)
			It("succeeded", func() { nft.TestAll(ts.II, selector) })
		})
	}
})

func newTestSuite(commType fsc.P2PCommunicationType, factor int, names ...string) (*token.TestSuite, *token.ReplicaSelector) {
	opts, selector := token.NewReplicationOptions(factor, names...)
	ts := token.NewTestSuite(StartPortDlog, nft.Topology(common.Opts{
		Backend:         "fabric",
		CommType:        commType,
		DefaultTMSOpts:  common.TMSOpts{TokenSDKDriver: zkatdlognoghv1.DriverIdentifier, Aries: true},
		SDKs:            []nodepkg.SDK{&fdlog.SDK{}},
		ReplicationOpts: opts,
	}))

	return ts, selector
}
