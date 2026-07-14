/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package party

import (
	errors2 "errors"

	views1 "github.com/LFDT-Panurus/panurus/integration/token/common/views"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/views"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/multisig"
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
			registry.RegisterFactory("MultiSigLock", &views.MultiSigLockViewFactory{}),
			registry.RegisterFactory("MultiSigSpend", &views.MultiSigSpendViewFactory{}),
			registry.RegisterFactory("CoOwnedBalance", &views.CoOwnedBalanceViewFactory{}),
			registry.RegisterFactory("transfer", &views.TransferViewFactory{}),
			registry.RegisterFactory("transferWithSelector", &views.TransferWithSelectorViewFactory{}),
			registry.RegisterFactory("redeem", &views.RedeemViewFactory{}),
			registry.RegisterFactory("swap", &views.SwapInitiatorViewFactory{}),
			registry.RegisterFactory("history", &views.ListUnspentTokensViewFactory{}),
			registry.RegisterFactory("balance", &views.BalanceViewFactory{}),
			registry.RegisterFactory("GetEnrollmentID", &views.GetEnrollmentIDViewFactory{}),
			registry.RegisterFactory("acceptedTransactionHistory", &views.ListAcceptedTransactionsViewFactory{}),
			registry.RegisterFactory("transactionInfo", &views.TransactionInfoViewFactory{}),
			registry.RegisterFactory("prepareTransfer", &views.PrepareTransferViewFactory{}),
			registry.RegisterFactory("broadcastPreparedTransfer", &views.BroadcastPreparedTransferViewFactory{}),
			registry.RegisterFactory("CheckPublicParamsMatch", &views.CheckPublicParamsMatchViewFactory{}),
			registry.RegisterFactory("CheckTTXDB", &views.CheckTTXDBViewFactory{}),
			registry.RegisterFactory("SetTransactionOwnerStatus", &views.SetTransactionOwnerStatusViewFactory{}),
			registry.RegisterFactory("PruneInvalidUnspentTokens", &views.PruneInvalidUnspentTokensViewFactory{}),
			registry.RegisterFactory("WhoDeletedToken", &views.WhoDeletedTokenViewFactory{}),
			registry.RegisterFactory("ListVaultUnspentTokens", &views.ListVaultUnspentTokensViewFactory{}),
			registry.RegisterFactory("withdrawal", &views.WithdrawalInitiatorViewFactory{}),
			registry.RegisterFactory("DoesWalletExist", &views.DoesWalletExistViewFactory{}),
			registry.RegisterFactory("RegisterRecipientData", &views.RegisterRecipientDataViewFactory{}),
			registry.RegisterFactory("TxFinality", &views1.TxFinalityViewFactory{}),
			registry.RegisterFactory("MaliciousTransfer", &views.MaliciousTransferViewFactory{}),
			registry.RegisterFactory("TxStatus", &views.TxStatusViewFactory{}),
			registry.RegisterFactory("TokensUpgrade", &views.TokensUpgradeInitiatorViewFactory{}),
			registry.RegisterFactory("SetSpendableFlag", &views.SetSpendableFlagViewFactory{}),
			registry.RegisterFactory("ListOwnerWalletIDsView", &views.ListOwnerWalletIDsViewFactory{}),
			registry.RegisterFactory("RegisterOwnerIdentity", &views.RegisterOwnerIdentityViewFactory{}),
			registry.RegisterFactory("TokenSelectorUnlock", &views.TokenSelectorUnlockViewFactory{}),
			registry.RegisterFactory("FinalityWithTimeout", &views.FinalityWithTimeoutViewFactory{}),
			registry.RegisterFactory("GetRevocationHandle", &views.GetRevocationHandleViewFactory{}),
			registry.RegisterFactory("GetPublicParams", &views.GetPublicParamsViewFactory{}),
			registry.RegisterFactory("FetchAndUpdatePublicParams", &views.UpdatePublicParamsViewFactory{}),
			registry.RegisterFactory("SetBinding", &views.SetBindingViewFactory{}),
			registry.RegisterResponder(&views.AcceptCashView{}, &views.IssueCashView{}),
			registry.RegisterResponder(&views.AcceptCashView{}, &views.TransferView{}),
			registry.RegisterResponder(&views.AcceptCashView{}, &views.TransferWithSelectorView{}),
			registry.RegisterResponder(&views.AcceptPreparedCashView{}, &views.PrepareTransferView{}),
			registry.RegisterFactory("PolicyLock", &views.PolicyLockViewFactory{}),
			registry.RegisterFactory("PolicySpend", &views.PolicySpendViewFactory{}),
			registry.RegisterFactory("PolicyOwnedBalance", &views.PolicyOwnedBalanceViewFactory{}),
			registry.RegisterResponder(&views.AcceptCashView{}, &views.MultiSigLockView{}),
			registry.RegisterResponder(&views.AcceptCashView{}, &views.MultiSigSpendView{}),
			registry.RegisterResponder(&views.MultiSigAcceptSpendView{}, &multisig.RequestSpendView{}),
			registry.RegisterResponder(&views.AcceptCashView{}, &views.PolicyLockView{}),
			registry.RegisterResponder(&views.AcceptCashView{}, &views.PolicySpendView{}),
			registry.RegisterResponder(&views.PolicyAcceptSpendView{}, &boolpolicy.RequestSpendView{}),
			registry.RegisterResponder(&views.SwapResponderView{}, &views.SwapInitiatorView{}),
			registry.RegisterResponder(&views.AcceptCashView{}, &views.MaliciousTransferView{}),
		)
	}); err != nil {
		return errors.WithMessagef(err, "failed to install party's views")
	}

	return nil
}
