/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tms

import (
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var logger = logging.MustGetLogger()

// ConfigService provides access to TMS configurations.
type ConfigService interface {
	Configurations() ([]driver.Configuration, error)
}

// tmsNormalizer normalizes token service options by matching them against available TMS configurations.
type tmsNormalizer struct {
	configService ConfigService
	normalizer    token.Normalizer
}

// NewTMSNormalizer creates a new TMS normalizer.
func NewTMSNormalizer(tmsProvider ConfigService, normalizer token.Normalizer) *tmsNormalizer {
	return &tmsNormalizer{
		configService: tmsProvider,
		normalizer:    normalizer,
	}
}

// Normalize fills in missing service options by matching against available TMS configurations.
// It filters configurations by network, channel, and namespace, then selects the first match.
func (p *tmsNormalizer) Normalize(opt *token.ServiceOptions) (*token.ServiceOptions, error) {
	// lookup configurations
	configs, err := p.configService.Configurations()
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting tms configs")
	}
	if len(configs) == 0 {
		return nil, errors.Errorf("no token management service configs found")
	}

	logger.Debugf("normalizing opts [%v]", opt)
	if len(opt.Network) != 0 {
		// filter configurations by network
		configs = Filter(configs, func(c driver.Configuration) bool {
			return c.ID().Network == opt.Network
		})
		if len(configs) == 0 {
			return nil, errors.Errorf("no token management service config found for network [%s]", opt.Network)
		}
	}

	if len(opt.Channel) != 0 {
		// filter configurations by channel
		configs = Filter(configs, func(c driver.Configuration) bool {
			return c.ID().Channel == opt.Channel
		})
		if len(configs) == 0 {
			return nil, errors.Errorf("no token management service config found for network and channel [%s:%s]", opt.Network, opt.Channel)
		}
	}

	if len(opt.Namespace) != 0 {
		// filter configurations by namespace
		configs = Filter(configs, func(c driver.Configuration) bool {
			return c.ID().Namespace == opt.Namespace
		})
		if len(configs) == 0 {
			return nil, errors.Errorf("no token management service config found for network, channel, and namespace [%s:%s:%s]", opt.Network, opt.Channel, opt.Namespace)
		}
	}

	// if we reach this point there must be at least one configuration
	logger.Debugf("found [%d] matching configurations for opts [%v], take the first one", len(configs), opt)
	id := configs[0].ID()
	opt.Network = id.Network
	opt.Channel = id.Channel
	opt.Namespace = id.Namespace

	// last pass
	return p.normalizer.Normalize(opt)
}

// Filter keeps elements where keep(x) == true, allocating a new result slice.
// This is a generic utility function for filtering slices.
func Filter[E any](in []E, keep func(E) bool) []E {
	out := make([]E, 0, len(in))
	for _, x := range in {
		if keep(x) {
			out = append(out, x)
		}
	}

	return out
}
