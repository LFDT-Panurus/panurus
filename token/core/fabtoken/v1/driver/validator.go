/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"github.com/LFDT-Panurus/panurus/token/core"
	v1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ValidatorDriver is the implementation of the ValidatorDriver
type ValidatorDriver struct {
	PublicParametersDeserializer
}

// NewValidatorDriver returns a new factory for the fabtoken validator driver.
func NewValidatorDriver() core.NamedFactory[driver.ValidatorDriver] {
	return core.NamedFactory[driver.ValidatorDriver]{
		Name:   core.DriverIdentifier(v1.FabTokenDriverName, v1.ProtocolV1),
		Driver: ValidatorDriver{},
	}
}

func (d ValidatorDriver) NewValidator(pp driver.PublicParameters) (driver.Validator, error) {
	if err := pp.Validate(); err != nil {
		return nil, errors.Wrapf(err, "failed validating public parameters")
	}
	ppp, ok := pp.(*v1.PublicParams)
	if !ok {
		return nil, errors.Errorf("invalid public parameters type [%T]", pp)
	}
	logger := logging.DriverLoggerFromPP("panurus.driver.fabtoken", string(core.DriverIdentifierFromPP(pp)))
	deserializer := NewDeserializer()

	return validator.NewValidator(logger, ppp, deserializer, nil, nil, nil), nil
}
