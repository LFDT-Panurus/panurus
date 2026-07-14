/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto/pkcs11"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto/protos-go/v1/config"
	"github.com/go-viper/mapstructure/v2"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
)

type (
	Config              = config.Config
	SigningIdentityInfo = config.SigningIdentityInfo
	KeyInfo             = config.KeyInfo
	CryptoConfig        = config.CryptoConfig
)

func UnmarshalConfig(data []byte) (*Config, error) {
	config := &Config{}
	if err := proto.Unmarshal(data, config); err != nil {
		return nil, errors.Wrapf(err, "failed to unamrshal config")
	}

	return config, nil
}

func MarshalConfig(config *Config) ([]byte, error) {
	data, err := proto.Marshal(config)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to marshal config")
	}

	return data, nil
}

type Opts struct {
	BCCSP *BCCSP `yaml:"BCCSP,omitempty"`
}

type BCCSP struct {
	Default string            `yaml:"Default,omitempty"`
	SW      *SoftwareProvider `yaml:"SW,omitempty"`
	PKCS11  *PKCS11           `yaml:"PKCS11,omitempty"`
}

type SoftwareProvider struct {
	Hash     string `yaml:"Hash,omitempty"`
	Security int    `yaml:"Security,omitempty"`
}

type PKCS11 struct {
	// Default algorithms when not specified (Deprecated?)
	Security int    `yaml:"Security"`
	Hash     string `yaml:"Hash"`

	// PKCS11 options
	Library          string         `yaml:"Library"`
	Label            string         `yaml:"Label"`
	Pin              string         `yaml:"Pin"`
	SoftwareVerify   bool           `yaml:"SoftwareVerify,omitempty"`
	Immutable        bool           `yaml:"Immutable,omitempty"`
	AltID            string         `yaml:"AltId,omitempty"`
	KeyIDs           []KeyIDMapping `mapstructure:"KeyIds"             yaml:"KeyIds,omitempty"`
	SessionCacheSize uint           `yaml:"SessionCacheSize,omitempty"`
}

type KeyIDMapping struct {
	SKI string `yaml:"SKI,omitempty"`
	ID  string `yaml:"ID,omitempty"`
}

// ToBCCSPOpts converts the passed opts to `config.BCCSP`
func ToBCCSPOpts(boxed any) (*BCCSP, error) {
	opts := &Opts{}
	config := &mapstructure.DecoderConfig{
		WeaklyTypedInput: true, // allow pin to be a string
		Result:           &opts,
	}

	decoder, err := mapstructure.NewDecoder(config)
	if err != nil {
		return opts.BCCSP, err
	}

	err = decoder.Decode(boxed)

	return opts.BCCSP, err
}

func ToPKCS11OptsOpts(o *PKCS11) *pkcs11.PKCS11Opts {
	res := &pkcs11.PKCS11Opts{
		Security:         o.Security,
		Hash:             o.Hash,
		Library:          o.Library,
		Label:            o.Label,
		Pin:              o.Pin,
		SoftwareVerify:   o.SoftwareVerify,
		Immutable:        o.Immutable,
		AltID:            o.AltID,
		SessionCacheSize: o.SessionCacheSize,
	}
	for _, d := range o.KeyIDs {
		res.KeyIDs = append(res.KeyIDs, pkcs11.KeyIDMapping{
			SKI: d.SKI,
			ID:  d.ID,
		})
	}

	return res
}

// BCCSPOpts returns a `BCCSP` instance. `defaultProvider` sets the `Default` value of the BCCSP,
// that is denoting the which provider impl is used. `defaultProvider` currently supports `SW` and `PKCS11`.
func BCCSPOpts(defaultProvider string) (*BCCSP, error) {
	bccsp := &BCCSP{
		Default: defaultProvider,
		SW: &SoftwareProvider{
			Hash:     "SHA2",
			Security: 256,
		},
		PKCS11: &PKCS11{
			Hash:     "SHA2",
			Security: 256,
		},
	}
	if defaultProvider == "PKCS11" {
		lib, pin, label, err := pkcs11.FindPKCS11Lib()
		if err != nil {
			return nil, errors.Wrapf(err, "faild to find PKCS11 lib [%s]", defaultProvider)
		}
		bccsp.PKCS11.Pin = pin
		bccsp.PKCS11.Label = label
		bccsp.PKCS11.Library = lib
	}

	return bccsp, nil
}
