/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
)

// ViewManager initiates a view and returns its result. It is the slice of the FSC view manager the
// service uses; declared locally so the lean module does not import the fabric platform (rule R7).
type ViewManager interface {
	// InitiateView runs v as an initiator and returns its result.
	InitiateView(ctx context.Context, v view.View) (any, error)
}

// ViewRegistry registers a responder view against the initiator that calls it. Declared locally for
// the same reason as ViewManager.
type ViewRegistry interface {
	// RegisterResponder registers responder as the view that answers calls initiated by initiatedBy.
	RegisterResponder(responder view.View, initiatedBy any) error
}

// Service is the endorsement entry point for one TMS: it starts an initiator to collect a quorum for
// a request, and, on an endorsing node, registers the responder that answers those requests. It is
// the EVM analog of the Fabric endorsement Service (design §6), built once per TMS by the lazy
// provider and reused thereafter.
type Service struct {
	registry    *Registry
	threshold   int
	factory     *DeltaFactory
	domain      eip712.Domain
	viewManager ViewManager
}

// NewService assembles the endorsement Service for one TMS. threshold is the number of distinct
// endorser signatures a quorum requires; it must be between 1 and the size of the endorser set,
// matching the EndorsementVerifier's on-chain constraint.
func NewService(
	registry *Registry,
	threshold int,
	factory *DeltaFactory,
	domain eip712.Domain,
	viewManager ViewManager,
) (*Service, error) {
	if registry == nil {
		return nil, errors.New("endorsement service: nil registry")
	}
	if threshold < 1 || threshold > registry.Len() {
		return nil, errors.Errorf("endorsement service: threshold %d out of range [1,%d]", threshold, registry.Len())
	}
	if factory == nil {
		return nil, errors.New("endorsement service: nil delta factory")
	}
	if viewManager == nil {
		return nil, errors.New("endorsement service: nil view manager")
	}

	return &Service{
		registry:    registry,
		threshold:   threshold,
		factory:     factory,
		domain:      domain,
		viewManager: viewManager,
	}, nil
}

// Endorse collects a quorum of endorsements for req by initiating an Initiator view, and returns the
// assembled Result. It is what the driver's RequestApproval calls.
func (s *Service) Endorse(context view.Context, req *EndorseRequest) (*Result, error) {
	if err := req.Validate(); err != nil {
		return nil, errors.Wrap(err, "invalid endorse request")
	}
	boxed, err := s.viewManager.InitiateView(
		context.Context(),
		NewInitiator(s.registry, s.threshold, s.factory, s.domain, req),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to run endorsement initiator")
	}
	result, ok := boxed.(*Result)
	if !ok {
		return nil, errors.Errorf("expected *Result from initiator, got [%T]", boxed)
	}

	return result, nil
}

// RegisterEndorser registers responder as the view that answers this node's endorsement requests.
// Called by the wiring only when this node is configured as an endorser (design §6.2); a
// non-endorsing node never registers one.
func RegisterEndorser(registry ViewRegistry, responder *Responder) error {
	if err := registry.RegisterResponder(responder, &Initiator{}); err != nil {
		return errors.Wrap(err, "failed to register endorsement responder")
	}

	return nil
}

// compile-time checks that the views satisfy the FSC view contract.
var (
	_ view.View = (*Initiator)(nil)
	_ view.View = (*Responder)(nil)
)
