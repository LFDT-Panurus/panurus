/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// TxOptions contains configuration options for token transactions including auditor selection,
// TMS identification, verification settings, timeouts, and transaction identifiers.
type TxOptions struct {
	Auditor                   view.Identity
	TMSID                     token.TMSID
	NoTransactionVerification bool
	Timeout                   time.Duration
	PollingTimeout            time.Duration
	TxID                      string
	Transaction               *Transaction
	NetworkTxID               network.TxID
	NoCachingRequest          bool
	AnonymousTransaction      bool
}

// CompileOpts applies all provided transaction options and returns the compiled TxOptions.
// Returns an error if any option fails to apply.
func CompileOpts(opts ...TxOption) (*TxOptions, error) {
	txOptions := &TxOptions{}
	for _, opt := range opts {
		if err := opt(txOptions); err != nil {
			return nil, err
		}
	}

	return txOptions, nil
}

// TxOption is a function that modifies TxOptions. It follows the functional options pattern
// for flexible and extensible configuration of token transactions.
type TxOption func(*TxOptions) error

// WithAuditor sets the auditor identity for the transaction. The auditor will validate
// and sign the transaction before it's submitted to the network.
func WithAuditor(auditor view.Identity) TxOption {
	return func(o *TxOptions) error {
		o.Auditor = auditor

		return nil
	}
}

// WithNetwork sets the network identifier for the transaction's TMS.
func WithNetwork(network string) TxOption {
	return func(o *TxOptions) error {
		o.TMSID.Network = network

		return nil
	}
}

// WithChannel sets the channel identifier for the transaction's TMS.
func WithChannel(channel string) TxOption {
	return func(o *TxOptions) error {
		o.TMSID.Channel = channel

		return nil
	}
}

// WithNamespace sets the namespace identifier for the transaction's TMS.
func WithNamespace(namespace string) TxOption {
	return func(o *TxOptions) error {
		o.TMSID.Namespace = namespace

		return nil
	}
}

// WithNoCachingRequest is used to tell the ordering view to not cache the token request
func WithNoCachingRequest() TxOption {
	return func(o *TxOptions) error {
		o.NoCachingRequest = true

		return nil
	}
}

// WithTMS filters by network, channel and namespace. Each of them can be empty
func WithTMS(network, channel, namespace string) TxOption {
	return func(o *TxOptions) error {
		o.TMSID.Network = network
		o.TMSID.Channel = channel
		o.TMSID.Namespace = namespace

		return nil
	}
}

// WithTMSID filters by TMS identifier
func WithTMSID(id token.TMSID) TxOption {
	return func(o *TxOptions) error {
		o.TMSID = id

		return nil
	}
}

// WithTMSIDPointer filters by TMS identifier, if passed
func WithTMSIDPointer(id *token.TMSID) TxOption {
	return func(o *TxOptions) error {
		if id == nil {
			return nil
		}
		o.TMSID = *id

		return nil
	}
}

// WithNoTransactionVerification disables transaction verification when receiving transactions.
// This should only be used in trusted environments or for testing purposes.
func WithNoTransactionVerification() TxOption {
	return func(o *TxOptions) error {
		o.NoTransactionVerification = true

		return nil
	}
}

// WithTimeout sets the overall timeout for transaction operations.
func WithTimeout(timeout time.Duration) TxOption {
	return func(o *TxOptions) error {
		o.Timeout = timeout

		return nil
	}
}

// WithPollingTimeout sets the polling interval for finality checks
func WithPollingTimeout(timeout time.Duration) TxOption {
	return func(o *TxOptions) error {
		if timeout <= 0 {
			return errors.Wrapf(ErrInvalidInput, "polling timeout must be positive")
		}
		o.PollingTimeout = timeout

		return nil
	}
}

// WithTxID sets a specific transaction ID for the transaction.
func WithTxID(txID string) TxOption {
	return func(o *TxOptions) error {
		o.TxID = txID

		return nil
	}
}

// WithTransactions sets an existing transaction to be used in the options.
func WithTransactions(tx *Transaction) TxOption {
	return func(o *TxOptions) error {
		o.Transaction = tx

		return nil
	}
}

// WithNetworkTxID sets the network-specific transaction ID for the transaction.
// This allows using a pre-existing network transaction ID instead of generating a new one.
func WithNetworkTxID(id network.TxID) TxOption {
	return func(o *TxOptions) error {
		o.NetworkTxID = id

		return nil
	}
}

// WithAnonymousTransaction is used to tell if the transaction needs to be anonymous or not
func WithAnonymousTransaction(v bool) TxOption {
	return func(o *TxOptions) error {
		o.AnonymousTransaction = v

		return nil
	}
}

