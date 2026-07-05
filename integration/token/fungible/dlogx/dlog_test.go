/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dlogx

import (
	"time"

	integration2 "github.com/LFDT-Panurus/panurus/integration"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	"github.com/LFDT-Panurus/panurus/integration/token/common"
	"github.com/LFDT-Panurus/panurus/integration/token/common/sdk/fxdlog"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/topology"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/views/fabricx/tmsdeploy"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	common2 "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabricx"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/node"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

	for _, t := range integration2.AllTestTypes {
		Describe("T1 Fungible with Auditor ne Issuer and Endorsers", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries|WithEndorsers, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("succeeded", Label("T1"), func() {
				time.Sleep(10 * time.Second)

				pps, err := GetPublicParamsInputs(ts.II)
				Expect(err).ToNot(HaveOccurred())

				tms := fungible.GetTMSByNetworkName(ts.II, "default")
				_, err = ts.II.Client("endorser-1").CallView("TMSDeploy", common2.JSONMarshall(
					&tmsdeploy.Deploy{
						Network:         tms.Network,
						Channel:         tms.Channel,
						Namespace:       tms.Namespace,
						PublicParamsRaw: pps[0].PublicParameters.Raw,
					},
				))
				Expect(err).NotTo(HaveOccurred())

				fungible.TestAll(ts.II, "auditor", nil, true, selector)
			})
		})
	}

})

func newTestSuite(commType fsc.P2PCommunicationType, mask int, factor int, tokenSelector string, names ...string) (*integration.TestSuite, *token2.ReplicaSelector) {
	opts, selector := token2.NewReplicationOptions(factor, names...)
	ts := integration.NewTestSuite(func() (*integration.Infrastructure, error) {
		i, err := integration.New(StartPortDlog(), "./testdata", topology.Topology(common.Opts{
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
		})...)
		i.DeleteOnStart = true
		i.DeleteOnStop = false
		i.RegisterPlatformFactory(fabricx.NewPlatformFactory())
		i.RegisterPlatformFactory(token.NewPlatformFactory(i))
		i.Generate()

		return i, err
	})

	return ts, selector
}
