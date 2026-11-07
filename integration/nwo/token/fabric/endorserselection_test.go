/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"strings"
	"testing"

	topology2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	fabricconfig "github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
	fabricnwo "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric"
	sfcnode "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	"github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// newNode returns an FSC node carrying the given options.
func newNode(name string, opts ...sfcnode.Option) *sfcnode.Node {
	return sfcnode.NewNode(name).AddOptions(opts...)
}

func TestEndorserSelectionForUnset(t *testing.T) {
	tms := newTMS()

	assert.Nil(t, EndorserSelectionFor(tms, newNode("alice", fabricnwo.WithOrganization("Org2"))),
		"a TMS that configures no selection must leave discovery unrestricted")
}

func TestEndorserSelectionForOwnOrg(t *testing.T) {
	tms := WithEndorsersFromOwnOrg(newTMS())

	// Each node selects its own organization, so the two end up with different selections.
	assert.Equal(t, []string{"Org1MSP"},
		EndorserSelectionFor(tms, newNode("issuer", fabricnwo.WithOrganization("Org1"))))
	assert.Equal(t, []string{"Org2MSP"},
		EndorserSelectionFor(tms, newNode("alice", fabricnwo.WithOrganization("Org2"))))
}

func TestEndorserSelectionForOwnOrgNoOrganization(t *testing.T) {
	tms := WithEndorsersFromOwnOrg(newTMS())

	// A node without a Fabric organization does not endorse: nothing to select.
	assert.Nil(t, EndorserSelectionFor(tms, newNode("norg")))
}

func TestEndorserSelectionForOwnOrgIgnoresOtherNetworks(t *testing.T) {
	tms := WithEndorsersFromOwnOrg(newTMS())
	node := newNode(
		"alice",
		fabricnwo.WithNetworkOrganization("testnet", "Org2"),
		fabricnwo.WithNetworkOrganization("other", "Org9"),
	)

	// Only the organization this TMS's network places the node in decides which peers may
	// endorse its invocations.
	assert.Equal(t, []string{"Org2MSP"}, EndorserSelectionFor(tms, node))
}

func TestEndorserSelectionForOwnOrgDeduplicates(t *testing.T) {
	tms := WithEndorsersFromOwnOrg(newTMS())
	node := newNode(
		"alice",
		fabricnwo.WithOrganization("Org2"),
		fabricnwo.WithNetworkOrganization("testnet", "Org2"),
	)

	// A repeated MSP ID is rejected by the production configuration loader, so the harness
	// must not emit one.
	assert.Equal(t, []string{"Org2MSP"}, EndorserSelectionFor(tms, node))
}

// TestGenerateExtensionEndorsementStanza renders the TMS extension and reads the endorser
// selection back out of it at the key the production configuration loader reads.
//
// The template is indentation-sensitive YAML, and a stanza emitted at the wrong depth
// silently lands under another key: the node would then start with no restriction at all
// and the integration test asserting the selection would pass for the wrong reason.
func TestGenerateExtensionEndorsementStanza(t *testing.T) {
	gomega.RegisterTestingT(t)
	tms := WithEndorsersFromOwnOrg(newTMS())
	handler := &NetworkHandler{Entries: map[string]*Entry{}}

	extension := handler.GenerateExtension(tms, newNode("alice", fabricnwo.WithOrganization("Org2")), "alice")

	assert.Equal(t, []string{"Org2MSP"}, endorserMSPIDsFromExtension(t, tms, extension))
}

func TestGenerateExtensionWithoutEndorsementStanza(t *testing.T) {
	gomega.RegisterTestingT(t)
	tms := newTMS()
	handler := &NetworkHandler{Entries: map[string]*Entry{}}

	extension := handler.GenerateExtension(tms, newNode("alice", fabricnwo.WithOrganization("Org2")), "alice")

	assert.Nil(t, endorserMSPIDsFromExtension(t, tms, extension),
		"a TMS with no selection must not emit the endorsement stanza")
}

// endorserMSPIDsFromExtension parses a rendered TMS extension and returns the value of the
// endorser-selection key, or nil when the extension does not set it. The key path is
// derived from the production constant, so renaming the key there fails this test rather
// than silently leaving the harness writing a key nothing reads.
func endorserMSPIDsFromExtension(t *testing.T, tms *topology2.TMS, extension string) []string {
	t.Helper()

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(extension), &parsed), "extension is not valid YAML:\n%s", extension)

	path := append([]string{"token", "tms", tms.TmsID()}, strings.Split(fabricconfig.EndorsersMSPIDsKey, ".")...)
	node := any(parsed)
	for _, key := range path {
		section, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		if node, ok = section[key]; !ok {
			return nil
		}
	}

	entries, ok := node.([]any)
	require.True(t, ok, "expected a list at [%s], got %T", strings.Join(path, "."), node)
	mspIDs := make([]string, 0, len(entries))
	for _, entry := range entries {
		mspID, ok := entry.(string)
		require.True(t, ok, "expected a string MSP ID, got %T", entry)
		mspIDs = append(mspIDs, mspID)
	}

	return mspIDs
}
