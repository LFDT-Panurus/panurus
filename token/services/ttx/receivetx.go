/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"encoding/json"
	"runtime/debug"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/utils"
	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	utilsession "github.com/LFDT-Panurus/panurus/token/services/utils/session"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// ReceiveTransactionView is a view to read a transaction from the context's session.
type ReceiveTransactionView struct {
	opts []TxOption
}

// NewReceiveTransactionView returns a new instance of ReceiveTransactionView with the given options.
func NewReceiveTransactionView(opts ...TxOption) *ReceiveTransactionView {
	return &ReceiveTransactionView{
		opts: opts,
	}
}

// Call listens to a versioned envelope from the context's session and returns a transaction.
// Supported message types are TypeTransaction, TypeTransactionResponse, and TypeSignatureRequest.
// If no timeout is specified via the opts, 4 minutes is used as default.
func (f *ReceiveTransactionView) Call(context view.Context) (any, error) {
	options, err := CompileOpts(f.opts...)
	if err != nil {
		return nil, errors.Join(err, ErrFailedCompilingOptions)
	}
	if options.Timeout == 0 {
		options.Timeout = time.Minute * 4
	}

	jsonSession := jsession.JSON(context)
	raw, err := jsonSession.ReceiveRawWithTimeout(options.Timeout)
	if err != nil {
		if errors.Is(err, utilsession.ErrTimeout) {
			return nil, errors.Join(err, ErrTimeout)
		}
		logger.ErrorfContext(context.Context(), err.Error())

		return nil, err
	}
	if len(raw) == 0 {
		info := context.Session().Info()
		logger.ErrorfContext(context.Context(), "received empty message, session closed [%s:%v]: [%s]", info.ID, info.Closed, string(debug.Stack()))

		return nil, errors.Errorf("received empty message, session closed [%s:%v]", info.ID, info.Closed)
	}

	env, err := jsession.UnwrapEnvelope(raw, "")
	if err != nil {
		return nil, err
	}
	if len(env.Body) == 0 {
		return nil, errors.Errorf("received empty envelope body")
	}

	logger.DebugfContext(context.Context(), "ReceiveTransactionView: received %s, len [%d][%s]", env.Type, len(env.Body), utils.Hashable(env.Body))

	switch env.Type {
	case TypeTransaction, TypeTransactionResponse:
		var payload TransactionPayload
		if err := json.Unmarshal(env.Body, &payload); err != nil {
			return nil, errors.Wrap(err, "failed unmarshalling transaction payload")
		}

		return NewTransactionFromBytes(context, payload.Raw)
	case TypeSignatureRequest:
		var signatureRequest SignatureRequest
		if err := json.Unmarshal(env.Body, &signatureRequest); err != nil {
			return nil, errors.Wrapf(err, "failed unmarshalling signature request")
		}

		return NewTransactionFromSignatureRequest(context, &signatureRequest)
	default:
		return nil, errors.Join(errors.Errorf("unexpected message type [%s]", env.Type), jsession.ErrTypeMismatch)
	}
}
