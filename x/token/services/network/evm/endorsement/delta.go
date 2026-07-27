/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// DeltaFactory turns a validated token request into the StateDelta both sides of the flow work with:
// the responder signs its EIP-712 digest, the initiator assembles the signatures over it and encodes
// it into the transaction. Sharing one construction path is how the §4.4 determinism guarantee (every
// endorser and the initiator produce byte-identical deltas) is met, by running the same code rather
// than trusting independent reimplementations to agree.
//
// Build validates the request against on-chain state (read through the getToken ledger at blockTag),
// then translates the validated actions with the StateDelta translator, binding the public
// parameters and the anchor-bound token-request hash.
type DeltaFactory struct {
	validator  RequestValidator
	pp         PublicParamsProvider
	client     client.EVMClient
	tokenState client.Address
	blockTag   string
}

// NewDeltaFactory assembles a DeltaFactory. An empty blockTag defaults to DefaultBlockTag.
func NewDeltaFactory(
	validator RequestValidator,
	pp PublicParamsProvider,
	evmClient client.EVMClient,
	tokenState client.Address,
	blockTag string,
) *DeltaFactory {
	if blockTag == "" {
		blockTag = DefaultBlockTag
	}

	return &DeltaFactory{
		validator:  validator,
		pp:         pp,
		client:     evmClient,
		tokenState: tokenState,
		blockTag:   blockTag,
	}
}

// Build validates req against on-chain state and returns the StateDelta to sign or assemble. A
// validation failure is wrapped with ErrValidation so callers can classify it.
func (f *DeltaFactory) Build(ctx context.Context, req *EndorseRequest) (*statedelta.StateDelta, error) {
	ppRaw, ppVersion, err := f.pp.PublicParams(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load public parameters")
	}

	ledger := NewLedger(ctx, f.client, f.tokenState, f.blockTag)
	actions, meta, err := f.validator.UnmarshallAndVerifyWithMetadata(
		ctx,
		ledger,
		token2.RequestAnchor(req.Anchor),
		req.TokenRequest,
	)
	if err != nil {
		return nil, errors.Join(ErrValidation, err)
	}

	anchor, err := keys.AnchorFromTxID(req.Anchor)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid anchor [%s]", req.Anchor)
	}

	tr := statedelta.NewTranslator(anchor, ppRaw, ppVersion)
	for i, action := range actions {
		if err := tr.Write(ctx, action); err != nil {
			return nil, errors.Wrapf(err, "failed to translate action %d", i)
		}
	}
	if err := tr.AddPublicParamsDependency(); err != nil {
		return nil, errors.Wrap(err, "failed to add public parameters dependency")
	}
	// The token-request hash is committed from the validator's TokenRequestToSign attribute (the
	// anchor-bound message-to-sign), matching the hash the rest of the SDK stores, exactly as the
	// Fabric responder does.
	if _, err := tr.CommitTokenRequest(meta[common.TokenRequestToSign], true); err != nil {
		return nil, errors.Wrap(err, "failed to commit token request")
	}

	return tr.StateDelta()
}

// compile-time check that DeltaFactory satisfies the DeltaBuilder the initiator depends on.
var _ DeltaBuilder = (*DeltaFactory)(nil)
