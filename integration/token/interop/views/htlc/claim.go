/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/id"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// Claim contains the input information to claim a token
type Claim struct {
	// TMSID identifies the TMS to use to perform the token operation
	TMSID token.TMSID
	// Wallet is the identifier of the wallet to use
	Wallet string
	// PreImage of the hash encoded in the htlc script in the token to be claimed
	PreImage []byte

	// Script is used to lookup for PreImage in case that field is empty
	Script *htlc.Script
	// ScriptTMSID is the TMSID where to perform the lookup.
	ScriptTMSID token.TMSID
}

type ClaimView struct {
	*Claim
}

func (r *ClaimView) Call(ctx view.Context) (res any, err error) {
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

	preImage := r.PreImage
	if len(preImage) == 0 && r.Script != nil {
		// Scan for the pre-image
		var err error
		preImage, err = htlc.ScanForPreImage(
			ctx,
			r.Script.HashInfo.Hash,
			r.Script.HashInfo.HashFunc,
			r.Script.HashInfo.HashEncoding,
			5*time.Minute,
			token.WithTMSID(r.ScriptTMSID),
		)
		assert.NoError(err, "failed to receive the preImage")

		// double-check the value of the key
		tms, err := token.GetManagementService(ctx, token.WithTMSID(r.ScriptTMSID))
		assert.NoError(err, "failed getting management service")
		network := network.GetInstance(ctx, tms.Network(), tms.Channel())
		assert.NotNil(network, "failed getting network")
		ledger, err := network.Ledger()
		assert.NoError(err, "failed getting ledger")
		transferMetadataKey, err := ledger.TransferMetadataKey(htlc.ClaimKey(r.Script.HashInfo.Hash))
		assert.NoError(err, "failed getting transfer metadata key")

		// double-check the content of the ledger, retry a few time to give time to the committer
		runner := utils.NewRetryRunner(logger, 3, 1*time.Second, true)
		assert.NoError(err, runner.RunWithContext(ctx.Context(), func() error {
			logger.Debugf("check transfer metadata key [%s]...", transferMetadataKey)
			stateValues, err := ledger.GetStates(ctx.Context(), tms.Namespace(), transferMetadataKey)
			if err != nil {
				return err
			}
			logger.Debugf("check transfer metadata key [%s], got [%v]", transferMetadataKey, stateValues)
			if len(stateValues) != 1 {
				return errors.Errorf("expected 1 state, found %d", len(stateValues))
			}
			if !bytes.Equal(stateValues[0], r.PreImage) {
				return errors.Errorf("pre-image mismatch [%s] vs [%s]", utils.Hashable(preImage), utils.Hashable(stateValues[0]))
			}

			return nil
		}))
	}

	claimWallet := htlc.GetWallet(ctx, r.Wallet, token.WithTMSID(r.TMSID))
	assert.NotNil(claimWallet, "wallet [%s] not found", r.Wallet)

	matched, err := htlc.Wallet(ctx, claimWallet).ListByPreImage(ctx.Context(), preImage)
	assert.NoError(err, "htlc script has expired")
	assert.True(matched.Count() == 1, "expected only one htlc script to match [%s], got [%d]", view.Identity(preImage), matched.Count())

	idProvider, err := id.GetProvider(ctx)
	assert.NoError(err, "failed getting id provider")
	tx, err = htlc.NewAnonymousTransaction(
		ctx,
		ttx.WithAuditor(idProvider.Identity("auditor")),
		ttx.WithTMSID(r.TMSID),
	)
	assert.NoError(err, "failed to create an htlc transaction")
	assert.NoError(tx.Claim(claimWallet, matched.At(0), preImage), "failed adding a claim for [%s]", matched.At(0).Id)

	_, err = ctx.RunView(htlc.NewCollectEndorsementsView(tx))
	assert.NoError(err, "failed to collect endorsements on htlc transaction")

	_, err = ctx.RunView(htlc.NewOrderingAndFinalityView(tx))
	assert.NoError(err, "failed to commit htlc transaction")

	return tx.ID(), nil
}

type ClaimViewFactory struct{}

func (p *ClaimViewFactory) NewView(in []byte) (view.View, error) {
	f := &ClaimView{Claim: &Claim{}}
	err := json.Unmarshal(in, f.Claim)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}
