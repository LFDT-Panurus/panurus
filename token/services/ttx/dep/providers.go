/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dep

import (
	"context"
	"reflect"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/db"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

var (
	networkProviderType         = reflect.TypeFor[*NetworkProvider]()
	tmsProviderType             = reflect.TypeFor[*TokenManagementServiceProvider]()
	networkIdentityProviderType = reflect.TypeFor[*NetworkIdentityProvider]()
	transactionDBProviderType   = reflect.TypeFor[*TransactionDBProvider]()
	auditDBProviderType         = reflect.TypeFor[*AuditDBProvider]()
)

// Network defines the subset of function of the network service needed by the ttx service.
//
//go:generate counterfeiter -o mock/network.go -fake-name Network . Network
type Network interface {
	AddFinalityListener(namespace string, txID string, listener network.FinalityListener) error
	GetTransactionStatus(ctx context.Context, namespace, txID string) (status int, tokenRequestHash []byte, message string, err error)
	NewEnvelope() *network.Envelope
	AnonymousIdentity() (view.Identity, error)
	LocalMembership() *network.LocalMembership
	ComputeTxID(n *network.TxID) string
}

// NetworkProvider given access to instances of the Network interface.
//
//go:generate counterfeiter -o mock/network_provider.go -fake-name NetworkProvider . NetworkProvider
type NetworkProvider interface {
	// GetNetwork returns the Network instance for the given network and channel identifiers.
	GetNetwork(network string, channel string) (Network, error)
}

// GetNetworkProvider retrieves the NetworkProvider from the given ServiceProvider.
func GetNetworkProvider(sp token.ServiceProvider) (NetworkProvider, error) {
	s, err := sp.GetService(networkProviderType)
	if err != nil {
		return nil, err
	}

	return s.(NetworkProvider), nil
}

// SignatureService defines the subset of function of the signature service needed by the ttx service.
type SignatureService interface {
	// IsMe checks if the given party is a local party meaning that a signer is bound to that identity.
	IsMe(ctx context.Context, party token.Identity) bool
}

// TokenManagementService defines the interface of a token management service needed by ttx service.
//
//go:generate counterfeiter -o mock/tms.go -fake-name TokenManagementService . TokenManagementService
type TokenManagementService interface {
	ID() token.TMSID
	Network() string
	Channel() string
	NewRequest(anchor token.RequestAnchor) (*token.Request, error)
	SelectorManager() (token.SelectorManager, error)
	PublicParametersManager() *token.PublicParametersManager
	SigService() *token.SignatureService
	WalletManager() *token.WalletManager
	NewFullRequestFromBytes(raw []byte) (*token.Request, error)
	Vault() *token.Vault
}

// TokenManagementServiceWithExtensions extends TokenManagementService with additional functions needed by ttx service.
//
//go:generate counterfeiter -o mock/tmse.go -fake-name TokenManagementServiceWithExtensions . TokenManagementServiceWithExtensions
type TokenManagementServiceWithExtensions interface {
	TokenManagementService
	SetTokenManagementService(req *token.Request) error
}

// TokenManagementServiceProvider provides instances of TokenManagementServiceWithExtensions.
//
//go:generate counterfeiter -o mock/tmsp.go -fake-name TokenManagementServiceProvider . TokenManagementServiceProvider
type TokenManagementServiceProvider interface {
	// TokenManagementService returns the TokenManagementServiceWithExtensions instance for the given options.
	TokenManagementService(opts ...token.ServiceOption) (TokenManagementServiceWithExtensions, error)
}

// GetManagementService retrieves the TokenManagementServiceWithExtensions from the given ServiceProvider.
func GetManagementService(sp token.ServiceProvider, opts ...token.ServiceOption) (TokenManagementServiceWithExtensions, error) {
	s, err := sp.GetService(tmsProviderType)
	if err != nil {
		return nil, err
	}
	tmsProvider, ok := s.(TokenManagementServiceProvider)
	if !ok {
		panic("implementation error, type must be TokenManagementServiceProvider")
	}

	return tmsProvider.TokenManagementService(opts...)
}

// NetworkIdentitySigner is an alias for the FSC's Signer interface
//
//go:generate counterfeiter -o mock/nis.go -fake-name NetworkIdentitySigner . NetworkIdentitySigner
type NetworkIdentitySigner = cdriver.Signer

// NetworkIdentityProvider defines the subset of function of the network identity provider needed by the ttx service.
//
//go:generate counterfeiter -o mock/nip.go -fake-name NetworkIdentityProvider . NetworkIdentityProvider
type NetworkIdentityProvider interface {
	DefaultIdentity() view.Identity
	GetSigner(identity view.Identity) (NetworkIdentitySigner, error)
}

// GetNetworkIdentityProvider retrieves the NetworkIdentityProvider from the given ServiceProvider.
func GetNetworkIdentityProvider(sp token.ServiceProvider) (NetworkIdentityProvider, error) {
	s, err := sp.GetService(networkIdentityProviderType)
	if err != nil {
		return nil, err
	}
	nip, ok := s.(NetworkIdentityProvider)
	if !ok {
		panic("implementation error, type must be NetworkIdentityProvider")
	}

	return nip, nil
}

// TransactionDB defines the subset of function of the transaction db needed by the ttx service.
//
//go:generate counterfeiter -o mock/transaction_db.go -fake-name TransactionDB . TransactionDB
type TransactionDB interface {
	GetStatus(ctx context.Context, txID string) (token.TxStatus, string, error)
	AddStatusListener(txID string, ch chan db.TransactionStatusEvent)
	DeleteStatusListener(txID string, ch chan db.TransactionStatusEvent)
}

// TransactionDBProvider is used to retrieves instances of TransactionDB
//
//go:generate counterfeiter -o mock/transaction_db_provider.go -fake-name TransactionDBProvider . TransactionDBProvider
type TransactionDBProvider interface {
	// TransactionDB returns the TransactionDB for the given tms id
	TransactionDB(tmsID token.TMSID) (TransactionDB, error)
}

// GetTransactionDB returns the TransactionDB for the given tms ID
func GetTransactionDB(sp token.ServiceProvider, tmsID token.TMSID) (TransactionDB, error) {
	s, err := sp.GetService(transactionDBProviderType)
	if err != nil {
		return nil, err
	}
	provider, ok := s.(TransactionDBProvider)
	if !ok {
		panic("implementation error, type must be TransactionDBProvider")
	}

	return provider.TransactionDB(tmsID)
}

// AuditDB defines the subset of function of the audit db needed by the ttx service.
//
//go:generate counterfeiter -o mock/audit_db.go -fake-name AuditDB . AuditDB
type AuditDB interface {
	GetStatus(ctx context.Context, txID string) (token.TxStatus, string, error)
	AddStatusListener(txID string, ch chan db.TransactionStatusEvent)
	DeleteStatusListener(txID string, ch chan db.TransactionStatusEvent)
}

// AuditDBProvider is used to retrieves instances of AuditDB
//
//go:generate counterfeiter -o mock/audit_db_provider.go -fake-name AuditDBProvider . AuditDBProvider
type AuditDBProvider interface {
	// AuditDB returns the AuditDB for the given tms id
	AuditDB(tmsID token.TMSID) (AuditDB, error)
}

// GetAuditDB returns the AuditDB for the given tms ID
func GetAuditDB(sp token.ServiceProvider, tmsID token.TMSID) (AuditDB, error) {
	s, err := sp.GetService(auditDBProviderType)
	if err != nil {
		return nil, err
	}
	provider, ok := s.(AuditDBProvider)
	if !ok {
		panic("implementation error, type must be AuditDBProvider")
	}

	return provider.AuditDB(tmsID)
}
