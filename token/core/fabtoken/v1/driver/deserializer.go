/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"github.com/LFDT-Panurus/panurus/token/core/common"
	v1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	"github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	htlc2 "github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Deserializer deserializes verifiers associated with issuers, owners, and auditors for the fabtoken driver.
type Deserializer struct {
	*common.Deserializer
}

// NewDeserializer returns a new deserializer for fabtoken.
func NewDeserializer() *Deserializer {
	des := deserializer.NewTypedVerifierDeserializerMultiplex()
	des.AddTypedVerifierDeserializer(x509.IdentityType, deserializer.NewTypedIdentityVerifierDeserializer(&x509.IdentityDeserializer{}, &x509.AuditMatcherDeserializer{}))
	des.AddTypedVerifierDeserializer(htlc2.ScriptType, htlc.NewTypedIdentityDeserializer(des))
	des.AddTypedVerifierDeserializer(multisig.Multisig, multisig.NewTypedIdentityDeserializer(des, des))
	des.AddTypedVerifierDeserializer(boolpolicy.Policy, boolpolicy.NewTypedIdentityDeserializer(des, des))

	return &Deserializer{Deserializer: common.NewDeserializer(des, des, des, des, des)}
}

// PublicParamsDeserializer deserializes fabtoken public parameters.
type PublicParamsDeserializer struct{}

// DeserializePublicParams deserializes the passed bytes into fabtoken public parameters.
func (p *PublicParamsDeserializer) DeserializePublicParams(raw []byte, name driver.TokenDriverName, version driver.TokenDriverVersion) (*v1.PublicParams, error) {
	return v1.NewPublicParamsFromBytes(raw, name, version)
}

// EIDRHDeserializer returns enrollment ID and revocation handle behind the owners of token.
type EIDRHDeserializer = deserializer.EIDRHDeserializer

// NewEIDRHDeserializer returns a new EIDRHDeserializer for fabtoken.
func NewEIDRHDeserializer() *EIDRHDeserializer {
	d := deserializer.NewEIDRHDeserializer()
	d.AddDeserializer(x509.IdentityType, &x509.AuditInfoDeserializer{})
	d.AddDeserializer(htlc2.ScriptType, htlc.NewAuditDeserializer(d))
	d.AddDeserializer(multisig.Multisig, &multisig.AuditInfoDeserializer{})
	d.AddDeserializer(boolpolicy.Policy, &boolpolicy.AuditInfoDeserializer{})

	return d
}

// PublicParametersDeserializer contains the logic to deserialize public parameters
type PublicParametersDeserializer struct{}

// PublicParametersFromBytes unmarshals the passed bytes into fabtoken public parameters.
func (d PublicParametersDeserializer) PublicParametersFromBytes(params []byte) (driver.PublicParameters, error) {
	pp, err := v1.NewPublicParamsFromBytes(params, v1.FabTokenDriverName, v1.ProtocolV1)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal public parameters")
	}

	return pp, nil
}
