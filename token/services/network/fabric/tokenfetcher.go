/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"context"
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	driver2 "github.com/LFDT-Panurus/panurus/token/services/network/driver"
	config3 "github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
)

// NewTokenExecutorProvider returns a provider of token query executors.
func NewTokenExecutorProvider(fnsProvider *fabric.NetworkServiceProvider, selectionProvider config3.EndorserSelectionProvider) *tokenFetcherProvider {
	return &tokenFetcherProvider{fnsProvider: fnsProvider, selectionProvider: selectionProvider}
}

type tokenFetcherProvider struct {
	fnsProvider       *fabric.NetworkServiceProvider
	selectionProvider config3.EndorserSelectionProvider
}

func (p *tokenFetcherProvider) GetExecutor(network, channel string) (driver2.TokenQueryExecutor, error) {
	return &tokenFetcher{
		fnsProvider:       p.fnsProvider,
		network:           network,
		channel:           channel,
		selectionProvider: p.selectionProvider,
	}, nil
}

type tokenFetcher struct {
	fnsProvider       *fabric.NetworkServiceProvider
	network           string
	channel           string
	selectionProvider config3.EndorserSelectionProvider
}

// QueryTokens invokes the chaincode's queryTokens function and unboxes its response. It
// carries the chaincode's own contract through unchanged: an absent token is a nil entry
// at its position in the result, not an error, so an error here means the request itself
// could not be completed (network, malformed ids, and the like).
func (f *tokenFetcher) QueryTokens(ctx context.Context, namespace string, IDs []*token.ID) ([][]byte, error) {
	idsRaw, err := json.Marshal(IDs)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling ids")
	}

	fns, err := f.fnsProvider.FabricNetworkService(f.network)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting fabric network service for network [%s]", f.network)
	}

	channel, err := fns.Channel(f.channel)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting channel [%s:%s]", f.network, f.channel)
	}
	selection, err := config3.SelectionFor(f.selectionProvider, f.network, f.channel, namespace)
	if err != nil {
		return nil, err
	}

	payloadBoxed, err := config3.RestrictedQuery(
		channel.Chaincode(namespace).Query(QueryTokensFunctions, idsRaw), selection,
		(*fabric.ChaincodeQuery).Call,
		"query tokens of ["+f.network+":"+f.channel+":"+namespace+"]",
		"failed to query the token chaincode for query tokens")
	if err != nil {
		return nil, err
	}

	// Unbox
	var tokens [][]byte
	if err := json.Unmarshal(payloadBoxed, &tokens); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal response")
	}

	return tokens, nil
}

type spentTokenFetcherProvider struct {
	fnsProvider       *fabric.NetworkServiceProvider
	keyTranslator     translator.KeyTranslator
	selectionProvider config3.EndorserSelectionProvider
}

// NewSpentTokenExecutorProvider returns a provider of spent-token query executors.
func NewSpentTokenExecutorProvider(fnsProvider *fabric.NetworkServiceProvider, keyTranslator translator.KeyTranslator, selectionProvider config3.EndorserSelectionProvider) *spentTokenFetcherProvider {
	return &spentTokenFetcherProvider{fnsProvider: fnsProvider, keyTranslator: keyTranslator, selectionProvider: selectionProvider}
}

func (p *spentTokenFetcherProvider) GetSpentExecutor(network, channel string) (driver2.SpentTokenQueryExecutor, error) {
	return &spentTokenFetcher{
		fnsProvider:       p.fnsProvider,
		network:           network,
		channel:           channel,
		keyTranslator:     p.keyTranslator,
		selectionProvider: p.selectionProvider,
	}, nil
}

type spentTokenFetcher struct {
	fnsProvider       *fabric.NetworkServiceProvider
	network           string
	channel           string
	keyTranslator     translator.KeyTranslator
	selectionProvider config3.EndorserSelectionProvider
}

func (f *spentTokenFetcher) QuerySpentTokens(ctx context.Context, namespace string, IDs []*token.ID, meta []string) ([]bool, error) {
	sIDs := make([]string, len(IDs))
	var err error
	for i, id := range IDs {
		sIDs[i], err = f.keyTranslator.CreateOutputKey(id.TxId, id.Index)
		if err != nil {
			return nil, errors.Wrapf(err, "cannot compute spent id for [%v]", id)
		}
	}

	idsRaw, err := json.Marshal(sIDs)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling ids")
	}

	fns, err := f.fnsProvider.FabricNetworkService(f.network)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting fabric network service for network [%s]", f.network)
	}

	channel, err := fns.Channel(f.channel)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting channel [%s:%s]", f.network, f.channel)
	}
	selection, err := config3.SelectionFor(f.selectionProvider, f.network, f.channel, namespace)
	if err != nil {
		return nil, err
	}

	payloadBoxed, err := config3.RestrictedQuery(
		channel.Chaincode(namespace).Query(AreTokensSpent, idsRaw), selection,
		(*fabric.ChaincodeQuery).Call,
		"query spent tokens of ["+f.network+":"+f.channel+":"+namespace+"]",
		"failed to query the token chaincode for tokens spent")
	if err != nil {
		return nil, err
	}

	// Unbox
	var spent []bool
	if err := json.Unmarshal(payloadBoxed, &spent); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal esponse")
	}
	if len(spent) != len(IDs) {
		return nil, errors.Errorf("unexpected number of spent flags: got [%d], expected [%d]", len(spent), len(IDs))
	}

	return spent, nil
}
