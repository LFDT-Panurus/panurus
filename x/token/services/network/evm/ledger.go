/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"sync"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/abi"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
)

// TokenState read methods the driver calls. The signatures are canonical ABI forms; their selectors
// are what the contract dispatches on.
const (
	getTokenMethod            = "getToken(bytes32)"         // #nosec G101 -- ABI method signature
	areTokensSpentMethod      = "areTokensSpent(bytes32[])" // #nosec G101 -- ABI method signature
	getPublicParametersMethod = "getPublicParameters()"     // #nosec G101 -- ABI method signature
	getTransferMetadataMethod = "getTransferMetadata(bytes32)"
	getTokenRequestHashMethod = "getTokenRequestHash(bytes32)"
	graphHidingMethod         = "graphHiding()"
)

// contractReader performs the driver's read-only calls against a TMS's TokenState clone. It is the
// shared plumbing behind the query methods on Network: resolve a token id, call, decode.
type contractReader struct {
	client     client.EVMClient
	tokenState client.Address
	blockTag   string

	// graphHiding caches the clone's mode once read. It is fixed at initialize, so one successful
	// read holds for the reader's lifetime; a failed read is not cached.
	modeMu      sync.Mutex
	graphHiding *bool
}

func newContractReader(evmClient client.EVMClient, tokenState client.Address, blockTag string) *contractReader {
	if blockTag == "" {
		blockTag = client.BlockTagFinalized
	}

	return &contractReader{client: evmClient, tokenState: tokenState, blockTag: blockTag}
}

// tokenData returns the stored bytes of a token, or empty if it does not exist.
func (r *contractReader) tokenData(ctx context.Context, id *token.ID) ([]byte, error) {
	if id == nil {
		return nil, errors.New("nil token id")
	}
	tokenID, err := tokenIDOf(id)
	if err != nil {
		return nil, err
	}
	raw, err := r.client.Call(ctx, r.tokenState, abi.EncodeBytes32Call(getTokenMethod, tokenID), r.blockTag)
	if err != nil {
		return nil, errors.Wrapf(err, "getToken failed for [%s:%d]", id.TxId, id.Index)
	}

	return abi.DecodeBytes(raw)
}

// liveTokenData returns the stored bytes of each token, nil for one that does not exist or has
// already been spent.
//
// The contract keeps a token's bytes after it is spent and only marks the spend, unlike the Fabric
// translator, which deletes the output key. Callers of QueryTokens and GetStates rely on the Fabric
// behaviour: the vault's unspent-token check, for one, can only notice a token spent on chain if the
// read for it comes back empty. So on a graph-revealing clone the spent flags are read for every
// token that has bytes, in one batch, and spent ones are dropped. That batch is part of the answer:
// if it fails, the read fails, rather than return bytes for a token that may be gone.
//
// A graph-hiding clone cannot say which ids are spent (areTokensSpent reverts there), and the Fabric
// translator does not delete spent outputs in that mode either, so the batch is skipped and the
// bytes are returned as stored, with no call beyond the token reads.
//
// With requireAll set, a token that is missing or spent is an error instead of a nil entry, and a
// missing one fails the call at once rather than after every other id has been read.
func (r *contractReader) liveTokenData(ctx context.Context, ids []*token.ID, requireAll bool) ([][]byte, error) {
	out := make([][]byte, len(ids))
	present := make([]*token.ID, 0, len(ids))
	positions := make([]int, 0, len(ids))
	for i, id := range ids {
		data, err := r.tokenData(ctx, id)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			if requireAll {
				return nil, errTokenDoesNotExist(id)
			}

			continue
		}
		out[i] = data
		present = append(present, id)
		positions = append(positions, i)
	}
	if len(present) == 0 {
		return out, nil
	}

	hiding, err := r.isGraphHiding(ctx)
	if err != nil {
		return nil, err
	}
	if hiding {
		return out, nil
	}
	spent, err := r.spent(ctx, present)
	if err != nil {
		return nil, err
	}
	for j, isSpent := range spent {
		if !isSpent {
			continue
		}
		if requireAll {
			return nil, errTokenDoesNotExist(present[j])
		}
		out[positions[j]] = nil
	}

	return out, nil
}

