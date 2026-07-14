/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"slices"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// defaultSpendRequestTimeout is applied by NewRequestSpendView so callers that never
// call WithTimeout are still protected against an unresponsive co-signer.
const defaultSpendRequestTimeout = 30 * time.Second

// SpendRequest is the request to spend a token
type SpendRequest struct {
	Token *token.UnspentToken
}

func ReceiveSpendRequest(context view.Context, opts ...ttx.TxOption) (*SpendRequest, error) {
	logger.DebugfContext(context.Context(), "receive a new spendRequest...")
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

func (r *SpendRequest) Bytes() ([]byte, error) {
	return json.Marshal(r)
}

func (r *SpendRequest) String() string {
	if r.Token == nil {
		return ""
	}

	return r.Token.String()
}

// ReceiveSpendRequestView receives a SpendRequest from the context's session.
type ReceiveSpendRequestView struct{}

func NewReceiveSpendRequestView() *ReceiveSpendRequestView {
	return &ReceiveSpendRequestView{}
}

func (f *ReceiveSpendRequestView) Call(context view.Context) (any, error) {
	tx := &SpendRequest{}
	s := session.NewTypedSessionFromContext(context)
	if err := s.ReceiveTypedWithTimeout(ttx.TypeSpendRequest, tx, time.Minute*4); err != nil {
		logger.ErrorfContext(context.Context(), "failed receiving request: %s", err)

		return nil, err
	}

	return tx, nil
}

// SpendResponse is the response to a SpendRequest
type SpendResponse struct {
	Err error
}

// RequestSpendView sends a SpendRequest to all parties and waits for their responses
type RequestSpendView struct {
	unspentToken *token.UnspentToken
	parties      []view.Identity
	options      *token2.ServiceOptions

	err     error
	timeout time.Duration
}

func NewRequestSpendView(unspentToken *token.UnspentToken, opts ...token2.ServiceOption) *RequestSpendView {
	if unspentToken == nil {
		return &RequestSpendView{err: errors.Errorf("unspentToken is nil")}
	}

	serviceOptions, err := token2.CompileServiceOptions(opts...)
	if err != nil {
		return &RequestSpendView{err: errors.Wrap(err, "failed to compile service options")}
	}

	identities, ok, err := multisig.Unwrap(unspentToken.Owner)
	if err != nil {
		return &RequestSpendView{err: errors.Wrap(err, "failed to unwrap identities")}
	}
	if !ok {
		return &RequestSpendView{err: errors.Errorf("unwrapping failed")}
	}

	return &RequestSpendView{
		unspentToken: unspentToken,
		parties:      identities,
		options:      serviceOptions,
		timeout:      defaultSpendRequestTimeout,
	}
}

func (c *RequestSpendView) Call(context view.Context) (any, error) {
	if c.err != nil {
		return nil, c.err
	}

	// send Transaction to each party and wait for their responses
	request := &SpendRequest{Token: c.unspentToken}

	collector := utils.NewAnswersCollector[string, *SpendResponse](len(c.parties), c.timeout)
	logger.DebugfContext(context.Context(), "Notify %d parties about request", len(c.parties))
	logger.DebugfContext(context.Context(), "Request [%v]", len(c.parties), request)
	counter := 0
	tms, err := token2.GetManagementService(context, token2.WithTMSID(c.options.TMSID()))
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting TMS for [%s]", c.options.TMSID())
	}
	areMe := tms.SigService().AreMe(context.Context(), c.parties...)
	for _, party := range c.parties {
		logger.DebugfContext(context.Context(), "notify party [%s] about request...", party)
		if slices.Contains(areMe, party.UniqueID()) {
			// it is me, skip
			logger.DebugfContext(context.Context(), "notify party [%s] about request, it is me, skipping...", party)

			continue
		}
		go c.collectSpendRequestAnswers(context, party, request, collector)
		counter++
	}

	logger.DebugfContext(context.Context(), "Wait for %d answers", counter)
	answers, err := collector.Collect(context.Context(), counter)
	if err != nil {
		return nil, errors.Wrapf(err, "failed waiting for multisig answers")
	}
	for _, a := range answers {
		if a.Err != nil {
			return nil, errors.Wrapf(a.Err, "got failure [%s] from [%s]", a.Key, a.Err)
		}
		if a.Value.Err != nil {
			return nil, errors.Wrapf(a.Value.Err, "got failure [%s] from [%s]", a.Key, a.Value.Err)
		}
	}

	return nil, nil
}

func (c *RequestSpendView) WithTimeout(timeout time.Duration) *RequestSpendView {
	c.timeout = timeout

	return c
}

func (c *RequestSpendView) collectSpendRequestAnswers(
	context view.Context,
	party view.Identity,
	request *SpendRequest,
	collector *utils.AnswersCollector[string, *SpendResponse]) {
	defer logger.DebugfContext(context.Context(), "received response for from [%v]", party)

	backendSession, err := context.GetSession(c, party, context.Initiator())
	if err != nil {
		collector.Send(party.UniqueID(), nil, errors.Wrapf(err, "failed to create session with [%s]", party))

		return
	}
	s := session.NewTypedSession(context, backendSession)

	logger.DebugfContext(context.Context(), "send request to [%v]", party)
	err = s.SendTyped(context.Context(), request, ttx.TypeSpendRequest)
	if err != nil {
		collector.Send(party.UniqueID(), nil, errors.Wrapf(err, "failed to send request to [%s]", party))

		return
	}
	response := &SpendResponse{}
	if err := s.ReceiveTyped(ttx.TypeSpendResponse, response); err != nil {
		collector.Send(party.UniqueID(), nil, errors.Wrapf(err, "failed to receive response from [%s]", party))

		return
	}
	logger.DebugfContext(context.Context(), "received response from [%v]: [%v]", party, response.Err)

	collector.Send(party.UniqueID(), response, nil)
}

// ReceiveSpendTxView is the co-owner's view: it ACKs the SpendRequest and
// returns the assembled transaction received from the initiator without
// endorsing it. The caller is responsible for inspecting the transaction
// (e.g. confirming it consumes the expected token and does not include
// other tokens owned by this node) and, if the checks pass, running
// ttx.NewEndorseView(tx) to produce the signature.
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
// returns the unsigned spend transaction so the caller can inspect it
// before deciding whether to endorse.
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
		return nil, errors.Wrap(err, "failed to send response")
	}
	logger.DebugfContext(context.Context(), "spend response sent")

	tx, err := ttx.ReceiveTransaction(context)
	if err != nil {
		return nil, errors.Wrap(err, "failed to receive transaction")
	}
	logger.DebugfContext(context.Context(), "multisig tx received with id [%s]", tx.ID())

	return tx, nil
}
