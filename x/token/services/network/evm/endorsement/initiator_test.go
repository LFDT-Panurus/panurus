/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// requestAnchor is the request's anchor in the form a delta carries it. A delta the initiator accepts
// must be bound to exactly this.
func requestAnchor(t *testing.T) [keys.AnchorLength]byte {
	t.Helper()
	a, err := keys.AnchorFromTxID(validRequest().Anchor)
	require.NoError(t, err)

	return a
}

// endorsedDelta is a minimal, well-formed delta bound to the request's anchor, standing in for what an
// endorser returns after validating and translating. Its exact contents do not matter; that every
// endorser returns the same bytes does, since that is what makes their signatures assemble.
func endorsedDelta(t *testing.T) *statedelta.StateDelta {
	t.Helper()
	var trh, pph [32]byte
	trh[0] = 0x11
	pph[0] = 0x22

	return &statedelta.StateDelta{
		Anchor:              requestAnchor(t),
		TokenRequestHash:    trh,
		PublicParamsHash:    pph,
		PublicParamsVersion: 1,
	}
}

// divergentDelta is a second well-formed delta for the same request: what an endorser running a
// different translation would return. It is legitimate on its face, so only disagreement with the
// others distinguishes it.
func divergentDelta(t *testing.T) *statedelta.StateDelta {
	t.Helper()
	d := endorsedDelta(t)
	d.PublicParamsVersion = 2

	return d
}

// endorserSet builds a registry of n endorsers keyed to the well-known test private keys 1..n, and
// returns their signers so a test can produce real signatures that recover to the registered
// addresses.
func endorserSet(t *testing.T, n int) (*Registry, []*eip712.Signer) {
	t.Helper()
	entries := make([]Endorser, 0, n)
	signers := make([]*eip712.Signer, 0, n)
	for k := 1; k <= n; k++ {
		s := newSigner(t, byte(k))
		entries = append(entries, Endorser{Identity: view.Identity(s.Address().Hex()), Address: s.Address()})
		signers = append(signers, s)
	}
	reg, err := NewRegistry(entries)
	require.NoError(t, err)

	return reg, signers
}

func newTestInitiator(reg *Registry, threshold int) *Initiator {
	return NewInitiator(reg, threshold, testDomain(), validRequest())
}

// endorsedBy builds the response an honest endorser sends: the delta it built, and its signature over
// that delta's digest.
func endorsedBy(t *testing.T, s *eip712.Signer, delta *statedelta.StateDelta) *EndorseResponse {
	t.Helper()
	sig, err := s.Sign(eip712.Digest(testDomain(), delta))
	require.NoError(t, err)

	return &EndorseResponse{Delta: delta, Signature: sig, EndorserAddress: s.Address().Hex()}
}

// answerWith returns an endorse closure where every listed party replies with delta, honestly signed.
// Parties not in the map return an error, so a test can model unreachable endorsers.
func answerWith(t *testing.T, signers map[string]*eip712.Signer, delta *statedelta.StateDelta) func(view.Identity) (*EndorseResponse, error) {
	t.Helper()

	return func(party view.Identity) (*EndorseResponse, error) {
		s, ok := signers[string(party)]
		if !ok {
			return nil, assert.AnError
		}

		return endorsedBy(t, s, delta), nil
	}
}

// byAddress indexes signers the way the registry addresses them.
func byAddress(signers []*eip712.Signer) map[string]*eip712.Signer {
	m := make(map[string]*eip712.Signer, len(signers))
	for _, s := range signers {
		m[s.Address().Hex()] = s
	}

	return m
}

func TestInitiatorAssemblesQuorum(t *testing.T) {
	reg, signers := endorserSet(t, 3)
	init := newTestInitiator(reg, 2)
	delta := endorsedDelta(t)

	result, err := init.Collect(context.Background(), answerWith(t, byAddress(signers), delta))
	require.NoError(t, err)
	require.Len(t, result.Endorsements, 2, "collection stops once the threshold is met")
	assert.Equal(t, validRequest().Anchor, result.Anchor)
	require.NotNil(t, result.Delta)

	// every collected signature recovers to a distinct registered endorser over the delta's digest
	digest := eip712.Digest(testDomain(), result.Delta)
	seen := map[string]struct{}{}
	for _, sig := range result.Endorsements {
		addr, err := eip712.RecoverAddress(digest, sig)
		require.NoError(t, err)
		assert.True(t, reg.IsEndorser(addr))
		_, dup := seen[addr.Hex()]
		assert.False(t, dup, "signatures must be from distinct endorsers")
		seen[addr.Hex()] = struct{}{}
	}
}

