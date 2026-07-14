/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"github.com/LFDT-Panurus/panurus/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// WithType returns a list token option that filter by the passed token type.
// If the passed token type is the empty string, all token types are selected.
func WithType(tokenType token2.Type) token.ListTokensOption {
	return func(o *token.ListTokensOptions) error {
		o.TokenType = tokenType

		return nil
	}
}

// MyWallet returns the default wallet, nil if not found.
func MyWallet(context view.Context, opts ...token.ServiceOption) *token.OwnerWallet {
	tms, err := token.GetManagementService(context, opts...)
	if err != nil {
		return nil
	}
	w, err := tms.WalletManager().OwnerWallet(context.Context(), "")
	if err != nil {
		return nil
	}

	return w
}

// MyWalletFromTx returns the default wallet for the tuple (network, channel, namespace) as identified by the passed
// transaction. Returns nil if no wallet is found.
func MyWalletFromTx(context view.Context, tx *Transaction) *token.OwnerWallet {
	tms, err := token.GetManagementService(
		context,
		token.WithNetwork(tx.Network()),
		token.WithChannel(tx.Channel()),
		token.WithNamespace(tx.Namespace()),
	)
	if err != nil {
		return nil
	}
	w, err := tms.WalletManager().OwnerWallet(context.Context(), "")
	if err != nil {
		return nil
	}

	return w
}

// GetWallet returns the wallet whose id is the passed id.
// If the passed id is empty, GetWallet has the same behaviour of MyWallet.
// It returns nil, if no wallet is found.
func GetWallet(context view.Context, id string, opts ...token.ServiceOption) *token.OwnerWallet {
	tms, err := token.GetManagementService(context, opts...)
	if err != nil {
		return nil
	}
	w, err := tms.WalletManager().OwnerWallet(context.Context(), id)
	if err != nil {
		return nil
	}

	return w
}

// GetWalletForChannel returns the wallet whose id is the passed id for the passed channel.
// If the passed id is empty, GetWalletForChannel has the same behaviour of MyWalletFromTx.
// It returns nil, if no wallet is found.
func GetWalletForChannel(context view.Context, channel, id string, opts ...token.ServiceOption) *token.OwnerWallet {
	tms, err := token.GetManagementService(context, append(opts, token.WithChannel(channel))...)
	if err != nil {
		return nil
	}
	w, err := tms.WalletManager().OwnerWallet(context.Context(), id)
	if err != nil {
		return nil
	}

	return w
}

// MyIssuerWallet returns the default issuer wallet, nil if not found
func MyIssuerWallet(context view.Context, opts ...token.ServiceOption) *token.IssuerWallet {
	tms, err := token.GetManagementService(context, opts...)
	if err != nil {
		return nil
	}
	w, err := tms.WalletManager().IssuerWallet(context.Context(), "")
	if err != nil {
		return nil
	}

	return w
}

// GetIssuerWallet returns the issuer wallet whose id is the passed id.
// If the passed id is empty, GetIssuerWallet has the same behaviour of MyIssuerWallet.
// It returns nil, if no wallet is found.
func GetIssuerWallet(context view.Context, id string, opts ...token.ServiceOption) *token.IssuerWallet {
	tms, err := token.GetManagementService(context, opts...)
	if err != nil {
		return nil
	}
	w, err := tms.WalletManager().IssuerWallet(context.Context(), id)
	if err != nil {
		return nil
	}

	return w
}

// GetIssuerWalletForChannel returns the issuer wallet whose id is the passed id for the passed channel.
// If the passed id is empty, GetIssuerWalletForChannel has the same behaviour of MyIssuerWallet.
// It returns nil, if no wallet is found.
func GetIssuerWalletForChannel(context view.Context, channel, id string, opts ...token.ServiceOption) *token.IssuerWallet {
	tms, err := token.GetManagementService(context, append(opts, token.WithChannel(channel))...)
	if err != nil {
		return nil
	}
	w, err := tms.WalletManager().IssuerWallet(context.Context(), id)
	if err != nil {
		return nil
	}

	return w
}

// MyAuditorWallet returns the default auditor wallet, nil if not found.
func MyAuditorWallet(context view.Context, opts ...token.ServiceOption) *token.AuditorWallet {
	tms, err := token.GetManagementService(context, opts...)
	if err != nil {
		return nil
	}
	w, err := tms.WalletManager().AuditorWallet(context.Context(), "")
	if err != nil {
		return nil
	}

	return w
}

// GetAuditorWallet returns the wallet whose id is the passed id.
// If the passed id is empty, GetAuditorWallet has the same behaviour of MyAuditorWallet.
// It returns nil, if no wallet is found.
func GetAuditorWallet(context view.Context, opts ...token.ServiceOption) *token.AuditorWallet {
	tms, err := token.GetManagementService(context, opts...)
	if err != nil {
		return nil
	}
	w, err := tms.WalletManager().AuditorWallet(context.Context(), "")
	if err != nil {
		return nil
	}

	return w
}
