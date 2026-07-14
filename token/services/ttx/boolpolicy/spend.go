/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package boolpolicy provides a spend-coordination protocol for policy identity tokens.
// For OR policies a single co-owner can spend unilaterally; for AND policies all
// co-owners must endorse. The RequestSpendView / ReceiveSpendTxView pair mirrors the
// multisig spend protocol and is reused for the AND case.
package boolpolicy

import (
	"slices"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// defaultSpendRequestTimeout is applied by NewRequestSpendView so callers that never
// call WithTimeout are still protected against an unresponsive co-owner.
const defaultSpendRequestTimeout = 30 * time.Second

// SpendRequest carries a policy token selected for spending to co-owners.
type SpendRequest struct {
	Token *token.UnspentToken
}

// Bytes serialises the request.
func (r *SpendRequest) Bytes() ([]byte, error) {
	return json.Marshal(r)
}

// String returns a brief description.
func (r *SpendRequest) String() string {
	if r.Token == nil {
		return ""
	}

	return r.Token.String()
}

// ReceiveSpendRequest receives an incoming SpendRequest on the current session.
func ReceiveSpendRequest(context view.Context) (*SpendRequest, error) {
	logger.DebugfContext(context.Context(), "receive a new policy spendRequest...")
	requestBoxed, err := context.RunView(NewReceiveSpendRequestView(), view.WithSameContext())
	if err != nil {
		return nil, err
	}
	request, ok := requestBoxed.(*SpendRequest)
	if !ok {
		return nil, errors.Errorf("received spendRequest of wrong type [%T]", request)
	}

	return request, nil
}

// ReceiveSpendRequestView is the responder-side view that reads a SpendRequest.
type ReceiveSpendRequestView struct{}

// NewReceiveSpendRequestView returns a new ReceiveSpendRequestView.
func NewReceiveSpendRequestView() *ReceiveSpendRequestView {
	return &ReceiveSpendRequestView{}
}

// Call implements view.View.
func (f *ReceiveSpendRequestView) Call(context view.Context) (any, error) {
	tx := &SpendRequest{}
	s := session.NewTypedSessionFromContext(context)
	if err := s.ReceiveTypedWithTimeout(ttx.TypeSpendRequest, tx, time.Minute*4); err != nil {
		logger.ErrorfContext(context.Context(), "failed receiving request: %s", err)

		return nil, err
	}

	return tx, nil
}

// SpendResponse is the ACK returned by a co-owner after receiving a SpendRequest.
type SpendResponse struct {
	Err error
}

// RequestSpendView sends a SpendRequest to all co-owners of a policy token and
// collects their acknowledgements.  This is needed for AND policies; OR-policy
// spends can skip this step.
type RequestSpendView struct {
	unspentToken *token.UnspentToken
	parties      []view.Identity
	options      *token2.ServiceOptions

	err     error
	timeout time.Duration
}

// NewRequestSpendView creates a RequestSpendView for the given policy token.
func NewRequestSpendView(unspentToken *token.UnspentToken, opts ...token2.ServiceOption) *RequestSpendView {
	if unspentToken == nil {
		return &RequestSpendView{err: errors.Errorf("unspentToken is nil")}
	}
	serviceOptions, err := token2.CompileServiceOptions(opts...)
	if err != nil {
		return &RequestSpendView{err: errors.Wrap(err, "failed to compile service options")}
	}
	pi, ok, err := boolpolicy.Unwrap(unspentToken.Owner)
	if err != nil {
		return &RequestSpendView{err: errors.Wrap(err, "failed to unwrap policy identity")}
	}
	if !ok {
		return &RequestSpendView{err: errors.Errorf("token is not a policy identity")}
	}
	parties := make([]view.Identity, len(pi.Identities))
	for i, b := range pi.Identities {
		parties[i] = b
	}

	return &RequestSpendView{
		unspentToken: unspentToken,
		parties:      parties,
		options:      serviceOptions,
		timeout:      defaultSpendRequestTimeout,
	}
}

// WithTimeout sets the maximum time to wait for all co-owners to respond.
func (c *RequestSpendView) WithTimeout(timeout time.Duration) *RequestSpendView {
	c.timeout = timeout

	return c
}

// Call implements view.View.
func (c *RequestSpendView) Call(context view.Context) (any, error) {
	if c.err != nil {
		return nil, c.err
	}
	request := &SpendRequest{Token: c.unspentToken}
	tms, err := token2.GetManagementService(context, token2.WithTMSID(c.options.TMSID()))
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting TMS for [%s]", c.options.TMSID())
	}
	areMe := tms.SigService().AreMe(context.Context(), c.parties...)
	collector := utils.NewAnswersCollector[string, *SpendResponse](len(c.parties), c.timeout)
	counter := 0
	for _, party := range c.parties {
		if slices.Contains(areMe, party.UniqueID()) {
			continue
		}
		go c.collectAnswers(context, party, request, collector)
		counter++
	}
	answers, err := collector.Collect(context.Context(), counter)
	if err != nil {
		return nil, errors.Wrapf(err, "failed waiting for policy answers")
	}
	for _, a := range answers {
		if a.Err != nil {
			return nil, errors.Wrapf(a.Err, "failure from [%s]", a.Key)
		}
		if a.Value.Err != nil {
			return nil, errors.Wrapf(a.Value.Err, "failure from [%s]", a.Key)
		}
	}

	return nil, nil
}

