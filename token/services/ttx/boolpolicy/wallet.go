/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"bytes"
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// QueryEngine knows how to iterate over unspent tokens.
type QueryEngine interface {
	UnspentTokensIteratorBy(ctx context.Context, id string, tokenType token2.Type) (driver.UnspentTokensIterator, error)
}

// TokenVault supports token deletion.
type TokenVault interface {
	DeleteTokens(ctx context.Context, toDelete ...*token2.ID) error
}

// OwnerWallet is a wallet wrapper that exposes policy-identity-filtered token lists.
type OwnerWallet struct {
	wallet      *token.OwnerWallet
	queryEngine QueryEngine
	vault       TokenVault
}

// ListTokens returns unspent tokens whose owner is a policy identity and whose
// component identities include at least one belonging to this wallet.
func (w *OwnerWallet) ListTokens(ctx context.Context, opts ...token.ListTokensOption) (*token2.UnspentTokens, error) {
	compiledOpts, err := token.CompileListTokensOption(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compile options")
	}
	it, err := w.filterIterator(ctx, compiledOpts.TokenType)
	if err != nil {
		return nil, errors.Wrap(err, "token selection failed")
	}
	toks, err := iterators.ReadAllPointers(it)
	if err != nil {
		return nil, err
	}

	return &token2.UnspentTokens{Tokens: toks}, nil
}

// ListTokensIterator is the iterator variant of ListTokens.
func (w *OwnerWallet) ListTokensIterator(ctx context.Context, opts ...token.ListTokensOption) (iterators.Iterator[*token2.UnspentToken], error) {
	compiledOpts, err := token.CompileListTokensOption(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compile options")
	}

	return w.filterIterator(ctx, compiledOpts.TokenType)
}

func (w *OwnerWallet) filterIterator(ctx context.Context, tokenType token2.Type) (iterators.Iterator[*token2.UnspentToken], error) {
	walletID := policyWallet(w.wallet)
	it, err := w.queryEngine.UnspentTokensIteratorBy(ctx, walletID, tokenType)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get iterator over unspent tokens")
	}

	return iterators.Filter(it, containsPolicy), nil
}

// GetWallet returns the owner wallet for the given id.
func GetWallet(context view.Context, id string, opts ...token.ServiceOption) *token.OwnerWallet {
	return ttx.GetWallet(context, id, opts...)
}

// Wallet returns an OwnerWallet wrapping the given wallet with policy-token filtering.
func Wallet(context view.Context, wallet *token.OwnerWallet) *OwnerWallet {
	if wallet == nil {
		return nil
	}
	tms := wallet.TMS()
	svc, err := tokens.GetService(context, tms.ID())
	if err != nil {
		return nil
	}

	return &OwnerWallet{
		wallet:      wallet,
		vault:       svc,
		queryEngine: tms.Vault().NewQueryEngine(),
	}
}

// VerifyApprover checks that tok is owned by a policy identity and that
// approver is listed as one of its component identities.  It returns an error
// when the token is not a policy token or when approver is absent from the
// policy's identity list.
func (w *OwnerWallet) VerifyApprover(tok *token2.UnspentToken, approver token.Identity) error {
	owner, err := identity.UnmarshalTypedIdentity(tok.Owner)
	if err != nil {
		return errors.Wrap(err, "failed to unmarshal token owner")
	}
	if owner.Type != boolpolicy.Policy {
		return errors.Errorf("token is not a policy identity (type %d)", owner.Type)
	}
	pi := &boolpolicy.PolicyIdentity{}
	if err := pi.Deserialize(owner.Identity); err != nil {
		return errors.Wrap(err, "failed to deserialize policy identity")
	}
	for _, componentID := range pi.Identities {
		if bytes.Equal(componentID, approver) {
			return nil
		}
	}

	return errors.Errorf("approver is not listed in policy [%s]", pi.Policy)
}

// containsPolicy returns true when the token's owner is a valid policy identity.
func containsPolicy(tok *token2.UnspentToken) bool {
	owner, err := identity.UnmarshalTypedIdentity(tok.Owner)
	if err != nil {
		return false
	}
	if owner.Type != boolpolicy.Policy {
		return false
	}

	return (&boolpolicy.PolicyIdentity{}).Deserialize(owner.Identity) == nil
}
