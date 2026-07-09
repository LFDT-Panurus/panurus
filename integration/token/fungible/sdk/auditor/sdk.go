/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditor

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
			registry.RegisterFactory("registerAuditor", &views.RegisterAuditorViewFactory{}),
			registry.RegisterFactory("historyAuditing", &views.ListAuditedTransactionsViewFactory{}),
			registry.RegisterFactory("holding", &views.CurrentHoldingViewFactory{}),
			registry.RegisterFactory("spending", &views.CurrentSpendingViewFactory{}),
			registry.RegisterFactory("balance", &views.BalanceViewFactory{}),
			registry.RegisterFactory("CheckPublicParamsMatch", &views.CheckPublicParamsMatchViewFactory{}),
			registry.RegisterFactory("SetTransactionAuditStatus", &views.SetTransactionAuditStatusViewFactory{}),
			registry.RegisterFactory("CheckTTXDB", &views.CheckTTXDBViewFactory{}),
			registry.RegisterFactory("PruneInvalidUnspentTokens", &views.PruneInvalidUnspentTokensViewFactory{}),
			registry.RegisterFactory("WhoDeletedToken", &views.WhoDeletedTokenViewFactory{}),
			registry.RegisterFactory("ListVaultUnspentTokens", &views.ListVaultUnspentTokensViewFactory{}),
			registry.RegisterFactory("CheckIfExistsInVault", &views.CheckIfExistsInVaultViewFactory{}),
			registry.RegisterFactory("RevokeUser", &views.RevokeUserViewFactory{}),
			registry.RegisterFactory("AuthorizeRedeemIssuer", &views.AuthorizeRedeemIssuerViewFactory{}),
			registry.RegisterFactory("DoesWalletExist", &views.DoesWalletExistViewFactory{}),
			registry.RegisterFactory("TxFinality", &views1.TxFinalityViewFactory{}),
			registry.RegisterFactory("GetPublicParams", &views.GetPublicParamsViewFactory{}),
			registry.RegisterFactory("FetchAndUpdatePublicParams", &views.UpdatePublicParamsViewFactory{}),
			registry.RegisterFactory("SetBinding", &views.SetBindingViewFactory{}),
		)
	}); err != nil {
		return errors.WithMessagef(err, "failed to install auditor's views")
	}

	return nil
}
