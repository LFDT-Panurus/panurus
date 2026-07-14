/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type Config interface {
	TranslatePath(path string) string
	UnmarshalKey(key string, rawVal any) error
}

type Wallets struct {
	DefaultCacheSize int                         `yaml:"defaultCacheSize,omitempty"`
	Certifiers       []driver.ConfiguredIdentity `yaml:"certifiers,omitempty"`
	Owners           []driver.ConfiguredIdentity `yaml:"owners,omitempty"`
	Issuers          []driver.ConfiguredIdentity `yaml:"issuers,omitempty"`
	Auditors         []driver.ConfiguredIdentity `yaml:"auditors,omitempty"`
}

type IdentityConfig struct {
	Config  Config
	Wallets *Wallets
}

func NewIdentityConfig(config Config) (*IdentityConfig, error) {
	wallets := &Wallets{}
	if err := config.UnmarshalKey("wallets", wallets); err != nil {
		return nil, errors.Wrap(err, "failed unmarshalling wallets")
	}

	return &IdentityConfig{Config: config, Wallets: wallets}, nil
}

func (i *IdentityConfig) CacheSizeForOwnerID(id string) int {
	for _, owner := range i.Wallets.Owners {
		if owner.ID == id {
			if owner.CacheSize <= 0 {
				return i.Wallets.DefaultCacheSize
			}

			return owner.CacheSize
		}
	}

	return i.Wallets.DefaultCacheSize
}

func (i *IdentityConfig) DefaultCacheSize() int {
	return i.Wallets.DefaultCacheSize
}

func (i *IdentityConfig) TranslatePath(path string) string {
	return i.Config.TranslatePath(path)
}

func (i *IdentityConfig) IdentitiesForRole(role driver.IdentityRoleType) ([]driver.ConfiguredIdentity, error) {
	switch role {
	case driver.IssuerRole:
		return i.Wallets.Issuers, nil
	case driver.AuditorRole:
		return i.Wallets.Auditors, nil
	case driver.OwnerRole:
		return i.Wallets.Owners, nil
	case driver.CertifierRole:
		return i.Wallets.Certifiers, nil
	default:
		return nil, errors.Errorf("unknown role [%d]", role)
	}
}