// CompileServiceOptions applies all provided service options and returns the compiled ServiceOptions.
// Returns an error if any option fails to apply.
func CompileServiceOptions(opts ...token.ServiceOption) (*token.ServiceOptions, error) {
	txOptions := &token.ServiceOptions{}
	for _, opt := range opts {
		if err := opt(txOptions); err != nil {
			return nil, err
		}
	}

	return txOptions, nil
}

// Keys used to store application-specific parameters in token.ServiceOptions.Params.
const (
	paramsKeyRecipientData          = "RecipientData"
	paramsKeyRecipientWalletID      = "RecipientWalletID"
	paramsKeyWalletSelector         = "WalletSelector"
	paramsKeyExchangeWalletSelector = "ExchangeWalletSelector"
)

// WithRecipientData is used to add a RecipientData to the service options
func WithRecipientData(recipientData *RecipientData) token.ServiceOption {
	return func(options *token.ServiceOptions) error {
		if options.Params == nil {
			options.Params = map[string]any{}
		}
		options.Params[paramsKeyRecipientData] = recipientData

		return nil
	}
}

// WithRecipientWalletID is used to add a recipient wallet id to the service options
func WithRecipientWalletID(walletID string) token.ServiceOption {
	return func(options *token.ServiceOptions) error {
		if len(walletID) == 0 {
			return nil
		}
		if options.Params == nil {
			options.Params = map[string]any{}
		}
		options.Params[paramsKeyRecipientWalletID] = walletID

		return nil
	}
}

// getRecipientWalletID extracts the recipient wallet ID from service options.
// Returns an empty string if the wallet ID is not set.
func getRecipientWalletID(opts *token.ServiceOptions) string {
	wBoxed, ok := opts.Params[paramsKeyRecipientWalletID]
	if !ok {
		return ""
	}

	return wBoxed.(string)
}

// WalletSelector lets the responder to a RecipientRequest choose which wallet to use
// to answer the request. defaultWallet is the wallet that would be used absent a
// selector (the responder's configured wallet, or the request's WalletID as fallback).
// Returning defaultWallet unchanged preserves the default behavior.
type WalletSelector func(request *RecipientRequest, defaultWallet string) (string, error)

// ExchangeWalletSelector is the WalletSelector equivalent for ExchangeRecipientRequest.
type ExchangeWalletSelector func(request *ExchangeRecipientRequest, defaultWallet string) (string, error)

// WithWalletSelector sets a callback used by the responder to a RecipientRequest to
// choose which wallet to use, overriding the default wallet resolution.
// The returned token.ServiceOption never fails to apply; it always returns a nil error,
// consistent with the other option setters in this file (e.g. WithRecipientData).
func WithWalletSelector(selector WalletSelector) token.ServiceOption {
	return func(options *token.ServiceOptions) error {
		if selector == nil {
			return nil
		}
		if options.Params == nil {
			options.Params = map[string]any{}
		}
		options.Params[paramsKeyWalletSelector] = selector

		return nil
	}
}

// getWalletSelector extracts the WalletSelector from service options, if set.
// Returns nil if the selector is not set or is not a WalletSelector.
func getWalletSelector(opts *token.ServiceOptions) WalletSelector {
	sBoxed, ok := opts.Params[paramsKeyWalletSelector]
	if !ok {
		return nil
	}
	selector, ok := sBoxed.(WalletSelector)
	if !ok {
		return nil
	}

	return selector
}

// WithExchangeWalletSelector sets a callback used by the responder to an
// ExchangeRecipientRequest to choose which wallet to use, overriding the default wallet
// resolution.
// The returned token.ServiceOption never fails to apply; it always returns a nil error,
// consistent with the other option setters in this file (e.g. WithRecipientData).
func WithExchangeWalletSelector(selector ExchangeWalletSelector) token.ServiceOption {
	return func(options *token.ServiceOptions) error {
		if selector == nil {
			return nil
		}
		if options.Params == nil {
			options.Params = map[string]any{}
		}
		options.Params[paramsKeyExchangeWalletSelector] = selector

		return nil
	}
}

// getExchangeWalletSelector extracts the ExchangeWalletSelector from service options, if set.
// Returns nil if the selector is not set or is not an ExchangeWalletSelector.
func getExchangeWalletSelector(opts *token.ServiceOptions) ExchangeWalletSelector {
	sBoxed, ok := opts.Params[paramsKeyExchangeWalletSelector]
	if !ok {
		return nil
	}
	selector, ok := sBoxed.(ExchangeWalletSelector)
	if !ok {
		return nil
	}

	return selector
}
