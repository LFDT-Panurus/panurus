/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
)

var logger = logging.MustGetLogger()

// EVMConfigKey is the TMS-scoped configuration key under which the EVM network service is declared,
// i.e. token.tms.<tms-id>.services.network.evm. Its presence marks a TMS as an EVM network.
const EVMConfigKey = "services.network.evm"

// networkResolver decides whether a (network, channel) pair is served by the EVM driver, and yields
// that network's configuration. It is a small seam over the configuration service so the driver's
// routing can be unit-tested without a full config service.
type networkResolver interface {
	// IsEVMNetwork reports whether the given network/channel has an EVM network configuration.
	IsEVMNetwork(network, channel string) bool
	// ConfigFor returns the EVM configuration for the given network/channel.
	ConfigFor(network, channel string) (*Config, error)
}

// Driver is the EVM network driver factory. It implements driver.Driver: the network provider calls
// New for every (network, channel) and uses the first driver that returns no error, so New must
// return an error for networks that are not configured for EVM.
type Driver struct {
	resolver networkResolver
}

// Compile-time assertion that Driver satisfies the factory contract.
var _ driver.Driver = (*Driver)(nil)

// NewDriver returns a new EVM network Driver. It is wired into the SDK dig container under the
// "network-drivers" group (see the evmdlog SDK module).
func NewDriver(configService *config.Service) driver.Driver {
	return &Driver{resolver: &configNetworkResolver{cs: configService}}
}

// New returns an EVM Network for the given network/channel, or an error if that network is not
// configured for EVM (so the network provider falls through to the next registered driver).
//
// The endorsement service and the submitter are supplied by the SDK wiring, which owns the key
// material and the FSC view manager they need; a Network built here can serve queries and assemble
// approvals once they are injected.
func (d *Driver) New(network, channel string) (driver.Network, error) {
	if !d.resolver.IsEVMNetwork(network, channel) {
		return nil, errors.Errorf("evm: no evm network configuration for [%s:%s]", network, channel)
	}
	logger.Debugf("creating evm network [%s:%s]", network, channel)

	config, err := d.resolver.ConfigFor(network, channel)
	if err != nil {
		return nil, err
	}
	evmClient, err := client.NewJSONRPCClient(config.Endpoint, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "evm: failed to create a client for [%s:%s]", network, channel)
	}

	return NewNetwork(network, config, evmClient, nil, nil)
}

// configNetworkResolver resolves EVM networks from the token-sdk configuration.
type configNetworkResolver struct {
	cs *config.Service
}

// IsEVMNetwork reports whether any configured TMS for the given network/channel declares the EVM
// network service block.
func (r *configNetworkResolver) IsEVMNetwork(network, channel string) bool {
	configs, err := r.cs.Configurations()
	if err != nil {
		logger.Errorf("failed to load token-sdk configurations while resolving evm network [%s:%s]: %v", network, channel, err)

		return false
	}
	for _, c := range configs {
		id := c.ID()
		if id.Network == network && id.Channel == channel && c.IsSet(EVMConfigKey) {
			return true
		}
	}

	return false
}

// ConfigFor loads and validates the EVM configuration of the first TMS declaring it for the given
// network/channel. Every TMS on one EVM network shares the endpoint and chain, so the first match is
// the network's configuration.
func (r *configNetworkResolver) ConfigFor(network, channel string) (*Config, error) {
	configs, err := r.cs.Configurations()
	if err != nil {
		return nil, errors.Wrapf(err, "evm: failed to load token-sdk configurations for [%s:%s]", network, channel)
	}
	for _, c := range configs {
		id := c.ID()
		if id.Network == network && id.Channel == channel && c.IsSet(EVMConfigKey) {
			return LoadConfig(c)
		}
	}

	return nil, errors.Errorf("evm: no evm network configuration for [%s:%s]", network, channel)
}
