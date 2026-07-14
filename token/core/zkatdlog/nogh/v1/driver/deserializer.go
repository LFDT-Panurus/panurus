/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"github.com/LFDT-Panurus/panurus/token/core/common"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	htlc2 "github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Deserializer deserializes verifiers associated with issuers, owners, and auditors.
type Deserializer struct {
	*common.Deserializer
}

// NewDeserializer returns a new zkatdlog deserializer.
func NewDeserializer(pp *v1.PublicParams) (*Deserializer, error) {
	if pp == nil {
		return nil, errors.New("failed to get deserializer: nil public parameters")
	}

	des := deserializer.NewTypedVerifierDeserializerMultiplex()
	for _, idemixIssuerPublicKey := range pp.IdemixIssuerPublicKeys {
		idemixDes, err := idemix.NewDeserializer(idemixIssuerPublicKey.PublicKey, idemixIssuerPublicKey.Curve)
		if err != nil {
			return nil, errors.Wrapf(err, "failed getting idemix deserializer for passed public params [%d]", idemixIssuerPublicKey.Curve)
		}
		des.AddTypedVerifierDeserializer(idemix.IdentityType, deserializer.NewTypedIdentityVerifierDeserializer(idemixDes, idemixDes))

		idemixNymDes := idemixnym.NewDeserializer(idemixDes)
		des.AddTypedVerifierDeserializer(idemixnym.IdentityType, deserializer.NewTypedIdentityVerifierDeserializer(idemixNymDes, idemixNymDes))
	}
	des.AddTypedVerifierDeserializer(x509.IdentityType, deserializer.NewTypedIdentityVerifierDeserializer(&x509.IdentityDeserializer{}, &x509.AuditMatcherDeserializer{}))
	des.AddTypedVerifierDeserializer(htlc2.ScriptType, htlc.NewTypedIdentityDeserializer(des))
	des.AddTypedVerifierDeserializer(multisig.Multisig, multisig.NewTypedIdentityDeserializer(des, des))
	des.AddTypedVerifierDeserializer(boolpolicy.Policy, boolpolicy.NewTypedIdentityDeserializer(des, des))

	return &Deserializer{Deserializer: common.NewDeserializer(des, des, des, des, des)}, nil
}

// TokenDeserializer deserializes zkatdlog tokens and metadata.
type TokenDeserializer struct{}

// DeserializeMetadata deserializes the passed bytes into a zkatdlog token metadata.
func (d *TokenDeserializer) DeserializeMetadata(raw []byte) (*token.Metadata, error) {
	metadata := &token.Metadata{}
	if err := metadata.Deserialize(raw); err != nil {
		return nil, err
	}

	return metadata, nil
}

// DeserializeToken deserializes the passed bytes into a zkatdlog token.
func (d *TokenDeserializer) DeserializeToken(raw []byte) (*token.Token, error) {
	token := &token.Token{}
	if err := token.Deserialize(raw); err != nil {
		return nil, err
	}

	return token, nil
}

// PublicParamsDeserializer deserializes zkatdlog public parameters.
type PublicParamsDeserializer struct{}

// DeserializePublicParams deserializes the passed bytes into zkatdlog public parameters.
func (p *PublicParamsDeserializer) DeserializePublicParams(raw []byte, name driver.TokenDriverName, version driver.TokenDriverVersion) (*v1.PublicParams, error) {
	return v1.NewPublicParamsFromBytes(raw, name, version)
}

// EIDRHDeserializer returns enrollment ID and revocation handle behind the owners of token.
type EIDRHDeserializer = deserializer.EIDRHDeserializer

// NewEIDRHDeserializer returns a new zkatdlog EIDRHDeserializer.
func NewEIDRHDeserializer() *EIDRHDeserializer {
	d := deserializer.NewEIDRHDeserializer()
	d.AddDeserializer(idemix.IdentityType, &idemix.AuditInfoDeserializer{})
	d.AddDeserializer(idemixnym.IdentityType, &idemixnym.AuditInfoDeserializer{})
	d.AddDeserializer(x509.IdentityType, &x509.AuditInfoDeserializer{})
	d.AddDeserializer(htlc2.ScriptType, htlc.NewAuditDeserializer(d))
	d.AddDeserializer(multisig.Multisig, &multisig.AuditInfoDeserializer{})
	d.AddDeserializer(boolpolicy.Policy, &boolpolicy.AuditInfoDeserializer{})

	return d
}

// PublicParametersDeserializer contains the logic to deserialize public parameters
type PublicParametersDeserializer struct{}

// PublicParametersFromBytes unmarshals the passed bytes into zkatdlog public parameters.
func (d PublicParametersDeserializer) PublicParametersFromBytes(params []byte) (driver.PublicParameters, error) {
	pp, err := v1.NewPublicParamsFromBytes(params, v1.DLogNoGHDriverName, v1.ProtocolV1)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal public parameters")
	}

	return pp, nil
}
