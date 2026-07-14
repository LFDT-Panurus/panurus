/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package token provides the Token Management Service (TMS), the main entry point for token operations.
// TMS manages token requests, wallets, validators, vaults, and public parameters for a specific
// network/channel/namespace combination. It coordinates all token-related activities including
// issuance, transfer, and redemption of tokens.
package token

import (
	"context"
	"fmt"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var logger = logging.MustGetLogger()

// TMSID models a TMS identifier
type TMSID = driver.TMSID

//go:generate counterfeiter -o mock/service_provider.go -fake-name ServiceProvider . ServiceProvider

// ServiceProvider is used to return instances of a given type
type ServiceProvider interface {
	// GetService returns an instance of the given type
	GetService(v any) (any, error)
}

// ManagementService (TMS) is the main entry point for all token operations.
// Each TMS instance is uniquely identified by network, channel, and namespace.
// It provides access to wallets, validators, vaults, selectors, and public parameters.
type ManagementService struct {
	id     TMSID
	tms    driver.TokenManagerService
	logger logging.Logger

	vaultProvider               VaultProvider
	certificationClientProvider CertificationClientProvider
	selectorManagerProvider     SelectorManagerProvider
	signatureService            *SignatureService

	vault                   *Vault
	publicParametersManager *PublicParametersManager
	walletManager           *WalletManager
	validator               *Validator
	auth                    *Authorization
	conf                    *Configuration
	tokensService           *TokensService
	certificationManager    *CertificationManager
}

// NewManagementService creates a new TMS instance for the specified network/channel/namespace.
// Returns an error if initialization fails.
func NewManagementService(
	id TMSID,
	tms driver.TokenManagerService,
	logger logging.Logger,
	vaultProvider VaultProvider,
	certificationClientProvider CertificationClientProvider,
	selectorManagerProvider SelectorManagerProvider,
) (*ManagementService, error) {
	ms := &ManagementService{
		id:                          id,
		tms:                         tms,
		logger:                      logger,
		vaultProvider:               vaultProvider,
		certificationClientProvider: certificationClientProvider,
		selectorManagerProvider:     selectorManagerProvider,
		signatureService: &SignatureService{
			deserializer:     tms.Deserializer(),
			identityProvider: tms.IdentityProvider(),
		},
	}
	if err := ms.init(); err != nil {
		return nil, err
	}

	return ms, nil
}

// GetManagementService retrieves a TMS instance using the provided options.
// Returns the default TMS if no options are specified.
// Options: WithNetwork, WithChannel, WithNamespace, WithPublicParameterFetcher, WithTMS, WithTMSID
func GetManagementService(sp ServiceProvider, opts ...ServiceOption) (*ManagementService, error) {
	ms, err := GetManagementServiceProvider(sp).GetManagementService(opts...)
	if err != nil {
		return nil, errors.Join(err, ErrFailedToGetTMS)
	}

	return ms, nil
}

// String returns a human-readable identifier for this TMS (format: "TMS[network:channel:namespace]").
func (t *ManagementService) String() string {
	return fmt.Sprintf("TMS[%s:%s:%s]", t.Network(), t.Channel(), t.Network())
}

// Network returns the blockchain network identifier for this TMS.
func (t *ManagementService) Network() string {
	return t.id.Network
}

// Channel returns the channel identifier for this TMS.
func (t *ManagementService) Channel() string {
	return t.id.Channel
}

// Namespace returns the namespace identifier for this TMS (empty string if not defined).
func (t *ManagementService) Namespace() string {
	return t.id.Namespace
}

// NewRequest creates a new empty token request with the specified anchor ID.
func (t *ManagementService) NewRequest(id RequestAnchor) (*Request, error) {
	return NewRequest(t, id), nil
}

// NewRequestFromBytes deserializes a token request from its actions and metadata bytes.
func (t *ManagementService) NewRequestFromBytes(anchor RequestAnchor, actions []byte, meta []byte) (*Request, error) {
	return NewRequestFromBytes(t, anchor, actions, meta)
}

// NewFullRequestFromBytes deserializes a complete token request from bytes.
func (t *ManagementService) NewFullRequestFromBytes(tr []byte) (*Request, error) {
	return NewFullRequestFromBytes(t, tr)
}

// NewMetadataFromBytes deserializes request metadata from bytes.
func (t *ManagementService) NewMetadataFromBytes(raw []byte) (*Metadata, error) {
	tokenRequestMetadata := &driver.TokenRequestMetadata{}
	if err := tokenRequestMetadata.FromBytes(raw); err != nil {
		return nil, err
	}

	return &Metadata{
		TokenService:         t.tms.TokensService(),
		WalletService:        t.tms.WalletService(),
		TokenRequestMetadata: tokenRequestMetadata,
		Logger:               t.logger,
	}, nil
}

// Validator returns the validator for verifying token transactions.
func (t *ManagementService) Validator() (*Validator, error) {
	return t.validator, nil
}

// Vault returns the token vault for storing and querying tokens.
func (t *ManagementService) Vault() *Vault {
	return t.vault
}

// WalletManager returns the manager for owner, issuer, and auditor wallets.
func (t *ManagementService) WalletManager() *WalletManager {
	return t.walletManager
}

// CertificationManager returns the manager for token certification (nil if not supported).
func (t *ManagementService) CertificationManager() *CertificationManager {
	return t.certificationManager
}

// CertificationClient creates a new certification client for requesting token certifications.
func (t *ManagementService) CertificationClient(ctx context.Context) (*CertificationClient, error) {
	certificationClient, err := t.certificationClientProvider.New(ctx, nil)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create certification client")
	}

	return &CertificationClient{cc: certificationClient}, nil
}

