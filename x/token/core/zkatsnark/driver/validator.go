/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"github.com/LFDT-Panurus/panurus/token/core"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/validator"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ValidatorDriver contains the static logic of the zkatsnark driver.
type ValidatorDriver struct {
	PublicParametersDeserializer
}

// NewValidatorDriver returns a new factory for the zkatsnark validator driver.
func NewValidatorDriver() core.NamedFactory[driver.ValidatorDriver] {
	return core.NamedFactory[driver.ValidatorDriver]{
		Name:   core.DriverIdentifier(driver.TokenDriverName("zkatsnark"), driver.TokenDriverVersion(1)),
		Driver: ValidatorDriver{},
	}
}

// NewValidator returns a new zkatsnark validator for the passed public parameters.
func (d ValidatorDriver) NewValidator(params driver.PublicParameters) (driver.Validator, error) {
	ppp, ok := params.(*pp.PublicParams)
	if !ok {
		return nil, errors.Errorf("invalid public parameters type [%T]", params)
	}
	if err := ppp.Validate(); err != nil {
		return nil, errors.Wrapf(err, "failed validating public parameters")
	}
	// Returns the validator for zkatsnark
	return validator.NewValidator(ppp)
}
