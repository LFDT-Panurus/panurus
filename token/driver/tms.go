/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"fmt"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// TMSID models a TMS identifier
type TMSID struct {
	Network   string
	Channel   string
	Namespace string
}

// String returns a string representation of the TMSID
func (t TMSID) String() string {
	return fmt.Sprintf("%s,%s,%s", t.Network, t.Channel, t.Namespace)
}

func (t TMSID) Equal(tmsid TMSID) bool {
	return t.Network == tmsid.Network && t.Channel == tmsid.Channel && t.Namespace == tmsid.Namespace
}

//go:generate counterfeiter -o mock/tms.go -fake-name TokenManagerService . TokenManagerService

// TokenManagerService is the core entry point of the Driver API. It provides access to a comprehensive
// suite of services for token issuance, transfer, auditing, and management. Each driver implementation
// must provide a concrete implementation of this interface to enable Panurus to interact with its
// specific token technology.
type TokenManagerService interface {
	// IssueService returns an instance of the IssueService interface, which manages the issuance of new tokens.
	IssueService() IssueService
	// TransferService returns an instance of the TransferService interface, which handles the transfer of token ownership.
	TransferService() TransferService
	// TokensService returns an instance of the TokensService interface, providing general token management utilities.
	TokensService() TokensService
	// TokensUpgradeService returns an instance of the TokensUpgradeService interface, enabling token upgrades.
	TokensUpgradeService() TokensUpgradeService
	// AuditorService returns an instance of the AuditorService interface, facilitating token auditing capabilities.
	AuditorService() AuditorService
	// CertificationService returns an instance of the CertificationService interface, managing token certifications.
	CertificationService() CertificationService
	// Deserializer returns an instance of the Deserializer interface, which deserializes identities to obtain signature verifiers.
	Deserializer() Deserializer
	// IdentityProvider returns an instance of the IdentityProvider interface, managing identities and their associated information.
	IdentityProvider() IdentityProvider
	// Validator returns an instance of the Validator interface, which provides mechanisms for validating token transactions.
	Validator() (Validator, error)
	// PublicParamsManager returns an instance of the PublicParamsManager interface, managing the driver's public parameters.
	PublicParamsManager() PublicParamsManager
	// Configuration returns an instance of the Configuration interface, providing access to driver-specific configuration.
	Configuration() Configuration
	// WalletService returns an instance of the WalletService interface, which manages issuer, owner, auditor, and certifier wallets.
	WalletService() WalletService
	// Authorization returns an instance of the Authorization interface, used to check the relationship between tokens and wallets.
	Authorization() Authorization
	// Done releases all the resources allocated by this service, ensuring a clean shutdown.
	Done() error
}

// ServiceOptions is used to configure the service
type ServiceOptions struct {
	// Network is the name of the network
	Network string
	// Channel is the name of the channel, if meaningful for the underlying backend
	Channel string
	// Namespace is the namespace of the token
	Namespace string
	// PublicParamsFetcher is used to fetch the public parameters
	PublicParamsFetcher PublicParamsFetcher
	// PublicParams contains the public params to use to instantiate the driver
	PublicParams []byte
	// Params is used to store any application specific parameter
	Params map[string]any
}

func (o ServiceOptions) String() string {
	return fmt.Sprintf("%s,%s,%s", o.Network, o.Channel, o.Namespace)
}

// ParamAsString returns the value bound to the passed key.
// If the key is not found, it returns the empty string.
// if the value bound to the passed key is not a string, it returns an error.
func (o ServiceOptions) ParamAsString(key string) (string, error) {
	if o.Params == nil {
		return "", nil
	}
	v, ok := o.Params[key]
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", errors.Errorf("expecting string, found [%T]", o)
	}

	return s, nil
}

//go:generate counterfeiter -o mock/token_manager_service_provider.go -fake-name TokenManagerServiceProvider . TokenManagerServiceProvider

type TokenManagerServiceProvider interface {
	// GetTokenManagerService returns a TokenManagerService instance for the passed parameters
	// If a TokenManagerService is not available, it creates one.
	GetTokenManagerService(opts ServiceOptions) (TokenManagerService, error)

	// Update uses the given options to update the public parameters of a given TMS.
	// If the public parameters in the options are identical to those in the current TMS, then nothing happens.
	// If a TMS does not exist for the given options, one is created with the given public parameters.
	Update(options ServiceOptions) error
}
