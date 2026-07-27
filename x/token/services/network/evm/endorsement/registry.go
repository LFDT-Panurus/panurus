/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
)

// Endorser binds one endorser's two identities: its FSC view.Identity, used to route the
// endorsement request over an authenticated session, and its Ethereum address, the value the
// contract recovers from the signature. Both come from config (design §6.1, §10).
type Endorser struct {
	Identity view.Identity
	Address  client.Address
}

// Registry resolves between an endorser's Ethereum address and its FSC identity, both directions.
// The EndorsementVerifier's on-chain set alone yields only addresses, which cannot address an FSC
// call, so the initiator needs address→identity to route requests and the contract-facing side
// needs identity→address to know which recovered address a responder speaks for (design §6.1).
//
// It is immutable after construction: the endorser set is fixed at contract-construction time in v1
// (runtime mutation is a quorum-gated feature deferred beyond v1, design §3.8), so a Registry is
// built once from config and only read thereafter, which also makes it safe to share across
// goroutines without locking.
type Registry struct {
	byAddress map[client.Address]view.Identity
	endorsers []Endorser
}

// NewRegistry builds a Registry from the endorser set. It rejects a set that could not form a sound
// quorum: an empty set, an endorser missing either identity, or two endorsers sharing an address
// (which would let one key be counted under two identities, defeating the distinct-signer rule the
// contract enforces on-chain, design §3.6). Duplicate identities are likewise rejected.
func NewRegistry(endorsers []Endorser) (*Registry, error) {
	if len(endorsers) == 0 {
		return nil, errors.New("endorsement registry: no endorsers configured")
	}
	byAddress := make(map[client.Address]view.Identity, len(endorsers))
	seenIdentity := make(map[string]struct{}, len(endorsers))
	for i, e := range endorsers {
		if e.Identity.IsNone() {
			return nil, errors.Errorf("endorsement registry: endorser %d has no fsc identity", i)
		}
		if e.Address == (client.Address{}) {
			return nil, errors.Errorf("endorsement registry: endorser %d [%s] has the zero address", i, e.Identity)
		}
		if _, ok := byAddress[e.Address]; ok {
			return nil, errors.Errorf("endorsement registry: duplicate endorser address %s", e.Address)
		}
		if _, ok := seenIdentity[e.Identity.UniqueID()]; ok {
			return nil, errors.Errorf("endorsement registry: duplicate endorser identity %s", e.Identity)
		}
		byAddress[e.Address] = e.Identity
		seenIdentity[e.Identity.UniqueID()] = struct{}{}
	}

	return &Registry{
		byAddress: byAddress,
		endorsers: append([]Endorser(nil), endorsers...),
	}, nil
}

// IdentityOf returns the FSC identity registered for the Ethereum address, and whether it is a
// known endorser. The initiator uses it to confirm that a recovered signer is an authorized endorser
// before counting its signature.
func (r *Registry) IdentityOf(address client.Address) (view.Identity, bool) {
	id, ok := r.byAddress[address]

	return id, ok
}

// IsEndorser reports whether the Ethereum address belongs to a registered endorser.
func (r *Registry) IsEndorser(address client.Address) bool {
	_, ok := r.byAddress[address]

	return ok
}

// Identities returns the FSC identities of all endorsers, the set the initiator opens sessions to.
// The slice is a copy; the registry stays immutable.
func (r *Registry) Identities() []view.Identity {
	out := make([]view.Identity, len(r.endorsers))
	for i, e := range r.endorsers {
		out[i] = e.Identity
	}

	return out
}

// Addresses returns the Ethereum addresses of all endorsers, the set to seed the EndorsementVerifier
// with. The slice is a copy.
func (r *Registry) Addresses() []client.Address {
	out := make([]client.Address, len(r.endorsers))
	for i, e := range r.endorsers {
		out[i] = e.Address
	}

	return out
}

// Len returns the number of registered endorsers.
func (r *Registry) Len() int { return len(r.endorsers) }
