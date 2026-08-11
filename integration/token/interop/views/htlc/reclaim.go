/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/id"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// ReclaimAll contains the input information to reclaim tokens
type ReclaimAll struct {
	// TMSID identifies the TMS to use to perform the token operation
	TMSID token.TMSID
	// Wallet is the identifier of the wallet that owns the tokens to reclaim
	Wallet string
}

// ReclaimAllView is the view of a party who wants to reclaim all previously locked tokens with an expired timeout
type ReclaimAllView struct {
	*ReclaimAll
}

func (r *ReclaimAllView) Call(context view.Context) (any, error) {
	// The sender will select tokens owned by this wallet
	senderWallet := htlc.GetWallet(context, r.Wallet, token.WithTMSID(r.TMSID))
	assert.NotNil(senderWallet, "sender wallet [%s] not found", r.Wallet)

	expired, err := htlc.Wallet(context, senderWallet).ListExpired(context.Context())
	assert.NoError(err, "cannot retrieve list of expired tokens")
	assert.True(expired.Count() > 0, "no htlc script has expired")

	idProvider, err := id.GetProvider(context)
	assert.NoError(err, "failed getting id provider")
	tx, err := htlc.NewAnonymousTransaction(
		context,
		ttx.WithAuditor(idProvider.Identity("auditor")),
		ttx.WithTMSID(r.TMSID),
	)
	assert.NoError(err, "failed to create an htlc transaction")
	for _, id := range expired.Tokens {
		assert.NoError(tx.Reclaim(senderWallet, id), "failed adding a reclaim for [%s]", id)
	}

	// The sender is ready to collect all the required signatures.
	// In this case, the sender's and the auditor's signatures.
	// Invoke the Token Chaincode to collect endorsements on the Token Request and prepare the relative transaction.
	// This is all done in one shot running the following view.
	_, err = context.RunView(htlc.NewCollectEndorsementsView(tx))
	assert.NoError(err, "failed to collect endorsements on htlc transaction")

	// Last but not least, the transaction is sent for ordering, and we wait for transaction finality.
	_, err = context.RunView(htlc.NewOrderingAndFinalityView(tx))
	assert.NoError(err, "failed to commit htlc transaction")

	return tx.ID(), nil
}

type ReclaimAllViewFactory struct{}

func (p *ReclaimAllViewFactory) NewView(in []byte) (view.View, error) {
	f := &ReclaimAllView{ReclaimAll: &ReclaimAll{}}
	err := json.Unmarshal(in, f.ReclaimAll)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

// ReclaimByHash contains the input information to reclaim tokens
type ReclaimByHash struct {
	// TMSID identifies the TMS to use to perform the token operation
	TMSID token.TMSID
	// Wallet is the identifier of the wallet that owns the tokens to reclaim
	Wallet string
	// Hash is the hash the reclaim should refer to
	Hash []byte
}

// ReclaimByHashView is the view of a party who wants to reclaim all previously locked tokens with an expired timeout
type ReclaimByHashView struct {
	*ReclaimByHash
}

func (r *ReclaimByHashView) Call(context view.Context) (any, error) {
	// The sender will select tokens owned by this wallet
	senderWallet := htlc.GetWallet(context, r.Wallet, token.WithTMSID(r.TMSID))
	assert.NotNil(senderWallet, "sender wallet [%s] not found", r.Wallet)

	expired, err := htlc.Wallet(context, senderWallet).GetExpiredByHash(context.Context(), r.Hash)
	assert.NoError(err, "cannot retrieve list of expired tokens")
	assert.NotNil(expired, "no htlc script with hash [%v] has expired", r.Hash)

	idProvider, err := id.GetProvider(context)
	assert.NoError(err, "failed to get identity provider")
	tx, err := htlc.NewAnonymousTransaction(
		context,
		ttx.WithAuditor(idProvider.Identity("auditor")),
		ttx.WithTMSID(r.TMSID),
	)
	assert.NoError(err, "failed to create an htlc transaction")
	assert.NoError(tx.Reclaim(senderWallet, expired), "failed adding a reclaim for [%s]", expired)

	// The sender is ready to collect all the required signatures.
	// In this case, the sender's and the auditor's signatures.
	// Invoke the Token Chaincode to collect endorsements on the Token Request and prepare the relative transaction.
	// This is all done in one shot running the following view.
	_, err = context.RunView(htlc.NewCollectEndorsementsView(tx))
	assert.NoError(err, "failed to collect endorsements on htlc transaction")

	// Last but not least, the transaction is sent for ordering, and we wait for transaction finality.
	_, err = context.RunView(htlc.NewOrderingAndFinalityView(tx))
	assert.NoError(err, "failed to commit htlc transaction")

	return tx.ID(), nil
}

type ReclaimByHashViewFactory struct{}

func (p *ReclaimByHashViewFactory) NewView(in []byte) (view.View, error) {
	f := &ReclaimByHashView{ReclaimByHash: &ReclaimByHash{}}
	err := json.Unmarshal(in, f.ReclaimByHash)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

// CheckExistenceReceivedExpiredByHash contains the input information to reclaim tokens
type CheckExistenceReceivedExpiredByHash struct {
	// TMSID identifies the TMS to use to perform the token operation
	TMSID token.TMSID
	// Wallet is the identifier of the wallet that owns the tokens to reclaim
	Wallet string
	// Hash is the hash the lookup should refer to
	Hash []byte
	// Exists if true, enforce that a token exists, false otherwiese
	Exists bool
}

// CheckExistenceReceivedExpiredByHashView is the view of a party who wants to reclaim all previously locked tokens with an expired timeout
type CheckExistenceReceivedExpiredByHashView struct {
	*CheckExistenceReceivedExpiredByHash
}

func (r *CheckExistenceReceivedExpiredByHashView) Call(context view.Context) (any, error) {
	// The sender will select tokens owned by this wallet
	senderWallet := htlc.GetWallet(context, r.Wallet, token.WithTMSID(r.TMSID))
	assert.NotNil(senderWallet, "sender wallet [%s] not found", r.Wallet)

	expired, err := htlc.Wallet(context, senderWallet).GetExpiredReceivedTokenByHash(context.Context(), r.Hash)
	if r.Exists {
		assert.NoError(err, "cannot retrieve expired received token by hash [%s]", r.Hash)
		assert.NotNil(expired, "no htlc script with hash [%v] has expired", r.Hash)
	} else {
		assert.Error(err)
		assert.True(expired == nil)
	}

	return nil, nil
}

type CheckExistenceReceivedExpiredByHashViewFactory struct{}

func (p *CheckExistenceReceivedExpiredByHashViewFactory) NewView(in []byte) (view.View, error) {
	f := &CheckExistenceReceivedExpiredByHashView{CheckExistenceReceivedExpiredByHash: &CheckExistenceReceivedExpiredByHash{}}
	err := json.Unmarshal(in, f.CheckExistenceReceivedExpiredByHash)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}
