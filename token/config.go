/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
)

// Configuration manages the configuration of Panurus
type Configuration struct {
	cm driver.Configuration
}

// NewConfiguration returns a new instance of Configuration
func NewConfiguration(cm driver.Configuration) *Configuration {
	return &Configuration{cm: cm}
}

// IsSet checks to see if the key has been set in any of the data locations
func (m *Configuration) IsSet(key string) bool {
	if m.cm == nil {
		return false
	}

	return m.cm.IsSet(key)
}

// UnmarshalKey takes a single key and unmarshals it into a Struct
func (m *Configuration) UnmarshalKey(key string, rawVal any) error {
	return m.cm.UnmarshalKey(key, rawVal)
}

// GetValidationConfig returns the validation configuration
func (m *Configuration) GetValidationConfig() (driver.ValidationConfig, error) {
	config := driver.DefaultValidationConfig
	if m.cm != nil && m.cm.IsSet("validation") {
		if err := m.cm.UnmarshalKey("validation", &config); err != nil {
			return config, err
		}
	}

	return config, nil
}
