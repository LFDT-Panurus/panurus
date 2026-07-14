/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type Vault interface {
	DeleteTokens(ctx context.Context, toDelete ...*token2.ID) error
}

type QueryEngine interface {
	// UnspentTokensIteratorBy returns an iterator over all unspent tokens by type and id. Type can be empty
	UnspentTokensIteratorBy(ctx context.Context, id string, tokenType token2.Type) (driver.UnspentTokensIterator, error)
}

type TokenVault interface {
	DeleteTokens(ctx context.Context, toDelete ...*token2.ID) error
}

// OwnerWallet is a combination of a wallet and a query service
type OwnerWallet struct {
	wallet      *token.OwnerWallet
	queryEngine QueryEngine
	vault       TokenVault
	bufferSize  uint32
}

// ListTokensAsSender returns a list of non-expired htlc-tokens whose sender id is in this wallet
func (w *OwnerWallet) ListTokensAsSender(ctx context.Context, opts ...token.ListTokensOption) (iterators.Iterator[*token2.UnspentToken], error) {
	compiledOpts, err := token.CompileListTokensOption(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compile options")
	}

	return w.filterIterator(ctx, compiledOpts.TokenType, true, SelectNonExpired)
}

// GetExpiredByHash returns the expired htlc-token whose sender id is in this wallet and whose hash is equal to the one passed as argument.
// It fails if no tokens are found or if more than one token is found.
func (w *OwnerWallet) GetExpiredByHash(ctx context.Context, hash []byte, opts ...token.ListTokensOption) (*token2.UnspentToken, error) {
	compiledOpts, err := token.CompileListTokensOption(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compile options")
	}

	tokens, err := w.filter(ctx, compiledOpts.TokenType, true, (&ExpiredAndHashSelector{Hash: hash}).Select)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to filter")
	}
	if len(tokens.Tokens) != 1 {
		return nil, errors.Errorf("expected to find only one token for the hash [%v], found [%d]", hash, len(tokens.Tokens))
	}

	return tokens.Tokens[0], nil
}

// ListExpired returns a list of expired htlc-tokens whose sender id is in this wallet
func (w *OwnerWallet) ListExpired(ctx context.Context, opts ...token.ListTokensOption) (*token2.UnspentTokens, error) {
	compiledOpts, err := token.CompileListTokensOption(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compile options")
	}

	return w.filter(ctx, compiledOpts.TokenType, true, SelectExpired)
}

// ListExpiredIterator returns an iterator of expired htlc-tokens whose sender id is in this wallet
func (w *OwnerWallet) ListExpiredIterator(ctx context.Context, opts ...token.ListTokensOption) (iterators.Iterator[*token2.UnspentToken], error) {
	compiledOpts, err := token.CompileListTokensOption(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compile options")
	}

	return w.filterIterator(ctx, compiledOpts.TokenType, true, SelectExpired)
}

// ListByPreImage returns a list of tokens whose recipient is this wallet and with a matching preimage
func (w *OwnerWallet) ListByPreImage(ctx context.Context, preImage []byte, opts ...token.ListTokensOption) (*token2.UnspentTokens, error) {
	compiledOpts, err := token.CompileListTokensOption(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compile options")
	}

	return w.filter(ctx, compiledOpts.TokenType, false, (&PreImageSelector{preImage: preImage}).Filter)
}

// ListByPreImageIterator returns an iterator of tokens whose recipient is this wallet and with a matching preimage
func (w *OwnerWallet) ListByPreImageIterator(ctx context.Context, preImage []byte, opts ...token.ListTokensOption) (iterators.Iterator[*token2.UnspentToken], error) {
	compiledOpts, err := token.CompileListTokensOption(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compile options")
	}

	return w.filterIterator(ctx, compiledOpts.TokenType, false, (&PreImageSelector{preImage: preImage}).Filter)
}

// ListTokens returns a list of tokens that matches the passed options and whose recipient belongs to this wallet
func (w *OwnerWallet) ListTokens(ctx context.Context, opts ...token.ListTokensOption) (*token2.UnspentTokens, error) {
	compiledOpts, err := token.CompileListTokensOption(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compile options")
	}

	return w.filter(ctx, compiledOpts.TokenType, false, SelectNonExpired)
}

// ListTokensIterator returns an iterator of tokens that matches the passed options and whose recipient belongs to this wallet
func (w *OwnerWallet) ListTokensIterator(ctx context.Context, opts ...token.ListTokensOption) (iterators.Iterator[*token2.UnspentToken], error) {
	compiledOpts, err := token.CompileListTokensOption(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compile options")
	}

	return w.filterIterator(ctx, compiledOpts.TokenType, false, SelectNonExpired)
}

// GetExpiredReceivedTokenByHash returns the expired htlc-token that matches the passed options, whose recipient belongs to this wallet, is expired, and hash the same hash.
// It fails if no tokens are found or if more than one token is found.
func (w *OwnerWallet) GetExpiredReceivedTokenByHash(ctx context.Context, hash []byte, opts ...token.ListTokensOption) (*token2.UnspentToken, error) {
	compiledOpts, err := token.CompileListTokensOption(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compile options")
	}

	tokens, err := w.filter(ctx, compiledOpts.TokenType, false, (&ExpiredAndHashSelector{Hash: hash}).Select)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to filter")
	}
	if len(tokens.Tokens) != 1 {
		return nil, errors.Errorf("expected to find only one token for the hash [%v], found [%d]", hash, len(tokens.Tokens))
	}

	return tokens.Tokens[0], nil
}

