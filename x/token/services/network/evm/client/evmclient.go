/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package client

import (
	"context"
	"math/big"
)

//go:generate counterfeiter -o mock/evmclient.go -fake-name EVMClient . EVMClient

// Receipt is the subset of an Ethereum transaction receipt the driver needs.
type Receipt struct {
	TxHash      Hash
	BlockNumber *uint64 // nil until the transaction is mined
	Status      uint64  // 1 = success, 0 = reverted
	Logs        []Log
}

// Log is an EVM event log entry.
type Log struct {
	Address     Address
	Topics      []Hash
	Data        []byte
	TxHash      Hash
	BlockNumber uint64
}

// LogFilter selects logs by contract address, block range and indexed topics.
// Topics follows the eth_getLogs convention: position i lists the acceptable values for topic i;
// an empty inner slice matches any value at that position.
type LogFilter struct {
	Address   Address
	FromBlock uint64
	ToBlock   uint64
	Topics    [][]Hash
}

// GasFees carries the EIP-1559 fee parameters suggested by the node.
type GasFees struct {
	MaxFeePerGas         *big.Int
	MaxPriorityFeePerGas *big.Int
}

// CallMsg describes a read-only call or a gas estimation request.
type CallMsg struct {
	From  *Address
	To    *Address
	Data  []byte
	Value *big.Int
}

// EVMClient abstracts the JSON-RPC surface the network driver depends on. It is intentionally
// minimal and backend-agnostic so the driver works against any EVM node, including fabric-x-evm.
// State-reading calls take an explicit block tag (for example "finalized") so callers control the
// consistency/finality of the data they read.
type EVMClient interface {
	// ChainID returns the chain ID reported by the node.
	ChainID(ctx context.Context) (*big.Int, error)
	// Ping checks that the node is reachable.
	Ping(ctx context.Context) error

	// Call performs a read-only contract call at the given block tag and returns the raw result.
	Call(ctx context.Context, to Address, data []byte, blockTag string) ([]byte, error)
	// GetLogs returns the logs matching the filter.
	GetLogs(ctx context.Context, q LogFilter) ([]Log, error)

	// PendingNonceAt returns the next nonce for account including pending transactions.
	PendingNonceAt(ctx context.Context, account Address) (uint64, error)
	// EstimateGas estimates the gas needed to execute msg.
	EstimateGas(ctx context.Context, msg CallMsg) (uint64, error)
	// SuggestGasFees returns the node's suggested EIP-1559 fees.
	SuggestGasFees(ctx context.Context) (GasFees, error)

	// SendRawTransaction submits a signed, RLP-encoded transaction and returns its hash.
	SendRawTransaction(ctx context.Context, rawTx []byte) (Hash, error)
	// GetTransactionReceipt returns the receipt for txHash, or (nil, nil) if not yet mined.
	GetTransactionReceipt(ctx context.Context, txHash Hash) (*Receipt, error)
	// IsPending reports whether txHash is still pending in the mempool. found is false when the node
	// does not know the transaction at all (dropped or never seen), which the finality resolver uses
	// to distinguish "pending" from "dropped".
	IsPending(ctx context.Context, txHash Hash) (pending bool, found bool, err error)
}