// TestInitiatorTakesTheDeltaFromTheEndorsers is the property this flow turns on: the initiator does
// not build a delta, it carries through the one the endorsers signed. The delta here holds a
// token-request hash the initiator has no way to derive (it covers the validator's
// TokenRequestToSign attribute, not the marshalled request), so a result carrying it can only have
// come from the responses.
func TestInitiatorTakesTheDeltaFromTheEndorsers(t *testing.T) {
	reg, signers := endorserSet(t, 2)
	init := newTestInitiator(reg, 2)

	delta := endorsedDelta(t)
	delta.TokenRequestHash = [32]byte{0xDE, 0xAD, 0xBE, 0xEF}
	delta.PublicParamsVersion = 42

	result, err := init.Collect(context.Background(), answerWith(t, byAddress(signers), delta))
	require.NoError(t, err)
	assert.Equal(t, delta, result.Delta, "the assembled delta is the endorsers', unchanged")
}

func TestInitiatorFailsBelowThreshold(t *testing.T) {
	reg, signers := endorserSet(t, 3)
	init := newTestInitiator(reg, 2)

	// only one endorser answers
	answer := map[string]*eip712.Signer{signers[0].Address().Hex(): signers[0]}

	_, err := init.Collect(context.Background(), answerWith(t, answer, endorsedDelta(t)))
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInsufficientEndorsements)
	assert.NotErrorIs(t, err, ErrDivergentDeltas, "one endorser cannot disagree with itself")
}

func TestInitiatorIgnoresDuplicateSigner(t *testing.T) {
	reg, signers := endorserSet(t, 3)
	init := newTestInitiator(reg, 2)
	delta := endorsedDelta(t)

	// endorser 0 answers for everyone: the same key recovered under three identities must count once,
	// so the quorum of 2 is never reached (the distinct-signer rule the contract also enforces).
	sameKey := func(view.Identity) (*EndorseResponse, error) {
		return endorsedBy(t, signers[0], delta), nil
	}

	_, err := init.Collect(context.Background(), sameKey)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientEndorsements)
}

func TestInitiatorDiscardsUnknownSigner(t *testing.T) {
	reg, signers := endorserSet(t, 2)
	init := newTestInitiator(reg, 2)
	delta := endorsedDelta(t)

	// a stranger (key 9, not registered) answers alongside one real endorser: the stranger's
	// signature recovers to an unregistered address and must not count toward the quorum.
	stranger := newSigner(t, 9)
	answer := func(party view.Identity) (*EndorseResponse, error) {
		if string(party) == signers[0].Address().Hex() {
			return endorsedBy(t, signers[0], delta), nil
		}

		return endorsedBy(t, stranger, delta), nil
	}

	_, err := init.Collect(context.Background(), answer)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientEndorsements)
}

// TestInitiatorDiscardsSignatureOverAnotherDelta covers an endorser whose signature does not cover the
// delta it sent: recovery over the delta's own digest yields an unrelated address, so it is discarded
// exactly like a stranger's.
func TestInitiatorDiscardsSignatureOverAnotherDelta(t *testing.T) {
	reg, signers := endorserSet(t, 2)
	init := newTestInitiator(reg, 2)

	answer := func(party view.Identity) (*EndorseResponse, error) {
		s := byAddress(signers)[string(party)]
		resp := endorsedBy(t, s, divergentDelta(t))
		resp.Delta = endorsedDelta(t) // the signature no longer covers the delta that travels with it

		return resp, nil
	}

	_, err := init.Collect(context.Background(), answer)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientEndorsements)
}

// TestInitiatorRejectsDeltaForAnotherAnchor is the binding check: an endorser could return a
// perfectly well-formed, honestly signed delta for a different request. The initiator has no
// validator to catch that with, but it does know the anchor it asked about.
func TestInitiatorRejectsDeltaForAnotherAnchor(t *testing.T) {
	reg, signers := endorserSet(t, 2)
	init := newTestInitiator(reg, 2)

	other, err := keys.AnchorFromTxID(anchorHex(0xEE))
	require.NoError(t, err)
	elsewhere := endorsedDelta(t)
	elsewhere.Anchor = other

	_, err = init.Collect(context.Background(), answerWith(t, byAddress(signers), elsewhere))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientEndorsements)
}

