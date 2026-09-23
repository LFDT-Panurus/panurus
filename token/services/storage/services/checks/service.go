/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package checks

import (
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
)

var logger = logging.MustGetLogger()

// Configuration resolves the configuration of a TMS.
type Configuration interface {
	// ConfigurationFor returns the configuration for the given coordinates
	ConfigurationFor(network, channel, namespace string) (*config.Configuration, error)
}

// ConfigFor returns the checks configuration of the passed TMS.
//
// A TMS with no configuration, or with one that cannot be read, falls back to
// the defaults with a warning rather than failing: the sweep is a safety net,
// and refusing to start one because a key is missing would leave the node with
// no checking at all, which is the state this service was written to end.
func ConfigFor(configuration Configuration, tmsID token.TMSID) Config {
	cfg, err := configuration.ConfigurationFor(tmsID.Network, tmsID.Channel, tmsID.Namespace)
	if err != nil {
		logger.Warnf("failed to get configuration for [%s], using default checks config: %v", tmsID, err)

		return DefaultConfig()
	}

	checksConfig, err := LoadConfig(cfg)
	if err != nil {
		logger.Warnf("failed to load checks config for [%s], using defaults: %v", tmsID, err)

		return DefaultConfig()
	}

	return checksConfig
}
