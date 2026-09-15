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
