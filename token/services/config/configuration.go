/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"fmt"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	"go.yaml.in/yaml/v3"
)

const (
	Network   = "network"
	Channel   = "channel"
	Namespace = "namespace"
)

// Configuration is the configuration of a given configuration
type Configuration struct {
	cp         Provider
	keyID      string
	tmsID      driver.TMSID
	validators []Validator
}

func NewConfiguration(cp Provider, keyID string, tmsID driver.TMSID) *Configuration {
	return &Configuration{
		cp:    cp,
		keyID: keyID,
		tmsID: tmsID,
	}
}

func (m *Configuration) Validate() error {
	// check TMS ID
	if len(m.tmsID.Network) == 0 {
		return errors.New("configuration error: missing required field 'network'")
	}
	if len(m.tmsID.Namespace) == 0 {
		return errors.New("configuration error: missing required field 'namespace'")
	}

	for _, validator := range m.validators {
		if err := validator.Validate(m); err != nil {
			return err
		}
	}

	return nil
}

// SetValidators sets the validators for this configuration.
func (m *Configuration) SetValidators(validators []Validator) {
	m.validators = validators
}

func (m *Configuration) ID() driver.TMSID {
	return m.tmsID
}

// TranslatePath translates the passed path relative to the config path
func (m *Configuration) TranslatePath(path string) string {
	return m.cp.TranslatePath(path)
}

// UnmarshalKey takes a single key and unmarshals it into a Struct
func (m *Configuration) UnmarshalKey(key string, rawVal any) error {
	return m.cp.UnmarshalKey(config.Join(TMSPath, m.keyID, key), rawVal)
}

func (m *Configuration) GetString(key string) string {
	return m.cp.GetString(config.Join(TMSPath, m.keyID, key))
}

func (m *Configuration) GetBool(key string) bool {
	return m.cp.GetBool(config.Join(TMSPath, m.keyID, key))
}

func (m *Configuration) IsSet(key string) bool {
	return m.cp.IsSet(config.Join(TMSPath, m.keyID, key))
}

// Serialize serializes this configuration with the respect to the passed tms ID
func (m *Configuration) Serialize(tmsID token.TMSID) ([]byte, error) {
	keyID := fmt.Sprintf("%s%s%s", tmsID.Network, tmsID.Channel, tmsID.Namespace)
	keys := map[string]any{}
	if err := m.cp.UnmarshalKey(config.Join(TMSPath, m.keyID), &keys); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling key [%s]", config.Join(TMSPath, m.keyID))
	}
	keys[Network] = tmsID.Network
	keys[Channel] = tmsID.Channel
	keys[Namespace] = tmsID.Namespace
	c := &TMSConfig{
		Token: TokenConfig{
			TMS: map[string]map[string]any{
				keyID: keys,
			},
		},
	}

	return yaml.Marshal(c)
}

// TMSConfig is the TMS configuration
type TMSConfig struct {
	Token TokenConfig `yaml:"token"`
}

// TokenConfig is used to serialize a TMS configuration
type TokenConfig struct {
	TMS map[string]map[string]any `mapstructure:"tms" yaml:"tms"`
}
