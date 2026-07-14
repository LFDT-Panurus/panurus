/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"bytes"
	"context"
	"fmt"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
)

var (
	logger = logging.MustGetLogger()
)

type TokenTransactionDB interface {
	GetTokenRequest(ctx context.Context, txID string) ([]byte, error)
	Transactions(ctx context.Context, params driver.QueryTransactionsParams, pagination driver2.Pagination) (*driver2.PageIterator[*driver.TransactionRecord], error)
}

//go:generate counterfeiter -o mock/token_management_service_provider.go --fake-name TokenManagementServiceProvider . TokenManagementServiceProvider
type TokenManagementServiceProvider interface {
	GetManagementService(opts ...token.ServiceOption) (*token.ManagementService, error)
}

//go:generate counterfeiter -o mock/network_provider.go --fake-name NetworkProvider . NetworkProvider
type NetworkProvider interface {
	GetNetwork(network string, channel string) (*network.Network, error)
}

type Checker = func(ctx context.Context) ([]string, error)

type NamedChecker struct {
	Name    string
	Checker Checker
}

type ChecksService struct {
	checkers []NamedChecker
}

func NewChecksService(checkers []NamedChecker) *ChecksService {
	return &ChecksService{checkers: checkers}
}

func (a *ChecksService) Check(ctx context.Context) ([]string, error) {
	var errorMessages []string
	for _, checker := range a.checkers {
		errs, err := checker.Checker(ctx)
		if err != nil {
			return nil, errors.Wrapf(err, "failed checking with checker [%s]", checker.Name)
		}
		errorMessages = append(errorMessages, errs...)
	}

	return errorMessages, nil
}

type DefaultCheckers struct {
	tmsProvider     TokenManagementServiceProvider
	networkProvider NetworkProvider
	db              TokenTransactionDB
	tokenDB         *tokens.Service
	tmsID           token.TMSID
}

func NewDefaultCheckers(tmsProvider TokenManagementServiceProvider, networkProvider NetworkProvider, db TokenTransactionDB, tokenDB *tokens.Service, tmsID token.TMSID) []NamedChecker {
	checkers := &DefaultCheckers{tmsProvider: tmsProvider, networkProvider: networkProvider, db: db, tokenDB: tokenDB, tmsID: tmsID}

	return []NamedChecker{
		{
			Name:    "Transaction Check",
			Checker: checkers.CheckTransactions,
		},
		{
			Name:    "Unspent Tokens Check",
			Checker: checkers.CheckUnspentTokens,
		},
		{
			Name:    "Token Spendability Check",
			Checker: checkers.CheckTokenSpendability,
		},
	}
}

// CheckTransactions checks that for each transaction stored in the local database,
// the status of this transaction matches the status of the transaction on the ledger.
func (a *DefaultCheckers) CheckTransactions(ctx context.Context) ([]string, error) {
	var errorMessages []string

	tms, err := a.tmsProvider.GetManagementService(token.WithTMSID(a.tmsID))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting tms [%s]", a.tmsID)
	}
	net, err := a.networkProvider.GetNetwork(tms.Network(), tms.Channel())
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get network [%s]", tms.ID())
	}
	l, err := net.Ledger()
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get ledger [%s]", tms.ID())
	}

	it, err := a.db.Transactions(ctx, driver.QueryTransactionsParams{}, nil)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed querying transactions [%s]", tms.ID())
	}
	defer it.Items.Close()
	for {
		transactionRecord, err := it.Items.Next()
		if err != nil {
			return nil, errors.WithMessagef(err, "failed querying transactions [%s]", tms.ID())
		}
		if transactionRecord == nil {
			break
		}

		tokenRequest, err := a.db.GetTokenRequest(ctx, transactionRecord.TxID)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed getting token request [%s]", transactionRecord.TxID)
		}
		if tokenRequest == nil {
			return nil, errors.Errorf("token request [%s] is nil", transactionRecord.TxID)
		}

		// check the ledger
		lVC, _, err := l.Status(transactionRecord.TxID)
		if err != nil {
			lVC = network.Unknown
		}
		switch {
		case transactionRecord.Status == driver.Confirmed && lVC != network.Valid:
			if err != nil {
				errorMessages = append(errorMessages, fmt.Sprintf("failed to get ledger transaction status for [%s]: [%s]", transactionRecord.TxID, err))
			}
			errorMessages = append(errorMessages, fmt.Sprintf("transaction record [%s] is valid for vault but not for the ledger [%d]", transactionRecord.TxID, lVC))
		case transactionRecord.Status == driver.Deleted && lVC != network.Invalid:
			if lVC != network.Unknown || transactionRecord.Status != driver.Deleted {
				if err != nil {
					errorMessages = append(errorMessages, fmt.Sprintf("failed to get ledger transaction status for [%s]: [%s]", transactionRecord.TxID, err))
				}
				errorMessages = append(errorMessages, fmt.Sprintf("transaction record [%s] is invalid for vault but not for the ledger [%d]", transactionRecord.TxID, lVC))
			}
		case transactionRecord.Status == driver.Unknown && lVC != network.Unknown:
			errorMessages = append(errorMessages, fmt.Sprintf("transaction record [%s] is unknown for vault but not for the ledger [%d]", transactionRecord.TxID, lVC))
		case transactionRecord.Status == driver.Pending && lVC == network.Busy:
			// this is fine, let's continue
		case transactionRecord.Status == driver.Pending && lVC != network.Unknown:
			errorMessages = append(errorMessages, fmt.Sprintf("transaction record [%s] is busy for vault but not for the ledger [%d]", transactionRecord.TxID, lVC))
		}
	}

	return errorMessages, nil
}

