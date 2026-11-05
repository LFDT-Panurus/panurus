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
		// TestAll and the small specs share one network, see token2.ShareNetwork. TestAll
		// (T1) runs first, on a fresh network, so its assertions on the whole transaction
		// history and on the issuer's 241 USD quota hold unchanged. Every later spec uses
		// its own token type, so TestAll's balances and the USD quota, which applies to the
		// exact type "USD" only, do not affect it; T4 and T5 use different types because
		// both assert absolute balances of the same wallets. T17 authorizes the issuer for
		// redeem, as TestAll already did (a KVS overwrite, so it changes nothing), and also
		// newIssuer, which no later spec redeems with; it runs before T2 because it relies
		// on newIssuer not being in the public parameters, which T2 adds. T3 revokes bob for
		// good, so it runs after T4 and T5, which move tokens to bob. T2 changes the public
		// parameters, so it runs last. Each label still runs on its own network when
		// filtered alone.
		Describe("Fungible", Ordered, ContinueOnFailure, t.Label, func() {
			ts, selector := newTestSuite(t.CommType, t.ReplicationFactor, "alice", "bob", "charlie")
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
			It("Update public params", Label("T2"), func() {
				UpdatePublicParams(ts.II, selector, fungible.WithTokenType("USDt2"))
			})
		})
	}
})

// UpdatePublicParams replaces the auditor and the issuer of the fabtoken public parameters
// with newAuditor and newIssuer, and checks with fungible.TestPublicParamsUpdate
// that the network keeps working, with the passed options.
func UpdatePublicParams(network *integration.Infrastructure, selector *token2.ReplicaSelector, opts ...fungible.TestOption) {
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

	fungible.TestPublicParamsUpdate(network, "newAuditor", ppBytes, "default", false, selector, false, opts...)
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
