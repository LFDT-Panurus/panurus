/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package core

import (
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/pp"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Config defines the configuration interface for a Token Management Service (TMS).
//
//go:generate counterfeiter -o mock/config.go -fake-name Config . Config
type Config interface {
	// ID returns the TMS identifier.
	ID() driver.TMSID
	// TranslatePath translates the passed path relative to the config path.
	TranslatePath(path string) string
	// UnmarshalKey unmarshals the configuration value associated with the key into rawVal.
	UnmarshalKey(key string, rawVal any) error
}

// NamedFactory associates a token driver identifier with its corresponding driver factory.
type NamedFactory[T any] struct {
	Name   TokenDriverIdentifier
	Driver T
}

// factoryDirectory maintains a map of token driver identifiers to their respective factories.
// It provides common functionality for retrieving public parameters from bytes.
type factoryDirectory[T driver.PPReader] struct {
	factories map[TokenDriverIdentifier]T
}

// newFactoryDirectory creates a new factoryDirectory from the provided named factories.
func newFactoryDirectory[T driver.PPReader](fs ...NamedFactory[T]) *factoryDirectory[T] {
	factories := make(map[TokenDriverIdentifier]T, len(fs))
	for _, f := range fs {
		factories[f.Name] = f.Driver
	}

	return &factoryDirectory[T]{factories: factories}
}

// PublicParametersFromBytes unmarshals the bytes to a driver.PublicParameters instance.
// The passed bytes are expected to encode a driver.SerializedPublicParameters instance.
// If no driver is registered for the public params' identifier, it returns an error.
// It is responsibility of the caller to validate the unmarshalled public parameters.
func (s *factoryDirectory[T]) PublicParametersFromBytes(params []byte) (driver.PublicParameters, error) {
	publicParams, err := serializedPublicParametersFromBytes(params)
	if err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling public params")
	}

	if f, ok := s.factories[TokenDriverIdentifier(publicParams.Identifier)]; ok {
		return f.PublicParametersFromBytes(params)
	}

	return nil, errors.Errorf("cannot load public parameters, driver [%s] not found", publicParams.Identifier)
}

// serializedPublicParametersFromBytes returns a driver.SerializedPublicParameters instance from the passed bytes.
func serializedPublicParametersFromBytes(raw []byte) (*pp.PublicParameters, error) {
	publicParams := &pp.PublicParameters{}
	if err := json.Unmarshal(raw, publicParams); err != nil {
		return nil, errors.Wrap(err, "failed deserializing public parameters")
	}

	return publicParams, nil
}

// PPManagerFactoryService manages factories for creating public parameters managers and validators.
type PPManagerFactoryService struct {
	*factoryDirectory[driver.PPMFactory]
}

// NewPPManagerFactoryService creates a new PPManagerFactoryService with the provided factories.
func NewPPManagerFactoryService(instantiators ...NamedFactory[driver.PPMFactory]) *PPManagerFactoryService {
	return &PPManagerFactoryService{factoryDirectory: newFactoryDirectory(instantiators...)}
}

// NewPublicParametersManager returns a new instance of driver.PublicParamsManager for the passed parameters.
// If no driver is registered for the public params' identifier, it returns an error.
func (s *PPManagerFactoryService) NewPublicParametersManager(pp driver.PublicParameters) (driver.PublicParamsManager, error) {
	if instantiator, ok := s.factories[DriverIdentifierFromPP(pp)]; ok {
		return instantiator.NewPublicParametersManager(pp)
	}

	return nil, errors.Errorf("cannot load public parameters, driver [%s] not found", DriverIdentifierFromPP(pp))
}

// WalletServiceFactoryService manages factories for creating wallet services.
type WalletServiceFactoryService struct {
	*factoryDirectory[driver.WalletServiceFactory]
}

// NewWalletServiceFactoryService creates a new WalletServiceFactoryService with the provided factories.
func NewWalletServiceFactoryService(fs ...NamedFactory[driver.WalletServiceFactory]) *WalletServiceFactoryService {
	return &WalletServiceFactoryService{factoryDirectory: newFactoryDirectory(fs...)}
}

// NewWalletService returns a new instance of driver.WalletService for the passed configuration and public parameters.
// If no driver is registered for the public params' identifier, it returns an error.
func (s *WalletServiceFactoryService) NewWalletService(tmsConfig driver.Configuration, ppRaw []byte) (driver.WalletService, error) {
	publicParams, err := s.PublicParametersFromBytes(ppRaw)
	if err != nil {
		return nil, err
	}
	if factory, ok := s.factories[DriverIdentifierFromPP(publicParams)]; ok {
		return factory.NewWalletService(tmsConfig, publicParams)
	}

	return nil, errors.Errorf("no validator found for token driver [%s]", DriverIdentifierFromPP(publicParams))
}

// TMSDriverService manages factories for creating token manager services.
type TMSDriverService struct {
	*factoryDirectory[driver.Driver]
}

// NewTokenDriverService creates a new TMSDriverService with the provided factories.
func NewTokenDriverService(factories []NamedFactory[driver.Driver]) *TMSDriverService {
	return &TMSDriverService{factoryDirectory: newFactoryDirectory(factories...)}
}

// NewTokenService returns a new instance of driver.TokenManagerService for the passed TMSID and public parameters.
// If no driver is registered for the public params' identifier, it returns an error.
func (s *TMSDriverService) NewTokenService(tmsID driver.TMSID, publicParamsRaw []byte) (driver.TokenManagerService, error) {
	publicParams, err := s.PublicParametersFromBytes(publicParamsRaw)
	if err != nil {
		return nil, err
	}
	if d, ok := s.factories[DriverIdentifierFromPP(publicParams)]; ok {
		return d.NewTokenService(tmsID, publicParamsRaw)
	}

	return nil, errors.Errorf("no token driver named '%s' found", DriverIdentifierFromPP(publicParams))
}

// TokenDriverService is an alias for TMSDriverService for backward compatibility.
type TokenDriverService = TMSDriverService

// ValidatorDriverService manages factories for creating validators.
type ValidatorDriverService struct {
	*factoryDirectory[driver.ValidatorDriver]
}

// NewValidatorDriverService creates a new ValidatorDriverService with the provided factories.
func NewValidatorDriverService(factories ...NamedFactory[driver.ValidatorDriver]) *ValidatorDriverService {
	return &ValidatorDriverService{factoryDirectory: newFactoryDirectory(factories...)}
}

// NewValidator returns a new instance of driver.Validator for the passed public parameters.
// If no driver is registered for the public params' identifier, it returns an error.
func (s *ValidatorDriverService) NewValidator(pp driver.PublicParameters) (driver.Validator, error) {
	if d, ok := s.factories[DriverIdentifierFromPP(pp)]; ok {
		return d.NewValidator(pp)
	}

	return nil, errors.Errorf("no validator found for token driver [%s]", DriverIdentifierFromPP(pp))
}
