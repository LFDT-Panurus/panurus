/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"

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
)

// contractReader performs the driver's read-only calls against a TMS's TokenState clone. It is the
// shared plumbing behind the query methods on Network: resolve a token id, call, decode.
type contractReader struct {
	client     client.EVMClient
	tokenState client.Address
	blockTag   string
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

// spent reports, for each token id, whether it has been spent. It calls the contract's batch method
// so a wallet checking many tokens costs one round trip.
//
// The contract resolves spent status through the content-bound marker recorded when the output was
// created (design §5.3), so the caller does not need the token's bytes, only its id.
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

// tokenIDOf resolves an SDK token id to its addressable on-chain id, the same derivation the
// translator and the contract use.
func tokenIDOf(id *token.ID) ([32]byte, error) {
	anchor, err := keys.AnchorFromTxID(id.TxId)
	if err != nil {
		return [32]byte{}, errors.Wrapf(err, "invalid token anchor [%s]", id.TxId)
	}

	return keys.ComputeTokenID(anchor, id.Index), nil
}
