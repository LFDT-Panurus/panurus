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

// setupAction is a minimal translator.SetupAction implementation carrying the raw public parameters to
// be committed to the delta. The SDK ships no concrete type for it (Fabric's own setupBehaviour defines
// the same minimal struct locally, in token/services/network/fabric/endorsement/fsc/responder.go).
type setupAction struct {
	publicParamsRaw []byte
}

// GetSetupParameters returns the raw public parameters carried by this action.
func (a *setupAction) GetSetupParameters() ([]byte, error) {
	return a.publicParamsRaw, nil
}

// SetupDeltaFactory turns a setup request's new public parameters into the StateDelta an endorser
// signs. It belongs to the responder alone, like DeltaFactory, but needs none of its collaborators: a
// setup delta reads no token state and is validated against no existing token request, so there is
// nothing for a RequestValidator or a Ledger to do. What it does need is a PublicParamsValidator, to
// check the new parameters are well-formed before signing anything over them, and the chain's current
// parameters, which the delta binds to as its optimistic-concurrency baseline exactly as an ordinary
// delta binds to the parameters it was validated against (TokenState.applyStateDelta compares
// PublicParamsHash/PublicParamsVersion against its current values for both delta kinds, then applies
// SetupParameters only for a setup delta - see _applySetup in TokenState.sol). Needing no existing TMS
// is what makes first-time setup of a namespace possible: the responder is registered before any TMS
// exists, and SetupPublicParams may run before one exists for this namespace at all.
type SetupDeltaFactory struct {
	ppValidator PublicParamsValidator
	current     PublicParamsProvider
}

// NewSetupDeltaFactory assembles a SetupDeltaFactory. current supplies the chain's currently active
// public parameters (whatever a namespace's TokenState was deployed or last updated with), the baseline
// the new setup delta's optimistic-concurrency check binds to.
func NewSetupDeltaFactory(ppValidator PublicParamsValidator, current PublicParamsProvider) *SetupDeltaFactory {
	return &SetupDeltaFactory{ppValidator: ppValidator, current: current}
}

// Build validates req's new public parameters and returns the StateDelta to sign, binding it to the
// chain's currently active parameters as the CAS baseline TokenState.applyStateDelta checks before
// applying the update.
func (f *SetupDeltaFactory) Build(ctx context.Context, req *EndorseRequest) (*statedelta.StateDelta, error) {
	newPP, err := f.ppValidator.PublicParametersFromBytes(req.PublicParamsRaw)
	if err != nil {
		return nil, errors.Join(ErrValidation, errors.Wrap(err, "failed to unmarshal public parameters"))
	}
	if err := newPP.Validate(); err != nil {
		return nil, errors.Join(ErrValidation, errors.Wrap(err, "failed to validate public parameters"))
	}

	currentRaw, currentVersion, err := f.current.PublicParams(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load the chain's current public parameters")
	}

	anchor, err := keys.AnchorFromTxID(req.Anchor)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid anchor [%s]", req.Anchor)
	}

	tr := statedelta.NewTranslator(anchor, currentRaw, currentVersion)
	if err := tr.Write(ctx, &setupAction{publicParamsRaw: req.PublicParamsRaw}); err != nil {
		return nil, errors.Wrap(err, "failed to translate setup action")
	}
	if err := tr.AddPublicParamsDependency(); err != nil {
		return nil, errors.Wrap(err, "failed to add public parameters dependency")
	}
	// A setup delta has no token request to bind a hash to; the new parameters are the content this
	// endorser is actually being asked to endorse, so they are what gets hashed and stored under the
	// anchor instead.
	if _, err := tr.CommitTokenRequest(req.PublicParamsRaw, true); err != nil {
		return nil, errors.Wrap(err, "failed to commit the setup request")
	}

	return tr.StateDelta()
}
