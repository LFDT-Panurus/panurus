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
// The service is per TMS rather than per network because validating a token request needs that TMS's
// validator, which knows its token driver and public parameters. Everything else it needs (the
// endorser set, the threshold, the chain, this node's signing key) is network-wide and is resolved
// once, here.
//
// The factory takes a TMS id and resolves the management service itself, per request. Holding the
// service would be wrong: updating public parameters evicts the cached management service and the next
// caller gets a rebuilt one, so a captured pointer keeps serving the parameters that were current when
// it was captured, and no amount of re-asking it for a validator changes that.
type ServiceFactory struct {
	registry     *Registry
	threshold    int
	domain       eip712.Domain
	client       client.EVMClient
	tokenState   client.Address
	blockTag     string
	publicParams PublicParamsProvider
	viewManager  ViewManager
	resolveTMS   TMSResolver

	mu       sync.Mutex
	services map[string]*Service
}

// FactoryConfig carries what a ServiceFactory needs that is not per TMS.
type FactoryConfig struct {
	// Registry binds each endorser's address to the identity that speaks for it.
	Registry *Registry
	// Threshold is how many distinct endorsements a transaction needs.
	Threshold int
	// Domain is the EIP-712 domain, bound to the chain and this TMS's TokenState.
	Domain eip712.Domain
	// Client reads the chain during validation.
	Client client.EVMClient
	// TokenState is the contract validation reads through.
	TokenState client.Address
	// BlockTag is the tag validation reads at.
	BlockTag string
	// PublicParams supplies the parameters a delta is bound to.
	PublicParams PublicParamsProvider
	// ViewManager runs the initiator.
	ViewManager ViewManager
	// TMS resolves a management service from its id, on every request rather than once.
	TMS TMSResolver
}

// NewServiceFactory returns a factory for the given network.
func NewServiceFactory(cfg FactoryConfig) (*ServiceFactory, error) {
	if cfg.Registry == nil {
		return nil, errors.New("endorsement factory: nil registry")
	}
	if cfg.Client == nil {
		return nil, errors.New("endorsement factory: nil evm client")
	}
	if cfg.ViewManager == nil {
		return nil, errors.New("endorsement factory: nil view manager")
	}
	if cfg.PublicParams == nil {
		return nil, errors.New("endorsement factory: nil public parameters provider")
	}
	if cfg.TMS == nil {
		return nil, errors.New("endorsement factory: nil tms resolver")
	}
	if cfg.Threshold < 1 || cfg.Threshold > cfg.Registry.Len() {
		return nil, errors.Errorf("endorsement factory: threshold %d out of range [1,%d]",
			cfg.Threshold, cfg.Registry.Len())
	}

	return &ServiceFactory{
		registry:     cfg.Registry,
		threshold:    cfg.Threshold,
		domain:       cfg.Domain,
		client:       cfg.Client,
		tokenState:   cfg.TokenState,
		blockTag:     cfg.BlockTag,
		publicParams: cfg.PublicParams,
		viewManager:  cfg.ViewManager,
		resolveTMS:   cfg.TMS,
		services:     map[string]*Service{},
	}, nil
}

// ForTMS returns the endorsement service for the given TMS, building it on first use.
func (f *ServiceFactory) ForTMS(tmsID token2.TMSID) (*Service, error) {
	if tmsID.Network == "" {
		return nil, errors.New("endorsement factory: tms id without a network")
	}
	key := tmsID.String()

	f.mu.Lock()
	defer f.mu.Unlock()
	if service, ok := f.services[key]; ok {
		return service, nil
	}

	// Both the management service and the validator are resolved per request. The service cached here
	// lives for the life of the node, but an endorsed setup delta replaces the management service
	// underneath it, and only a fresh one knows the new public parameters.
	resolve := ResolveValidator(func() (RequestValidator, error) {
		tms, err := f.resolveTMS(tmsID)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to resolve tms [%s]", tmsID)
		}

		return tms.Validator()
	})

	service, err := NewService(
		f.registry,
		f.threshold,
		NewDeltaFactory(resolve, f.publicParams, f.client, f.tokenState, f.blockTag),
		f.domain,
		f.viewManager,
	)
	if err != nil {
		return nil, errors.Wrapf(err, "endorsement factory: failed to build the service for [%s]", tmsID)
	}
	f.services[key] = service

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

			return NewDeltaFactory(validator, f.publicParams, f.client, f.tokenState, f.blockTag), nil
		},
		signer,
		f.domain,
	), nil
}
