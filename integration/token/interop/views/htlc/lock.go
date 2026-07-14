/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"crypto"
	"encoding/json"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/id"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// Lock contains the input information to lock a token
type Lock struct {
	// TMSID identifies the TMS to use to perform the token operation
	TMSID token.TMSID
	// ReclamationDeadline is the time after which we can reclaim the funds in case they were not transferred
	ReclamationDeadline time.Duration
	// Wallet is the identifier of the wallet that owns the tokens to transfer
	Wallet string
	// Type of tokens to transfer
	Type token2.Type
	// Amount to transfer
	Amount uint64
	// Recipient is the identity of the recipient's FSC node
	Recipient view.Identity
	// Hash is the hash to use in the script, if nil, a fresh one is generated
	Hash []byte
	// HashFunc is the hash function to use in the script
	HashFunc crypto.Hash
}

type LockInfo struct {
	TxID     string
	PreImage []byte
	Hash     []byte
}

type LockView struct {
	*Lock
}

func (hv *LockView) Call(context view.Context) (res any, err error) {
	var tx *htlc.Transaction
	defer func() {
		if e := recover(); e != nil {
			txID := "none"
			if tx != nil {
				txID = tx.ID()
			}
			if err == nil {
				err = errors.Errorf("<<<[%s]>>>: %s", txID, e)
			} else {
				err = errors.Errorf("<<<[%s]>>>: %s", txID, err)
			}
		}
	}()
	// As a first step, the sender contacts the recipient's FSC node
	// to ask for the identity to use to assign ownership of the freshly created token.
	// Notice that, this step would not be required if the sender knows already which
	// identity the recipient wants to use.
	me, recipient, err := htlc.ExchangeRecipientIdentities(context, "", hv.Recipient, token.WithTMSID(hv.TMSID))
	assert.NoError(err, "failed getting recipient identity")

	// At this point, the sender is ready to prepare the htlc transaction
	// and specify the auditor that must be contacted to approve the operation.
	idProvider, err := id.GetProvider(context)
	assert.NoError(err, "failed getting id provider")
	tx, err = htlc.NewAnonymousTransaction(
		context,
		ttx.WithAuditor(idProvider.Identity("auditor")),
		ttx.WithTMSID(hv.TMSID),
	)
	assert.NoError(err, "failed creating an htlc transaction")

	// The sender will select tokens owned by this wallet
	senderWallet := htlc.GetWallet(context, hv.Wallet, token.WithTMSID(hv.TMSID))
	assert.NotNil(senderWallet, "sender wallet [%s] not found", hv.Wallet)

	// The sender adds a lock operation to the transaction.
	preImage, err := tx.Lock(
		context.Context(),
		senderWallet,
		me,
		hv.Type,
		hv.Amount,
		recipient,
		hv.ReclamationDeadline,
		htlc.WithHash(hv.Hash),
		htlc.WithHashFunc(hv.HashFunc),
		htlc.WithHashEncoding(encoding.None),
	)
	assert.NoError(err, "failed adding a lock operation")

	// The sender is ready to collect all the required signatures.
	// In this case, the sender's and the auditor's signatures.
	// Invoke the Token Chaincode to collect endorsements on the Token Request and prepare the relative transaction.
	// This is all done in one shot running the following view.
	// Before completing, all recipients receive the approved transaction.
	// Depending on the token driver implementation, the recipient's signature might or might not be needed to make
	// the htlc transaction valid.
	_, err = context.RunView(htlc.NewCollectEndorsementsView(tx))
	assert.NoError(err, "failed to collect endorsements for htlc transaction")

	// Last but not least, the locker sends the transaction for ordering and waits for transaction finality.
	_, err = context.RunView(htlc.NewOrderingAndFinalityView(tx))
	assert.NoError(err, "failed to commit htlc transaction")

	outputs, err := tx.Outputs()
	assert.NoError(err, "failed getting outputs")

	return &LockInfo{
		TxID:     tx.ID(),
		PreImage: preImage,
		Hash:     outputs.ScriptAt(0).HashInfo.Hash,
	}, nil
}

type LockViewFactory struct{}

func (p *LockViewFactory) NewView(in []byte) (view.View, error) {
	f := &LockView{Lock: &Lock{}}
	err := json.Unmarshal(in, f.Lock)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

type LockAcceptView struct {
}

func (h *LockAcceptView) Call(context view.Context) (any, error) {
	// The recipient of a token responds, as first operation,
	// to a request for a recipient identity.
	// The recipient can do that by using the following code.
	// The recipient identity will be taken from the default wallet, if not otherwise specified.
	me, sender, err := htlc.RespondExchangeRecipientIdentities(context)
	assert.NoError(err, "failed to respond to identity request")

	// At some point, the recipient receives the htlc transaction that in the meantime has been assembled
	tx, err := htlc.ReceiveTransaction(context)
	assert.NoError(err, "failed to receive tokens")

	// The recipient can perform any check on the transaction as required by the business process
	// In particular, here, the recipient checks that the transaction contains at least one output, and
	// that there is at least one output that names the recipient. The recipient is receiving something.
	outputs, err := tx.Outputs()
	assert.NoError(err, "failed getting outputs")
	assert.True(outputs.Count() >= 1, "expected at least one output, got [%d]", outputs.Count())
	outputs = outputs.ByScript()
	assert.True(outputs.Count() == 1, "expected only one htlc output, got [%d]", outputs.Count())

	script := outputs.ScriptAt(0)
	assert.NoError(script.Validate(time.Now()), "script is not valid")
	assert.NotNil(script, "expected an htlc script")
	assert.True(me.Equal(script.Recipient), "expected me as recipient of the script")
	assert.True(sender.Equal(script.Sender), "expected sender as sender of the script")

	// If everything is fine, the recipient accepts and sends back her signature.
	// Notice that, a signature from the recipient might or might not be required to make the transaction valid.
	// This depends on the driver implementation.
	_, err = context.RunView(htlc.NewAcceptView(tx))
	assert.NoError(err, "failed to accept new tokens")

	// Before completing, the recipient waits for finality of the transaction
	_, err = context.RunView(htlc.NewFinalityView(tx))
	assert.NoError(err, "new tokens were not committed")

	return tx, nil
}
