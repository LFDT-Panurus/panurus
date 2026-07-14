/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issuer

import (
	errors2 "errors"

	views1 "github.com/LFDT-Panurus/panurus/integration/token/common/views"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/views"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	dig2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/sdk/dig"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
)

type SDK struct {
	dig2.SDK
}

func NewFrom(sdk dig2.SDK) *SDK {
	return &SDK{SDK: sdk}
}

func (p *SDK) Install() error {
	if err := p.SDK.Install(); err != nil {
		return err
	}
	if err := p.SDK.Container().Invoke(func(
		registry *view.Registry,
	) error {
		return errors2.Join(
			registry.RegisterFactory("issue", &views.IssueCashViewFactory{}),
			registry.RegisterFactory("transfer", &views.TransferViewFactory{}),
			registry.RegisterFactory("transferWithSelector", &views.TransferWithSelectorViewFactory{}),
			registry.RegisterFactory("redeem", &views.RedeemViewFactory{}),
			registry.RegisterFactory("balance", &views.BalanceViewFactory{}),
			registry.RegisterFactory("historyIssuedToken", &views.ListIssuedTokensViewFactory{}),
			registry.RegisterFactory("issuedTokenQuery", &views.ListIssuedTokensViewFactory{}),
			registry.RegisterFactory("issuerBalance", &views.IssuerBalanceViewFactory{}),
			registry.RegisterFactory("GetEnrollmentID", &views.GetEnrollmentIDViewFactory{}),
			registry.RegisterFactory("acceptedTransactionHistory", &views.ListAcceptedTransactionsViewFactory{}),
			registry.RegisterFactory("transactionInfo", &views.TransactionInfoViewFactory{}),
			registry.RegisterFactory("CheckPublicParamsMatch", &views.CheckPublicParamsMatchViewFactory{}),
			registry.RegisterFactory("CheckTTXDB", &views.CheckTTXDBViewFactory{}),
			registry.RegisterFactory("RegisterIssuerIdentity", &views.RegisterIssuerIdentityViewFactory{}),
			registry.RegisterFactory("PruneInvalidUnspentTokens", &views.PruneInvalidUnspentTokensViewFactory{}),
			registry.RegisterFactory("WhoDeletedToken", &views.WhoDeletedTokenViewFactory{}),
			registry.RegisterFactory("GetPublicParams", &views.GetPublicParamsViewFactory{}),
			registry.RegisterFactory("SetKVSEntry", &views.SetKVSEntryViewFactory{}),
			registry.RegisterFactory("DoesWalletExist", &views.DoesWalletExistViewFactory{}),
			registry.RegisterFactory("TxFinality", &views1.TxFinalityViewFactory{}),
			registry.RegisterFactory("issue", &views.IssueCashViewFactory{}),
			registry.RegisterFactory("FetchAndUpdatePublicParams", &views.UpdatePublicParamsViewFactory{}),
			registry.RegisterFactory("SetBinding", &views.SetBindingViewFactory{}),
			registry.RegisterResponder(&views.WithdrawalResponderView{}, &views.WithdrawalInitiatorView{}),
			registry.RegisterResponder(&views.TokensUpgradeResponderView{}, &views.TokensUpgradeInitiatorView{}),
			registry.RegisterResponder(&views.IssuerRedeemAcceptView{}, &views.RedeemView{}),
		)
	}); err != nil {
		return errors.WithMessagef(err, "failed to install issuer's views")
	}

	return nil
}
