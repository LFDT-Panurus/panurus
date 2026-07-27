/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/abi"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
)

// getTokenMethod is the canonical ABI signature of the TokenState read the ledger calls to resolve a
// token id to its stored bytes.
const getTokenMethod = "getToken(bytes32)" // #nosec G101 -- ABI method signature, not a credential

// Ledger is the read-only view of on-chain token state the endorser's validator reads through. The
// SDK validator is stateless: it validates the input tokens a request carries against the ledger's
// record of what exists, calling GetState for each consumed token id. On the EVM backend that record
// lives in the TokenState contract, so GetState resolves a token.ID to its addressable on-chain id
// and reads getToken at a fixed block tag (design §6.2).
//
// It reads at BlockTag (default "finalized", design §7.2) so an endorser validates against settled
// state and reorg handling stays out of v1. It captures a context at construction because the SDK's
// ledger contract (driver.GetStateFnc) carries none.
//
// Ledger satisfies token.Ledger, so it can be passed straight to
// Validator.UnmarshallAndVerifyWithMetadata.
type Ledger struct {
	ctx        context.Context
	client     client.EVMClient
	tokenState client.Address
	blockTag   string
}

// DefaultBlockTag is the block tag the ledger reads at when none is configured: the PoS finalized
// tag, which removes reorg handling from validation (design §7.2).
const DefaultBlockTag = "finalized"

// NewLedger returns a Ledger reading the TokenState contract through the EVM client. An empty
// blockTag defaults to DefaultBlockTag.
func NewLedger(ctx context.Context, evmClient client.EVMClient, tokenState client.Address, blockTag string) *Ledger {
	if blockTag == "" {
		blockTag = DefaultBlockTag
	}

	return &Ledger{ctx: ctx, client: evmClient, tokenState: tokenState, blockTag: blockTag}
}

// GetState returns the on-chain token bytes stored for id, or an empty slice if no token exists at
// that id (getToken returns empty bytes for an unknown id, which the validator reads as "absent",
// exactly as the Fabric ledger returns nil for a missing key). It resolves id to its addressable
// on-chain id (keccak256(abi.encode(anchor, index))) the same way the translator and the contract
// do, so a token created by one endorser is found by another.
func (l *Ledger) GetState(id token.ID) ([]byte, error) {
	anchor, err := keys.AnchorFromTxID(id.TxId)
	if err != nil {
		return nil, errors.Wrapf(err, "ledger: invalid token anchor [%s]", id.TxId)
	}
	tokenID := keys.ComputeTokenID(anchor, id.Index)

	raw, err := l.client.Call(l.ctx, l.tokenState, abi.EncodeBytes32Call(getTokenMethod, tokenID), l.blockTag)
	if err != nil {
		return nil, errors.Wrapf(err, "ledger: getToken call failed for [%s:%d]", id.TxId, id.Index)
	}
	data, err := abi.DecodeBytes(raw)
	if err != nil {
		return nil, errors.Wrapf(err, "ledger: failed to decode getToken result for [%s:%d]", id.TxId, id.Index)
	}

	return data, nil
}
