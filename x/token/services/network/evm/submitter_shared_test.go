/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
)

// TestSubmittersForOneAccountShareANonceSequence checks that two TMS configured with the same
// submitter key get one nonce sequence between them. Each gets its own Submitter, bound to its own
// TokenState, but the account is the same, and two counters for one account hand out the same nonce.
func TestSubmittersForOneAccountShareANonceSequence(t *testing.T) {
	d := &Driver{}
	evmClient := &mock.EVMClient{}
	shared := writeKey(t, anvilKey1)

	cfgA := validConfig()
	cfgA.Submitter.Keystore = shared
	cfgB := validConfig()
	cfgB.Contracts.TokenState = "0x00000000000000000000000000000000000000b2"
	cfgB.Submitter.Keystore = shared

	a, err := d.newSubmitter(cfgA, evmClient)
	require.NoError(t, err)
	b, err := d.newSubmitter(cfgB, evmClient)
	require.NoError(t, err)
	assert.NotSame(t, a, b, "each TMS still gets its own submitter, bound to its own TokenState")
	assert.Same(t, a.nonces, b.nonces, "but one account has one nonce sequence")

	cfgOther := validConfig()
	cfgOther.Submitter.Keystore = writeKey(t, anvilStrangerKey)
	other, err := d.newSubmitter(cfgOther, evmClient)
	require.NoError(t, err)
	assert.NotSame(t, a.nonces, other.nonces, "a different account keeps its own")

	cfgOtherChain := validConfig()
	cfgOtherChain.ChainID = testChainID + 1
	cfgOtherChain.Submitter.Keystore = shared
	otherChain, err := d.newSubmitter(cfgOtherChain, evmClient)
	require.NoError(t, err)
	assert.NotSame(t, a.nonces, otherChain.nonces, "and so does the same account on another chain")
}
