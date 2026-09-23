/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	evmdriver "github.com/LFDT-Panurus/panurus/x/token/services/network/evm"
	evmclient "github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
)

// TestFundsFreshAccountsOnBesu is the property the topology depends on: an account generated for a
// node can pay for its own transactions, and the accounts it hands out do not share a nonce sequence.
//
// Sharing one pre-funded account across nodes is what this replaced. It works until a second node
// broadcasts and then fails with "nonce too low", which reads as a driver bug and is not one.
func TestFundsFreshAccountsOnBesu(t *testing.T) {
	node := startTestBesu(t, "evm-fund-besu", 18749)

	evmClient, err := evmclient.NewJSONRPCClient(node.Endpoint(), nil)
	require.NoError(t, err)

	operator, err := WriteFundedSubmitter(t.TempDir(), "operator")
	require.NoError(t, err)
	key, err := evmdriver.LoadKeyForAddress(operator.Keystore, operator.Address.Hex())
	require.NoError(t, err)

	funder, err := NewFunder(evmClient, key, operator.Address, big.NewInt(node.ChainID()))
	require.NoError(t, err)

	ctx := context.Background()
	dir := t.TempDir()

	first, err := funder.FundedIdentity(ctx, dir, "alice", DefaultFunding)
	require.NoError(t, err)
	second, err := funder.FundedIdentity(ctx, dir, "bob", DefaultFunding)
	require.NoError(t, err)

	assert.NotEqual(t, first.Address, second.Address, "each node must get its own account")

	// A funded account starts its own nonce sequence at zero, independent of the operator's and of the
	// other node's. That independence is the whole point of funding one per node.
	for _, identity := range []Identity{first, second} {
		nonce, err := evmClient.PendingNonceAt(ctx, identity.Address)
		require.NoError(t, err)
		assert.EqualValues(t, 0, nonce, "a fresh account has sent nothing")
	}

	// And it can actually pay: spend from one of them, which is what a node's submitter does.
	spender, err := evmdriver.LoadKeyForAddress(first.Keystore, first.Address.Hex())
	require.NoError(t, err)
	spending, err := NewFunder(evmClient, spender, first.Address, big.NewInt(node.ChainID()))
	require.NoError(t, err)
	require.NoError(t, spending.Fund(ctx, second.Address, big.NewInt(1)),
		"a funded account must be able to pay for a transaction")

	nonce, err := evmClient.PendingNonceAt(ctx, first.Address)
	require.NoError(t, err)
	assert.EqualValues(t, 1, nonce, "the spender's own sequence moved, and only its own")

	nonce, err = evmClient.PendingNonceAt(ctx, second.Address)
	require.NoError(t, err)
	assert.EqualValues(t, 0, nonce, "being paid does not move an account's nonce")
}

// TestNewFunderValidates checks the guards that would otherwise produce a funder that cannot sign or
// cannot reach a chain, failing much later with something less obvious.
func TestNewFunderValidates(t *testing.T) {
	operator, err := WriteFundedSubmitter(t.TempDir(), "operator")
	require.NoError(t, err)
	key, err := evmdriver.LoadKeyForAddress(operator.Keystore, operator.Address.Hex())
	require.NoError(t, err)

	_, err = NewFunder(nil, key, operator.Address, big.NewInt(1337))
	require.Error(t, err)

	_, err = NewFunder(&evmclient.JSONRPCClient{}, nil, operator.Address, big.NewInt(1337))
	require.Error(t, err)

	_, err = NewFunder(&evmclient.JSONRPCClient{}, key, operator.Address, nil)
	require.Error(t, err)
}
