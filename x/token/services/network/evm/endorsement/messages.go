/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Message types stamped on the endorsement envelopes exchanged over an FSC session. They travel in
// the versioned envelope of token/services/utils/json/session, so a receiver rejects a message of
// the wrong type or protocol version before decoding the body.
const (
	// TypeEndorseRequest is the initiator's request to an endorser.
	TypeEndorseRequest = "evm.endorse.request"
	// TypeEndorseResponse is the endorser's reply carrying its signature.
	TypeEndorseResponse = "evm.endorse.response"
)

// EndorseRequest is what the initiator sends to each endorser: the marshalled token request to
// validate, the TMS it belongs to, the request anchor, and the optional approval metadata. There is
// deliberately NO digest field: an endorser must recompute the StateDelta and its EIP-712 digest
// from the validated actions itself and sign that, never a digest handed to it, or a malicious
// initiator could get honest endorsers to sign a delta that does not match the request they
// validated (design §4.5, §6.4; the property @atharrva01 raised on the PR).
type EndorseRequest struct {
	// TokenRequest is the marshalled token request the endorser validates and translates.
	TokenRequest []byte `json:"token_request"`
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
	if len(r.TokenRequest) == 0 {
		return errors.New("endorse request: empty token request")
	}
	if len(r.Anchor) == 0 {
		return errors.New("endorse request: empty anchor")
	}
	if len(r.TMSID.Network) == 0 || len(r.TMSID.Namespace) == 0 {
		return errors.Errorf("endorse request: incomplete tms id [%s]", r.TMSID)
	}

	return nil
}

// EndorseResponse is the endorser's reply. On success it carries the 65-byte {r,s,v} signature over
// the EIP-712 digest the endorser recomputed, and the Ethereum address it signed with (a hint for
// the initiator; the initiator still recovers the address from the signature and does not trust this
// field for authorization). On failure Err carries the reason and Signature is empty.
type EndorseResponse struct {
	// Signature is the 65-byte {r,s,v} endorsement over the recomputed digest, empty on failure.
	Signature []byte `json:"signature,omitempty"`
	// EndorserAddress is the 0x-prefixed address the endorser signed with, for diagnostics.
	EndorserAddress string `json:"endorser_address,omitempty"`
	// Err is a non-empty human-readable reason when the endorser declined to sign.
	Err string `json:"err,omitempty"`
}

// Error returns the endorser's failure as an error, or nil when the response is a success.
func (r *EndorseResponse) Error() error {
	if len(r.Err) != 0 {
		return errors.New(r.Err)
	}
	if len(r.Signature) == 0 {
		return errors.New("endorse response: neither signature nor error present")
	}

	return nil
}