func (c *RequestSpendView) collectAnswers(context view.Context, party view.Identity, request *SpendRequest, collector *utils.AnswersCollector[string, *SpendResponse]) {
	defer logger.DebugfContext(context.Context(), "received response from [%v]", party)

	backendSession, err := context.GetSession(c, party, context.Initiator())
	if err != nil {
		collector.Send(party.UniqueID(), nil, errors.Wrapf(err, "failed to create session with [%s]", party))

		return
	}
	s := session.NewTypedSession(context, backendSession)
	if err = s.SendTyped(context.Context(), request, ttx.TypeSpendRequest); err != nil {
		collector.Send(party.UniqueID(), nil, errors.Wrapf(err, "failed to send request to [%s]", party))

		return
	}
	response := &SpendResponse{}
	if err := s.ReceiveTyped(ttx.TypeSpendResponse, response); err != nil {
		collector.Send(party.UniqueID(), nil, errors.Wrapf(err, "failed to receive response from [%s]", party))

		return
	}
	collector.Send(party.UniqueID(), response, nil)
}

// ReceiveSpendTxView is the co-owner's view for AND-policy spends: it ACKs
// the SpendRequest and returns the assembled transaction received from the
// initiator without endorsing it. The caller inspects the transaction and,
// if the checks pass, runs ttx.NewEndorseView(tx) to produce the signature.
//
// Splitting receive from endorse lets the application decide which
// business-logic checks to apply rather than baking a fixed policy into
// the library.
type ReceiveSpendTxView struct {
	request *SpendRequest
}

// NewReceiveSpendTxView returns a new ReceiveSpendTxView for the given request.
func NewReceiveSpendTxView(request *SpendRequest) *ReceiveSpendTxView {
	return &ReceiveSpendTxView{request: request}
}

// ReceiveSpendTx is a convenience wrapper that runs ReceiveSpendTxView and
// returns the unsigned spend transaction so the caller can inspect it before
// deciding whether to endorse.
func ReceiveSpendTx(context view.Context, request *SpendRequest) (*Transaction, error) {
	resultBoxed, err := context.RunView(NewReceiveSpendTxView(request))
	if err != nil {
		return nil, errors.Wrap(err, "failed to receive spend transaction")
	}
	result, ok := resultBoxed.(*ttx.Transaction)
	if !ok {
		return nil, errors.Errorf("received result of wrong type [%T]", result)
	}

	return &Transaction{Transaction: result}, nil
}

// Call implements view.View. It sends the SpendResponse ACK, receives the
// assembled transaction, and returns it without endorsing. Endorsement is
// the caller's responsibility once any business-logic checks pass.
func (a *ReceiveSpendTxView) Call(context view.Context) (any, error) {
	s := session.NewTypedSessionFromContext(context)
	if err := s.SendTyped(context.Context(), &SpendResponse{}, ttx.TypeSpendResponse); err != nil {
		return nil, errors.Wrap(err, "failed to send spend response")
	}
	tx, err := ttx.ReceiveTransaction(context)
	if err != nil {
		return nil, errors.Wrap(err, "failed to receive transaction")
	}

	return tx, nil
}
