/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"strings"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/config/mocks"
	config3 "github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestConfigService returns a configuration service holding a single TMS at the
// test coordinates, serving the endorser selection key from mspIDs.
func newTestConfigService(mspIDs []string) *config.Service {
	return newTestConfigServiceWithFSCEndorsement(mspIDs, false)
}

// newTestConfigServiceWithFSCEndorsement additionally controls whether the TMS is in FSC
// endorsement mode.
func newTestConfigServiceWithFSCEndorsement(mspIDs []string, fscEndorsement bool) *config.Service {
	cp := &mocks.Provider{}
	cp.IsSetStub = func(key string) bool {
		return fscEndorsement && strings.HasSuffix(key, endorsement.FSCEndorsementKey)
	}
	cp.UnmarshalKeyStub = func(key string, rawVal any) error {
		switch {
		case key == "token.tms":
			*rawVal.(*map[string]any) = map[string]any{"id1": nil}
		case key == "token.tms.id1":
			*rawVal.(*driver.TMSID) = driver.TMSID{
				Network:   testNetwork,
				Channel:   testChannel,
				Namespace: testNamespace,
			}
		case strings.HasSuffix(key, config3.EndorsersMSPIDsKey):
			*rawVal.(*[]string) = mspIDs
		}

		return nil
	}

	return config.NewService(cp)
}

// TestConfigEndorserSelectionProvider covers the provider that backs the
// public-parameters fetcher in a deployed node.
func TestConfigEndorserSelectionProvider(t *testing.T) {
	t.Run("reads the configured selection", func(t *testing.T) {
		p := NewConfigEndorserSelectionProvider(newTestConfigService([]string{"Org1MSP"}))

		selection, err := p.EndorserSelectionFor(testNetwork, testChannel, testNamespace)
		require.NoError(t, err)
		assert.Equal(t, config3.EndorserSelection{MSPIDs: []string{"Org1MSP"}}, selection)
	})

	t.Run("an unknown TMS is an error, which the fetcher reads as no preference", func(t *testing.T) {
		p := NewConfigEndorserSelectionProvider(newTestConfigService([]string{"Org1MSP"}))

		selection, err := p.EndorserSelectionFor("other-network", testChannel, testNamespace)
		require.Error(t, err)
		assert.False(t, selection.IsSet())
	})

	t.Run("FSC endorsement mode selects nothing", func(t *testing.T) {
		// The local org often hosts no peer in FSC-endorsement deployments, so applying
		// the selection there would strand the public-parameters query with no endorser.
		p := NewConfigEndorserSelectionProvider(newTestConfigServiceWithFSCEndorsement([]string{"Org1MSP"}, true))

		selection, err := p.EndorserSelectionFor(testNetwork, testChannel, testNamespace)
		require.NoError(t, err)
		assert.False(t, selection.IsSet())
	})

	t.Run("an invalid selection is rejected even in FSC endorsement mode", func(t *testing.T) {
		p := NewConfigEndorserSelectionProvider(newTestConfigServiceWithFSCEndorsement([]string{"Org1MSP", "Org1MSP"}, true))

		_, err := p.EndorserSelectionFor(testNetwork, testChannel, testNamespace)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "more than once")
	})

	t.Run("an invalid selection is rejected", func(t *testing.T) {
		p := NewConfigEndorserSelectionProvider(newTestConfigService([]string{"Org1MSP", "Org1MSP"}))

		_, err := p.EndorserSelectionFor(testNetwork, testChannel, testNamespace)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "more than once")
	})
}
