/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"github.com/LFDT-Panurus/panurus/token/core"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// PPMFactory is a factory for creating zkatsnark public parameters managers.
type PPMFactory struct{ ValidatorDriver }

// NewPPMFactory returns a new factory for the zkatsnark public parameters manager.
func NewPPMFactory() core.NamedFactory[driver.PPMFactory] {
	return core.NamedFactory[driver.PPMFactory]{
		Name:   core.DriverIdentifier(driver.TokenDriverName("zkatsnark"), driver.TokenDriverVersion(1)),
		Driver: &PPMFactory{},
	}
}

// NewPublicParametersManager returns a new zkatsnark public parameters manager for the passed public parameters.
func (d *PPMFactory) NewPublicParametersManager(params driver.PublicParameters) (driver.PublicParamsManager, error) {
	ppp, ok := params.(*pp.PublicParams)
	if !ok {
		return nil, errors.Errorf("invalid public parameters type [%T]", params)
	}

	return common.NewPublicParamsManagerFromParams[*pp.PublicParams](ppp)
}