// PublicParametersManager returns the manager for accessing TMS public parameters.
func (t *ManagementService) PublicParametersManager() *PublicParametersManager {
	return t.publicParametersManager
}

// PublicParameters returns the public parameters, nil if not set yet.
func (t *ManagementService) PublicParameters() *PublicParameters {
	return t.publicParametersManager.PublicParameters()
}

// SelectorManager returns the manager for token selection strategies.
func (t *ManagementService) SelectorManager() (SelectorManager, error) {
	return t.selectorManagerProvider.SelectorManager(t)
}

// SigService returns the service for signature verification and deserialization.
func (t *ManagementService) SigService() *SignatureService {
	return t.signatureService
}

// ID returns the unique identifier (network, channel, namespace) for this TMS.
func (t *ManagementService) ID() TMSID {
	return TMSID{
		Network:   t.Network(),
		Channel:   t.Channel(),
		Namespace: t.Namespace(),
	}
}

// Configuration returns the TMS configuration settings.
func (t *ManagementService) Configuration() *Configuration {
	return t.conf
}

// Authorization returns the authorization service for access control.
func (t *ManagementService) Authorization() *Authorization {
	return t.auth
}

// TokensService returns the service for token operations and upgrades.
func (t *ManagementService) TokensService() *TokensService {
	return t.tokensService
}

// init initializes all TMS components (vault, wallets, validator, etc.).
func (t *ManagementService) init() error {
	v, err := t.vaultProvider.Vault(t.id.Network, t.id.Channel, t.id.Namespace)
	if err != nil {
		return errors.WithMessagef(err, "failed to get vault for [%s]", t.id)
	}
	// Initialize certification storage only if the driver supports it
	var certStorage *CertificationStorage
	if cs := v.CertificationStorage(); cs != nil {
		certStorage = &CertificationStorage{cs}
	}
	t.vault = &Vault{
		v:                    v,
		logger:               t.logger,
		certificationStorage: certStorage,
	}
	t.walletManager = &WalletManager{managementService: t, walletService: t.tms.WalletService()}
	validator, err := t.tms.Validator()
	if err != nil {
		return errors.WithMessagef(err, "failed to get validator")
	}
	t.validator = &Validator{backend: validator}
	t.auth = &Authorization{Authorization: t.tms.Authorization()}
	t.conf = NewConfiguration(t.tms.Configuration())
	t.tokensService = &TokensService{ts: t.tms.TokensService(), tus: t.tms.TokensUpgradeService()}
	t.publicParametersManager = &PublicParametersManager{
		ppm: t.tms.PublicParamsManager(),
		pp:  &PublicParameters{PublicParameters: t.tms.PublicParamsManager().PublicParameters()},
	}
	// Initialize certification manager if certification is supported
	cs := t.tms.CertificationService()
	if cs != nil {
		t.certificationManager = &CertificationManager{c: cs}
	}

	return nil
}

// NewWalletManager creates a wallet manager from a driver wallet service.
func NewWalletManager(walletService driver.WalletService) *WalletManager {
	return &WalletManager{walletService: walletService}
}
