/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"github.com/LFDT-Panurus/panurus/token/services/network/common"
	config3 "github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ConfigEndorserSelectionProvider resolves the endorser selection of a TMS from its
// configuration.
type ConfigEndorserSelectionProvider struct {
	configuration common.Configuration
}

// NewConfigEndorserSelectionProvider returns an EndorserSelectionProvider backed by the
// given configuration service.
func NewConfigEndorserSelectionProvider(configuration common.Configuration) *ConfigEndorserSelectionProvider {
	return &ConfigEndorserSelectionProvider{configuration: configuration}
}

// EndorserSelectionFor returns the endorser selection configured for the given
// coordinates. It fails when no configuration matches them, which callers that run
// before a TMS is resolvable treat as "no preference".
//
// Whether the selection applies at all is config.ResolveEndorserSelection's decision,
// shared with the endorsement service loader, so that the two cannot drift.
func (p *ConfigEndorserSelectionProvider) EndorserSelectionFor(network, channel, namespace string) (config3.EndorserSelection, error) {
	configuration, err := p.configuration.ConfigurationFor(network, channel, namespace)
	if err != nil {
		return config3.EndorserSelection{}, errors.WithMessagef(err, "failed to get configuration for [%s:%s:%s]", network, channel, namespace)
	}

	selection, chaincodeEndorsement, err := config3.ResolveEndorserSelection(configuration)
	if err != nil {
		return config3.EndorserSelection{}, err
	}
	if !chaincodeEndorsement {
		return config3.EndorserSelection{}, nil
	}

	return selection, nil
}