// CheckUnspentTokens checks that for each unspent token, the content of the local database matches the ledger
func (a *DefaultCheckers) CheckUnspentTokens(ctx context.Context) ([]string, error) {
	var errorMessages []string

	tms, err := a.tmsProvider.GetManagementService(token.WithTMSID(a.tmsID))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting tms [%s]", a.tmsID)
	}
	net, err := a.networkProvider.GetNetwork(tms.Network(), tms.Channel())
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get network [%s]", tms.ID())
	}
	qe := tms.Vault().NewQueryEngine()
	uit, err := qe.UnspentTokensIterator(ctx)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed querying utxo engine")
	}
	defer uit.Close()
	var unspentTokenIDs []*token2.ID
	for {
		tok, err := uit.Next()
		if err != nil {
			return nil, errors.WithMessagef(err, "failed querying next unspent token")
		}
		if tok == nil {
			break
		}
		unspentTokenIDs = append(unspentTokenIDs, &tok.Id)
	}
	ledgerTokenContent, err := net.QueryTokens(ctx, tms.Namespace(), unspentTokenIDs)
	if err != nil {
		errorMessages = append(errorMessages, fmt.Sprintf("failed to query tokens: [%s]", err))
	} else {
		if len(unspentTokenIDs) != len(ledgerTokenContent) {
			return nil, errors.Errorf("length diffrence")
		}
		index := 0
		if err := qe.GetTokenOutputs(ctx, unspentTokenIDs, func(id *token2.ID, tokenRaw []byte) error {
			for _, content := range ledgerTokenContent {
				if bytes.Equal(content, tokenRaw) {
					return nil
				}
			}

			errorMessages = append(errorMessages, fmt.Sprintf("token content does not match at [%s][%d], [%s]", id, index, utils.Hashable(tokenRaw)))
			index++

			return nil
		}); err != nil {
			return nil, errors.WithMessagef(err, "failed to match ledger token content with local")
		}
	}

	return errorMessages, nil
}

// CheckTokenSpendability checks that for each unspent token, it is still spendable.
// Spendability is verified against the current TMS for the given TMS ID.
// A token is still spendable if:
// - The token type is among the supported;
// - The token is parsable;
// - The token's recipients are still valid.
func (a *DefaultCheckers) CheckTokenSpendability(ctx context.Context) ([]string, error) {
	var errorMessages []string

	tms, err := a.tmsProvider.GetManagementService(token.WithTMSID(a.tmsID))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting tms [%s]", a.tmsID)
	}
	tv := tms.Vault()
	uit, err := tv.NewQueryEngine().UnspentLedgerTokensIteratorBy(ctx)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed querying utxo engine")
	}
	defer uit.Close()

	ts := tms.TokensService()
	sigService := tms.SigService()
	supportedTokenFormats := ts.SupportedTokenFormats()
	supportedTokenFormatsSet := collections.NewSet(supportedTokenFormats...)
	logger.DebugfContext(ctx, "checking token spendability for [%s], supported tokens [%s]", tms.ID(), supportedTokenFormatsSet.ToSlice())
	for {
		tok, err := uit.Next()
		if err != nil {
			return nil, errors.WithMessagef(err, "failed querying next unspent token")
		}
		if tok == nil {
			break
		}
		// is the token's format supported?
		if !supportedTokenFormatsSet.Contains(tok.Format) {
			errorMessages = append(errorMessages, fmt.Sprintf("token format not supported [%s][%s]", tok.ID, tok.Format))

			continue
		}

		logger.DebugfContext(ctx, "deobfuscating token [%s][%s]...", tok.ID, tok.Format)
		// extract the token's recipients and try to get a verifier for it
		_, _, recipients, _, err := ts.Deobfuscate(ctx, tok.Token, tok.TokenMetadata)
		if err != nil {
			errorMessages = append(errorMessages, fmt.Sprintf("failed to deobfuscate token [%s][%s], [%s]", tok.ID, tok.Format, err))

			continue
		}
		logger.DebugfContext(ctx, "deobfuscated token [%s][%s][%v]...", tok.ID, tok.Format, recipients)
		if len(recipients) == 0 {
			errorMessages = append(errorMessages, fmt.Sprintf("token recipient list is empty for [%s][%s]", tok.ID, tok.Format))

			continue
		}
		for _, recipient := range recipients {
			_, err = sigService.OwnerVerifier(ctx, recipient)
			if err != nil {
				errorMessages = append(errorMessages, fmt.Sprintf("failed to verify recipient [%s][%s][%s], [%s]", tok.ID, recipient, tok.Format, err))
			}
		}
	}

	logger.DebugfContext(ctx, "finished checks with [%d] error messages", len(errorMessages))

	return errorMessages, nil
}
