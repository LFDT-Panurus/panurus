/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	htlc2 "github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Deserializer deserializes verifiers associated with issuers, owners, and auditors.
type Deserializer struct {
	*common.Deserializer
}

// NewDeserializer returns a new zkatsnark deserializer.
func NewDeserializer(ppp *pp.PublicParams) (*Deserializer, error) {
	if ppp == nil {
		return nil, errors.New("failed to get deserializer: nil public parameters")
	}

	des := deserializer.NewTypedVerifierDeserializerMultiplex()

	for _, idemixIssuerPublicKey := range ppp.IdemixIssuerPublicKeys {
		idemixDes, err := idemix.NewDeserializer(idemixIssuerPublicKey.PublicKey, idemixIssuerPublicKey.Curve)
		if err != nil {
			return nil, errors.Wrapf(err, "failed getting idemix deserializer for curve [%d]", idemixIssuerPublicKey.Curve)
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

// PublicParamsDeserializer deserializes zkatsnark public parameters.
type PublicParamsDeserializer struct{}

// DeserializePublicParams deserializes the passed bytes into zkatsnark public parameters.
func (p *PublicParamsDeserializer) DeserializePublicParams(raw []byte, name driver.TokenDriverName, version driver.TokenDriverVersion) (*pp.PublicParams, error) {
	return pp.DeserializePublicParams(raw)
}

// EIDRHDeserializer returns enrollment ID and revocation handle behind the owners of token.
type EIDRHDeserializer = deserializer.EIDRHDeserializer

// NewEIDRHDeserializer returns a new zkatsnark EIDRHDeserializer.
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

// PublicParametersFromBytes unmarshals the passed bytes into zkatsnark public parameters.
func (d PublicParametersDeserializer) PublicParametersFromBytes(params []byte) (driver.PublicParameters, error) {
	ppp, err := pp.DeserializePublicParams(params)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal public parameters")
	}

	return ppp, nil
}
