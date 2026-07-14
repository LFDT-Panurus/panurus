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
)

var logger = logging.MustGetLogger()

// EVMConfigKey is the TMS-scoped configuration key under which the EVM network service is declared,
// i.e. token.tms.<tms-id>.services.network.evm. Its presence marks a TMS as an EVM network.
const EVMConfigKey = "services.network.evm"

// networkResolver decides whether a (network, channel) pair is served by the EVM driver. It is a
// small seam over the configuration so the driver's routing can be unit-tested without a full
// config service.
type networkResolver interface {
	// IsEVMNetwork reports whether the given network/channel has an EVM network configuration.
	IsEVMNetwork(network, channel string) bool
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
func (d *Driver) New(network, channel string) (driver.Network, error) {
	if !d.resolver.IsEVMNetwork(network, channel) {
		return nil, errors.Errorf("evm: no evm network configuration for [%s:%s]", network, channel)
	}
	logger.Debugf("creating evm network [%s:%s]", network, channel)

	return newNetwork(network), nil
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
