//go:build !pkcs11

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pkcs11

import (
	"fmt"

	"github.com/hyperledger/fabric-lib-go/bccsp"
)

type KeyIDMapping struct {
	SKI string `yaml:"SKI,omitempty"`
	ID  string `yaml:"ID,omitempty"`
}

type PKCS11Opts struct {
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

// String renders the PKCS11 With redacted Pin,
// So interpolated (with %v/%s/%+v) doesn't reach a log line.
func (o PKCS11Opts) String() string {
	return fmt.Sprintf(
		"{Security:%d Hash:%s Library:%s Label:%s Pin:[REDACTED] SoftwareVerify:%t Immutable:%t AltID:%s KeyIDs:%v SessionCacheSize:%d}",
		o.Security, o.Hash, o.Library, o.Label, o.SoftwareVerify, o.Immutable, o.AltID, o.KeyIDs, o.SessionCacheSize,
	)
}

func NewProvider(opts any, ks bccsp.KeyStore, mapper func(ski []byte) []byte) (bccsp.BCCSP, error) {
	panic("pkcs11 not included in build. Use: go build -tags pkcs11")
}

func ToPKCS11OptsOpts(o any) *PKCS11Opts {
	panic("pkcs11 not included in build. Use: go build -tags pkcs11")
}

func FindPKCS11Lib() (lib, pin, label string, err error) {
	panic("pkcs11 not included in build. Use: go build -tags pkcs11")
}
