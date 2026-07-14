/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabtoken

import (
	"math"

	integration2 "github.com/LFDT-Panurus/panurus/integration"
	gfabtokenv1 "github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/fabtokenv1"
	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	"github.com/LFDT-Panurus/panurus/integration/token/common"
	"github.com/LFDT-Panurus/panurus/integration/token/common/sdk/ffabtoken"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/topology"
	fabtokenv1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	nodepkg "github.com/hyperledger-labs/fabric-smart-client/pkg/node"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("EndToEnd", func() {
	for _, t := range integration2.AllTestTypes {
		Describe("Fungible", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, t.ReplicationFactor, "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("succeeded", Label("T1"), func() { fungible.TestAll(ts.II, "auditor", nil, true, selector) })
			It("Update public params", Label("T2"), func() { UpdatePublicParams(ts.II, selector) })
			It("Test Identity Revocation", Label("T3"), func() { fungible.TestRevokeIdentity(ts.II, "auditor", selector) })
			It("Test Remote Wallet (GRPC)", Label("T4"), func() { fungible.TestRemoteOwnerWallet(ts.II, "auditor", selector, false) })
			It("Test Remote Wallet (WebSocket)", Label("T5"), func() { fungible.TestRemoteOwnerWallet(ts.II, "auditor", selector, true) })
		})
	}
})

func UpdatePublicParams(network *integration.Infrastructure, selector *token2.ReplicaSelector) {
	tms := fungible.GetTMSByNetworkName(network, "default")
	auditorId := fungible.GetAuditorIdentity(tms, "newAuditor")
	issuerId := fungible.GetIssuerIdentity(tms, "newIssuer")
	publicParam := fabtokenv1.PublicParams{
		DriverName:        fabtokenv1.FabTokenDriverName,
		DriverVersion:     fabtokenv1.ProtocolV1,
		QuantityPrecision: uint64(64),
		Auditor:           auditorId,
		IssuerIDs:         []driver.Identity{issuerId},
		MaxToken:          math.MaxUint64,
	}
	ppBytes, err := publicParam.Serialize()
	Expect(err).NotTo(HaveOccurred())

	fungible.TestPublicParamsUpdate(network, "newAuditor", ppBytes, "default", false, selector, false)
}

func newTestSuite(commType fsc.P2PCommunicationType, factor int, names ...string) (*token2.TestSuite, *token2.ReplicaSelector) {
	opts, selector := token2.NewReplicationOptions(factor, names...)
	ts := token2.NewTestSuite(StartPortDlog, topology.Topology(
		common.Opts{
			Backend:         "fabric",
			CommType:        commType,
			DefaultTMSOpts:  common.TMSOpts{TokenSDKDriver: gfabtokenv1.DriverIdentifier, Aries: true},
			SDKs:            []nodepkg.SDK{&ffabtoken.SDK{}},
			ReplicationOpts: opts,
			WebEnabled:      true, // Needed for the Remote Wallet with websockets
			FSCLogSpec:      "info",
		},
	))

	return ts, selector
}
