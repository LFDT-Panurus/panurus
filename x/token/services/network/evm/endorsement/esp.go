/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"sync"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
)

// ServiceFactory builds the endorsement service for a TMS, and caches it.
//
// The service is keyed by TMS because that is the unit RequestApproval works in, but it no longer
// holds anything derived from a TMS: the initiator collects signatures and takes the delta from the
// endorsers, so nothing on that side needs a validator. Validation lives entirely in the responder,
// which resolves its TMS when a request arrives.
//
// What the factory holds unconditionally is only what is genuinely shared by every TMS on this
// network: the chain client and the view manager that runs the initiator. Everything else - the
// endorser set, threshold, EIP-712 domain, TokenState and the parameters a delta binds to - is
// per-TMS, since two TMS sharing one network can each have their own TokenState clone and their own
// endorsement policy (design: every TMS's contracts.tokenState is documented as "this TMS's TokenState
// clone"). Register binds one TMS's configuration in; ForTMS and NewResponder's returned Responder
// both resolve it by TMS id rather than assuming the network has only one.
type ServiceFactory struct {
	client      client.EVMClient
	viewManager ViewManager

	mu       sync.Mutex
	perTMS   map[string]TMSConfig
	services map[string]*Service
}

// FactoryConfig carries what a ServiceFactory needs that is genuinely shared across every TMS on this
// network.
type FactoryConfig struct {
	// Client reads the chain during validation.
	Client client.EVMClient
	// ViewManager runs the initiator.
	ViewManager ViewManager
}

// TMSConfig is what one TMS contributes to the endorsement factory: its own endorser set, quorum,
// EIP-712 domain and the contract and parameters a delta binds to.
type TMSConfig struct {
	// Registry binds each endorser's address to the identity that speaks for it.
	Registry *Registry
	// Threshold is how many distinct endorsements a transaction needs.
	Threshold int
	// Domain is the EIP-712 domain, bound to the chain and this TMS's TokenState.
	Domain eip712.Domain
	// TokenState is the contract validation reads through.
	TokenState client.Address
	// BlockTag is the tag validation reads at.
	BlockTag string
	// PublicParams supplies the parameters a delta is bound to.
	PublicParams PublicParamsProvider
}

// NewServiceFactory returns a factory for the given network. Register must be called once per TMS
// before ForTMS or the Responder NewResponder returns can serve that TMS's requests.
func NewServiceFactory(cfg FactoryConfig) (*ServiceFactory, error) {
	if cfg.Client == nil {
		return nil, errors.New("endorsement factory: nil evm client")
	}
	if cfg.ViewManager == nil {
		return nil, errors.New("endorsement factory: nil view manager")
	}

	return &ServiceFactory{
		client:      cfg.Client,
		viewManager: cfg.ViewManager,
		perTMS:      map[string]TMSConfig{},
		services:    map[string]*Service{},
	}, nil
}

// Register binds one TMS's endorsement configuration to the factory. Calling it again for the same
// TMS replaces the configuration but does not evict an already-built Service; that only happens
// through the usual public-parameters-update eviction path the driver drives separately.
func (f *ServiceFactory) Register(tmsID token2.TMSID, cfg TMSConfig) error {
	if tmsID.Network == "" {
		return errors.New("endorsement factory: tms id without a network")
	}
	if cfg.Registry == nil {
		return errors.New("endorsement factory: nil registry")
	}
	if cfg.PublicParams == nil {
		return errors.New("endorsement factory: nil public parameters provider")
	}
	if cfg.Threshold < 1 || cfg.Threshold > cfg.Registry.Len() {
		return errors.Errorf("endorsement factory: threshold %d out of range [1,%d]",
			cfg.Threshold, cfg.Registry.Len())
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.perTMS[tmsID.String()] = cfg

	return nil
}

// configFor returns the registered configuration for a TMS, or an error naming it if none was ever
// registered - the same refusal shape as a TMS this endorser genuinely does not serve.
func (f *ServiceFactory) configFor(tmsID token2.TMSID) (TMSConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cfg, ok := f.perTMS[tmsID.String()]
	if !ok {
		return TMSConfig{}, errors.Errorf("endorsement factory: no configuration registered for tms [%s]", tmsID)
	}

	return cfg, nil
}

// ForTMS returns the endorsement service for the given TMS, building it on first use.
func (f *ServiceFactory) ForTMS(tmsID token2.TMSID) (*Service, error) {
	if tmsID.Network == "" {
		return nil, errors.New("endorsement factory: tms id without a network")
	}
	key := tmsID.String()

	f.mu.Lock()
	if service, ok := f.services[key]; ok {
		f.mu.Unlock()

		return service, nil
	}
	f.mu.Unlock()

	cfg, err := f.configFor(tmsID)
	if err != nil {
		return nil, err
	}

	service, err := NewService(cfg.Registry, cfg.Threshold, cfg.Domain, f.viewManager)
	if err != nil {
		return nil, errors.Wrapf(err, "endorsement factory: failed to build the service for [%s]", tmsID)
	}

	f.mu.Lock()
	f.services[key] = service
	f.mu.Unlock()

	return service, nil
}

// TMSResolver returns the management service for a TMS id. It is called when a request arrives rather
// than at construction, for two reasons: it lets an endorser be registered before its TMS exists, and
// it keeps both sides off a management service that a public-parameters update has since replaced.
type TMSResolver func(tmsID token2.TMSID) (*token2.ManagementService, error)

// NewResponder builds the view that answers endorsement requests. The wiring registers it only on a
// node configured as an endorser; a node without a signing key has nothing to answer with.
//
// It is deliberately not tied to a TMS. An endorser has to be registered before any request reaches
// it, and that is before its TMS has necessarily been built, since building one goes through the
// network driver. The TMS is therefore resolved per request, from the id the request carries.
func (f *ServiceFactory) NewResponder(
	authorizer *Authorizer,
	signer EndorserSigner,
	resolve TMSResolver,
) (*Responder, error) {
	if signer == nil {
		return nil, errors.New("endorsement factory: an endorser needs a signing key")
	}
	if resolve == nil {
		return nil, errors.New("endorsement factory: an endorser needs a way to resolve a tms")
	}

	return NewResponder(
		authorizer,
		func(tmsID token2.TMSID) (*DeltaFactory, error) {
			tms, err := resolve(tmsID)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to resolve tms [%s]", tmsID)
			}
			validator, err := tms.Validator()
			if err != nil {
				return nil, errors.Wrapf(err, "failed to get the validator for [%s]", tmsID)
			}
			cfg, err := f.configFor(tmsID)
			if err != nil {
				return nil, err
			}

			return NewDeltaFactory(validator, cfg.PublicParams, f.client, cfg.TokenState, cfg.BlockTag), nil
		},
		signer,
		func(tmsID token2.TMSID) (eip712.Domain, error) {
			cfg, err := f.configFor(tmsID)
			if err != nil {
				return eip712.Domain{}, err
			}

			return cfg.Domain, nil
		},
	), nil
}
