/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type TokenTransactionDB interface {
	GetTokenRequest(ctx context.Context, txID string) ([]byte, error)
	GetTransactionEndorsementAcks(ctx context.Context, id string) (map[string][]byte, error)
}

// TransactionInfo contains the transaction info.
type TransactionInfo struct {
	// EndorsementAcks contains the endorsement ACKs received at time of dissemination.
	EndorsementAcks map[string][]byte
	// ApplicationMetadata contains the application metadata
	ApplicationMetadata map[string][]byte

	TokenRequest []byte
}

// TransactionInfoProvider allows the retrieval of the transaction info
type TransactionInfoProvider struct {
	tms   dep.TokenManagementService
	ttxDB TokenTransactionDB
}

func newTransactionInfoProvider(tms dep.TokenManagementService, ttxDB TokenTransactionDB) *TransactionInfoProvider {
	return &TransactionInfoProvider{tms: tms, ttxDB: ttxDB}
}

// TransactionInfo returns the transaction info for the given transaction ID.
func (a *TransactionInfoProvider) TransactionInfo(ctx context.Context, txID string) (*TransactionInfo, error) {
	endorsementAcks, err := a.ttxDB.GetTransactionEndorsementAcks(ctx, txID)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to load endorsement acks for [%s]", txID)
	}

	tr, err := a.ttxDB.GetTokenRequest(ctx, txID)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to load token request for [%s]", txID)
	}

	applicationMetadata, err := a.loadTransient(tr, txID)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to load transient for [%s]", txID)
	}

	return &TransactionInfo{
		EndorsementAcks:     endorsementAcks,
		ApplicationMetadata: applicationMetadata,
		TokenRequest:        tr,
	}, nil
}

func (a *TransactionInfoProvider) loadTransient(trRaw []byte, txID string) (map[string][]byte, error) {
	if len(trRaw) == 0 {
		logger.Debugf("transaction [%s], no token request found, skip it", txID)

		return nil, nil
	}
	request, err := a.tms.NewFullRequestFromBytes(trRaw)
	if err != nil {
		logger.Debugf("transaction [%s], failed getting zkat state from transient map [%s]", txID, err)

		return nil, err
	}
	if request.Metadata == nil {
		logger.Debugf("transaction [%s], no metadata found, skip it", txID)

		return nil, nil
	}

	return request.Metadata.Application, nil
}
