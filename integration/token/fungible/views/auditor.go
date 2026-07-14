/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type AuditView struct {
	*token.TMSID
}

func (a *AuditView) Call(context view.Context) (any, error) {
	logger.Debugf("AuditView: [%s]", context.ID())
	tx, err := ttx.ReceiveTransaction(context, TxOpts(a.TMSID, ttx.WithNoTransactionVerification())...)

	assert.NoError(err, "failed receiving transaction")
	logger.Debugf("AuditView: [%s]", tx.ID())

	w := ttx.MyAuditorWallet(context, ServiceOpts(a.TMSID)...)
	assert.NotNil(w, "failed getting default auditor wallet")

	// Validate
	logger.Debugf("AuditView: get auditor [%s]", tx.ID())
	auditor, err := ttx.NewAuditor(context, w)
	assert.NoError(err, "failed to get auditor instance")
	assert.NoError(auditor.Validate(tx), "failed auditing verification")
	logger.Debugf("AuditView: get auditor done [%s]", tx.ID())

	// Check ValidationRecords
	logger.Debugf("AuditView: check metadata [%s]", tx.ID())
	opRaw := tx.ApplicationMetadata("github.com/LFDT-Panurus/panurus/integration/token/fungible/issue")
	if len(opRaw) != 0 {
		assert.Equal([]byte("issue"), opRaw, "expected 'issue' application metadata")
		metaRaw := tx.ApplicationMetadata("github.com/LFDT-Panurus/panurus/integration/token/fungible/meta")
		assert.Equal([]byte("meta"), metaRaw, "expected 'meta' application metadata")
	}
	logger.Debugf("AuditView: check metadata done [%s]", tx.ID())

	// Check limits

	// extract inputs and outputs
	logger.Debugf("AuditView: audit [%s]", tx.ID())
	inputs, outputs, err := auditor.Audit(context.Context(), tx)
	assert.NoError(err, "failed retrieving inputs and outputs")
	logger.Debugf("AuditView: audit done [%s]", tx.ID())
	defer auditor.Release(context.Context(), tx)

	logger.Debugf("AuditView: [%s] get query executor... ", tx.ID())

	// R1: Default payment limit is set to 200. All payments of an amount less than or equal to Default Payment Limit is valid.
	eIDs := inputs.EnrollmentIDs()
	tokenTypes := inputs.TokenTypes()
	fmt.Printf("Limits on inputs [%v][%v]\n", eIDs, tokenTypes)
	for _, eID := range eIDs {
		assert.NotEmpty(eID, "enrollment id should not be empty")
		for _, tokenType := range tokenTypes {
			if tokenType == "MAX" {
				continue
			}
			// compute the payment done in the transaction
			sent := inputs.ByEnrollmentID(eID).ByType(tokenType).Sum()
			received := outputs.ByEnrollmentID(eID).ByType(tokenType).Sum()
			fmt.Printf("Payment Limit: [%s] Sent [%d], Received [%d], type [%s]\n", eID, sent.Int64(), received.Int64(), tokenType)

			diff := big.NewInt(0).Sub(sent, received)
			if diff.Cmp(big.NewInt(0)) <= 0 {
				continue
			}
			fmt.Printf("Payment Limit: [%s] Diff [%d], type [%s]\n", eID, diff.Int64(), tokenType)

			assert.True(diff.Cmp(big.NewInt(200)) <= 0, "payment limit reached [%s][%s][%s]", eID, tokenType, diff.Text(10))
			// R3: The default configuration is customized by a specific organisation (Guarantor)
		}
	}

	// R2: Default cumulative payment limit is set to 2000.
	for _, eID := range eIDs {
		assert.NotEmpty(eID, "enrollment id should not be empty")
		for _, tokenType := range tokenTypes {
			if tokenType == "MAX" {
				continue
			}
			// compute the payment done in the transaction
			sent := inputs.ByEnrollmentID(eID).ByType(tokenType).Sum()
			received := outputs.ByEnrollmentID(eID).ByType(tokenType).Sum()
			fmt.Printf("Cumulative Limit: [%s] Sent [%d], Received [%d], type [%s]\n", eID, sent.Int64(), received.Int64(), tokenType)

			diff := sent.Sub(sent, received)
			if diff.Cmp(big.NewInt(0)) <= 0 {
				continue
			}
			fmt.Printf("Cumulative Limit: [%s] Diff [%d], type [%s]\n", eID, diff.Int64(), tokenType)

			// load last 10 payments, add diff, and check that it is below the threshold
			filter, err := auditor.NewPaymentsFilter().ByEnrollmentId(eID).ByType(tokenType).Last(10).Execute(context.Context())
			assert.NoError(err, "failed retrieving last 10 payments")
			sumLastPayments := filter.Sum()
			fmt.Printf("Cumulative Limit: [%s] Last NewPaymentsFilter [%s], type [%s]\n", eID, sumLastPayments.Text(10), tokenType)

			// R3: The default configuration is customized by a specific organisation (Guarantor)
			total := sumLastPayments.Add(sumLastPayments, diff)
			assert.True(total.Cmp(big.NewInt(2000)) < 0, "cumulative payment limit reached [%s][%s][%s]", eID, tokenType, total.Text(10))
		}
	}

	// R4: Default holding limit is set to 3000.
	eIDs = outputs.EnrollmentIDs()
	tokenTypes = outputs.TokenTypes()
	for _, eID := range eIDs {
		assert.NotEmpty(eID, "enrollment id should not be empty")
		for _, tokenType := range tokenTypes {
			if tokenType == "MAX" {
				continue
			}
			// compute the amount received
			received := outputs.ByEnrollmentID(eID).ByType(tokenType).Sum()
			sent := inputs.ByEnrollmentID(eID).ByType(tokenType).Sum()
			fmt.Printf("Holding Limit: [%s] Sent [%d], Received [%d], type [%s]\n", eID, sent.Int64(), received.Int64(), tokenType)

			diff := received.Sub(received, sent)
			if diff.Cmp(big.NewInt(0)) <= 0 {
				// Nothing received
				continue
			}
			fmt.Printf("Holding Limit: [%s] Diff [%d], type [%s]\n", eID, diff.Int64(), tokenType)

			// load current holding, add diff, and check that it is below the threshold
			filter, err := auditor.NewHoldingsFilter().ByEnrollmentId(eID).ByType(tokenType).Execute(context.Context())
			assert.NoError(err, "failed retrieving holding for [%s][%s]", eIDs, tokenTypes)
			currentHolding := filter.Sum()

			fmt.Printf("Holding Limit: [%s] Current [%s], type [%s]\n", eID, currentHolding.Text(10), tokenType)

			total := currentHolding.Add(currentHolding, diff)
			assert.True(total.Cmp(big.NewInt(3000)) < 0, "holding limit reached [%s][%s][%s]", eID, tokenType, total.Text(10))
		}
	}

	kvsInstance := GetKVS(context)

	// R5: identify redeemed outputs and the issuer that approved each of them, and reject any
	// redeem whose approving issuer is not on the auditor's allow-list.
	for _, redeem := range outputs.ByRedeem().Outputs() {
		if redeem.Issuer.IsNone() {
			return nil, errors.Errorf("redeemed output [%s:%d] has no approving issuer", tx.ID(), redeem.Index)
		}
		issuerKey := utils.Hashable(redeem.Issuer).String()
		fmt.Printf("Redeem: [%s:%d] type [%s] quantity [%s] approved by issuer [%s]\n", tx.ID(), redeem.Index, redeem.Type, redeem.Quantity, issuerKey)

		k := kvs.CreateCompositeKeyOrPanic("authorizedRedeemIssuers", []string{issuerKey})
		if !kvsInstance.Exists(context.Context(), k) {
			return nil, errors.Errorf("redeem [%s:%d] approved by issuer [%s] which is not on the authorized issuers list", tx.ID(), redeem.Index, issuerKey)
		}
	}

	for _, rID := range inputs.RevocationHandles() {
		rh := utils.Hashable(rID).String()
		// logger.Infof("input RH [%s]", rh)
		assert.NotNil(rID, "found an input with empty RH")
		k := kvs.CreateCompositeKeyOrPanic("revocationList", []string{rh})
		if kvsInstance.Exists(context.Context(), k) {
			return nil, errors.Errorf("%s Identity is in revoked state", rh)
		}
	}

	for _, rID := range outputs.RevocationHandles() {
		rh := utils.Hashable(rID).String()
		// logger.Infof("output RH [%s]", rh)
		assert.NotNil(rID, "found an output with empty RH")
		k := kvs.CreateCompositeKeyOrPanic("revocationList", []string{rh})
		if kvsInstance.Exists(context.Context(), k) {
			return nil, errors.Errorf("%s Identity is in revoked state", rh)
		}
	}

	logger.Debugf("AuditView: Approve... [%s]", tx.ID())
	res, err := context.RunView(ttx.NewAuditApproveView(w, tx))
	logger.Debugf("AuditView: Approve...done [%s]", tx.ID())

	return res, err
}

