/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark

import (
	integration2 "github.com/LFDT-Panurus/panurus/integration"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatsnarkv1"
	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	"github.com/LFDT-Panurus/panurus/integration/token/common"
	"github.com/LFDT-Panurus/panurus/integration/token/common/sdk/fzkatsnark"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/topology"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	nodepkg "github.com/hyperledger-labs/fabric-smart-client/pkg/node"
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
	for _, t := range integration2.AllTestTypes {
		Describe("T1 Fungible with Auditor ne Issuer", t.Label, func() {
			ts, selector := newTestSuite(t.CommType, Aries, t.ReplicationFactor, "", "alice", "bob", "charlie")
			BeforeEach(ts.Setup)
			AfterEach(ts.TearDown)
			It("succeeded", Label("T1"), func() { fungible.TestAll(ts.II, "auditor", nil, true, selector) })
		})
	}
})

func newTestSuite(commType fsc.P2PCommunicationType, mask int, factor int, tokenSelector string, names ...string) (*token2.TestSuite, *token2.ReplicaSelector) {
	opts, selector := token2.NewReplicationOptions(factor, names...)
	ts := token2.NewTestSuite(StartPortZkatsnark, topology.Topology(
		common.Opts{
			Backend:             "fabric",
			CommType:            commType,
			DefaultTMSOpts:      common.TMSOpts{TokenSDKDriver: zkatsnarkv1.DriverIdentifier, Aries: mask&Aries > 0},
			NoAuditor:           mask&NoAuditor > 0,
			AuditorAsIssuer:     mask&AuditorAsIssuer > 0,
			HSM:                 mask&HSM > 0,
			WebEnabled:          mask&WebEnabled > 0,
			SDKs:                []nodepkg.SDK{&fzkatsnark.SDK{}},
			Monitoring:          false,
			ReplicationOpts:     opts,
			FSCBasedEndorsement: mask&WithEndorsers > 0,
			FSCLogSpec:          "debug",
			TokenSelector:       tokenSelector,
		},
	))

	return ts, selector
}
