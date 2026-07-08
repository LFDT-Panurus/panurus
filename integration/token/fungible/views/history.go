/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"encoding/json"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/pagination"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// ListIssuedTokens contains the input to query the list of issued tokens
type ListIssuedTokens struct {
	// Wallet whose identities own the token
	Wallet string
	// TokenType is the token type to select
	TokenType token2.Type
	// The TMS to pick in case of multiple TMSIDs
	TMSID *token.TMSID
}

type ListIssuedTokensView struct {
	*ListIssuedTokens
}

func (p *ListIssuedTokensView) Call(context view.Context) (any, error) {
	// Tokens issued by identities in this wallet will be listed
	wallet := ttx.GetIssuerWallet(context, p.Wallet, ServiceOpts(p.TMSID)...)
	if wallet == nil {
		return nil, errors.Errorf("wallet [%s] not found", p.Wallet)
	}

	// Return the list of issued tokens by type
	return wallet.ListIssuedTokens(context.Context(), ttx.WithType(p.TokenType))
}

type ListIssuedTokensViewFactory struct{}

func (i *ListIssuedTokensViewFactory) NewView(in []byte) (view.View, error) {
	f := &ListIssuedTokensView{ListIssuedTokens: &ListIssuedTokens{}}
	if err := json.Unmarshal(in, f.ListIssuedTokens); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling input")
	}

	return f, nil
}

// IssuerBalanceQuery contains the input to query the issued/redeemed/net balances of an issuer wallet.
type IssuerBalanceQuery struct {
	// Wallet is the issuer wallet whose balances are computed
	Wallet string
	// TokenType is the token type to select
	TokenType token2.Type
	// The TMS to pick in case of multiple TMSIDs
	TMSID *token.TMSID
}

// IssuerBalance is the result of an IssuerBalanceQuery. Values are decimal strings.
type IssuerBalance struct {
	// Issued is the gross sum of the quantities issued by the wallet.
	Issued string
	// Redeemed is the gross sum of the quantities redeemed against the wallet's issuer.
	Redeemed string
	// Net is the net issued supply: Issued minus Redeemed.
	Net string
}

type IssuerBalanceView struct {
	*IssuerBalanceQuery
}

func (p *IssuerBalanceView) Call(context view.Context) (any, error) {
	wallet := ttx.GetIssuerWallet(context, p.Wallet, ServiceOpts(p.TMSID)...)
	if wallet == nil {
		return nil, errors.Errorf("wallet [%s] not found", p.Wallet)
	}

	opts := &driver.IssuerBalanceOptions{TokenType: p.TokenType}
	issued, err := wallet.IssuedBalance(context.Context(), opts)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting issued balance")
	}
	redeemed, err := wallet.RedeemedBalance(context.Context(), opts)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting redeemed balance")
	}
	net, err := wallet.Balance(context.Context(), opts)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting net balance")
	}

	return IssuerBalance{
		Issued:   issued.String(),
		Redeemed: redeemed.String(),
		Net:      net.String(),
	}, nil
}

type IssuerBalanceViewFactory struct{}

func (i *IssuerBalanceViewFactory) NewView(in []byte) (view.View, error) {
	f := &IssuerBalanceView{IssuerBalanceQuery: &IssuerBalanceQuery{}}
	if err := json.Unmarshal(in, f.IssuerBalanceQuery); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling input")
	}

	return f, nil
}

type ListAuditedTransactions struct {
	From            *time.Time
	To              *time.Time
	SearchDirection ttxdb.SearchDirection
}

type ListAuditedTransactionsView struct {
	*ListAuditedTransactions
}

func (p *ListAuditedTransactionsView) Call(context view.Context) (any, error) {
	// Tokens issued by identities in this wallet will be listed
	w := ttx.MyAuditorWallet(context)
	if w == nil {
		return nil, errors.New("failed getting default auditor wallet")
	}

	// Get query executor
	auditor, err := ttx.NewAuditor(context, w)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get auditor instance")
	}

	it, err := auditor.Transactions(context.Context(), ttxdb.QueryTransactionsParams{From: p.From, To: p.To, SearchDirection: p.SearchDirection}, pagination.None())
	if err != nil {
		return nil, errors.Wrapf(err, "failed querying transactions")
	}

	return iterators.ReadAllPointers(it.Items)
}

type ListAuditedTransactionsViewFactory struct{}

func (i *ListAuditedTransactionsViewFactory) NewView(in []byte) (view.View, error) {
	f := &ListAuditedTransactionsView{ListAuditedTransactions: &ListAuditedTransactions{}}
	if err := json.Unmarshal(in, f.ListAuditedTransactions); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling input")
	}

	return f, nil
}

// ListAcceptedTransactions contains the input to query the list of accepted tokens
type ListAcceptedTransactions struct {
	SenderWallet    string
	RecipientWallet string
	From            *time.Time
	To              *time.Time
	ActionTypes     []ttxdb.ActionType
	Statuses        []ttxdb.TxStatus
	TMSID           *token.TMSID
	IDs             []string
	SearchDirection ttxdb.SearchDirection
}

type ListAcceptedTransactionsView struct {
	*ListAcceptedTransactions
}

func (p *ListAcceptedTransactionsView) Call(context view.Context) (any, error) {
	// Get query executor
	tms, err := token.GetManagementService(context, ServiceOpts(p.TMSID)...)
	assert.NoError(err, "failed getting management service")
	owner := ttx.NewOwner(context, tms)
	it, err := owner.Transactions(context.Context(), ttxdb.QueryTransactionsParams{
		SenderWallet:    p.SenderWallet,
		RecipientWallet: p.RecipientWallet,
		From:            p.From,
		To:              p.To,
		ActionTypes:     p.ActionTypes,
		Statuses:        p.Statuses,
		IDs:             p.IDs,
		SearchDirection: p.SearchDirection,
	}, pagination.None())
	if err != nil {
		return nil, errors.Wrapf(err, "failed querying transactions")
	}

	return iterators.ReadAllPointers(it.Items)
}

type ListAcceptedTransactionsViewFactory struct{}

func (l *ListAcceptedTransactionsViewFactory) NewView(in []byte) (view.View, error) {
	v := &ListAcceptedTransactionsView{ListAcceptedTransactions: &ListAcceptedTransactions{}}
	if err := json.Unmarshal(in, v.ListAcceptedTransactions); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling input")
	}

	return v, nil
}

// TransactionInfo contains the input information to search for transaction info
type TransactionInfo struct {
	TransactionID string
	TMSID         *token.TMSID
}

type TransactionInfoView struct {
	*TransactionInfo
}

func (t *TransactionInfoView) Call(context view.Context) (any, error) {
	tms, err := token.GetManagementService(context, ServiceOpts(t.TMSID)...)
	assert.NoError(err, "failed getting management service")
	owner := ttx.NewOwner(context, tms)
	info, err := owner.TransactionInfo(context.Context(), t.TransactionID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting transaction info")
	}

	return info, nil
}

type TransactionInfoViewFactory struct{}

func (p *TransactionInfoViewFactory) NewView(in []byte) (view.View, error) {
	f := &TransactionInfoView{TransactionInfo: &TransactionInfo{}}
	if err := json.Unmarshal(in, f.TransactionInfo); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling input")
	}

	return f, nil
}