// ListExpiredReceivedTokens returns a list of tokens that matches the passed options, whose recipient belongs to this wallet, and are expired
func (w *OwnerWallet) ListExpiredReceivedTokens(ctx context.Context, opts ...token.ListTokensOption) (*token2.UnspentTokens, error) {
	compiledOpts, err := token.CompileListTokensOption(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compile options")
	}

	return w.filter(ctx, compiledOpts.TokenType, false, SelectExpired)
}

// ListExpiredReceivedTokensIterator returns an iterator of tokens that matches the passed options, whose recipient belongs to this wallet, and are expired
func (w *OwnerWallet) ListExpiredReceivedTokensIterator(ctx context.Context, opts ...token.ListTokensOption) (iterators.Iterator[*token2.UnspentToken], error) {
	compiledOpts, err := token.CompileListTokensOption(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compile options")
	}

	return w.filterIterator(ctx, compiledOpts.TokenType, false, SelectExpired)
}

// DeleteExpiredReceivedTokens removes the expired htlc-tokens that have been reclaimed
func (w *OwnerWallet) DeleteExpiredReceivedTokens(context view.Context, opts ...token.ListTokensOption) error {
	it, err := w.ListExpiredReceivedTokensIterator(context.Context(), opts...)
	if err != nil {
		return errors.WithMessagef(err, "failed to get an iterator of expired received tokens")
	}

	return iterators.ForEach(iterators.Batch(it, w.bufferSize), func(buffer *[]*token2.UnspentToken) error {
		return w.deleteTokens(context, *buffer)
	})
}

// DeleteClaimedSentTokens removes the claimed htlc-tokens whose sender id is in this wallet
func (w *OwnerWallet) DeleteClaimedSentTokens(context view.Context, opts ...token.ListTokensOption) error {
	it, err := w.ListTokensAsSender(context.Context(), opts...)
	if err != nil {
		return errors.WithMessagef(err, "failed to get an iterator of expired received tokens")
	}

	return iterators.ForEach(iterators.Batch(it, w.bufferSize), func(buffer *[]*token2.UnspentToken) error {
		return w.deleteTokens(context, *buffer)
	})
}

func (w *OwnerWallet) deleteTokens(context view.Context, tokens []*token2.UnspentToken) error {
	logger.DebugfContext(context.Context(), "delete tokens from vault [%d][%v]", len(tokens), tokens)
	if len(tokens) == 0 {
		return nil
	}

	// get spent flags
	ids := make([]*token2.ID, len(tokens))
	for i, tok := range tokens {
		ids[i] = &tok.Id
	}
	tms := w.wallet.TMS()
	meta, err := tms.WalletManager().SpentIDs(ids)
	if err != nil {
		return errors.WithMessagef(err, "failed to compute spent ids for [%v]", ids)
	}
	net := network.GetInstance(context, tms.Network(), tms.Channel())
	if net == nil {
		return errors.Errorf("cannot load network [%s:%s]", tms.Network(), tms.Channel())
	}
	spent, err := net.AreTokensSpent(context.Context(), tms.Namespace(), ids, meta)
	if err != nil {
		return errors.WithMessagef(err, "cannot fetch spent flags from network [%s:%s] for ids [%v]", tms.Network(), tms.Channel(), ids)
	}

	// remove the tokens flagged as spent
	var toDelete []*token2.ID
	for i, tok := range tokens {
		if spent[i] {
			logger.DebugfContext(context.Context(), "token [%s] is spent", tok.Id)
			toDelete = append(toDelete, &tok.Id)
		} else {
			logger.DebugfContext(context.Context(), "token [%s] is not spent", tok.Id)
		}
	}
	if err := w.vault.DeleteTokens(context.Context(), toDelete...); err != nil {
		return errors.WithMessagef(err, "failed to remove token ids [%v]", toDelete)
	}

	return nil
}

func (w *OwnerWallet) filter(ctx context.Context, tokenType token2.Type, sender bool, selector SelectFunction) (*token2.UnspentTokens, error) {
	it, err := w.filterIterator(ctx, tokenType, sender, selector)
	if err != nil {
		return nil, errors.Wrap(err, "token selection failed")
	}
	tokens, err := iterators.ReadAllPointers(it)
	if err != nil {
		return nil, err
	}

	return &token2.UnspentTokens{Tokens: tokens}, nil
}

func (w *OwnerWallet) filterIterator(ctx context.Context, tokenType token2.Type, sender bool, selector SelectFunction) (iterators.Iterator[*token2.UnspentToken], error) {
	var walletID string
	if sender {
		walletID = senderWallet(ctx, w.wallet)
	} else {
		walletID = recipientWallet(ctx, w.wallet)
	}
	it, err := w.queryEngine.UnspentTokensIteratorBy(ctx, walletID, tokenType)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get iterator over unspent tokens")
	}

	return iterators.Filter(it, IsScript(selector)), nil
}

// GetWallet returns the wallet whose id is the passed id
func GetWallet(context view.Context, id string, opts ...token.ServiceOption) *token.OwnerWallet {
	return ttx.GetWallet(context, id, opts...)
}

// Wallet returns an OwnerWallet which contains a wallet and a query service
func Wallet(context view.Context, wallet *token.OwnerWallet) *OwnerWallet {
	if wallet == nil {
		return nil
	}

	tms := wallet.TMS()
	tokens, err := tokens.GetService(context, tms.ID())
	if err != nil {
		return nil
	}

	return &OwnerWallet{
		wallet:      wallet,
		vault:       tokens,
		queryEngine: tms.Vault().NewQueryEngine(),
		bufferSize:  100,
	}
}
