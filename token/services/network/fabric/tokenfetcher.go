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
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
)

func NewTokenExecutorProvider(fnsProvider *fabric.NetworkServiceProvider) *tokenFetcherProvider {
	return &tokenFetcherProvider{fnsProvider: fnsProvider}
}

type tokenFetcherProvider struct {
	fnsProvider *fabric.NetworkServiceProvider
}

func (p *tokenFetcherProvider) GetExecutor(network, channel string) (driver2.TokenQueryExecutor, error) {
	return &tokenFetcher{fnsProvider: p.fnsProvider, network: network, channel: channel}, nil
}

type tokenFetcher struct {
	fnsProvider *fabric.NetworkServiceProvider
	network     string
	channel     string
}

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
	stdChannelChaincode := channel.Chaincode(namespace)
	query := stdChannelChaincode.Query(QueryTokensFunctions, idsRaw)
	payloadBoxed, err := query.Call()
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to query the token chaincode for query tokens")
	}

	// Unbox
	var tokens [][]byte
	if err := json.Unmarshal(payloadBoxed, &tokens); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal response")
	}

	return tokens, nil
}

type spentTokenFetcherProvider struct {
	fnsProvider   *fabric.NetworkServiceProvider
	keyTranslator translator.KeyTranslator
}

func NewSpentTokenExecutorProvider(fnsProvider *fabric.NetworkServiceProvider, keyTranslator translator.KeyTranslator) *spentTokenFetcherProvider {
	return &spentTokenFetcherProvider{fnsProvider: fnsProvider, keyTranslator: keyTranslator}
}

func (p *spentTokenFetcherProvider) GetSpentExecutor(network, channel string) (driver2.SpentTokenQueryExecutor, error) {
	return &spentTokenFetcher{
		fnsProvider:   p.fnsProvider,
		network:       network,
		channel:       channel,
		keyTranslator: p.keyTranslator,
	}, nil
}

type spentTokenFetcher struct {
	fnsProvider   *fabric.NetworkServiceProvider
	network       string
	channel       string
	keyTranslator translator.KeyTranslator
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
	stdChannelChaincode := channel.Chaincode(namespace)
	query := stdChannelChaincode.Query(AreTokensSpent, idsRaw)
	payloadBoxed, err := query.Call()
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to query the token chaincode for tokens spent")
	}

	// Unbox
	var spent []bool
	if err := json.Unmarshal(payloadBoxed, &spent); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal esponse")
	}

	return spent, nil
}
