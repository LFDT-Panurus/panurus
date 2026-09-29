/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// Message types stamped on the endorsement envelopes exchanged over an FSC session. They travel in
// the versioned envelope of token/services/utils/json/session, so a receiver rejects a message of
// the wrong type or protocol version before decoding the body.
const (
	// TypeEndorseRequest is the initiator's request to an endorser.
	TypeEndorseRequest = "evm.endorse.request"
	// TypeEndorseResponse is the endorser's reply carrying the delta it built and its signature.
	TypeEndorseResponse = "evm.endorse.response"
)

// RequestKind selects which of the two protocols an EndorseRequest carries. Both travel over the same
// message type and reach the same registered Responder (FSC routes a session to a responder by the
// initiating view's Go type alone, and RequestApproval and SetupPublicParams share one Initiator type -
// see registerEndorser's doc comment in driver.go), so the responder tells them apart by this field
// rather than by which view initiated the session.
type RequestKind string

const (
	// KindApproval is an ordinary token-request approval: TokenRequest carries the marshalled request,
	// validated against on-chain token state and the current public parameters.
	KindApproval RequestKind = "approval"
	// KindSetup is a public-parameters setup or update: PublicParamsRaw carries the new parameters,
	// validated structurally but not against any token state, since none is read for a setup delta.
	KindSetup RequestKind = "setup"
)

// EndorseRequest is what the initiator sends to each endorser: the request to validate, the TMS it
// belongs to, the request anchor, and the optional approval metadata. There is deliberately NO digest
// field: an endorser must recompute the StateDelta and its EIP-712 digest from the validated actions
// itself and sign that, never a digest handed to it, or a malicious initiator could get honest
// endorsers to sign a delta that does not match the request they validated.
type EndorseRequest struct {
	// Kind selects which of TokenRequest or PublicParamsRaw is populated, and which validation and
	// translation path the responder runs.
	Kind RequestKind `json:"kind"`
	// TokenRequest is the marshalled token request the endorser validates and translates. Populated
	// only when Kind is KindApproval.
	TokenRequest []byte `json:"token_request,omitempty"`
	// PublicParamsRaw is the new public parameters' raw serialized bytes. Populated only when Kind is
	// KindSetup.
	PublicParamsRaw []byte `json:"public_params_raw,omitempty"`
	// TMSID identifies the token management system (network, channel, namespace) the request targets.
	TMSID token2.TMSID `json:"tms_id"`
	// Anchor is the token-request anchor (the SDK transaction id), the RequestAnchor validation and
	// translation are bound to.
	Anchor string `json:"anchor"`
	// Metadata is the optional approval metadata forwarded from the initiator.
	Metadata map[string][]byte `json:"metadata,omitempty"`
}

// Validate checks the request carries the fields an endorser needs before it does any work.
func (r *EndorseRequest) Validate() error {
	if len(r.Anchor) == 0 {
		return errors.New("endorse request: empty anchor")
	}
	if len(r.TMSID.Network) == 0 || len(r.TMSID.Namespace) == 0 {
		return errors.Errorf("endorse request: incomplete tms id [%s]", r.TMSID)
	}
	switch r.Kind {
	case KindApproval:
		if len(r.TokenRequest) == 0 {
			return errors.New("endorse request: empty token request")
		}
	case KindSetup:
		if len(r.PublicParamsRaw) == 0 {
			return errors.New("endorse request: empty public parameters")
		}
	default:
		return errors.Errorf("endorse request: unknown kind [%s]", r.Kind)
	}

	return nil
}

// EndorseResponse is the endorser's reply. On success it carries the StateDelta this endorser
// translated the request into, the 65-byte {r,s,v} signature over that delta's EIP-712 digest, and
// the Ethereum address it signed with (a hint for the initiator; the initiator still recovers the
// address from the signature and does not trust this field for authorization). On failure Err
// carries the reason and the rest is empty.
//
// The delta travels back rather than being rebuilt by the initiator, mirroring Fabric, where the
// RWSet reaches the client inside the endorsers' proposal responses. Producing it is validation work,
// and validation is what this flow delegates to the endorsers; an initiator that rebuilt it would be
// repeating every endorser's job to learn something the quorum already decided.
type EndorseResponse struct {
	// Delta is the StateDelta this endorser built from the request it validated, and the message its
	// signature covers. The initiator takes the delta from here, requires a threshold of endorsers to
	// have signed the same one, and encodes that into the transaction.
	Delta *statedelta.StateDelta `json:"delta,omitempty"`
	// Signature is the 65-byte {r,s,v} endorsement over Delta's digest, empty on failure.
	Signature []byte `json:"signature,omitempty"`
	// EndorserAddress is the 0x-prefixed address the endorser signed with, for diagnostics.
	EndorserAddress string `json:"endorser_address,omitempty"`
	// Err is a non-empty human-readable reason when the endorser declined to sign.
	Err string `json:"err,omitempty"`
}

// Error returns the endorser's failure as an error, or nil when the response is a success. A reply
// carrying a signature but no delta is a failure too: there is nothing for the initiator to verify
// the signature against, so it cannot be counted toward a quorum.
func (r *EndorseResponse) Error() error {
	if len(r.Err) != 0 {
		return errors.New(r.Err)
	}
	if len(r.Signature) == 0 {
		return errors.New("endorse response: neither signature nor error present")
	}
	if r.Delta == nil {
		return errors.New("endorse response: signature without a state delta")
	}

	return nil
}
