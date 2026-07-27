/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"

	token2 "github.com/LFDT-Panurus/panurus/token"
	session2 "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
)

// receiveTimeout bounds how long a responder waits for the request on its session before giving up.
const receiveTimeout = 30 * time.Second

// Responder endorses a token request for one TMS. It is the EVM analog of the Fabric
// RequestApprovalResponderView, running the flow of design §6.2:
//
//	receive → authorize → validate → translate → sign → reply
//
// Every collaborator is injected so the flow is unit-testable without an FSC runtime or a live node,
// and so one Responder serves exactly one TMS (multi-TMS routing is the Service's job, keyed by
// TMSID). The responder never signs a digest handed to it: it recomputes the StateDelta from the
// validated actions and signs that, so a malicious initiator cannot get it to endorse a delta that
// does not match the request it validated (design §4.5).
type Responder struct {
	tmsID      token2.TMSID
	authorizer *Authorizer
	factory    *DeltaFactory
	signer     EndorserSigner
	domain     eip712.Domain
}

// NewResponder assembles a Responder for one TMS from its collaborators. The DeltaFactory carries the
// validator, public-parameters provider and the ledger this endorser validates and translates with.
func NewResponder(
	tmsID token2.TMSID,
	authorizer *Authorizer,
	factory *DeltaFactory,
	signer EndorserSigner,
	domain eip712.Domain,
) *Responder {
	return &Responder{
		tmsID:      tmsID,
		authorizer: authorizer,
		factory:    factory,
		signer:     signer,
		domain:     domain,
	}
}

// Call implements the FSC responder view: receive the request on the context's session, endorse it,
// and reply. The session authenticates the caller, so authorization uses ts.Info().Caller rather
// than anything the request declares. A declined endorsement is sent back to the initiator as a
// response carrying the reason (so the initiator sees why), and also returned as the view's error.
func (r *Responder) Call(context view.Context) (any, error) {
	ts := session2.NewTypedSessionFromContext(context)

	var req EndorseRequest
	if err := ts.ReceiveTypedWithTimeout(TypeEndorseRequest, &req, receiveTimeout); err != nil {
		return nil, errors.Wrap(err, "failed to receive endorse request")
	}

	resp := r.Handle(context.Context(), ts.Info().Caller, &req)
	if err := ts.SendTyped(context.Context(), resp, TypeEndorseResponse); err != nil {
		return nil, errors.Wrap(err, "failed to send endorse response")
	}
	if err := resp.Error(); err != nil {
		return nil, err
	}

	return resp, nil
}

// Handle runs the endorsement decision for one request from the authenticated caller and returns the
// response to send back. It never returns an error: a refusal is a well-formed EndorseResponse with
// Err set, so the initiator always learns the outcome. Splitting it out from Call keeps the decision
// testable without a session.
func (r *Responder) Handle(ctx context.Context, caller view.Identity, req *EndorseRequest) *EndorseResponse {
	sig, err := r.endorse(ctx, caller, req)
	if err != nil {
		return &EndorseResponse{Err: err.Error()}
	}

	return &EndorseResponse{Signature: sig, EndorserAddress: r.signer.Address().Hex()}
}

// endorse is the decision proper: authorize, then validate-and-translate through the shared factory,
// then sign. It returns the signature or the first failure. The digest is derived here, from the
// delta this endorser built, never taken from the request.
func (r *Responder) endorse(ctx context.Context, caller view.Identity, req *EndorseRequest) ([]byte, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if !r.tmsID.Equal(req.TMSID) {
		return nil, errors.Errorf("request targets tms [%s], this endorser serves [%s]", req.TMSID, r.tmsID)
	}
	if err := r.authorizer.Authorize(caller); err != nil {
		return nil, err
	}

	delta, err := r.factory.Build(ctx, req)
	if err != nil {
		return nil, err
	}

	digest := eip712.Digest(r.domain, delta)

	return r.signer.Sign(digest)
}

// compile-time check that *token.Validator satisfies RequestValidator, so the production wiring
// (tms.Validator()) can be injected directly.
var _ RequestValidator = (*token2.Validator)(nil)
