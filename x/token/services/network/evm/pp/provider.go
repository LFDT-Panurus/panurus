/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/abi"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
)

// getPublicParametersMethod is the canonical ABI signature of the TokenState read that returns the
// stored parameters.
const getPublicParametersMethod = "getPublicParameters()" // #nosec G101 -- ABI method signature

// ChainProvider supplies the public parameters an endorser binds a StateDelta to, reading both the
// bytes and the version from the contract.
//
// Reading both from the same place is the point. The contract rejects a delta unless its
// publicParamsHash and publicParamsVersion match what it holds at apply time, so an endorser that
// took the bytes from its local configuration and the version from the chain could sign a delta that
// is guaranteed to be rejected. Taking both from the chain makes the pair consistent by construction.
type ChainProvider struct {
	client     client.EVMClient
	tokenState client.Address
	blockTag   string
	versions   *VersionKeeper
}

// NewChainProvider returns a provider reading the given TokenState clone. An empty blockTag defaults
// to the finalized tag.
func NewChainProvider(evmClient client.EVMClient, tokenState client.Address, blockTag string) *ChainProvider {
	if blockTag == "" {
		blockTag = client.BlockTagFinalized
	}

	return &ChainProvider{
		client:     evmClient,
		tokenState: tokenState,
		blockTag:   blockTag,
		versions:   NewVersionKeeper(evmClient, tokenState, blockTag),
	}
}

// PublicParams returns the parameters currently stored on chain and their version.
func (p *ChainProvider) PublicParams(ctx context.Context) ([]byte, uint64, error) {
	raw, err := p.client.Call(ctx, p.tokenState, abi.MethodID(getPublicParametersMethod), p.blockTag)
	if err != nil {
		return nil, 0, errors.Wrap(err, "failed to read the public parameters")
	}
	params, err := abi.DecodeBytes(raw)
	if err != nil {
		return nil, 0, errors.Wrap(err, "failed to decode the public parameters")
	}

	version, err := p.versions.GetVersion(ctx)
	if err != nil {
		return nil, 0, err
	}

	return params, version, nil
}

// Invalidate drops the cached version, so the next read picks up an applied update.
func (p *ChainProvider) Invalidate() { p.versions.Invalidate() }
