/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/id"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/sig"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// AcceptView is used to accept tokens without the need to generate any signature.
// This is a view executed by a responder.
// This view is to be used in conjunction with CollectEndorsementsView.
// Usually, AcceptView is preceded by an invocation of `tx.ReceiveTransaction(context)`
// necessary if the initiator has invoked CollectEndorsementsView.
type AcceptView struct {
	tx   *Transaction
	opts []token.ServiceOption
}

// NewAcceptView returns a new instance of AcceptView given in input a transaction.
func NewAcceptView(tx *Transaction, opts ...token.ServiceOption) *AcceptView {
	return &AcceptView{tx: tx, opts: opts}
}

// Call accepts the tokens created by the transaction this view has been created with.
func (s *AcceptView) Call(context view.Context) (any, error) {
	// validate inputs
	if s.tx == nil {
		return nil, errors.WithMessagef(ErrInvalidInput, "transaction is nil")
	}

	// store transaction in the token transaction database
	if err := StoreTransactionRecords(context, s.tx); err != nil {
		return nil, errors.Wrapf(err, "failed storing transaction records %s", s.tx.ID())
	}

	// ack
	if err := s.ack(context); err != nil {
		return nil, errors.Wrapf(err, "failed acknowledging transaction %s", s.tx.ID())
	}

	// metrics
	labels := []string{
		"network", s.tx.Network(),
		"channel", s.tx.Channel(),
		"namespace", s.tx.Namespace(),
	}
	GetMetrics(context).AcceptedTransactions.With(labels...).Add(1)

	// cache request
	if err := s.cacheRequest(context); err != nil {
		return nil, errors.Wrapf(err, "failed caching request for [%s]", s.tx.ID())
	}

	return s.tx, nil
}

// ack sends back an acknowledgement by signing the received transaction
// with the identity of the FSC node running this stack.
func (s *AcceptView) ack(context view.Context) error {
	txRaw := s.tx.FromRaw
	idProvider, err := id.GetProvider(context)
	if err != nil {
		return errors.Wrapf(err, "failed to get identity provider")
	}
	defaultIdentity := idProvider.DefaultIdentity()

	logger.DebugfContext(context.Context(), "signing ack response [%s] with identity [%s]", utils.Hashable(txRaw), defaultIdentity)
	sigService, err := sig.GetService(context)
	if err != nil {
		return errors.Wrapf(err, "failed to get sig service")
	}
	signer, err := sigService.GetSigner(defaultIdentity)
	if err != nil {
		return errors.WithMessagef(err, "failed to get signer for default identity")
	}
	logger.DebugfContext(context.Context(), "Sign ack for distribution")
	sigma, err := signer.Sign(txRaw)
	if err != nil {
		return errors.WithMessagef(err, "failed to sign ack response")
	}

	// Ack for distribution
	// Send the signature back
	logger.DebugfContext(context.Context(), "ack response: [%s] from [%s]", utils.Hashable(sigma), defaultIdentity)
	if err := jsession.NewTypedSessionFromContext(context).SendTyped(context.Context(), &SignaturePayload{Signature: sigma}, TypeSignature); err != nil {
		return errors.WithMessagef(err, "failed sending ack")
	}

	return nil
}

// cacheRequest stores the token request in the tokens database for faster future lookups.
// Caching failures are logged as warnings but don't cause the operation to fail.
func (s *AcceptView) cacheRequest(context view.Context) error {
	// cache the token request into the tokens db
	t, err := tokens.GetService(context, s.tx.TMSID())
	if err != nil {
		return errors.Wrapf(err, "failed to get tokens db for [%s]", s.tx.TMSID())
	}

	if err := t.CacheRequest(context.Context(), s.tx.TokenRequest); err != nil {
		logger.WarnfContext(context.Context(), "failed to cache token request [%s], this might cause delay, investigate when possible: [%s]", s.tx.TokenRequest.Anchor, err)
	}

	return nil
}
