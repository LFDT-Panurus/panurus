/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"math"
	"math/big"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/abi"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// Submitter turns an endorsed StateDelta into a signed Ethereum transaction and sends it. It owns the
// submitter key, the nonce sequence and the gas policy, which are the three things that make a
// transaction acceptable to the node (design §8).
//
// It is deliberately separate from endorsement: the endorsers authorize *what* is applied, the
// submitter only decides *how* it reaches the chain (nonce, gas, fees). Paying for a transaction
// grants no authority over its contents, which the contract verifies independently.
type Submitter struct {
	client     client.EVMClient
	key        *secp256k1.PrivateKey
	address    client.Address
	tokenState client.Address
	chainID    *big.Int
	gas        GasConfig
	nonces     *NonceManager
}

// NewSubmitter assembles a Submitter for one TMS.
func NewSubmitter(
	evmClient client.EVMClient,
	key *secp256k1.PrivateKey,
	tokenState client.Address,
	chainID *big.Int,
	gas GasConfig,
) (*Submitter, error) {
	if evmClient == nil {
		return nil, errors.New("submitter: nil evm client")
	}
	if key == nil {
		return nil, errors.New("submitter: nil signing key")
	}
	if chainID == nil || chainID.Sign() <= 0 {
		return nil, errors.New("submitter: chain id must be set and positive")
	}
	address := eip712.PubKeyToAddress(key.PubKey())

	return &Submitter{
		client:     evmClient,
		key:        key,
		address:    address,
		tokenState: tokenState,
		chainID:    chainID,
		gas:        gas,
		nonces:     NewNonceManager(evmClient, address),
	}, nil
}

// Address returns the submitter's Ethereum account, the one that pays for gas.
func (s *Submitter) Address() client.Address { return s.address }

// Submit encodes applyStateDelta(delta, endorsements), signs it and broadcasts it, returning the raw
// transaction and its hash. The whole nonce-relevant sequence runs under the nonce manager's lock, so
// a failure here (the delta reverts on estimation, the node rejects the broadcast) can never race a
// different transaction's allocation: the nonce this attempt held is simply not consumed, and the next
// caller, whether that is a retry of this same transaction or somebody else's, gets it instead.
func (s *Submitter) Submit(
	ctx context.Context,
	delta *statedelta.StateDelta,
	endorsements [][]byte,
) (rawTx []byte, txHash client.Hash, err error) {
	if delta == nil {
		return nil, client.Hash{}, errors.New("submitter: nil state delta")
	}
	if len(endorsements) == 0 {
		return nil, client.Hash{}, errors.New("submitter: no endorsements to submit")
	}

	data := abi.EncodeApplyStateDelta(delta, endorsements)

	err = s.nonces.WithNonce(ctx, func(nonce uint64) error {
		gasLimit, gasErr := s.gasLimit(ctx, data)
		if gasErr != nil {
			return gasErr
		}
		fees, feeErr := s.client.SuggestGasFees(ctx)
		if feeErr != nil {
			return errors.Wrapf(ErrNetworkUnavailable, "submitter: failed to get gas fees: %v", feeErr)
		}

		tx := &client.DynamicFeeTx{
			ChainID:              s.chainID,
			Nonce:                nonce,
			MaxPriorityFeePerGas: fees.MaxPriorityFeePerGas,
			MaxFeePerGas:         fees.MaxFeePerGas,
			Gas:                  gasLimit,
			To:                   &s.tokenState,
			Value:                big.NewInt(0), // applyStateDelta is not payable
			Data:                 data,
		}

		signed, signErr := client.SignTx(tx, s.key)
		if signErr != nil {
			return errors.Wrap(signErr, "submitter: failed to sign the transaction")
		}

		hash, sendErr := s.client.SendRawTransaction(ctx, signed)
		if sendErr != nil {
			// A transaction the node refused to accept was never judged by the chain, so this is the
			// transient class: the delta is still valid and the caller should retry rather than
			// re-derive it. A node that executed it and rejected it comes back through gasLimit instead.
			//
			// It also carries ErrNonceMayBeConsumed. This is the one step that can fail after the node
			// has the transaction, so "the broadcast failed" is not proof the chain never took it: a
			// timeout or a dropped connection looks identical to a node that accepted and mined it and
			// then lost the reply. The nonce manager re-reads the chain rather than reissuing a nonce
			// that may already be spent.
			return errors.Wrapf(
				errors.Join(ErrNetworkUnavailable, ErrNonceMayBeConsumed),
				"submitter: failed to broadcast the transaction: %v", sendErr)
		}

		rawTx, txHash = signed, hash

		return nil
	})
	if err != nil {
		return nil, client.Hash{}, err
	}

	return rawTx, txHash, nil
}

// gasLimit resolves the gas limit for a call, either from configuration or by asking the node and
// applying the configured safety margin (state can change between estimation and execution, so the
// bare estimate is not enough).
func (s *Submitter) gasLimit(ctx context.Context, data []byte) (uint64, error) {
	if s.gas.Strategy == GasStrategyFixed {
		if s.gas.Limit == 0 {
			return 0, errors.New("submitter: fixed gas strategy with no limit configured")
		}

		return s.gas.Limit, nil
	}

	estimate, err := s.client.EstimateGas(ctx, client.CallMsg{
		From: &s.address,
		To:   &s.tokenState,
		Data: data,
	})
	if err != nil {
		// The node executed this transaction to estimate it and the transaction reverted. That is the
		// chain rejecting the delta - a double spend, a stale set of public parameters, a quorum it
		// will not accept - and not a problem with the estimate. Sending it anyway would mine it with
		// status 0 and reach the same verdict, having paid for the privilege, so it is reported here
		// as the permanent rejection it is rather than as a failure to estimate gas.
		if errors.Is(err, client.ErrExecutionReverted) {
			return 0, errors.Wrapf(ErrTransactionReverted, "submitter: %v", err)
		}

		// Everything else the estimate can fail with says nothing about the transaction: the node was
		// unreachable, timed out, or answered something we could not read. Transient, so retry.
		return 0, errors.Wrapf(ErrNetworkUnavailable, "submitter: failed to estimate gas: %v", err)
	}

	multiplier := s.gas.Multiplier
	if multiplier < 1 {
		multiplier = DefaultGasMultiplier
	}
	scaled := math.Ceil(float64(estimate) * multiplier)
	if scaled > math.MaxInt64 {
		return 0, errors.Errorf("submitter: gas estimate %v overflows", scaled)
	}

	return uint64(scaled), nil
}
