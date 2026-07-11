/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package membership

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	role2 "github.com/LFDT-Panurus/panurus/token/services/identity/role"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var toString = map[driver.IdentityRoleType]string{
	driver.OwnerRole:     "Owner",
	driver.IssuerRole:    "Issuer",
	driver.AuditorRole:   "Auditor",
	driver.CertifierRole: "Certifier",
}

//go:generate counterfeiter -o mock/sp.go -fake-name StorageProvider . StorageProvider
type StorageProvider interface {
	IdentityStore(tmsID token.TMSID) (driver.IdentityStoreService, error)
}

// RoleFactory is the factory for creating wallets, idemix and x509
type RoleFactory struct {
	Logger                 logging.Logger
	TMSID                  token.TMSID
	Config                 Config
	FSCIdentity            driver.Identity
	NetworkDefaultIdentity driver.Identity
	IdentityProvider       IdentityProvider
	StorageProvider        StorageProvider
	DeserializerManager    SignerDeserializerManager
	// SignerRouter, when set, is passed to every LocalMembership created by NewRole so loaded
	// KeyManagers self-register by conf_id. Optional: nil disables conf_id-pinned routing for
	// roles built by this factory.
	SignerRouter *identity.SignerRouter
}

// NewRoleFactory creates a new RoleFactory
func NewRoleFactory(
	logger logging.Logger,
	TMSID token.TMSID,
	config Config,
	fscIdentity driver.Identity,
	networkDefaultIdentity driver.Identity,
	identityProvider IdentityProvider,
	storageProvider StorageProvider,
	deserializerManager SignerDeserializerManager,
) *RoleFactory {
	return &RoleFactory{
		Logger:                 logger,
		TMSID:                  TMSID,
		Config:                 config,
		FSCIdentity:            fscIdentity,
		NetworkDefaultIdentity: networkDefaultIdentity,
		IdentityProvider:       identityProvider,
		StorageProvider:        storageProvider,
		DeserializerManager:    deserializerManager,
	}
}

// SetSignerRouter sets the router that roles created by subsequent NewRole calls register their
// KeyManagers with by conf_id.
func (f *RoleFactory) SetSignerRouter(router *identity.SignerRouter) {
	f.SignerRouter = router
}

func (f *RoleFactory) NewRole(role driver.IdentityRoleType, defaultAnon bool, targets []driver.Identity, kmps ...KeyManagerProvider) (driver.Role, error) {
	identityDB, err := f.StorageProvider.IdentityStore(f.TMSID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get wallet path storage")
	}
	lm := NewLocalMembership(
		f.Logger.Named("membership.role."+driver.RoleToString(role)),
		f.Config,
		f.NetworkDefaultIdentity,
		f.DeserializerManager,
		identityDB,
		toString[role],
		defaultAnon,
		f.IdentityProvider,
		kmps...,
	)
	if f.SignerRouter != nil {
		lm.SetSignerRouter(f.SignerRouter)
	}
	identities, err := f.Config.IdentitiesForRole(role)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get identities for role [%d]", role)
	}
	if err := lm.Load(context.Background(), identities, targets); err != nil {
		return nil, errors.WithMessagef(err, "failed to load identities")
	}

	return role2.NewRole(f.Logger, role, f.TMSID.Network, f.FSCIdentity, lm), nil
}
