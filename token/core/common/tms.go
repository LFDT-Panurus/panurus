/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
)

// SignatureInstrumentation bundles the observability and policy a driver installs on a token
// service: the observer its signature operations are reported to, and the gate that may deny
// them. It is an interface so that this package does not depend on the policy implementation;
// sigpolicy.Stack is what a driver actually passes.
type SignatureInstrumentation interface {
	// Observer returns the observer signature operations are reported to.
	Observer() sigobserve.Observer
	// Gate returns the gate consulted before a client-facing signature operation, or nil when
	// no policy is active.
	Gate() sigobserve.Gate
	// Stop releases the resources held by the instrumentation.
	Stop()
}

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

	signatureInstrumentation SignatureInstrumentation
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

// SetSignatureInstrumentation installs the signature observability and policy bundle. It is a
// setter rather than a constructor parameter because the bundle is assembled together with the
// wallet service, before this service exists, and because a driver that installs none must keep
// working unchanged.
func (s *Service[T]) SetSignatureInstrumentation(si SignatureInstrumentation) {
	s.signatureInstrumentation = si
}

// SignatureObserver returns the observer the client-facing signature service reports denials to,
// or a no-op observer when no instrumentation is installed.
func (s *Service[T]) SignatureObserver() sigobserve.Observer {
	if s.signatureInstrumentation == nil {
		return sigobserve.Nop
	}

	return s.signatureInstrumentation.Observer()
}

// SignatureGate returns the gate the client-facing signature service consults, or nil when no
// policy is active.
func (s *Service[T]) SignatureGate() sigobserve.Gate {
	if s.signatureInstrumentation == nil {
		return nil
	}

	return s.signatureInstrumentation.Gate()
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
	if s.signatureInstrumentation != nil {
		s.signatureInstrumentation.Stop()
	}

	// call done on all the services that support it
	if s.walletService != nil {
		return s.walletService.Done()
	}

	return nil
}
