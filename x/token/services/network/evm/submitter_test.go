/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"math/big"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/abi"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// testKey returns the well-known secp256k1 test key with the given low byte.
func testKey(low byte) *secp256k1.PrivateKey {
	raw := make([]byte, 32)
	raw[31] = low

	return secp256k1.PrivKeyFromBytes(raw)
}

func testDelta() *statedelta.StateDelta {
	var anchor, hash [32]byte
	anchor[31] = 0xA1
	hash[31] = 0x0F

	return &statedelta.StateDelta{
		Anchor:              anchor,
		TokenRequestHash:    hash,
		PublicParamsHash:    hash,
		PublicParamsVersion: 1,
	}
}

// readySubmitterClient returns a mock node that answers everything a successful submit needs.
func readySubmitterClient() *mock.EVMClient {
	evm := &mock.EVMClient{}
	evm.PendingNonceAtReturns(3, nil)
	evm.EstimateGasReturns(100_000, nil)
	evm.SuggestGasFeesReturns(client.GasFees{
		MaxFeePerGas:         big.NewInt(20_000_000_000),
		MaxPriorityFeePerGas: big.NewInt(1_000_000_000),
	}, nil)
	evm.SendRawTransactionReturns(client.Hash{}, nil)

	return evm
}

func testSubmitter(t *testing.T, evm client.EVMClient, gas GasConfig) *Submitter {
	t.Helper()
	tokenState, err := client.HexToAddress(testTokenState)
	require.NoError(t, err)
	s, err := NewSubmitter(evm, testKey(1), tokenState, big.NewInt(testChainID), gas)
	require.NoError(t, err)

	return s
}

func estimateGas() GasConfig {
	return GasConfig{Strategy: GasStrategyEstimate, Multiplier: DefaultGasMultiplier}
}

// TestSubmitSendsSignedApplyStateDelta checks the whole write path: the endorsed delta is encoded as
// an applyStateDelta call, signed, and sent to the TokenState.
func TestSubmitSendsSignedApplyStateDelta(t *testing.T) {
	evm := readySubmitterClient()
	s := testSubmitter(t, evm, estimateGas())
	endorsements := [][]byte{make([]byte, 65)}

	rawTx, _, err := s.Submit(t.Context(), testDelta(), endorsements)
	require.NoError(t, err)
	require.NotEmpty(t, rawTx)

	require.Equal(t, 1, evm.SendRawTransactionCallCount())
	_, sent := evm.SendRawTransactionArgsForCall(0)
	assert.Equal(t, rawTx, sent)
	assert.Equal(t, client.TxTypeDynamicFee, rawTx[0], "the driver sends type-2 transactions")

	// the gas estimation must have been for the same calldata that gets signed
	_, msg := evm.EstimateGasArgsForCall(0)
	assert.Equal(t, abi.EncodeApplyStateDelta(testDelta(), endorsements), msg.Data)
	require.NotNil(t, msg.To)
	assert.Equal(t, s.tokenState, *msg.To)
}

// TestSubmitAppliesGasMultiplier checks the safety margin: state can change between estimation and
// execution, so the bare estimate is not enough.
func TestSubmitAppliesGasMultiplier(t *testing.T) {
	evm := readySubmitterClient()
	evm.EstimateGasReturns(100_000, nil)
	s := testSubmitter(t, evm, GasConfig{Strategy: GasStrategyEstimate, Multiplier: 1.5})

	limit, err := s.gasLimit(t.Context(), []byte{0x01})
	require.NoError(t, err)
	assert.Equal(t, uint64(150_000), limit)
}

func TestSubmitFixedGasStrategy(t *testing.T) {
	evm := readySubmitterClient()
	s := testSubmitter(t, evm, GasConfig{Strategy: GasStrategyFixed, Limit: 250_000})

	limit, err := s.gasLimit(t.Context(), []byte{0x01})
	require.NoError(t, err)
	assert.Equal(t, uint64(250_000), limit)
	assert.Zero(t, evm.EstimateGasCallCount(), "a fixed limit must not consult the node")
}

// TestSubmitDoesNotAdvanceTheNonceOnFailure is the production trap: a nonce allocated for a
// transaction that never reached the node must not be lost, or every later transaction sits
// unmineable behind the gap. Submit runs the whole attempt under the nonce manager's lock, so a
// failure simply leaves the nonce exactly where it was, ready to be reused without asking the chain
// again.
func TestSubmitDoesNotAdvanceTheNonceOnFailure(t *testing.T) {
	evm := readySubmitterClient()
	evm.SendRawTransactionReturns(client.Hash{}, errors.New("broadcast rejected"))
	s := testSubmitter(t, evm, estimateGas())

	_, _, err := s.Submit(t.Context(), testDelta(), [][]byte{make([]byte, 65)})
	require.Error(t, err)

	next, initialized := s.nonces.Cached()
	assert.True(t, initialized, "the manager still knows its sequence; there was nothing to lose")
	assert.Equal(t, uint64(3), next, "the nonce this attempt held must still be the next one handed out")
}

