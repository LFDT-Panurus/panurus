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
// When pp carries x509 owner roots, x509 owners must chain to them to match their audit info.
func NewDeserializer(pp *v1.PublicParams) (*Deserializer, error) {
	checker, err := newOwnerChainChecker(pp)
	if err != nil {
		return nil, err
	}
	des := deserializer.NewTypedVerifierDeserializerMultiplex()
	des.AddTypedVerifierDeserializer(x509.IdentityType, deserializer.NewTypedIdentityVerifierDeserializer(&x509.IdentityDeserializer{}, &x509.AuditMatcherDeserializer{Checker: checker}))
	des.AddTypedVerifierDeserializer(htlc2.ScriptType, htlc.NewTypedIdentityDeserializer(des))
	des.AddTypedVerifierDeserializer(multisig.Multisig, multisig.NewTypedIdentityDeserializer(des, des))
	des.AddTypedVerifierDeserializer(boolpolicy.Policy, boolpolicy.NewTypedIdentityDeserializer(des, des))

	return &Deserializer{Deserializer: common.NewDeserializer(des, des, des, des, des)}, nil
}

// newOwnerChainChecker returns the x509 owner chain checker of pp, nil if pp has no x509 owner roots.
// The issuers and auditor of pp are exempt.
func newOwnerChainChecker(pp *v1.PublicParams) (*x509.OwnerChainChecker, error) {
	if pp == nil {
		return nil, errors.New("nil public parameters")
	}
	exempt := append(append([]driver.Identity{}, pp.Issuers()...), pp.Auditors()...)
	checker, err := x509.NewOwnerChainChecker(pp.X509OwnerRoots, pp.X509OwnerIntermediates, exempt)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to build the x509 owner chain checker")
	}

	return checker, nil
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
// When pp carries x509 owner roots, x509 owners must chain to them and their audit info must be bound to them.
func NewEIDRHDeserializer(pp *v1.PublicParams) (*EIDRHDeserializer, error) {
	checker, err := newOwnerChainChecker(pp)
	if err != nil {
		return nil, err
	}
	d := deserializer.NewEIDRHDeserializer()
	d.AddDeserializer(x509.IdentityType, &x509.AuditInfoDeserializer{Checker: checker})
	d.AddDeserializer(htlc2.ScriptType, htlc.NewAuditDeserializer(d))
	d.AddDeserializer(multisig.Multisig, multisig.NewAuditInfoDeserializer(d))
	d.AddDeserializer(boolpolicy.Policy, boolpolicy.NewAuditInfoDeserializer(d))

	return d, nil
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
