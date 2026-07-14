/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wallet

import (
	"context"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var (
	// This makes sure that Service implements driver.WalletService
	_ tdriver.WalletService = &Service{}
)

var (
	// ErrNilRecipientData is returned when a nil RecipientData is passed to RegisterRecipientIdentity
	ErrNilRecipientData = errors.New("nil recipient data")
)

type RoleRegistries map[idriver.IdentityRoleType]RoleRegistry

// RoleRegistry models an external registry that holds wallets for a given role.
// It is used by the wallet service to lookup and register identities and wallets.
//
//go:generate counterfeiter -o mock/registry.go -fake-name RoleRegistry . RoleRegistry
type RoleRegistry interface {
	WalletIDs(ctx context.Context) ([]string, error)
	RegisterIdentity(ctx context.Context, config tdriver.IdentityConfiguration) error
	WalletByID(ctx context.Context, role idriver.IdentityRoleType, id tdriver.WalletLookupID) (tdriver.Wallet, error)
	// Done releases all the resources allocated by this service.
	Done() error
}

// Service implements the driver.WalletService interface.
// Service exposes wallet-related helper operations used by the token management layer.
// It delegates identity operations to the configured IdentityProvider and uses
// registries for role-specific wallet lookups and registrations.
type Service struct {
	Logger           logging.Logger
	IdentityProvider tdriver.IdentityProvider
	Deserializer     tdriver.Deserializer
	RoleRegistries   RoleRegistries
}

// NewService creates a new wallet Service.
func NewService(
	logger logging.Logger,
	identityProvider tdriver.IdentityProvider,
	deserializer tdriver.Deserializer,
	roleRegistries RoleRegistries,
) *Service {
	return &Service{
		Logger:           logger,
		IdentityProvider: identityProvider,
		Deserializer:     deserializer,
		RoleRegistries:   roleRegistries,
	}
}

// RegisterOwnerIdentity registers a long-term owner identity using the owner registry.
func (s *Service) RegisterOwnerIdentity(ctx context.Context, config tdriver.IdentityConfiguration) error {
	return s.RoleRegistries[idriver.OwnerRole].RegisterIdentity(ctx, config)
}

// RegisterIssuerIdentity registers a long-term issuer identity using the issuer registry.
func (s *Service) RegisterIssuerIdentity(ctx context.Context, config tdriver.IdentityConfiguration) error {
	return s.RoleRegistries[idriver.IssuerRole].RegisterIdentity(ctx, config)
}

// GetAuditInfo retrieves audit information for the given identity using the configured IdentityProvider.
func (s *Service) GetAuditInfo(ctx context.Context, id tdriver.Identity) ([]byte, error) {
	return s.IdentityProvider.GetAuditInfo(ctx, id)
}

// GetEnrollmentID extracts the enrollment id from the passed audit information using the IdentityProvider.
func (s *Service) GetEnrollmentID(ctx context.Context, identity tdriver.Identity, auditInfo []byte) (string, error) {
	return s.IdentityProvider.GetEnrollmentID(ctx, identity, auditInfo)
}

// GetRevocationHandle extracts the revocation handle from the passed audit information using the IdentityProvider.
func (s *Service) GetRevocationHandle(ctx context.Context, identity tdriver.Identity, auditInfo []byte) (string, error) {
	return s.IdentityProvider.GetRevocationHandler(ctx, identity, auditInfo)
}

// GetEIDAndRH returns both enrollment ID and revocation handle from audit info via the IdentityProvider.
func (s *Service) GetEIDAndRH(ctx context.Context, identity tdriver.Identity, auditInfo []byte) (string, string, error) {
	return s.IdentityProvider.GetEIDAndRH(ctx, identity, auditInfo)
}

// RegisterRecipientIdentity registers the passed identity as a third-party recipient identity.
// The function performs these steps:
//   - validate the input
//   - match the identity against the provided audit info using the Deserializer (before any provider registration)
//   - ask the IdentityProvider to register the recipient identity
//   - store the recipient data via the IdentityProvider
//
// If RegisterRecipientData fails after RegisterRecipientIdentity succeeds, the IdentityProvider
// may implement identity.RecipientRegistrationRollback so partial registration can be undone.
func (s *Service) RegisterRecipientIdentity(ctx context.Context, data *tdriver.RecipientData) error {
	if data == nil {
		return errors.Wrapf(ErrNilRecipientData, "invalid recipient data")
	}

	s.Logger.DebugfContext(ctx, "register recipient identity [%s] with audit info [%s]", data.Identity, utils.Hashable(data.AuditInfo))

	if err := s.Deserializer.MatchIdentity(ctx, data.Identity, data.AuditInfo); err != nil {
		return errors.Wrapf(err, "failed to match identity to audit information for [%s]:[%s]", data.Identity, utils.Hashable(data.AuditInfo))
	}

	if err := s.IdentityProvider.RegisterRecipientIdentity(ctx, data.Identity); err != nil {
		return errors.Wrapf(err, "failed to register recipient identity")
	}

	if err := s.IdentityProvider.RegisterRecipientData(ctx, data); err != nil {
		if rb, ok := s.IdentityProvider.(identity.RecipientRegistrationRollback); ok {
			rb.RollbackPartialRecipientRegistration(ctx, data.Identity)
		}

		return errors.Wrapf(err, "failed registering audit info for owner [%s]", data.Identity)
	}

	return nil
}

// Wallet returns a wallet bound to the passed identity. It tries to resolve an owner wallet first
// and then an issuer wallet. It returns nil if no wallet is found.
func (s *Service) Wallet(ctx context.Context, identity tdriver.Identity) tdriver.Wallet {
	w, _ := s.OwnerWallet(ctx, identity)
	if w != nil {
		return w
	}
	iw, _ := s.IssuerWallet(ctx, identity)
	if iw != nil {
		return iw
	}

	return nil
}

// OwnerWalletIDs returns the list of owner wallet identifiers from the owner registry.
func (s *Service) OwnerWalletIDs(ctx context.Context) ([]string, error) {
	return s.RoleRegistries[idriver.OwnerRole].WalletIDs(ctx)
}

// OwnerWallet returns the OwnerWallet instance bound to the passed lookup id.
func (s *Service) OwnerWallet(ctx context.Context, id tdriver.WalletLookupID) (tdriver.OwnerWallet, error) {
	w, err := s.RoleRegistries[idriver.OwnerRole].WalletByID(ctx, idriver.OwnerRole, id)
	if err != nil {
		return nil, err
	}

	return w.(tdriver.OwnerWallet), nil
}

// IssuerWallet returns the IssuerWallet instance bound to the passed lookup id.
func (s *Service) IssuerWallet(ctx context.Context, id tdriver.WalletLookupID) (tdriver.IssuerWallet, error) {
	w, err := s.RoleRegistries[idriver.IssuerRole].WalletByID(ctx, idriver.IssuerRole, id)
	if err != nil {
		return nil, err
	}

	return w.(tdriver.IssuerWallet), nil
}

// AuditorWallet returns the AuditorWallet instance bound to the passed lookup id.
func (s *Service) AuditorWallet(ctx context.Context, id tdriver.WalletLookupID) (tdriver.AuditorWallet, error) {
	w, err := s.RoleRegistries[idriver.AuditorRole].WalletByID(ctx, idriver.AuditorRole, id)
	if err != nil {
		return nil, err
	}

	return w.(tdriver.AuditorWallet), nil
}

// CertifierWallet returns the CertifierWallet instance bound to the passed lookup id.
func (s *Service) CertifierWallet(ctx context.Context, id tdriver.WalletLookupID) (tdriver.CertifierWallet, error) {
	w, err := s.RoleRegistries[idriver.CertifierRole].WalletByID(ctx, idriver.CertifierRole, id)
	if err != nil {
		return nil, err
	}

	return w.(tdriver.CertifierWallet), nil
}

// SpendIDs returns the spend ids for the passed token ids.
// The current implementation converts token.ID into its string representation.
func (s *Service) SpendIDs(ids ...*token.ID) ([]string, error) {
	if len(ids) == 0 {
		return []string{}, nil
	}
	res := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == nil {
			// skip nil ids
			continue
		}
		res = append(res, id.String())
	}

	return res, nil
}

// Done releases all the resources allocated by this service.
func (s *Service) Done() error {
	var err error
	for _, reg := range s.RoleRegistries {
		err = errors.Join(err, reg.Done())
	}

	return err
}

// Convert converts a map of concrete registries into a map of the RoleRegistry interface type.
func Convert[T RoleRegistry](s map[idriver.IdentityRoleType]T) RoleRegistries {
	res := make(RoleRegistries, len(s))
	for role, v := range s {
		res[role] = v
	}

	return res
}
