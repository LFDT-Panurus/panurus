/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/abi"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
)

// anchorHex returns the hex of a 32-byte anchor with the given low byte, the form a token.ID's TxId
// takes for an EVM-backed token.
func anchorHex(low byte) string {
	var a [32]byte
	a[31] = low

	return hex.EncodeToString(a[:])
}

// abiBytes encodes b as an ABI dynamic `bytes` return value, the shape getToken produces on-chain.
func abiBytes(b []byte) []byte {
	out := make([]byte, 64)
	out[31] = 0x20
	binary.BigEndian.PutUint64(out[56:64], uint64(len(b)))
	out = append(out, b...)
	if pad := (32 - len(b)%32) % 32; pad != 0 {
		out = append(out, make([]byte, pad)...)
	}

	return out
}

func TestLedgerGetStateResolvesAndDecodes(t *testing.T) {
	want := []byte("on-chain-token-bytes spanning two words")
	evm := &mock.EVMClient{}
	evm.CallReturns(abiBytes(want), nil)

	tokenState := addr(0xAA)
	l := NewLedger(context.Background(), evm, tokenState, "")

	txID := anchorHex(0xB1)
	got, err := l.GetState(token.ID{TxId: txID, Index: 3})
	require.NoError(t, err)
	assert.Equal(t, want, got)

	// the call must target the TokenState at the finalized tag, with data = getToken(computed id).
	require.Equal(t, 1, evm.CallCallCount())
	_, to, data, tag := evm.CallArgsForCall(0)
	assert.Equal(t, tokenState, to)
	assert.Equal(t, DefaultBlockTag, tag)

	anchor, err := keys.AnchorFromTxID(txID)
	require.NoError(t, err)
	wantData := abi.EncodeBytes32Call("getToken(bytes32)", keys.ComputeTokenID(anchor, 3))
	assert.Equal(t, wantData, data)
}

func TestLedgerGetStateEmptyForAbsentToken(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.CallReturns(abiBytes(nil), nil) // getToken returns empty bytes for an unknown id

	l := NewLedger(context.Background(), evm, addr(0xAA), "finalized")
	got, err := l.GetState(token.ID{TxId: anchorHex(0x01), Index: 0})
	require.NoError(t, err)
	assert.Empty(t, got, "an absent token reads as empty, which the validator treats as not found")
}

func TestLedgerGetStateErrors(t *testing.T) {
	t.Run("invalid anchor", func(t *testing.T) {
		l := NewLedger(context.Background(), &mock.EVMClient{}, addr(0xAA), "")
		_, err := l.GetState(token.ID{TxId: "not-hex", Index: 0})
		require.Error(t, err)
	})

	t.Run("call failure is wrapped", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.CallReturns(nil, assert.AnError)
		l := NewLedger(context.Background(), evm, addr(0xAA), "")
		_, err := l.GetState(token.ID{TxId: anchorHex(0x01), Index: 0})
		require.Error(t, err)
	})

	t.Run("undecodable result is wrapped", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.CallReturns([]byte{0x01, 0x02}, nil) // too short to be an ABI bytes return
		l := NewLedger(context.Background(), evm, addr(0xAA), "")
		_, err := l.GetState(token.ID{TxId: anchorHex(0x01), Index: 0})
		require.Error(t, err)
	})
}

// compile-time check that Ledger satisfies the SDK ledger contract.
var _ interface {
	GetState(id token.ID) ([]byte, error)
} = (*Ledger)(nil)
