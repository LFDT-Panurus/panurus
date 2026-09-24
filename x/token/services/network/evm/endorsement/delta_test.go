/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
)

// TestDeltaFactoryRefusesStalePublicParams is the regression test for F1: an endorser used to stamp a
// delta with the chain's public-parameters bytes/hash regardless of what the local validator had
// actually validated the request against, so a node whose pp.Watcher had not yet caught up with an
// on-chain update would sign a delta asserting parameters it never validated with. Build must instead
// refuse with ErrStalePublicParams and produce no delta.
func TestDeltaFactoryRefusesStalePublicParams(t *testing.T) {
	validator := &fakeValidator{
		actions: []any{issueAction()},
		meta:    map[string][]byte{common.TokenRequestToSign: []byte(trsMessage)},
	}
	chain := &fakePP{raw: []byte("chain-currently-holds-v2"), version: 2}
	local := &fakePP{raw: []byte("this-node-still-validates-against-v1")}
	factory := NewDeltaFactory(validator, local, chain, &mock.EVMClient{}, addr(0xAA), "")

	delta, err := factory.Build(context.Background(), validRequest())
	require.ErrorIs(t, err, ErrStalePublicParams)
	assert.Nil(t, delta)
}

// TestDeltaFactoryBindsToValidatorsPublicParams is the companion positive case: when the local
// validator's parameters agree with the chain's, Build proceeds and the delta's PublicParamsHash is
// SHA-256 of the bytes the validator actually validated with (which is also, in this agreeing case,
// what the chain holds - the two are required to be byte-identical whenever the hashes match).
func TestDeltaFactoryBindsToValidatorsPublicParams(t *testing.T) {
	validator := &fakeValidator{
		actions: []any{issueAction()},
		meta:    map[string][]byte{common.TokenRequestToSign: []byte(trsMessage)},
	}
	pp := &fakePP{raw: []byte(testPPRaw), version: testPPVer}
	factory := NewDeltaFactory(validator, pp, pp, &mock.EVMClient{}, addr(0xAA), "")

	delta, err := factory.Build(context.Background(), validRequest())
	require.NoError(t, err)
	require.NotNil(t, delta)
	assert.Equal(t, crypto.SHA256([]byte(testPPRaw)), delta.PublicParamsHash[:],
		"the delta must bind the hash of what the validator actually validated with")
	assert.Equal(t, testPPVer, delta.PublicParamsVersion, "the version still comes from the chain")
}

// TestSetupDeltaFactoryBuildsASetupDelta is the regression test for issue #2412 item 1: a setup
// request used to be unreachable because it went through the ordinary DeltaFactory.Build, which hands
// raw public-parameters bytes to a RequestValidator expecting a marshalled token.Request. Build must
// instead produce a well-formed setup delta binding the chain's current parameters as its
// optimistic-concurrency baseline, with the new parameters as SetupParameters.
func TestSetupDeltaFactoryBuildsASetupDelta(t *testing.T) {
	current := &fakePP{raw: []byte(testPPRaw), version: testPPVer}
	factory := NewSetupDeltaFactory(&fakePPValidator{pp: &fakePublicParameters{}}, current)

	newPP := []byte("new-public-parameters")
	delta, err := factory.Build(context.Background(), &EndorseRequest{
		Kind: KindSetup, PublicParamsRaw: newPP, TMSID: testTMSID(), Anchor: anchorHex(0xC1),
	})
	require.NoError(t, err)
	require.NotNil(t, delta)
	assert.True(t, delta.IsSetup)
	assert.Equal(t, newPP, delta.SetupParameters)
	assert.Equal(t, crypto.SHA256([]byte(testPPRaw)), delta.PublicParamsHash[:],
		"the CAS baseline is the chain's CURRENT parameters, not the new ones")
	assert.Equal(t, testPPVer, delta.PublicParamsVersion)
	assert.Empty(t, delta.SpentRefs)
	assert.Empty(t, delta.Outputs)
}

// TestSetupDeltaFactoryRejectsInvalidPublicParams checks a structurally invalid new-parameters payload
// is refused before anything is signed.
func TestSetupDeltaFactoryRejectsInvalidPublicParams(t *testing.T) {
	current := &fakePP{raw: []byte(testPPRaw), version: testPPVer}

	t.Run("unmarshal failure", func(t *testing.T) {
		factory := NewSetupDeltaFactory(&fakePPValidator{err: assert.AnError}, current)
		delta, err := factory.Build(context.Background(), &EndorseRequest{
			Kind: KindSetup, PublicParamsRaw: []byte("garbage"), TMSID: testTMSID(), Anchor: anchorHex(0xC1),
		})
		require.ErrorIs(t, err, ErrValidation)
		assert.Nil(t, delta)
	})

	t.Run("structural validation failure", func(t *testing.T) {
		factory := NewSetupDeltaFactory(&fakePPValidator{pp: &fakePublicParameters{err: assert.AnError}}, current)
		delta, err := factory.Build(context.Background(), &EndorseRequest{
			Kind: KindSetup, PublicParamsRaw: []byte("new-pp"), TMSID: testTMSID(), Anchor: anchorHex(0xC1),
		})
		require.ErrorIs(t, err, ErrValidation)
		assert.Nil(t, delta)
	})
}

// TestSetupDeltaFactorySurfacesChainReadFailure checks a failure to read the chain's current
// parameters is surfaced rather than signing a delta with a fabricated CAS baseline.
func TestSetupDeltaFactorySurfacesChainReadFailure(t *testing.T) {
	factory := NewSetupDeltaFactory(&fakePPValidator{pp: &fakePublicParameters{}}, &fakePP{err: assert.AnError})

	delta, err := factory.Build(context.Background(), &EndorseRequest{
		Kind: KindSetup, PublicParamsRaw: []byte("new-pp"), TMSID: testTMSID(), Anchor: anchorHex(0xC1),
	})
	require.Error(t, err)
	assert.Nil(t, delta)
}
