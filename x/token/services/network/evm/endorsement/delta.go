/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"bytes"
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// DeltaFactory turns a validated token request into the StateDelta an endorser signs and returns. It
// belongs to the responder alone: the initiator neither validates nor translates, it takes the delta
// from the endorsers' replies. The determinism guarantee (see "StateDelta determinism" in
// docs/services/network-ethereum-internals.md) that every endorser produces byte-identical deltas is
// met by them all running this one construction path, rather than by trusting independent
// reimplementations to agree.
//
// Build validates the request against on-chain state (read through the getToken ledger at blockTag),
// then translates the validated actions with the StateDelta translator, binding the public
// parameters and the anchor-bound token-request hash.
type DeltaFactory struct {
	validator  RequestValidator
	localPP    LocalPublicParams
	pp         PublicParamsProvider
	client     client.EVMClient
	tokenState client.Address
	blockTag   string
}

// NewDeltaFactory assembles a DeltaFactory. An empty blockTag defaults to DefaultBlockTag.
//
// localPP is the public parameters manager of the TMS that validator itself validates against - the
// same one validator was built from. Build cross-checks its hash against the chain's before signing,
// so validator and localPP must always be resolved from the same TMS snapshot; esp.go's TMSResolver
// does this by construction.
func NewDeltaFactory(
	validator RequestValidator,
	localPP LocalPublicParams,
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
		localPP:    localPP,
		pp:         pp,
		client:     evmClient,
		tokenState: tokenState,
		blockTag:   blockTag,
	}
}

// Build validates req against on-chain state and returns the StateDelta to sign. A validation
// failure is wrapped with ErrValidation so callers can classify it.
//
// Before doing either, it checks that the parameters it is about to validate against and stamp the
// delta with - read fresh from the chain - are the same ones the local validator actually validated
// with. They can disagree: an endorsed setup delta updates the chain immediately, but this node's
// pp.Watcher only catches up on its next poll (driver.go keeps serving the old TMS meanwhile, per its
// own documented tradeoff). Signing regardless would produce a delta whose PublicParamsHash asserts
// this endorser validated under parameters it never actually used - the exact false statement
// statedelta/types.go documents PublicParamsHash as ruling out. Refusing instead costs the requester a
// retry, which is what ErrStalePublicParams is for.
func (f *DeltaFactory) Build(ctx context.Context, req *EndorseRequest) (*statedelta.StateDelta, error) {
	ppRaw, ppVersion, err := f.pp.PublicParams(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load public parameters")
	}

	chainHash := crypto.SHA256(ppRaw)
	localHash := f.localPP.PublicParamsHash()
	if !bytes.Equal(chainHash, localHash) {
		return nil, errors.Wrapf(ErrStalePublicParams,
			"local public parameters hash to [%x], chain currently holds [%x] at version %d",
			localHash, chainHash, ppVersion)
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
