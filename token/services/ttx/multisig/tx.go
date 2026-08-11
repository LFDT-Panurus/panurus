/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Transaction wraps a ttx.Transaction to provide a more user-friendly API for multisig transactions.
type Transaction struct {
	*ttx.Transaction
}

// Wrap wraps a ttx.Transaction to provide a more user-friendly API for multisig transactions.
func Wrap(tx *ttx.Transaction) *Transaction {
	return &Transaction{Transaction: tx}
}

// Lock locks the given amount of tokens of the given type in the sender's wallet and transfers them to the recipient.
func (t *Transaction) Lock(senderWallet *token2.OwnerWallet, tokenType token.Type, amount uint64, recipient token2.Identity, opts ...token2.TransferOption) error {
	return t.Transfer(
		senderWallet,
		tokenType,
		[]uint64{amount},
		[]token2.Identity{recipient},
		opts...,
	)
}

// Spend spends the given token.
func (t *Transaction) Spend(senderWallet *token2.OwnerWallet, at *token.UnspentToken, recipient token2.Identity, opts ...token2.TransferOption) error {
	// convert quantity to uint64
	q, err := token.ToQuantity(at.Quantity, t.TokenRequest.TokenService.PublicParametersManager().PublicParameters().Precision())
	if err != nil {
		return errors.Wrapf(err, "failed to convert quantity [%s] to uint64", at.Quantity)
	}

	return t.Transfer(
		senderWallet,
		at.Type,
		[]uint64{q.ToBigInt().Uint64()},
		[]token2.Identity{recipient},
		append(opts, token2.WithTokenIDs(&at.Id))...,
	)
}