// isGraphHiding reports the clone's graphHiding mode, reading it from the contract the first time.
func (r *contractReader) isGraphHiding(ctx context.Context) (bool, error) {
	r.modeMu.Lock()
	defer r.modeMu.Unlock()
	if r.graphHiding != nil {
		return *r.graphHiding, nil
	}
	raw, err := r.client.Call(ctx, r.tokenState, abi.MethodID(graphHidingMethod), r.blockTag)
	if err != nil {
		return false, errors.Wrap(err, "graphHiding failed")
	}
	v, err := abi.DecodeUint64(raw)
	if err != nil {
		return false, errors.Wrap(err, "graphHiding: decode")
	}
	hiding := v != 0
	r.graphHiding = &hiding

	return hiding, nil
}

// errTokenDoesNotExist reports a token that is not on chain, or no longer is because it was spent.
func errTokenDoesNotExist(id *token.ID) error {
	return errors.Errorf("token [%s:%d] does not exist", id.TxId, id.Index)
}

// spent reports, for each token id, whether it has been spent. It calls the contract's batch method
// so a wallet checking many tokens costs one round trip.
//
// The contract resolves spent status through the content-bound marker recorded when the output was
// created, so the caller does not need the token's bytes, only its id.
func (r *contractReader) spent(ctx context.Context, ids []*token.ID) ([]bool, error) {
	tokenIDs := make([][32]byte, len(ids))
	for i, id := range ids {
		if id == nil {
			return nil, errors.Errorf("nil token id at index %d", i)
		}
		tokenID, err := tokenIDOf(id)
		if err != nil {
			return nil, err
		}
		tokenIDs[i] = tokenID
	}

	raw, err := r.client.Call(
		ctx,
		r.tokenState,
		abi.EncodeCall(areTokensSpentMethod, abi.Bytes32Array(tokenIDs)),
		r.blockTag,
	)
	if err != nil {
		return nil, errors.Wrap(err, "areTokensSpent failed")
	}
	out, err := abi.DecodeBoolArray(raw)
	if err != nil {
		return nil, err
	}
	if len(out) != len(ids) {
		return nil, errors.Errorf("areTokensSpent returned %d results for %d ids", len(out), len(ids))
	}

	return out, nil
}

// publicParameters returns the public parameters currently stored on chain.
func (r *contractReader) publicParameters(ctx context.Context) ([]byte, error) {
	raw, err := r.client.Call(ctx, r.tokenState, abi.MethodID(getPublicParametersMethod), r.blockTag)
	if err != nil {
		return nil, errors.Wrap(err, "getPublicParameters failed")
	}

	return abi.DecodeBytes(raw)
}

// transferMetadata returns the value stored for a transfer metadata key, or empty if unset.
func (r *contractReader) transferMetadata(ctx context.Context, key [32]byte) ([]byte, error) {
	raw, err := r.client.Call(ctx, r.tokenState, abi.EncodeBytes32Call(getTransferMetadataMethod, key), r.blockTag)
	if err != nil {
		return nil, errors.Wrap(err, "getTransferMetadata failed")
	}

	return abi.DecodeBytes(raw)
}

// TokenRequestHash returns the token-request hash recorded for an anchor, and whether the anchor has
// been applied at all. The contract records it as the last step of applyStateDelta and a failed apply
// reverts the whole transaction, so a recorded hash proves the transition was committed. It satisfies
// the finality manager's StateReader.
func (r *contractReader) TokenRequestHash(ctx context.Context, anchor [32]byte) ([]byte, bool, error) {
	raw, err := r.client.Call(ctx, r.tokenState, abi.EncodeBytes32Call(getTokenRequestHashMethod, anchor), r.blockTag)
	if err != nil {
		return nil, false, errors.Wrap(err, "getTokenRequestHash failed")
	}
	hash, err := abi.DecodeBytes32(raw)
	if err != nil {
		return nil, false, err
	}
	if hash == ([32]byte{}) {
		return nil, false, nil
	}

	return hash[:], true, nil
}

// tokenIDOf resolves an SDK token id to its addressable on-chain id, the same derivation the
// translator and the contract use.
func tokenIDOf(id *token.ID) ([32]byte, error) {
	anchor, err := keys.AnchorFromTxID(id.TxId)
	if err != nil {
		return [32]byte{}, errors.Wrapf(err, "invalid token anchor [%s]", id.TxId)
	}

	return keys.ComputeTokenID(anchor, id.Index), nil
}
