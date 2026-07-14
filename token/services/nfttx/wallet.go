/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type OwnerWallet struct {
	token.ServiceProvider
	*token.OwnerWallet
	Precision uint64
}

func (o *OwnerWallet) QueryByKey(ctx context.Context, state any, key string, value string) error {
	qe, err := NewQueryExecutor(o.ServiceProvider, o.ID(), o.Precision)
	if err != nil {
		return errors.WithMessagef(err, "failed to create query executor")
	}

	return qe.QueryByKey(ctx, state, key, value)
}

// WithType returns a list token option that filter by the passed token type.
// If the passed token type is the empty string, all token types are selected.
func WithType(tokenType token2.Type) token.ListTokensOption {
	return func(o *token.ListTokensOptions) error {
		o.TokenType = tokenType

		return nil
	}
}

// MyWallet returns the default wallet, nil if not found.
func MyWallet(context view.Context, opts ...token.ServiceOption) *OwnerWallet {
	tms, err := token.GetManagementService(context, opts...)
	if err != nil {
		return nil
	}
	w, err := tms.WalletManager().OwnerWallet(context.Context(), "")
	if err != nil {
		return nil
	}

	return &OwnerWallet{OwnerWallet: w, ServiceProvider: context, Precision: tms.PublicParametersManager().PublicParameters().Precision()}
}

// MyWalletFromTx returns the default wallet for the tuple (network, channel, namespace) as identified by the passed
// transaction. Returns nil if no wallet is found.
func MyWalletFromTx(context view.Context, tx *Transaction) *OwnerWallet {
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

	return &OwnerWallet{OwnerWallet: w, ServiceProvider: context, Precision: tms.PublicParametersManager().PublicParameters().Precision()}
}

// GetWallet returns the wallet whose id is the passed id.
// If the passed id is empty, GetWallet has the same behaviour of MyWallet.
// It returns nil, if no wallet is found.
func GetWallet(context view.Context, id string, opts ...token.ServiceOption) *OwnerWallet {
	tms, err := token.GetManagementService(context, opts...)
	if err != nil {
		return nil
	}
	w, err := tms.WalletManager().OwnerWallet(context.Context(), id)
	if err != nil {
		return nil
	}

	return &OwnerWallet{OwnerWallet: w, ServiceProvider: context, Precision: tms.PublicParametersManager().PublicParameters().Precision()}
}

// GetWalletForChannel returns the wallet whose id is the passed id for the passed channel.
// If the passed id is empty, GetWalletForChannel has the same behaviour of MyWalletFromTx.
// It returns nil, if no wallet is found.
func GetWalletForChannel(context view.Context, channel, id string, opts ...token.ServiceOption) *OwnerWallet {
	tms, err := token.GetManagementService(context, append(opts, token.WithChannel(channel))...)
	if err != nil {
		return nil
	}
	w, err := tms.WalletManager().OwnerWallet(context.Context(), id)
	if err != nil {
		return nil
	}

	return &OwnerWallet{OwnerWallet: w, ServiceProvider: context, Precision: tms.PublicParametersManager().PublicParameters().Precision()}
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