// TestInitiatorRejectsMalformedDelta checks the initiator will not encode a delta that breaks the
// StateDelta invariants into a transaction, even when the signature over it is genuine.
func TestInitiatorRejectsMalformedDelta(t *testing.T) {
	reg, signers := endorserSet(t, 2)
	init := newTestInitiator(reg, 2)

	malformed := endorsedDelta(t)
	malformed.MetadataKeys = [][32]byte{{0x02}, {0x01}} // not strictly ascending
	malformed.MetadataVals = [][]byte{{0xAA}, {0xBB}}

	_, err := init.Collect(context.Background(), answerWith(t, byAddress(signers), malformed))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientEndorsements)
}

// TestInitiatorRejectsResponseWithoutADelta covers an endorser that signs but sends nothing to verify
// the signature against.
func TestInitiatorRejectsResponseWithoutADelta(t *testing.T) {
	reg, signers := endorserSet(t, 2)
	init := newTestInitiator(reg, 2)

	answer := func(party view.Identity) (*EndorseResponse, error) {
		resp := endorsedBy(t, byAddress(signers)[string(party)], endorsedDelta(t))
		resp.Delta = nil

		return resp, nil
	}

	_, err := init.Collect(context.Background(), answer)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientEndorsements)
}

// TestInitiatorIgnoresADivergentEndorser is the fault-tolerance property that keying by delta buys.
// One endorser out of three translates the request differently; the other two agree, so the quorum
// still forms over what they signed, and the odd one out is simply not counted.
func TestInitiatorIgnoresADivergentEndorser(t *testing.T) {
	reg, signers := endorserSet(t, 3)
	init := newTestInitiator(reg, 2)

	agreed, odd := endorsedDelta(t), divergentDelta(t)
	first := signers[0].Address().Hex()
	answer := func(party view.Identity) (*EndorseResponse, error) {
		s := byAddress(signers)[string(party)]
		if string(party) == first {
			return endorsedBy(t, s, odd), nil
		}

		return endorsedBy(t, s, agreed), nil
	}

	result, err := init.Collect(context.Background(), answer)
	require.NoError(t, err)
	assert.Equal(t, agreed, result.Delta, "the quorum forms over the delta the majority signed")
	require.Len(t, result.Endorsements, 2)
}

// TestInitiatorReportsDivergence checks that when endorsers answer but no single delta reaches the
// threshold, the failure says so. Reported as a plain shortfall it would look like unavailable
// endorsers, and the determinism bug behind it would stay hidden.
func TestInitiatorReportsDivergence(t *testing.T) {
	reg, signers := endorserSet(t, 2)
	init := newTestInitiator(reg, 2)

	first := signers[0].Address().Hex()
	answer := func(party view.Identity) (*EndorseResponse, error) {
		s := byAddress(signers)[string(party)]
		if string(party) == first {
			return endorsedBy(t, s, endorsedDelta(t)), nil
		}

		return endorsedBy(t, s, divergentDelta(t)), nil
	}

	_, err := init.Collect(context.Background(), answer)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInsufficientEndorsements)
	assert.ErrorIs(t, err, ErrDivergentDeltas)
}

// TestInitiatorRejectsAnUnusableAnchor checks the initiator fails before contacting anyone when it
// cannot derive the anchor it would have to bind the replies to.
func TestInitiatorRejectsAnUnusableAnchor(t *testing.T) {
	reg, _ := endorserSet(t, 2)
	req := validRequest()
	req.Anchor = "not-hex"
	init := NewInitiator(reg, 2, testDomain(), req)

	asked := false
	_, err := init.Collect(context.Background(), func(view.Identity) (*EndorseResponse, error) {
		asked = true

		return nil, assert.AnError
	})
	require.Error(t, err)
	assert.False(t, asked, "no endorser is contacted for a request whose anchor cannot be parsed")
}

// TestInitiatorStopsOnCancellation checks a cancelled context ends the round rather than working
// through the rest of the endorser set.
func TestInitiatorStopsOnCancellation(t *testing.T) {
	reg, signers := endorserSet(t, 3)
	init := newTestInitiator(reg, 3)

	ctx, cancel := context.WithCancel(context.Background())
	asked := 0
	answer := answerWith(t, byAddress(signers), endorsedDelta(t))
	_, err := init.Collect(ctx, func(party view.Identity) (*EndorseResponse, error) {
		asked++
		cancel()

		return answer(party)
	})
	require.Error(t, err)
	assert.Equal(t, 1, asked, "collection stops at the first check after cancellation")
}
