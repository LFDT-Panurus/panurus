/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/common"
	config3 "github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
)

// ConfigEndorserSelectionProvider resolves the endorser selection of a TMS from its
// configuration, cached per TMS, so a change needs a restart.
type ConfigEndorserSelectionProvider struct {
	selections lazy.Provider[token2.TMSID, config3.EndorserSelection]
}

// NewConfigEndorserSelectionProvider returns an EndorserSelectionProvider backed by the
// given configuration service.
func NewConfigEndorserSelectionProvider(configuration common.Configuration) *ConfigEndorserSelectionProvider {
	return &ConfigEndorserSelectionProvider{
		selections: lazy.NewProvider(func(tmsID token2.TMSID) (config3.EndorserSelection, error) {
			return resolveEndorserSelection(configuration, tmsID)
		}),
	}
}

// EndorserSelectionFor returns the endorser selection configured for the given
// coordinates. An unconfigured TMS fails with config.ErrConfigurationNotFound.
func (p *ConfigEndorserSelectionProvider) EndorserSelectionFor(network, channel, namespace string) (config3.EndorserSelection, error) {
	// shortcut: failures are not cached, so a namespace that never gets a configuration
	// re-resolves under the cache's write lock on every query; revisit if reads contend.
	return p.selections.Get(token2.TMSID{Network: network, Channel: channel, Namespace: namespace})
}

func resolveEndorserSelection(configuration common.Configuration, tmsID token2.TMSID) (config3.EndorserSelection, error) {
	c, err := configuration.ConfigurationFor(tmsID.Network, tmsID.Channel, tmsID.Namespace)
	if err != nil {
		return config3.EndorserSelection{}, errors.WithMessagef(err, "failed to get configuration for [%s]", tmsID)
	}

	selection, chaincodeEndorsement, err := config3.ResolveEndorserSelection(c)
	if err != nil {
		return config3.EndorserSelection{}, err
	}
	if !chaincodeEndorsement {
		return config3.EndorserSelection{}, nil
	}

	return selection, nil
}
