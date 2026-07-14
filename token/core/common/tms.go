/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
)

// ValidatorFactory is a function that returns a driver.Validator instance.
type ValidatorFactory = func() (driver.Validator, error)

// PublicParametersManager defines an interface for managing public parameters.
type PublicParametersManager[T driver.PublicParameters] interface {
	driver.PublicParamsManager
	PublicParams() T
}

// Service is a generic implementation of a token service.
type Service[T driver.PublicParameters] struct {
	Logger                  logging.Logger
	PublicParametersManager PublicParametersManager[T]
	deserializer            driver.Deserializer
	identityProvider        driver.IdentityProvider
	configuration           driver.Configuration
	certificationService    driver.CertificationService
	walletService           driver.WalletService
	issueService            driver.IssueService
	transferService         driver.TransferService
	auditorService          driver.AuditorService
	tokensService           driver.TokensService
	tokensUpgradeService    driver.TokensUpgradeService
	authorization           driver.Authorization
	validator               driver.Validator
}

// NewTokenService returns a new token service instance for the passed arguments.
func NewTokenService[T driver.PublicParameters](
	logger logging.Logger,
	ws driver.WalletService,
	publicParametersManager PublicParametersManager[T],
	identityProvider driver.IdentityProvider,
	deserializer driver.Deserializer,
	configManager driver.Configuration,
	certificationService driver.CertificationService,
	issueService driver.IssueService,
	transferService driver.TransferService,
	auditorService driver.AuditorService,
	tokensService driver.TokensService,
	tokensUpgradeService driver.TokensUpgradeService,
	authorization driver.Authorization,
	validator driver.Validator,
) (*Service[T], error) {
	s := &Service[T]{
		Logger:                  logger,
		PublicParametersManager: publicParametersManager,
		identityProvider:        identityProvider,
		deserializer:            deserializer,
		configuration:           configManager,
		certificationService:    certificationService,
		walletService:           ws,
		issueService:            issueService,
		transferService:         transferService,
		auditorService:          auditorService,
		tokensService:           tokensService,
		tokensUpgradeService:    tokensUpgradeService,
		authorization:           authorization,
		validator:               validator,
	}

	return s, nil
}

// IdentityProvider returns the identity provider associated with the service.
func (s *Service[T]) IdentityProvider() driver.IdentityProvider {
	return s.identityProvider
}

// Deserializer returns the deserializer associated with the service.
func (s *Service[T]) Deserializer() driver.Deserializer {
	return s.deserializer
}

// CertificationService returns the certification service associated with the service.
func (s *Service[T]) CertificationService() driver.CertificationService {
	return s.certificationService
}

// PublicParamsManager returns the manager of the public parameters associated with the service.
func (s *Service[T]) PublicParamsManager() driver.PublicParamsManager {
	return s.PublicParametersManager
}

// Configuration returns the configuration manager associated with the service.
func (s *Service[T]) Configuration() driver.Configuration {
	return s.configuration
}

// WalletService returns the wallet service associated with the service.
func (s *Service[T]) WalletService() driver.WalletService {
	return s.walletService
}

// IssueService returns the issue service associated with the service.
func (s *Service[T]) IssueService() driver.IssueService {
	return s.issueService
}

// TransferService returns the transfer service associated with the service.
func (s *Service[T]) TransferService() driver.TransferService {
	return s.transferService
}

// AuditorService returns the auditor service associated with the service.
func (s *Service[T]) AuditorService() driver.AuditorService {
	return s.auditorService
}

// TokensService returns the tokens service associated with the service.
func (s *Service[T]) TokensService() driver.TokensService {
	return s.tokensService
}

// TokensUpgradeService returns the tokens upgrade service associated with the service.
func (s *Service[T]) TokensUpgradeService() driver.TokensUpgradeService {
	return s.tokensUpgradeService
}

// Authorization returns the authorization service associated with the service.
func (s *Service[T]) Authorization() driver.Authorization {
	return s.authorization
}

// Validator returns the validator associated with the service.
func (s *Service[T]) Validator() (driver.Validator, error) {
	return s.validator, nil
}

// Done releases all the resources allocated by this service.
func (s *Service[T]) Done() error {
	// call done on all the services that support it
	if s.walletService != nil {
		return s.walletService.Done()
	}

	return nil
}
