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
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// responseTimeout bounds how long the initiator waits for one endorser's reply.
const responseTimeout = 30 * time.Second

// Result is what a completed endorsement yields: the delta to apply and the collected quorum of
// signatures over its EIP-712 digest. The driver's RequestApproval wraps it into the network
// envelope; Broadcast ABI-encodes applyStateDelta(delta, endorsements).
type Result struct {
	// Anchor is the token-request anchor the endorsement is for.
	Anchor string
	// Delta is the StateDelta the quorum agreed on and the transaction will apply. It comes from the
	// endorsers, not from the initiator.
	Delta *statedelta.StateDelta
	// Endorsements are the collected 65-byte {r,s,v} signatures, one per distinct endorser, in the
	// order they were collected. Each has been recovered to a registered endorser and de-duplicated
	// against the digest of Delta, so the set satisfies the contract's threshold and distinct-signer
	// rules.
	Endorsements [][]byte
}

// Initiator collects a threshold of endorser signatures over one request and assembles them into a
// Result. It is the EVM analog of the Fabric RequestApprovalView (design §6.3): it opens a session to
// each registered endorser, sends the request, and gathers the replies.
//
// It does no validation and builds no StateDelta of its own. Validating a token request and
// translating it is the endorsers' job, and it is what this whole flow exists to delegate; each
// endorser returns the delta it built alongside its signature, exactly as a Fabric endorser returns
// the RWSet inside its proposal response. Rebuilding the delta locally would mean an initiator
// repeating every endorser's work, reading the chain itself, and failing the transaction whenever its
// own view of public parameters or ledger state differed from theirs. None of that buys a security
// property: the contract's rule is that a threshold of registered endorsers signed the digest, and a
// quorum that wanted to submit something else would not need the initiator at all.
//
// What it does check is cheap and needs no validator: that a returned delta is bound to the anchor it
// asked about and is structurally well formed, that each signature recovers to a registered endorser
// over that delta's own digest, and that the endorsers counted are distinct. This mirrors the
// contract's rules (recover, authorize, distinct), so the initiator never assembles a quorum the
// contract would reject.
type Initiator struct {
	registry  *Registry
	threshold int
	domain    eip712.Domain
	request   *EndorseRequest
}

// NewInitiator returns an Initiator for one request. threshold is the number of distinct endorser
// signatures the quorum requires.
func NewInitiator(registry *Registry, threshold int, domain eip712.Domain, request *EndorseRequest) *Initiator {
	return &Initiator{
		registry:  registry,
		threshold: threshold,
		domain:    domain,
		request:   request,
	}
}

// Call implements the FSC initiator view: request an endorsement from each registered endorser over
// its own session and assemble the quorum. It returns the *Result.
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

// agreement accumulates the signatures collected over one delta. Endorsers that validated the same
// request must translate it into byte-identical deltas (the §4.4 determinism guarantee), so a healthy
// network produces exactly one of these. Keying them by digest is what makes a divergent endorser
// merely ignored rather than able to break every transaction it takes part in.
type agreement struct {
	delta      *statedelta.StateDelta
	signatures [][]byte
	signers    map[string]struct{}
}

// Collect drives the assembly independently of the session transport: for each registered endorser it
// calls endorse, checks the reply, and accumulates distinct valid signatures per delta until one delta
// reaches the threshold. Separating it from Call keeps the quorum logic testable without an FSC
// runtime.
//
// A single endorser's failure (declined, unreachable, malformed or unauthorized signature, or a delta
// that does not belong to this request) is not fatal: the initiator moves on and still succeeds if
// enough others agree. It fails only when no single delta collected threshold distinct signatures.
func (i *Initiator) Collect(ctx context.Context, endorse func(view.Identity) (*EndorseResponse, error)) (*Result, error) {
	anchor, err := keys.AnchorFromTxID(i.request.Anchor)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid anchor [%s]", i.request.Anchor)
	}

	agreed := make(map[[32]byte]*agreement, 1)
	for _, party := range i.registry.Identities() {
		if err := ctx.Err(); err != nil {
			return nil, errors.Wrap(err, "endorsement collection interrupted")
		}

		resp, err := endorse(party)
		if err != nil {
			logger.Debugf("endorser [%s] did not respond: %v", party, err)

			continue
		}
		if err := resp.Error(); err != nil {
			logger.Debugf("endorser [%s] declined: %v", party, err)

			continue
		}
		if err := i.bind(anchor, resp.Delta); err != nil {
			logger.Debugf("discarding delta from [%s]: %v", party, err)

			continue
		}

		digest := eip712.Digest(i.domain, resp.Delta)
		signer, err := i.verify(digest, resp.Signature)
		if err != nil {
			logger.Debugf("discarding signature from [%s]: %v", party, err)

			continue
		}

		quorum, known := agreed[digest]
		if !known {
			quorum = &agreement{delta: resp.Delta, signers: make(map[string]struct{}, i.threshold)}
			agreed[digest] = quorum
			if len(agreed) > 1 {
				// Two endorsers validated the same request and translated it differently. That is a
				// determinism bug in the translator rather than a fault of either endorser, and it stays
				// invisible if it is only ever reported as a missing quorum.
				logger.Warnf("endorsers disagree on the delta for [%s]: %d distinct deltas so far",
					i.request.Anchor, len(agreed))
			}
		}
		if _, dup := quorum.signers[signer]; dup {
			logger.Debugf("discarding duplicate signature recovered to [%s]", signer)

			continue
		}
		quorum.signers[signer] = struct{}{}
		quorum.signatures = append(quorum.signatures, resp.Signature)

		if len(quorum.signatures) >= i.threshold {
			return &Result{Anchor: i.request.Anchor, Delta: quorum.delta, Endorsements: quorum.signatures}, nil
		}
	}

	return nil, noQuorum(agreed, i.threshold)
}

// bind checks that a returned delta belongs to the request this initiator sent, using only what can be
// computed without a validator: the anchor is derived from the request, and the delta's own structural
// invariants must hold before it is encoded into a transaction.
//
// It deliberately does not check TokenRequestHash. That hash covers the validator's TokenRequestToSign
// attribute rather than the marshalled request the initiator holds, so reproducing it would mean
// running the validation this flow delegates. The endorsers bind it, and the contract stores what they
// signed.
func (i *Initiator) bind(anchor [keys.AnchorLength]byte, delta *statedelta.StateDelta) error {
	if delta.Anchor != anchor {
		return errors.Wrapf(ErrDeltaMismatch, "delta anchor [%x] is not the request anchor [%x]", delta.Anchor, anchor)
	}
	if err := delta.Validate(); err != nil {
		return errors.Wrapf(ErrDeltaMismatch, "malformed delta: %v", err)
	}

	return nil
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

// noQuorum reports why the collection ended without one. It separates too few endorsements from
// endorsers that answered but disagreed on the delta, because the second is a determinism failure in
// the translator and needs a different fix than an endorser that was simply unavailable.
func noQuorum(agreed map[[32]byte]*agreement, threshold int) error {
	best := 0
	for _, quorum := range agreed {
		if len(quorum.signatures) > best {
			best = len(quorum.signatures)
		}
	}
	if len(agreed) > 1 {
		return errors.Wrapf(errors.Join(ErrInsufficientEndorsements, ErrDivergentDeltas),
			"endorsers signed %d distinct deltas, the largest agreement had %d of %d required",
			len(agreed), best, threshold)
	}

	return errors.Wrapf(ErrInsufficientEndorsements, "collected %d of %d required", best, threshold)
}
