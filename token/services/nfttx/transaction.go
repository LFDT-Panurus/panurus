/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx

import (
	"encoding/base64"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/nfttx/marshaller"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

var logger = logging.MustGetLogger()

type TxOption ttx.TxOption

func WithAuditor(auditor view.Identity) TxOption {
	return func(o *ttx.TxOptions) error {
		o.Auditor = auditor

		return nil
	}
}

type Transaction struct {
	*ttx.Transaction
}

func NewAnonymousTransaction(sp view.Context, opts ...TxOption) (*Transaction, error) {
	// convert opts to ttx.TxOption
	txOpts := make([]ttx.TxOption, len(opts))
	for i, opt := range opts {
		txOpts[i] = ttx.TxOption(opt)
	}
	tx, err := ttx.NewAnonymousTransaction(sp, txOpts...)
	if err != nil {
		return nil, err
	}

	return &Transaction{Transaction: tx}, nil
}

func Wrap(tx *ttx.Transaction) *Transaction {
	return &Transaction{Transaction: tx}
}

func ReceiveTransaction(context view.Context) (*Transaction, error) {
	logger.DebugfContext(context.Context(), "receive a new transaction...")

	txBoxed, err := context.RunView(ttx.NewReceiveTransactionView(), view.WithSameContext())
	if err != nil {
		return nil, err
	}

	cctx, ok := txBoxed.(*ttx.Transaction)
	if !ok {
		return nil, errors.Errorf("received transaction of wrong type [%T]", cctx)
	}
	logger.DebugfContext(context.Context(), "received transaction with id [%s]", cctx.ID())
	// Check that the transaction is valid
	if err := cctx.IsValid(context.Context()); err != nil {
		return nil, errors.WithMessagef(err, "invalid transaction %s", cctx.ID())
	}

	return &Transaction{Transaction: cctx}, nil
}

func (t *Transaction) Issue(wallet *token.IssuerWallet, state any, recipient view.Identity, opts ...token.IssueOption) error {
	// set state id first
	_, err := t.setStateID(state)
	if err != nil {
		return err
	}
	// marshal state to json
	stateJSON, err := marshaller.Marshal(state)
	if err != nil {
		return errors.Wrap(err, "failed to marshal state")
	}
	stateJSONStr := token2.Type(base64.StdEncoding.EncodeToString(stateJSON))

	// Issue
	return t.Transaction.Issue(wallet, recipient, stateJSONStr, 1, opts...)
}

func (t *Transaction) Transfer(wallet *OwnerWallet, state any, recipient view.Identity, opts ...token.TransferOption) error {
	// marshal state to json
	stateJSON, err := marshaller.Marshal(state)
	if err != nil {
		return errors.Wrap(err, "failed to marshal state")
	}
	stateJSONStr := token2.Type(base64.StdEncoding.EncodeToString(stateJSON))

	return t.Transaction.Transfer(wallet.OwnerWallet, stateJSONStr, []uint64{1}, []view.Identity{recipient}, opts...)
}

func (t *Transaction) Outputs() (*OutputStream, error) {
	os, err := t.Transaction.Outputs()
	if err != nil {
		return nil, err
	}

	return &OutputStream{OutputStream: os}, nil
}

func (t *Transaction) setStateID(s any) (string, error) {
	logger.Debugf("setStateID %v...", s)
	defer logger.Debugf("setStateID...done")
	var key string
	var err error
	switch d := s.(type) {
	case AutoLinearState:
		logger.Debugf("AutoLinearState...")
		key, err = d.GetLinearID()
		if err != nil {
			return "", err
		}
	case LinearState:
		logger.Debugf("LinearState...")
		key = GenerateUUID()
		key = d.SetLinearID(key)
	default:
		return "", nil
	}

	return key, nil
}
