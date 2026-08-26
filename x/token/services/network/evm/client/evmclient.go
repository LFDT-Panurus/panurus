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

// Block tags for state-reading calls. They live here, in the package every other one depends on, so
// there is a single source of truth: the driver, the endorsers' validation ledger and the version
// keeper must all read at the same tag, or they would validate against different views of the chain.
const (
	// BlockTagFinalized is the PoS finalized tag, the default everywhere: reading at it removes reorg
	// handling from v1 (design §7.2).
	BlockTagFinalized = "finalized"
	// BlockTagSafe is the weaker "safe head" tag, available for deployments that accept reorg risk in
	// exchange for lower latency.
	BlockTagSafe = "safe"
	// BlockTagLatest is the chain head; it offers no reorg protection and is intended for local
	// development against an instant-mining node.
	BlockTagLatest = "latest"
)

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
	// Removed is true when the node is reporting that a reorg has undone the block this log was
	// mined in. A caller treating a log as evidence of a committed, permanent effect must not trust
	// one with Removed set.
	Removed bool
}

// LogFilter selects logs by contract address, block range and indexed topics.
// Topics follows the eth_getLogs convention: position i lists the acceptable values for topic i;
// an empty inner slice matches any value at that position.
//
// The upper end of the range is ToBlockTag when set, otherwise ToBlock, with ToBlock == 0 meaning
// "latest": a range ending at genesis is never a useful query, and searching up to the head is what a
// caller looking for an event actually wants, so the zero value is spent on the common case rather
// than requiring a separate round trip to read the current block number. ToBlockTag lets a caller that
// cares about reorg safety search only up to its configured tag (e.g. "finalized") instead.
type LogFilter struct {
	Address   Address
	FromBlock uint64
	ToBlock   uint64
	// ToBlockTag, when non-empty, is sent as the upper bound instead of ToBlock (e.g. BlockTagFinalized
	// or BlockTagSafe).
	ToBlockTag string
	Topics     [][]Hash
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
	// CodeAt returns the contract code deployed at address, empty if there is none. A call against an
	// address with no code succeeds and returns nothing rather than failing, so this is the only way
	// to tell a contract apart from an address that merely looks like one.
	CodeAt(ctx context.Context, address Address, blockTag string) ([]byte, error)
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
