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
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// fakeDeltaBuilder returns a fixed delta, so initiator tests isolate the assembly logic from
// validation/translation (exercised in the responder and delta tests).
type fakeDeltaBuilder struct {
	delta *statedelta.StateDelta
	err   error
}

func (f *fakeDeltaBuilder) Build(context.Context, *EndorseRequest) (*statedelta.StateDelta, error) {
	return f.delta, f.err
}

// fixedDelta is a minimal, well-formed delta for digest computation. Its exact contents do not
// matter; determinism does, so the initiator and the signers compute the same digest.
func fixedDelta() *statedelta.StateDelta {
	var trh, pph [32]byte
	trh[0] = 0x11
	pph[0] = 0x22

	return &statedelta.StateDelta{
		Anchor:              [32]byte{0xC1},
		TokenRequestHash:    trh,
		PublicParamsHash:    pph,
		PublicParamsVersion: 1,
	}
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
	return NewInitiator(reg, threshold, &fakeDeltaBuilder{delta: fixedDelta()}, testDomain(), validRequest())
}

// signWith returns an endorse closure that answers, for the party whose identity matches a signer's
// address, a real signature over the initiator's digest. Parties not in the map return an error, so
// a test can model unreachable or non-responding endorsers.
func signWith(t *testing.T, signers map[string]*eip712.Signer, digest [32]byte) func(view.Identity) (*EndorseResponse, error) {
	t.Helper()

	return func(party view.Identity) (*EndorseResponse, error) {
		s, ok := signers[string(party)]
		if !ok {
			return nil, assert.AnError
		}
		sig, err := s.Sign(digest)
		require.NoError(t, err)

		return &EndorseResponse{Signature: sig, EndorserAddress: s.Address().Hex()}, nil
	}
}

func digestOf(delta *statedelta.StateDelta) [32]byte { return eip712.Digest(testDomain(), delta) }

func TestInitiatorAssemblesQuorum(t *testing.T) {
	reg, signers := endorserSet(t, 3)
	init := newTestInitiator(reg, 2)
	digest := digestOf(fixedDelta())

	// all three endorsers sign; the initiator stops at the threshold of 2
	answer := map[string]*eip712.Signer{}
	for _, s := range signers {
		answer[s.Address().Hex()] = s
	}

	result, err := init.Collect(context.Background(), signWith(t, answer, digest))
	require.NoError(t, err)
	require.Len(t, result.Endorsements, 2, "collection stops once the threshold is met")
	assert.Equal(t, validRequest().Anchor, result.Anchor)
	require.NotNil(t, result.Delta)

	// every collected signature recovers to a distinct registered endorser
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

func TestInitiatorFailsBelowThreshold(t *testing.T) {
	reg, signers := endorserSet(t, 3)
	init := newTestInitiator(reg, 2)
	digest := digestOf(fixedDelta())

	// only one endorser answers
	answer := map[string]*eip712.Signer{signers[0].Address().Hex(): signers[0]}

	_, err := init.Collect(context.Background(), signWith(t, answer, digest))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientEndorsements)
}

func TestInitiatorIgnoresDuplicateSigner(t *testing.T) {
	reg, signers := endorserSet(t, 3)
	init := newTestInitiator(reg, 2)
	digest := digestOf(fixedDelta())

	// endorser 0 answers for everyone: the same key recovered under three identities must count once,
	// so the quorum of 2 is never reached (the distinct-signer rule the contract also enforces).
	sameKey := func(view.Identity) (*EndorseResponse, error) {
		sig, err := signers[0].Sign(digest)
		require.NoError(t, err)

		return &EndorseResponse{Signature: sig, EndorserAddress: signers[0].Address().Hex()}, nil
	}

	_, err := init.Collect(context.Background(), sameKey)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientEndorsements)
}

func TestInitiatorDiscardsUnknownSigner(t *testing.T) {
	reg, signers := endorserSet(t, 2)
	init := newTestInitiator(reg, 2)
	digest := digestOf(fixedDelta())

	// a stranger (key 9, not registered) answers alongside one real endorser: the stranger's
	// signature recovers to an unregistered address and must not count toward the quorum.
	stranger := newSigner(t, 9)
	answer := func(party view.Identity) (*EndorseResponse, error) {
		if string(party) == signers[0].Address().Hex() {
			sig, _ := signers[0].Sign(digest)

			return &EndorseResponse{Signature: sig}, nil
		}
		sig, _ := stranger.Sign(digest)

		return &EndorseResponse{Signature: sig}, nil
	}

	_, err := init.Collect(context.Background(), answer)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientEndorsements)
}

func TestInitiatorDiscardsSignatureOverWrongDigest(t *testing.T) {
	reg, signers := endorserSet(t, 2)
	init := newTestInitiator(reg, 2)

	// both endorsers sign a DIFFERENT digest than the initiator computes: every signature recovers to
	// an unrelated address and is discarded, so no quorum forms.
	wrong := digestOf(&statedelta.StateDelta{Anchor: [32]byte{0x99}, TokenRequestHash: [32]byte{0x1}, PublicParamsHash: [32]byte{0x2}, PublicParamsVersion: 1})
	answer := map[string]*eip712.Signer{}
	for _, s := range signers {
		answer[s.Address().Hex()] = s
	}

	_, err := init.Collect(context.Background(), signWith(t, answer, wrong))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientEndorsements)
}

func TestInitiatorPropagatesBuilderFailure(t *testing.T) {
	reg, _ := endorserSet(t, 2)
	init := NewInitiator(reg, 2, &fakeDeltaBuilder{err: assert.AnError}, testDomain(), validRequest())

	_, err := init.Collect(context.Background(), func(view.Identity) (*EndorseResponse, error) {
		return nil, assert.AnError
	})
	require.Error(t, err)
}
