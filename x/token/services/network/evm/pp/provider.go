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

// maxPublicParamsReadAttempts bounds the retry PublicParams performs when it detects a torn read. A
// public-parameters update is a rare, isolated event (design §3.5), so a bracket almost always comes
// back clean on the first attempt; the bound exists only so a chain updating on every single block
// cannot spin this forever.
const maxPublicParamsReadAttempts = 3

// ChainProvider supplies the public parameters an endorser binds a StateDelta to, reading both the
// bytes and the version from the contract.
//
// Reading both from the same place is the point. The contract rejects a delta unless its
// publicParamsHash and publicParamsVersion match what it holds at apply time, so an endorser that
// took the bytes from its local configuration and the version from the chain could sign a delta that
// is guaranteed to be rejected. Taking both from the chain makes the pair consistent by construction.
//
// Which is also why neither half is cached here. A cached version paired with freshly read bytes is
// the same mismatch by a quieter route: it costs nothing until parameters are updated, and then every
// delta built afterwards carries the new bytes under the old version and is rejected on chain, for as
// long as the cache lives. One extra eth_call per endorsement is a fair price for not having a
// correctness bug that only appears after an update.
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
//
// There is no single getter for both (getPublicParameters and getPublicParamsVersion are separate
// calls), so an endorsed setup delta landing between them could tear the pair: bytes from before the
// update paired with the version from after, or the reverse. The contract would catch this at apply
// time, since it checks both fields together and reverts StalePublicParams unless they match exactly
// what it currently holds, but that turns a purely local race in this function into a doomed,
// gas-spending transaction rather than nothing happening at all.
//
// The version is read before and after the bytes, and the whole read is retried if it moved: version
// and bytes only ever change together, in the same transaction (design §3.5), so two matching reads
// bracketing the bytes read is proof nothing landed in between.
func (p *ChainProvider) PublicParams(ctx context.Context) ([]byte, uint64, error) {
	var lastVersion uint64
	for range maxPublicParamsReadAttempts {
		before, err := p.versions.Sync(ctx)
		if err != nil {
			return nil, 0, err
		}

		raw, err := p.client.Call(ctx, p.tokenState, abi.MethodID(getPublicParametersMethod), p.blockTag)
		if err != nil {
			return nil, 0, errors.Wrap(err, "failed to read the public parameters")
		}
		params, err := abi.DecodeBytes(raw)
		if err != nil {
			return nil, 0, errors.Wrap(err, "failed to decode the public parameters")
		}

		after, err := p.versions.Sync(ctx)
		if err != nil {
			return nil, 0, err
		}
		if after == before {
			return params, after, nil
		}
		lastVersion = after
	}

	return nil, 0, errors.Errorf(
		"evm pp: public parameters changed on every read attempt (%d), last observed version %d",
		maxPublicParamsReadAttempts, lastVersion,
	)
}

// Invalidate drops the cached version. PublicParams re-reads the version on every call, so there is
// nothing stale for this to clear; it is kept because a caller that has just applied an update should
// not have to know that.
func (p *ChainProvider) Invalidate() { p.versions.Invalidate() }
