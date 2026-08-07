/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nwo

import (
	"context"
	"crypto/sha256"
	"math/big"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/pp"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// setupReceiptTimeout bounds the wait for the update's receipt. It only has to cover a block on a
// development network, but the whole point of waiting is to surface a revert here rather than as a
// later, much more confusing, timeout on the nodes reading the new parameters back.
const setupReceiptTimeout = 30 * time.Second

// SetupUpdater changes a TMS's on-chain public parameters.
//
// The TokenState contract has no administrative setter: the only way parameters change is an endorsed
// setup delta (design §3.5), which is what this builds and submits. That is not a detour around the
// test network's needs, it is the same authority the update would need in production. A harness
// standing up its own network holds every endorser key, so it can act as the operator that would
// otherwise collect those signatures.
type SetupUpdater struct {
	client     client.EVMClient
	tokenState client.Address
	blockTag   string
	domain     eip712.Domain
	endorsers  []*eip712.Signer
	threshold  int
	submitter  *evm.Submitter
}

// SetupUpdaterConfig is what an updater needs to act for one TMS: the contract to update, the chain it
// lives on, the endorser keys to sign with, the quorum to reach, and the account that pays.
type SetupUpdaterConfig struct {
	// Client talks to the node.
	Client client.EVMClient
	// TokenState is the per-TMS contract clone whose parameters change.
	TokenState client.Address
	// ChainID and TokenState together fix the EIP-712 domain, so a delta signed for one TMS is not
	// valid for another.
	ChainID *big.Int
	// BlockTag is the tag the current parameters are read at. Defaults to latest.
	BlockTag string
	// EndorserKeys are the endorsers' private keys, as 32-byte scalars.
	EndorserKeys [][]byte
	// Threshold is the number of distinct signatures the verifier requires. Zero means all of them.
	Threshold uint
	// Submitter broadcasts the update and pays for it.
	Submitter *evm.Submitter
}

// NewSetupUpdater returns an updater for one TMS.
func NewSetupUpdater(config SetupUpdaterConfig) (*SetupUpdater, error) {
	if config.Client == nil {
		return nil, errors.New("evm nwo: setup updater needs a client")
	}
	if config.Submitter == nil {
		return nil, errors.New("evm nwo: setup updater needs a submitter to broadcast with")
	}
	if config.ChainID == nil {
		return nil, errors.New("evm nwo: setup updater needs the chain id")
	}
	if len(config.EndorserKeys) == 0 {
		return nil, errors.New("evm nwo: setup updater needs at least one endorser key")
	}

	endorsers := make([]*eip712.Signer, 0, len(config.EndorserKeys))
	for i, raw := range config.EndorserKeys {
		signer, err := eip712.NewSignerFromBytes(raw)
		if err != nil {
			return nil, errors.Wrapf(err, "evm nwo: endorser key %d is unusable", i)
		}
		endorsers = append(endorsers, signer)
	}

	threshold := int(config.Threshold)
	if threshold <= 0 || threshold > len(endorsers) {
		threshold = len(endorsers)
	}
	blockTag := config.BlockTag
	if blockTag == "" {
		blockTag = "latest"
	}

	return &SetupUpdater{
		client:     config.Client,
		tokenState: config.TokenState,
		blockTag:   blockTag,
		domain:     eip712.Domain{ChainID: config.ChainID, VerifyingContract: config.TokenState},
		endorsers:  endorsers,
		threshold:  threshold,
		submitter:  config.Submitter,
	}, nil
}

// Update replaces the on-chain public parameters with ppRaw and waits for the update to be mined.
//
// It returns once the parameters have actually changed, so a caller that then polls the nodes is
// waiting only for them to catch up, not for the chain.
func (u *SetupUpdater) Update(ctx context.Context, ppRaw []byte) error {
	if len(ppRaw) == 0 {
		return errors.New("evm nwo: cannot update to empty public parameters")
	}

	delta, err := u.buildDelta(ctx, ppRaw)
	if err != nil {
		return err
	}

	endorsements, err := u.endorse(delta)
	if err != nil {
		return err
	}

	_, txHash, err := u.submitter.Submit(ctx, delta, endorsements)
	if err != nil {
		return errors.Wrap(err, "evm nwo: failed to submit the public-parameters update")
	}

	return u.awaitReceipt(ctx, txHash)
}

// buildDelta assembles the setup delta. The parameters it supersedes are read from the chain rather
// than assumed: the contract rejects a delta that names a version other than the current one, which is
// what keeps two concurrent updates from silently reordering.
func (u *SetupUpdater) buildDelta(ctx context.Context, ppRaw []byte) (*statedelta.StateDelta, error) {
	current, version, err := pp.NewChainProvider(u.client, u.tokenState, u.blockTag).PublicParams(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "evm nwo: failed to read the current public parameters")
	}

	delta := &statedelta.StateDelta{
		Anchor: setupAnchor(ppRaw, version),
		// There is no token request behind an administrative update, so the field records what the
		// update carried instead of a request that does not exist.
		TokenRequestHash:    sha256.Sum256(ppRaw),
		PublicParamsHash:    sha256.Sum256(current),
		PublicParamsVersion: version,
		IsSetup:             true,
		SetupParameters:     ppRaw,
	}
	if err := delta.Validate(); err != nil {
		return nil, errors.Wrap(err, "evm nwo: built an invalid setup delta")
	}

	return delta, nil
}

// endorse signs the delta with as many endorser keys as the quorum requires.
func (u *SetupUpdater) endorse(delta *statedelta.StateDelta) ([][]byte, error) {
	digest := eip712.Digest(u.domain, delta)

	endorsements := make([][]byte, 0, u.threshold)
	for _, signer := range u.endorsers[:u.threshold] {
		signature, err := signer.Sign(digest)
		if err != nil {
			return nil, errors.Wrapf(err, "evm nwo: endorser [%s] failed to sign the update", signer.Address())
		}
		endorsements = append(endorsements, signature)
	}

	return endorsements, nil
}

// awaitReceipt waits for the update to be mined and fails on a revert. A reverted setup leaves the
// parameters untouched with no other trace, so without this the failure would only show up much later,
// as nodes that never see the new parameters.
func (u *SetupUpdater) awaitReceipt(ctx context.Context, txHash client.Hash) error {
	ctx, cancel := context.WithTimeout(ctx, setupReceiptTimeout)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		receipt, err := u.client.GetTransactionReceipt(ctx, txHash)
		if err != nil {
			return errors.Wrapf(err, "evm nwo: failed to read the receipt of the update [%s]", txHash)
		}
		if receipt != nil {
			if receipt.Status != 1 {
				return errors.Errorf("evm nwo: the public-parameters update [%s] reverted", txHash)
			}

			return nil
		}

		select {
		case <-ctx.Done():
			return errors.Errorf("evm nwo: the public-parameters update [%s] was not mined in time", txHash)
		case <-ticker.C:
		}
	}
}

// setupAnchor derives the anchor for an update. The contract refuses an anchor it has already
// processed, so deriving it from what the update carries and the version it supersedes gives each
// update a distinct one without needing any state on this side.
func setupAnchor(ppRaw []byte, supersedes uint64) [32]byte {
	h := sha256.New()
	h.Write([]byte("evm-nwo-setup"))
	var version [8]byte
	for i := range version {
		version[i] = byte(supersedes >> (8 * (7 - i)))
	}
	h.Write(version[:])
	h.Write(ppRaw)

	var out [32]byte
	copy(out[:], h.Sum(nil))

	return out
}
