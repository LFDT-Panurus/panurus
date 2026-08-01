/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package client

import (
	"math/big"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
)

// TxTypeDynamicFee is the EIP-2718 type byte of an EIP-1559 (dynamic-fee) transaction. The driver
// only ever sends type-2 transactions: they are the current standard and are supported by Besu and
// every modern node (design §8).
const TxTypeDynamicFee byte = 0x02

// signatureLength is the length of the compact secp256k1 signature decred returns: {v, r, s}.
const signatureLength = 65

// DynamicFeeTx is an unsigned EIP-1559 transaction. The driver builds one per `applyStateDelta`
// call: To is the TokenState clone, Data the ABI-encoded call, Value always zero (the contract is
// not payable).
//
// Fees follow EIP-1559: MaxFeePerGas caps the total the sender pays per gas, MaxPriorityFeePerGas
// the tip to the proposer. Both come from the node's suggestion, adjusted by the configured gas
// strategy.
type DynamicFeeTx struct {
	ChainID              *big.Int
	Nonce                uint64
	MaxPriorityFeePerGas *big.Int
	MaxFeePerGas         *big.Int
	Gas                  uint64
	To                   *Address // nil means contract creation; the driver always sets it
	Value                *big.Int
	Data                 []byte
	// AccessList is always empty for the driver's calls. It is part of the signed payload, so it is
	// encoded (as an empty list) rather than omitted.
}

// SigningHash returns the hash the sender signs: keccak256(0x02 || rlp([chainId, nonce,
// maxPriorityFeePerGas, maxFeePerGas, gas, to, value, data, accessList])) per EIP-1559. The field
// order is normative; changing it produces a valid-looking but unusable transaction.
func (t *DynamicFeeTx) SigningHash() [32]byte {
	return crypto.Keccak256Hash(append([]byte{TxTypeDynamicFee}, rlpList(t.payload())...))
}

// payload encodes the nine signed fields as the concatenation of their RLP encodings.
func (t *DynamicFeeTx) payload() []byte {
	var buf []byte
	buf = append(buf, rlpBigInt(t.ChainID)...)
	buf = append(buf, rlpUint(t.Nonce)...)
	buf = append(buf, rlpBigInt(t.MaxPriorityFeePerGas)...)
	buf = append(buf, rlpBigInt(t.MaxFeePerGas)...)
	buf = append(buf, rlpUint(t.Gas)...)
	buf = append(buf, rlpString(t.toBytes())...)
	buf = append(buf, rlpBigInt(t.Value)...)
	buf = append(buf, rlpString(t.Data)...)
	// empty access list
	buf = append(buf, rlpList(nil)...)

	return buf
}

// toBytes returns the recipient address bytes, or an empty slice for contract creation.
func (t *DynamicFeeTx) toBytes() []byte {
	if t.To == nil {
		return nil
	}

	return t.To.Bytes()
}

// SignTx signs the transaction with the submitter's secp256k1 key and returns the raw, broadcastable
// transaction: 0x02 || rlp([...signed fields, yParity, r, s]).
//
// Signature format differs from the endorsement signatures (design §8): a typed transaction carries
// yParity as 0 or 1, NOT the legacy v of 27/28, and r and s are encoded as minimal big-endian RLP
// integers rather than fixed 32-byte words. s is normalized to the lower half of the group order
// (EIP-2); decred already produces canonical signatures, and this asserts it rather than assuming.
func SignTx(tx *DynamicFeeTx, key *secp256k1.PrivateKey) ([]byte, error) {
	if tx == nil {
		return nil, errors.New("nil transaction")
	}
	if key == nil {
		return nil, errors.New("nil signing key")
	}
	if tx.ChainID == nil || tx.ChainID.Sign() <= 0 {
		return nil, errors.New("transaction chain id must be set and positive")
	}

	hash := tx.SigningHash()
	// SignCompact returns {v, r, s} with v = 27 + recovery id, the same layout the endorsement
	// signer reorders; here the recovery id is carried as yParity instead.
	compact := ecdsa.SignCompact(key, hash[:], false)
	if len(compact) != signatureLength {
		return nil, errors.Errorf("unexpected compact signature length %d", len(compact))
	}

	yParity := compact[0] - 27
	if yParity > 1 {
		return nil, errors.Errorf("unexpected recovery id %d", yParity)
	}
	r := new(big.Int).SetBytes(compact[1:33])
	s := new(big.Int).SetBytes(compact[33:65])
	if s.Cmp(secp256k1HalfOrder) > 0 {
		return nil, errors.New("signing produced a non-canonical (high-s) signature")
	}

	var buf []byte
	buf = append(buf, tx.payload()...)
	buf = append(buf, rlpUint(uint64(yParity))...)
	buf = append(buf, rlpBigInt(r)...)
	buf = append(buf, rlpBigInt(s)...)

	return append([]byte{TxTypeDynamicFee}, rlpList(buf)...), nil
}

// TxHash returns the hash of a raw, signed transaction: keccak256 over the full encoded bytes
// (including the type byte). This is the hash the node reports and the driver tracks for finality.
func TxHash(rawTx []byte) Hash {
	return crypto.Keccak256Hash(rawTx)
}

// secp256k1HalfOrder is N/2, the upper bound for a canonical (non-malleable) s value (EIP-2).
var secp256k1HalfOrder = new(big.Int).SetBytes([]byte{
	0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0x5d, 0x57, 0x6e, 0x73, 0x57, 0xa4, 0x50, 0x1d, 0xdf, 0xe9, 0x2f, 0x46, 0x68, 0x1b, 0x20, 0xa0,
})
