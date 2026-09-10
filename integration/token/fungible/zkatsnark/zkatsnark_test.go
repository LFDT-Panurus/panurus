/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark

import (
	integration2 "github.com/LFDT-Panurus/panurus/integration"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
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
	for _, t := range integration2.WebSocketNoReplicationOnly {
		Describe("T1 Fungible with Auditor ne Issuer and Endorsers", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries|WithEndorsers, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("succeeded", Label("T1"), func() {
				fungible.TestAll(ts.II, "auditor", nil, true, selector)
			})
		})
	}
})

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

	return ts, selector
}
