/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package qe

import (
	"context"
	errors2 "errors"

	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	driver3 "github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabricx/core/committer/queryservice"
)

type (
	Namespace = driver.Namespace
	TokenData = []byte
	Data      = []byte
)

// QueryStatesExecutor models an executor for querying states.
type QueryStatesExecutor interface {
	// QueryState retrieves the raw value for the provided key from the specified namespace.
	QueryState(ctx context.Context, namespace Namespace, key string) (Data, error)
	// QueryStates returns the values of the given keys in the given namespace.
	QueryStates(_ context.Context, namespace Namespace, keys []string) ([]Data, error)
}

type in struct {
	network, channel string
}

// ExecutorProvider looks up tokens by parsing the whole ledger instead of using the chaincode.
// ExecutorProvider models a provider for executors.
type ExecutorProvider struct {
	p lazy.Provider[in, *Executor]
}

// NewExecutorProvider returns a new ExecutorProvider instance that
// lazily creates and caches Executor instances for each channel.
func NewExecutorProvider(qsProvider queryservice.Provider) *ExecutorProvider {
	p := lazy.NewProviderWithKeyMapper[in, string, *Executor](
		func(i in) string { return i.channel },
		func(i in) (*Executor, error) {
			l := NewExecutor(i.network, i.channel, qsProvider)

			return l, nil
		},
	)

	return &ExecutorProvider{p: p}
}

// GetSpentExecutor returns the Executor for the given network and channel
// as a driver3.SpentTokenQueryExecutor.
func (p *ExecutorProvider) GetSpentExecutor(network, channel string) (driver3.SpentTokenQueryExecutor, error) {
	return p.p.Get(in{network: network, channel: channel})
}

// GetExecutor returns the Executor for the given network and channel
// as a driver3.TokenQueryExecutor.
func (p *ExecutorProvider) GetExecutor(network, channel string) (driver3.TokenQueryExecutor, error) {
	return p.p.Get(in{network: network, channel: channel})
}

// GetStateExecutor returns the Executor for the given network and channel
// as a QueryStatesExecutor.
func (p *ExecutorProvider) GetStateExecutor(network, channel string) (QueryStatesExecutor, error) {
	return p.p.Get(in{network: network, channel: channel})
}

// Executor models a state and token query service implementation for FabricX.
type Executor struct {
	qsProvider    queryservice.Provider
	keyTranslator translator.KeyTranslator
	network       string
	channel       string
}

// NewExecutor returns a new Executor instance for the specified network and channel.
func NewExecutor(network string, channel string, qsProvider queryservice.Provider) *Executor {
	return &Executor{
		network:       network,
		channel:       channel,
		qsProvider:    qsProvider,
		keyTranslator: &keys.Translator{},
	}
}

// QueryTokens retrieves raw token data from the ledger for the specified token IDs.
// It generates output keys for each ID and performs a batch state query.
func (e *Executor) QueryTokens(_ context.Context, namespace driver.Namespace, ids []*token.ID) ([]TokenData, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	keys := make([]driver.PKey, 0, len(ids))
	for _, id := range ids {
		if id == nil {
			continue
		}
		outputID, err := e.keyTranslator.CreateOutputKey(id.TxId, id.Index)
		if err != nil {
			return nil, errors.Wrapf(err, "error creating output id key [%s:%d]", id.TxId, id.Index)
		}
		keys = append(keys, outputID)
	}
	if len(keys) == 0 {
		return nil, nil
	}

	qs, err := e.qsProvider.Get(e.network, e.channel)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting qs [%s:%s]", e.network, e.channel)
	}
	res, err := qs.GetStates(map[driver.Namespace][]driver.PKey{
		namespace: keys,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting states [%s:%s] for keys [%v]", e.network, e.channel, keys)
	}

	// map[driver.Namespace]map[driver.PKey]driver.VaultValue
	tokens := make([]TokenData, 0, len(res[namespace]))
	ns := res[namespace]
	var errs []error
	for _, key := range keys {
		value := ns[key]
		if len(value.Raw) == 0 {
			errs = append(errs, errors.Errorf("output for key [%s] does not exist", key))

			continue
		}
		tokens = append(tokens, value.Raw)
	}
	if len(errs) != 0 {
		return nil, errors2.Join(errs...)
	}

	return tokens, nil
}

// QuerySpentTokens checks if the specified token IDs have been spent by
// verifying their existence in the ledger. For non-graph hiding drivers,
// a token is considered spent if its key is missing (empty raw value).
func (e *Executor) QuerySpentTokens(_ context.Context, namespace driver.Namespace, ids []*token.ID, meta []string) ([]bool, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	// This operation depends on the driver.
	// Let's assume for now that the driver is non-graph hiding
	keys := make([]driver.PKey, 0, len(ids))
	for _, id := range ids {
		if id == nil {
			continue
		}
		outputID, err := e.keyTranslator.CreateOutputKey(id.TxId, id.Index)
		if err != nil {
			return nil, errors.Wrapf(err, "error creating output id key [%s:%d]", id.TxId, id.Index)
		}
		keys = append(keys, outputID)
	}
	if len(keys) == 0 {
		return nil, nil
	}

	qs, err := e.qsProvider.Get(e.network, e.channel)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting qs [%s:%s]", e.network, e.channel)
	}
	res, err := qs.GetStates(map[driver.Namespace][]driver.PKey{
		namespace: keys,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting states [%s:%s]", e.network, e.channel)
	}

	// map[driver.Namespace]map[driver.PKey]driver.VaultValue
	spentFlags := make([]bool, len(res[namespace]))
	ns := res[namespace]
	for i, key := range keys {
		value := ns[key]
		spentFlags[i] = len(value.Raw) == 0
	}

	return spentFlags, nil
}

// QueryStates retrieves the raw values for the provided keys from the specified
// namespace. It triggers a batch query to the query service.
func (e *Executor) QueryStates(_ context.Context, namespace driver.Namespace, keys []string) ([]Data, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	qs, err := e.qsProvider.Get(e.network, e.channel)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting qs [%s:%s]", e.network, e.channel)
	}
	res, err := qs.GetStates(map[driver.Namespace][]driver.PKey{
		namespace: keys,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting states [%s:%s] for keys [%v]", e.network, e.channel, keys)
	}

	// map[driver.Namespace]map[driver.PKey]driver.VaultValue
	tokens := make([]Data, 0, len(res[namespace]))
	ns := res[namespace]
	var errs []error
	for _, key := range keys {
		value := ns[key]
		if len(value.Raw) == 0 {
			errs = append(errs, errors.Errorf("output for key [%s] does not exist", key))

			continue
		}
		tokens = append(tokens, value.Raw)
	}
	if len(errs) != 0 {
		return nil, errors2.Join(errs...)
	}

	return tokens, nil
}

// QueryState retrieves the raw value for the provided key from the specified namespace.
// It triggers a batch query to the query service.
func (e *Executor) QueryState(ctx context.Context, namespace driver.Namespace, key string) (Data, error) {
	data, err := e.QueryStates(ctx, namespace, []string{key})
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}

	return data[0], nil
}
