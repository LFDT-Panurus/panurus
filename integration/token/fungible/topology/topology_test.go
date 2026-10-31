/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package topology

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
	fabric2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/fabtokenv1"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatsnarkv1"
	tokentopology "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/LFDT-Panurus/panurus/integration/token/common"
	endorsementfsc "github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	"github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noReplication is the replication option set of a topology with one instance per node.
type noReplication struct{}

func (noReplication) For(string) []node.Option { return nil }

// tmsOf builds the topology described by opts and returns its single TMS.
func tmsOf(t *testing.T, opts common.Opts) *tokentopology.TMS {
	t.Helper()

	opts.Backend = "fabric"
	opts.DefaultTMSOpts = common.TMSOpts{TokenSDKDriver: zkatdlognoghv1.DriverIdentifier, Aries: true}
	tmss := tmssOf(t, opts)
	require.Len(t, tmss, 1)

	return tmss[0]
}

// tmssOf builds the topology described by opts, keeping its backend and default TMS options, and
// returns all of its TMSs.
func tmssOf(t *testing.T, opts common.Opts) []*tokentopology.TMS {
	t.Helper()

	// The topology builders assert with gomega.
	gomega.RegisterTestingT(t)

	opts.CommType = fsc.WebSocket
	opts.ReplicationOpts = noReplication{}

	var tmss []*tokentopology.TMS
	for _, topology := range Topology(opts) {
		if tokenTopology, ok := topology.(*token.Topology); ok {
			tmss = tokenTopology.GetTMSs()
		}
	}

	return tmss
}

func TestTopologyDefaultsToOneOrgAndNoEndorserSelection(t *testing.T) {
	tms := tmsOf(t, common.Opts{})

	assert.Equal(t, []string{"Org1"}, fabric2.GetOrgs(tms),
		"only Org1 hosts the token chaincode unless the namespace policy spans more orgs")
	assert.Empty(t, fabric2.GetNamespacePolicy(tms), "the default is unanimity over the hosting orgs")
	assert.False(t, fabric2.EndorsersFromOwnOrg(tms),
		"endorser selection is opt-in: an unconfigured topology must keep default discovery")
}

// TestTopologyOwnOrgEndorsersSpanAllOrgs covers the configuration the T15 spec of the dlog
// suite runs: every node pins endorsement to its own organization, which is only
// meaningful if the chaincode is hosted by every organization and the namespace policy can
// be satisfied by one of them alone.
func TestTopologyOwnOrgEndorsersSpanAllOrgs(t *testing.T) {
	const policy = "OR ('Org1MSP.member','Org2MSP.member')"
	tms := tmsOf(t, common.Opts{NamespacePolicy: policy, EndorsersFromOwnOrg: true})

	assert.Equal(t, []string{"Org1", "Org2"}, fabric2.GetOrgs(tms),
		"a namespace policy naming an org whose peers do not host the chaincode can never be satisfied")
	assert.Equal(t, policy, fabric2.GetNamespacePolicy(tms))
	assert.True(t, fabric2.EndorsersFromOwnOrg(tms))
}

// upgradeOpts returns the options of a fabtoken network with one transient ZKAT-SNARK TMS, as used by
// the fabtoken -> ZKAT-SNARK upgrade scenarios.
func upgradeOpts(backend string) common.Opts {
	return common.Opts{
		Backend:        backend,
		DefaultTMSOpts: common.TMSOpts{TokenSDKDriver: fabtokenv1.DriverIdentifier, PublicParamsGenArgs: []string{"64"}},
		ExtraTMSs: []common.TMSOpts{{
			Alias:               "zkatsnark-32bits",
			TokenSDKDriver:      zkatsnarkv1.DriverIdentifier,
			PublicParamsGenArgs: []string{"32"},
			Aries:               true,
		}},
	}
}

// TestTopologyExtraTMSInheritsEndorsementSettings checks that an extra (transient) TMS gets the same
// FSC endorsement, namespace policy and own-org settings as the default TMS. Both TMSs share the same
// TMS ID, so their configuration extensions are merged: an extra TMS without them would wipe the
// default TMS's fsc_endorsement block.
func TestTopologyExtraTMSInheritsEndorsementSettings(t *testing.T) {
	const policy = "OR ('Org1MSP.member','Org2MSP.member')"
	opts := upgradeOpts("fabricx")
	opts.FSCBasedEndorsement = true
	opts.FSCEndorsementPolicyType = endorsementfsc.NamespacePolicy
	opts.NamespacePolicy = policy
	opts.EndorsersFromOwnOrg = true

	tmss := tmssOf(t, opts)
	require.Len(t, tmss, 2)
	def, extra := tmss[0], tmss[1]
	require.False(t, def.Transient)
	require.True(t, extra.Transient)

	assert.True(t, fabric2.IsFSCEndorsementEnabled(def))
	assert.True(t, fabric2.IsFSCEndorsementEnabled(extra))
	assert.NotEmpty(t, fabric2.Endorsers(def))
	assert.Equal(t, fabric2.Endorsers(def), fabric2.Endorsers(extra))
	assert.Equal(t, endorsementfsc.NamespacePolicy, fabric2.FSCEndorsementPolicyType(extra))
	assert.Equal(t, policy, fabric2.GetNamespacePolicy(extra))
	assert.True(t, fabric2.EndorsersFromOwnOrg(extra))
}

// TestTopologyExtraTMSWithoutEndorsement checks that an extra TMS stays without FSC endorsement when
// the network does not use it, as in the Fabric upgrade suite.
func TestTopologyExtraTMSWithoutEndorsement(t *testing.T) {
	tmss := tmssOf(t, upgradeOpts("fabric"))
	require.Len(t, tmss, 2)

	for _, tms := range tmss {
		assert.False(t, fabric2.IsFSCEndorsementEnabled(tms))
		assert.Empty(t, fabric2.Endorsers(tms))
		assert.Empty(t, fabric2.GetNamespacePolicy(tms))
		assert.False(t, fabric2.EndorsersFromOwnOrg(tms))
	}
	assert.Equal(t, "1outn", fabric2.FSCEndorsementPolicyType(tmss[1]))
}