// TestSubmitClassifiesARevertedEstimate covers the verdict the shared fungible bodies actually read.
//
// When the node estimates gas it executes the transaction, so an estimate that reverts is the chain
// rejecting the delta - a double spend, stale public parameters, a quorum it will not accept. Sending
// it anyway mines it with status 0 and reaches the same answer, having paid for it. The submitter has
// to report that as the permanent rejection it is, and it must not report the same way when the node
// merely failed to answer: those callers retry.
func TestSubmitClassifiesARevertedEstimate(t *testing.T) {
	reverted := errors.Wrapf(client.ErrExecutionReverted, "eth_estimateGas failed: execution reverted")

	t.Run("a reverted estimate is a permanent rejection", func(t *testing.T) {
		evm := readySubmitterClient()
		evm.EstimateGasReturns(0, reverted)
		s := testSubmitter(t, evm, estimateGas())

		_, _, err := s.Submit(t.Context(), testDelta(), [][]byte{make([]byte, 65)})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrTransactionReverted)
		assert.Zero(t, evm.SendRawTransactionCallCount(),
			"a transaction the node has already rejected must not be paid for")
	})

	// The fungible bodies assert on this wording when they broadcast the second of two conflicting
	// transfers, so it is part of the driver's contract with them and not just a log line.
	t.Run("the message says the transaction is not valid", func(t *testing.T) {
		evm := readySubmitterClient()
		evm.EstimateGasReturns(0, reverted)
		s := testSubmitter(t, evm, estimateGas())

		_, _, err := s.Submit(t.Context(), testDelta(), [][]byte{make([]byte, 65)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is not valid")
	})

	t.Run("an unreachable node is not a rejection", func(t *testing.T) {
		evm := readySubmitterClient()
		evm.EstimateGasReturns(0, errors.New("connection refused"))
		s := testSubmitter(t, evm, estimateGas())

		_, _, err := s.Submit(t.Context(), testDelta(), [][]byte{make([]byte, 65)})
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrTransactionReverted,
			"a node that never judged the transaction says nothing about its validity")
		require.ErrorIs(t, err, ErrNetworkUnavailable, "the caller should retry this one")
	})

	// A node that refuses the transaction has not executed it either, so it belongs in the same
	// transient class as an unreachable one, not with the reverts.
	t.Run("a refused broadcast is transient", func(t *testing.T) {
		evm := readySubmitterClient()
		evm.SendRawTransactionReturns(client.Hash{}, errors.New("connection reset"))
		s := testSubmitter(t, evm, estimateGas())

		_, _, err := s.Submit(t.Context(), testDelta(), [][]byte{make([]byte, 65)})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNetworkUnavailable)
		require.NotErrorIs(t, err, ErrTransactionReverted)
	})

	// Not consuming the nonce is what makes the retry advice usable: a rejected transaction that
	// consumed a nonce locally would leave every later one stuck behind a gap.
	t.Run("a rejected transaction leaves its nonce ready to reuse", func(t *testing.T) {
		evm := readySubmitterClient()
		evm.EstimateGasReturns(0, reverted)
		s := testSubmitter(t, evm, estimateGas())

		_, _, err := s.Submit(t.Context(), testDelta(), [][]byte{make([]byte, 65)})
		require.Error(t, err)
		next, initialized := s.nonces.Cached()
		assert.True(t, initialized)
		assert.Equal(t, uint64(3), next, "the rejected attempt's nonce must still be next in line")
	})
}

func TestSubmitRejectsIncompleteInput(t *testing.T) {
	s := testSubmitter(t, readySubmitterClient(), estimateGas())

	t.Run("nil delta", func(t *testing.T) {
		_, _, err := s.Submit(t.Context(), nil, [][]byte{make([]byte, 65)})
		require.Error(t, err)
	})

	t.Run("no endorsements", func(t *testing.T) {
		_, _, err := s.Submit(t.Context(), testDelta(), nil)
		require.Error(t, err, "an unendorsed delta must never be broadcast")
	})
}

func TestNewSubmitterValidates(t *testing.T) {
	tokenState, err := client.HexToAddress(testTokenState)
	require.NoError(t, err)

	t.Run("nil client", func(t *testing.T) {
		_, err := NewSubmitter(nil, testKey(1), tokenState, big.NewInt(1), estimateGas())
		require.Error(t, err)
	})

	t.Run("nil key", func(t *testing.T) {
		_, err := NewSubmitter(&mock.EVMClient{}, nil, tokenState, big.NewInt(1), estimateGas())
		require.Error(t, err)
	})

	t.Run("missing chain id", func(t *testing.T) {
		_, err := NewSubmitter(&mock.EVMClient{}, testKey(1), tokenState, nil, estimateGas())
		require.Error(t, err, "an unset chain id would allow cross-chain replay")
	})
}

// TestSubmitterAddressMatchesKey checks the submitter reports the account that actually pays, derived
// from its own key.
func TestSubmitterAddressMatchesKey(t *testing.T) {
	s := testSubmitter(t, readySubmitterClient(), estimateGas())
	assert.Equal(t, "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf", s.Address().Hex())
}
