/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"github.com/LFDT-Panurus/panurus/token/core"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ValidatorDriver contains the static logic of the zkatdlog driver.
type ValidatorDriver struct {
	PublicParametersDeserializer
}

// NewValidatorDriver returns a new factory for the zkatdlog validator driver.
func NewValidatorDriver() core.NamedFactory[driver.ValidatorDriver] {
	return core.NamedFactory[driver.ValidatorDriver]{
		Name:   core.DriverIdentifier(v1.DLogNoGHDriverName, v1.ProtocolV1),
		Driver: ValidatorDriver{},
	}
}

// NewValidator returns a new zkatdlog validator for the passed public parameters.
func (d ValidatorDriver) NewValidator(pp driver.PublicParameters) (driver.Validator, error) {
	ppp, ok := pp.(*v1.PublicParams)
	if !ok {
		return nil, errors.Errorf("invalid public parameters type [%T]", pp)
	}
	if err := pp.Validate(); err != nil {
		return nil, errors.Wrapf(err, "failed validating public parameters")
	}
	deserializer, err := NewDeserializer(ppp)
	if err != nil {
		return nil, errors.Errorf("failed to create token service deserializer: %v", err)
	}
	logger := logging.DriverLoggerFromPP("panurus.driver.zkatdlog", string(pp.TokenDriverName()))

	return validator.New(
		logger,
		ppp,
		deserializer,
		nil,
		nil,
		nil,
	), nil
}
