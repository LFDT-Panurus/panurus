/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package client

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testKey returns the well-known secp256k1 test key with the given low byte; key 1 is the
// address 0x7e5f4552091a69125d5dfcb7b8c2659029395bdf, reproducible with any Ethereum tooling.
func testKey(low byte) *secp256k1.PrivateKey {
	raw := make([]byte, 32)
	raw[31] = low

	return secp256k1.PrivKeyFromBytes(raw)
}

// goldenTx is the transaction pinned by TestSignTxGoldenVector. Keep it in sync with the committed
// expectations below; both were produced by this code and independently decoded with
// `cast decode-transaction`, which recovered the expected signer.
func goldenTx(t *testing.T) *DynamicFeeTx {
	t.Helper()
	to, err := HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	require.NoError(t, err)

	return &DynamicFeeTx{
		ChainID:              big.NewInt(31337),
		Nonce:                7,
		MaxPriorityFeePerGas: big.NewInt(1_000_000_000),
		MaxFeePerGas:         big.NewInt(20_000_000_000),
		Gas:                  100_000,
		To:                   &to,
		Value:                big.NewInt(0),
		Data:                 []byte{0xde, 0xad, 0xbe, 0xef},
	}
}

// TestSignTxGoldenVector is the Phase-A gate: a signed EIP-1559 transaction must match, byte for
// byte, a vector independently validated with foundry's `cast decode-transaction`, which recovered
// signer 0x7e5f4552091a69125d5dfcb7b8c2659029395bdf (the address of test key 1) and reported the same
// transaction hash. Since the recovered signer depends on every encoded byte, this pins the whole
// chain: RLP field order and minimality, the type prefix, the signing hash, and the yParity/r/s
// signature encoding.
func TestSignTxGoldenVector(t *testing.T) {
	const (
		wantSigningHash = "6569407866fe8e08fd874689c6d85c060753957cf6784ed1278d62756223c174"
		wantRawTx       = "02f872827a6907843b9aca008504a817c800830186a0945fbdb2315678afecb367f032d93f642f64180aa3" +
			"8084deadbeefc080a0c082262bf546fb58f63a42598eb821d9f47e05e56780fceb478e4526141356b9" +
			"a0096827805bd69e284b1ab5e4d5a9719a859439a570722c1f77ab9aa2f52b645a"
		wantTxHash = "0x853f272fffc6efc284fc16a254decca742d2347e05703e501c59968f78f81ffa"
	)

	tx := goldenTx(t)
	h := tx.SigningHash()
	assert.Equal(t, wantSigningHash, hex.EncodeToString(h[:]), "signing hash diverges from the golden vector")

	rawTx, err := SignTx(tx, testKey(1))
	require.NoError(t, err)
	assert.Equal(t, wantRawTx, hex.EncodeToString(rawTx), "raw transaction diverges from the golden vector")
	assert.Equal(t, wantTxHash, TxHash(rawTx).Hex())
}

// TestSignTxIsDeterministic pins RFC 6979 determinism: the same key and transaction always produce
// the same raw bytes, which is what makes the golden vector reproducible.
func TestSignTxIsDeterministic(t *testing.T) {
	first, err := SignTx(goldenTx(t), testKey(1))
	require.NoError(t, err)
	second, err := SignTx(goldenTx(t), testKey(1))
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

// TestSignTxTypePrefix checks the EIP-2718 envelope: a typed transaction starts with its type byte.
func TestSignTxTypePrefix(t *testing.T) {
	rawTx, err := SignTx(goldenTx(t), testKey(1))
	require.NoError(t, err)
	require.NotEmpty(t, rawTx)
	assert.Equal(t, TxTypeDynamicFee, rawTx[0])
}

// TestSignTxDistinctInputsDiverge checks that fields actually enter the signed payload: changing any
// one of them changes the signing hash (and so the raw transaction).
func TestSignTxDistinctInputsDiverge(t *testing.T) {
	base := goldenTx(t).SigningHash()

	mutations := map[string]func(*DynamicFeeTx){
		"nonce":    func(tx *DynamicFeeTx) { tx.Nonce++ },
		"chain id": func(tx *DynamicFeeTx) { tx.ChainID = big.NewInt(1) },
		"gas":      func(tx *DynamicFeeTx) { tx.Gas++ },
		"max fee":  func(tx *DynamicFeeTx) { tx.MaxFeePerGas = big.NewInt(1) },
		"tip":      func(tx *DynamicFeeTx) { tx.MaxPriorityFeePerGas = big.NewInt(1) },
		"value":    func(tx *DynamicFeeTx) { tx.Value = big.NewInt(1) },
		"data":     func(tx *DynamicFeeTx) { tx.Data = []byte{0x01} },
		"to":       func(tx *DynamicFeeTx) { addr := Address{}; tx.To = &addr },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			tx := goldenTx(t)
			mutate(tx)
			assert.NotEqual(t, base, tx.SigningHash(), "%s must be covered by the signing hash", name)
		})
	}
}

// TestSignTxDifferentKeysDiffer checks the signature depends on the key.
func TestSignTxDifferentKeysDiffer(t *testing.T) {
	one, err := SignTx(goldenTx(t), testKey(1))
	require.NoError(t, err)
	two, err := SignTx(goldenTx(t), testKey(2))
	require.NoError(t, err)
	assert.NotEqual(t, one, two)
}

func TestSignTxRejectsInvalidInput(t *testing.T) {
	t.Run("nil transaction", func(t *testing.T) {
		_, err := SignTx(nil, testKey(1))
		require.Error(t, err)
	})

	t.Run("nil key", func(t *testing.T) {
		_, err := SignTx(goldenTx(t), nil)
		require.Error(t, err)
	})

	t.Run("missing chain id", func(t *testing.T) {
		tx := goldenTx(t)
		tx.ChainID = nil
		_, err := SignTx(tx, testKey(1))
		require.Error(t, err, "an unset chain id would allow cross-chain replay")
	})

	t.Run("zero chain id", func(t *testing.T) {
		tx := goldenTx(t)
		tx.ChainID = big.NewInt(0)
		_, err := SignTx(tx, testKey(1))
		require.Error(t, err)
	})
}

// TestSignTxContractCreation checks a nil recipient encodes as the empty string (contract creation)
// rather than a zero address; the driver always sets To, but the encoding must still be correct.
func TestSignTxContractCreation(t *testing.T) {
	tx := goldenTx(t)
	tx.To = nil
	rawTx, err := SignTx(tx, testKey(1))
	require.NoError(t, err)
	require.NotEmpty(t, rawTx)

	zero := Address{}
	withZero := goldenTx(t)
	withZero.To = &zero
	assert.NotEqual(t, tx.SigningHash(), withZero.SigningHash(),
		"contract creation must not encode like a call to the zero address")
}
