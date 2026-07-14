/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// PublicParamsDeserializer deserializes public parameters from their raw representation.
type PublicParamsDeserializer[T driver.PublicParameters] interface {
	DeserializePublicParams(raw []byte, name driver.TokenDriverName, version driver.TokenDriverVersion) (T, error)
}

// PublicParamsManager manages public parameters.
type PublicParamsManager[T driver.PublicParameters] struct {
	publicParameters T
	// label of the public params
	DriverName    driver.TokenDriverName
	DriverVersion driver.TokenDriverVersion
	ppHash        driver.PPHash
}

// NewPublicParamsManager returns a new PublicParamsManager instance for the passed arguments.
func NewPublicParamsManager[T driver.PublicParameters](
	PublicParamsDeserializer PublicParamsDeserializer[T],
	driverName driver.TokenDriverName,
	driverVersion driver.TokenDriverVersion,
	ppRaw []byte,
) (*PublicParamsManager[T], error) {
	ppm := &PublicParamsManager[T]{
		DriverName:    driverName,
		DriverVersion: driverVersion,
	}
	if len(ppRaw) == 0 {
		return nil, errors.Errorf("empty public parameters")
	}
	pp, err := PublicParamsDeserializer.DeserializePublicParams(ppRaw, driverName, driverVersion)
	if err != nil {
		return nil, err
	}
	if err := pp.Validate(); err != nil {
		return nil, errors.WithMessagef(err, "invalid public parameters")
	}
	if len(pp.Issuers()) == 0 {
		logger.Warnf("no issuers definied in the public parameters")
	}
	ppm.publicParameters = pp
	ppm.ppHash = utils.Hashable(ppRaw).Raw()

	return ppm, nil
}

// NewPublicParamsManagerFromParams returns a new PublicParamsManager instance for the passed public parameters.
func NewPublicParamsManagerFromParams[T driver.PublicParameters](pp T) (*PublicParamsManager[T], error) {
	if err := pp.Validate(); err != nil {
		return nil, errors.WithMessagef(err, "invalid public parameters")
	}
	if len(pp.Issuers()) == 0 {
		logger.Warnf("no issuers definied in the public parameters")
	}

	return &PublicParamsManager[T]{
		publicParameters: pp,
	}, nil
}

// PublicParameters returns the public parameters managed by this manager.
func (v *PublicParamsManager[T]) PublicParameters() driver.PublicParameters {
	return v.publicParameters
}

// NewCertifierKeyPair returns a new certifier key pair.
func (v *PublicParamsManager[T]) NewCertifierKeyPair() ([]byte, []byte, error) {
	return nil, nil, errors.Errorf("not supported")
}

// PublicParams returns the public parameters managed by this manager.
func (v *PublicParamsManager[T]) PublicParams() T {
	return v.publicParameters
}

// PublicParamsHash returns the hash of the public parameters.
func (v *PublicParamsManager[T]) PublicParamsHash() driver.PPHash {
	return v.ppHash
}