// AuthorizeRedeemIssuer adds the passed issuer identity to the auditor's allow-list of issuers
// that are authorized to approve redeem operations.
type AuthorizeRedeemIssuer struct {
	// Issuer is the identity of the issuer to authorize, in its raw serialized form.
	Issuer []byte
}

type AuthorizeRedeemIssuerView struct {
	*AuthorizeRedeemIssuer
}

func (a *AuthorizeRedeemIssuerView) Call(context view.Context) (any, error) {
	issuerKey := utils.Hashable(a.Issuer).String()
	logger.Infof("authorize redeem issuer [%s]", issuerKey)
	kvsInstance := GetKVS(context)
	k := kvs.CreateCompositeKeyOrPanic("authorizedRedeemIssuers", []string{issuerKey})
	assert.NoError(kvsInstance.Put(context.Context(), k, issuerKey), "failed to put authorized redeem issuer")

	return nil, nil
}

type AuthorizeRedeemIssuerViewFactory struct{}

func (a *AuthorizeRedeemIssuerViewFactory) NewView(in []byte) (view.View, error) {
	f := &AuthorizeRedeemIssuerView{AuthorizeRedeemIssuer: &AuthorizeRedeemIssuer{}}
	err := json.Unmarshal(in, f.AuthorizeRedeemIssuer)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

type RegisterAuditor struct {
	*token.TMSID
}

type RegisterAuditorView struct {
	*RegisterAuditor
}

func (r *RegisterAuditorView) Call(context view.Context) (any, error) {
	return context.RunView(ttx.NewRegisterAuditorView(
		&AuditView{r.TMSID},
		ServiceOpts(r.TMSID)...,
	))
}

type RegisterAuditorViewFactory struct{}

func (p *RegisterAuditorViewFactory) NewView(in []byte) (view.View, error) {
	f := &RegisterAuditorView{RegisterAuditor: &RegisterAuditor{}}
	if in != nil {
		err := json.Unmarshal(in, f.RegisterAuditor)
		assert.NoError(err, "failed unmarshalling input")
	}

	return f, nil
}

type CurrentHolding struct {
	EnrollmentID string
	TokenType    token2.Type
	TMSID        token.TMSID
}

// CurrentHoldingView is used to retrieve the current holding of token type of the passed enrollment id
type CurrentHoldingView struct {
	*CurrentHolding
}

func (r *CurrentHoldingView) Call(context view.Context) (any, error) {
	tms, err := token.GetManagementService(context, token.WithTMSID(r.TMSID))
	assert.NoError(err)
	assert.NotNil(tms, "tms not found [%s]", r.TMSID)

	w, err := tms.WalletManager().AuditorWallet(context.Context(), "")
	assert.NoError(err, "failed getting default auditor wallet")

	auditor, err := ttx.NewAuditor(context, w)
	assert.NoError(err, "failed to get auditor instance")

	filter, err := auditor.NewHoldingsFilter().ByEnrollmentId(r.EnrollmentID).ByType(r.TokenType).Execute(context.Context())
	assert.NoError(err, "failed retrieving holding for [%s][%s]", r.EnrollmentID, r.TokenType)
	currentHolding := filter.Sum()
	decimal := currentHolding.Text(10)
	logger.Debugf("Current Holding: [%s][%s][%s]", r.EnrollmentID, r.TokenType, decimal)

	return decimal, nil
}

type CurrentHoldingViewFactory struct{}

func (p *CurrentHoldingViewFactory) NewView(in []byte) (view.View, error) {
	f := &CurrentHoldingView{CurrentHolding: &CurrentHolding{}}
	err := json.Unmarshal(in, f.CurrentHolding)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

type CurrentSpending struct {
	EnrollmentID string       `json:"enrollment_id"`
	TokenType    token2.Type  `json:"token_type"`
	TMSID        *token.TMSID `json:"tmsid"`
}

// CurrentSpendingView is used to retrieve the current spending of token type of the passed enrollment id
type CurrentSpendingView struct {
	*CurrentSpending
}

func (r *CurrentSpendingView) Call(context view.Context) (any, error) {
	w := ttx.MyAuditorWallet(context, ServiceOpts(r.TMSID)...)
	assert.NotNil(w, "failed getting default auditor wallet")

	auditor, err := ttx.NewAuditor(context, w)
	assert.NoError(err, "failed to get auditor instance")

	filter, err := auditor.NewPaymentsFilter().ByEnrollmentId(r.EnrollmentID).ByType(r.TokenType).Execute(context.Context())
	assert.NoError(err, "failed retrieving spending for [%s][%s]", r.EnrollmentID, r.TokenType)
	currentSpending := filter.Sum()
	decimal := currentSpending.Text(10)
	logger.Debugf("Current Spending: [%s][%s][%s]", r.EnrollmentID, r.TokenType, decimal)

	return decimal, nil
}

type CurrentSpendingViewFactory struct{}

func (p *CurrentSpendingViewFactory) NewView(in []byte) (view.View, error) {
	f := &CurrentSpendingView{CurrentSpending: &CurrentSpending{}}
	err := json.Unmarshal(in, f.CurrentSpending)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

type SetTransactionAuditStatus struct {
	TxID    string
	Status  ttx.TxStatus
	Message string
}

// SetTransactionAuditStatusView is used to set the status of a given transaction in the audit db
type SetTransactionAuditStatusView struct {
	*SetTransactionAuditStatus
}

func (r *SetTransactionAuditStatusView) Call(context view.Context) (any, error) {
	w := ttx.MyAuditorWallet(context)
	assert.NotNil(w, "failed getting default auditor wallet")

	auditor, err := ttx.NewAuditor(context, w)
	assert.NoError(err, "failed to get auditor instance")
	assert.NoError(auditor.SetStatus(context.Context(), r.TxID, r.Status, r.Message), "failed to set status of [%s] to [%d]", r.TxID, r.Status)

	return nil, nil
}

type SetTransactionAuditStatusViewFactory struct{}

func (p *SetTransactionAuditStatusViewFactory) NewView(in []byte) (view.View, error) {
	f := &SetTransactionAuditStatusView{SetTransactionAuditStatus: &SetTransactionAuditStatus{}}
	err := json.Unmarshal(in, f.SetTransactionAuditStatus)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}
