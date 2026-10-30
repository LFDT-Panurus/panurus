/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark

import (
	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
)

// Service is the zkatsnark token manager service.
type Service struct {
	*common.Service[*pp.PublicParams]
}

// NewTokenService returns a new zkatsnark token manager service backed by the passed wallet service.
func NewTokenService(
	logger logging.Logger,
	ws driver.WalletService,
	ppm common.PublicParametersManager[*pp.PublicParams],
	identityProvider driver.IdentityProvider,
	deserializer driver.Deserializer,
	configuration driver.Configuration,
	issueService driver.IssueService,
	transferService driver.TransferService,
	auditorService driver.AuditorService,
	tokensService driver.TokensService,
	tokensUpgradeService driver.TokensUpgradeService,
	authorization driver.Authorization,
	validator driver.Validator,
) (*Service, error) {
	root, err := common.NewTokenService[*pp.PublicParams](
		logger,
		ws,
		ppm,
		identityProvider,
		deserializer,
		configuration,
		nil,
		issueService,
		transferService,
		auditorService,
		tokensService,
		tokensUpgradeService,
		authorization,
		validator,
	)
	if err != nil {
		return nil, err
	}

	s := &Service{
		Service: root,
	}

	return s, nil
}
