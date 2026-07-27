/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"

	session2 "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// responseTimeout bounds how long the initiator waits for one endorser's reply.
const responseTimeout = 30 * time.Second

// DeltaBuilder produces the StateDelta a request translates to. The initiator builds the delta
// itself (determinism guarantees it equals what the endorsers signed), both to know the digest it
// must verify each signature against and to carry the delta in the assembled result. *DeltaFactory
// satisfies it.
type DeltaBuilder interface {
	// Build validates and translates req into its StateDelta.
	Build(ctx context.Context, req *EndorseRequest) (*statedelta.StateDelta, error)
}

// Result is what a completed endorsement yields: the delta to apply and the collected quorum of
// signatures over its EIP-712 digest. The driver's RequestApproval wraps it into the network
// envelope; Broadcast (Week 5) ABI-encodes applyStateDelta(delta, endorsements).
type Result struct {
	// Anchor is the token-request anchor the endorsement is for.
	Anchor string
	// Delta is the StateDelta every endorser signed and the transaction will apply.
	Delta *statedelta.StateDelta
	// Endorsements are the collected 65-byte {r,s,v} signatures, one per distinct endorser, in the
	// order they were collected. Each has been recovered to a registered endorser and de-duplicated,
	// so the set satisfies the contract's threshold and distinct-signer rules.
	Endorsements [][]byte
}

// Initiator collects a threshold of endorser signatures over a request's StateDelta and assembles
// them into a Result. It is the EVM analog of the Fabric RequestApprovalView (design §6.3): it opens
// a session to each registered endorser, sends the request, and gathers the replies.
//
// It does not trust an endorser's self-reported address: it recovers the signer from each signature
// against the digest it computed locally, and counts a signature only if it recovers to a registered
// endorser not already counted. This mirrors the contract's on-chain rules (recover, authorize,
// distinct) so the initiator never assembles a quorum the contract would reject.
type Initiator struct {
	registry  *Registry
	threshold int
	builder   DeltaBuilder
	domain    eip712.Domain
	request   *EndorseRequest
}

// NewInitiator returns an Initiator for one request. threshold is the number of distinct endorser
// signatures the quorum requires.
func NewInitiator(registry *Registry, threshold int, builder DeltaBuilder, domain eip712.Domain, request *EndorseRequest) *Initiator {
	return &Initiator{
		registry:  registry,
		threshold: threshold,
		builder:   builder,
		domain:    domain,
		request:   request,
	}
}

// Call implements the FSC initiator view: build the delta, then request an endorsement from each
// registered endorser over its own session and assemble the quorum. It returns the *Result.
func (i *Initiator) Call(context view.Context) (any, error) {
	return i.Collect(context.Context(), func(party view.Identity) (*EndorseResponse, error) {
		return i.requestFrom(context, party)
	})
}

// requestFrom performs one endorsement round-trip over a fresh session to party.
func (i *Initiator) requestFrom(context view.Context, party view.Identity) (*EndorseResponse, error) {
	ts, err := session2.NewTypedSessionToParty(context, party)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open session to endorser [%s]", party)
	}
	if err := ts.SendTyped(context.Context(), i.request, TypeEndorseRequest); err != nil {
		return nil, errors.Wrapf(err, "failed to send request to endorser [%s]", party)
	}
	var resp EndorseResponse
	if err := ts.ReceiveTypedWithTimeout(TypeEndorseResponse, &resp, responseTimeout); err != nil {
		return nil, errors.Wrapf(err, "failed to receive response from endorser [%s]", party)
	}

	return &resp, nil
}

// Collect drives the assembly independently of the session transport: for each registered endorser
// it calls endorse, verifies the reply, and accumulates distinct valid signatures until the
// threshold is met. Separating it from Call keeps the quorum logic testable without an FSC runtime.
//
// A single endorser's failure (declined, unreachable, malformed or unauthorized signature) is not
// fatal: the initiator moves on and still succeeds if enough others sign. It fails only when fewer
// than threshold distinct endorsers produced a verifiable signature.
func (i *Initiator) Collect(ctx context.Context, endorse func(view.Identity) (*EndorseResponse, error)) (*Result, error) {
	delta, err := i.builder.Build(ctx, i.request)
	if err != nil {
		return nil, errors.Wrap(err, "failed to build delta")
	}
	digest := eip712.Digest(i.domain, delta)

	signatures := make([][]byte, 0, i.threshold)
	seen := make(map[string]struct{}, i.threshold)

	for _, party := range i.registry.Identities() {
		resp, err := endorse(party)
		if err != nil {
			logger.Debugf("endorser [%s] did not respond: %v", party, err)

			continue
		}
		if err := resp.Error(); err != nil {
			logger.Debugf("endorser [%s] declined: %v", party, err)

			continue
		}

		signer, err := i.verify(digest, resp.Signature)
		if err != nil {
			logger.Debugf("discarding signature from [%s]: %v", party, err)

			continue
		}
		if _, dup := seen[signer]; dup {
			logger.Debugf("discarding duplicate signature recovered to [%s]", signer)

			continue
		}
		seen[signer] = struct{}{}
		signatures = append(signatures, resp.Signature)

		if len(signatures) >= i.threshold {
			break
		}
	}

	if len(signatures) < i.threshold {
		return nil, errors.Wrapf(ErrInsufficientEndorsements, "collected %d of %d required", len(signatures), i.threshold)
	}

	return &Result{Anchor: i.request.Anchor, Delta: delta, Endorsements: signatures}, nil
}

// verify recovers the signer from sig over digest and confirms it is a registered endorser. It
// returns the signer's registry key (its address hex) so the caller can de-duplicate. An unknown
// signer is ErrUnknownSigner.
func (i *Initiator) verify(digest [32]byte, sig []byte) (string, error) {
	address, err := eip712.RecoverAddress(digest, sig)
	if err != nil {
		return "", errors.Wrap(err, "failed to recover signer")
	}
	if !i.registry.IsEndorser(address) {
		return "", errors.Wrapf(ErrUnknownSigner, "address [%s]", address)
	}

	return address.Hex(), nil
}
