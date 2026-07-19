/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
)

// ResourceLimitsPath is the configuration key holding the process-wide validator resource
// limits. It is deliberately not scoped under a TMS (unlike TMSPath): the composition roots that
// need it (the validator driver service used both by the FSC/DI runtime and by the standalone
// Fabric chaincode process) only ever have public parameters in scope, never a TMS identifier.
var ResourceLimitsPath = config.Join(RootKey, "validation", "limits")

// resourceLimitsConfig is the yaml-facing shape of ResourceLimitsPath. Any field left unset (its
// zero value) is replaced by the corresponding driver.DefaultResourceLimits() value.
type resourceLimitsConfig struct {
	MaxRequestBytes   int `yaml:"maxRequestBytes,omitempty"`
	MaxActions        int `yaml:"maxActions,omitempty"`
	MaxSignatures     int `yaml:"maxSignatures,omitempty"`
	MaxSignatureBytes int `yaml:"maxSignatureBytes,omitempty"`
	MaxActionBytes    int `yaml:"maxActionBytes,omitempty"`

	MaxInputs             int `yaml:"maxInputs,omitempty"`
	MaxOutputs            int `yaml:"maxOutputs,omitempty"`
	MaxMetadataEntries    int `yaml:"maxMetadataEntries,omitempty"`
	MaxMetadataKeyBytes   int `yaml:"maxMetadataKeyBytes,omitempty"`
	MaxMetadataValueBytes int `yaml:"maxMetadataValueBytes,omitempty"`

	MaxProofBytes int `yaml:"maxProofBytes,omitempty"`
}

func (c resourceLimitsConfig) toResourceLimits() driver.ResourceLimits {
	return driver.ResourceLimits{
		MaxRequestBytes:   c.MaxRequestBytes,
		MaxActions:        c.MaxActions,
		MaxSignatures:     c.MaxSignatures,
		MaxSignatureBytes: c.MaxSignatureBytes,
		MaxActionBytes:    c.MaxActionBytes,

		MaxInputs:             c.MaxInputs,
		MaxOutputs:            c.MaxOutputs,
		MaxMetadataEntries:    c.MaxMetadataEntries,
		MaxMetadataKeyBytes:   c.MaxMetadataKeyBytes,
		MaxMetadataValueBytes: c.MaxMetadataValueBytes,

		MaxProofBytes: c.MaxProofBytes,
	}
}

// ResourceLimitsProvider resolves driver.ResourceLimits from key ResourceLimitsPath, overlaying
// driver.DefaultResourceLimits() onto any field the configuration leaves unset. It is the
// config-service-backed implementation of driver.ResourceLimitsProvider used by the FSC/DI
// runtime.
type ResourceLimitsProvider struct {
	cp Provider
}

// NewResourceLimitsProvider returns a driver.ResourceLimitsProvider backed by the passed
// configuration provider.
func NewResourceLimitsProvider(cp Provider) *ResourceLimitsProvider {
	return &ResourceLimitsProvider{cp: cp}
}

// ResourceLimits implements driver.ResourceLimitsProvider.
func (p *ResourceLimitsProvider) ResourceLimits() (driver.ResourceLimits, error) {
	c := resourceLimitsConfig{}
	if p.cp.IsSet(ResourceLimitsPath) {
		if err := p.cp.UnmarshalKey(ResourceLimitsPath, &c); err != nil {
			return driver.ResourceLimits{}, errors.Wrapf(err, "invalid configuration for key [%s]", ResourceLimitsPath)
		}
	}

	return c.toResourceLimits().WithDefaults(), nil
}
