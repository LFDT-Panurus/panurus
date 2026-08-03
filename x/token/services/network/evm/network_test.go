/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"testing"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/endorsement"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

const (
	testTokenState = "0x5FbDB2315678afecb367f032d93F642f64180aa3" // #nosec G101 -- contract address, not a credential
	testChainID    = 31337
)

// validConfig returns a minimal configuration that passes validation, for tests that need a Network
// rather than a specific configuration document.
func validConfig() *Config {
	return &Config{
		Endpoint:  "http://localhost:8545",
		ChainID:   testChainID,
		Contracts: ContractsConfig{TokenState: testTokenState},
		Endorsement: EndorsementConfig{
			Threshold: 1,
			Endorsers: []EndorserBinding{
				{Address: "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf", FSCIdentity: "endorser-1"},
			},
		},
	}
}

// testNetwork builds a Network over the given client and endorsement service; nil values are replaced
// with empty stubs so callers only supply what their test exercises.
func testNetwork(t *testing.T, evmClient client.EVMClient, endorser EndorsementService) *Network {
	t.Helper()
	if evmClient == nil {
		evmClient = &mock.EVMClient{}
	}
	c := validConfig()
	c.applyDefaults()
	require.NoError(t, c.Validate())

	n, err := NewNetwork("evm-net", c, evmClient, endorser, nil)
	require.NoError(t, err)

	return n
}

// anchorHex returns a valid 32-byte anchor in the hex form a token.ID carries.
func anchorHex(low byte) string {
	var a [32]byte
	a[31] = low

	return hex.EncodeToString(a[:])
}

// abiBytes encodes b as an ABI dynamic bytes return value, the shape a getToken call returns.
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

// abiBoolArray encodes flags as an ABI bool[] return value.
func abiBoolArray(flags []bool) []byte {
	out := make([]byte, 64)
	out[31] = 0x20
	binary.BigEndian.PutUint64(out[56:64], uint64(len(flags)))
	for _, f := range flags {
		word := make([]byte, 32)
		if f {
			word[31] = 1
		}
		out = append(out, word...)
	}

	return out
}

// stubEndorser returns a fixed endorsement result, or an error.
type stubEndorser struct {
	result *endorsement.Result
	err    error
	seen   *endorsement.EndorseRequest
}

func (s *stubEndorser) Endorse(_ view.Context, req *endorsement.EndorseRequest) (*endorsement.Result, error) {
	s.seen = req
	if s.err != nil {
		return nil, s.err
	}

	return s.result, nil
}

// --- Normalize / Connect -------------------------------------------------------------------------

func TestNormalize(t *testing.T) {
	n := testNetwork(t, nil, nil)

	t.Run("fills the network and clears the channel", func(t *testing.T) {
		opt, err := n.Normalize(&token2.ServiceOptions{})
		require.NoError(t, err)
		assert.Equal(t, "evm-net", opt.Network)
		assert.Empty(t, opt.Channel)
	})

	t.Run("rejects a channel, since evm has none", func(t *testing.T) {
		_, err := n.Normalize(&token2.ServiceOptions{Channel: "ch0"})
		require.Error(t, err)
	})

	t.Run("rejects nil options", func(t *testing.T) {
		_, err := n.Normalize(nil)
		require.Error(t, err)
	})
}

// TestConnectVerifiesChain checks the startup guard: a node on a different chain than configured must
// fail here rather than at the first transaction, where it would surface as a rejected signature.
func TestConnectVerifiesChain(t *testing.T) {
	t.Run("matching chain id connects", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.ChainIDReturns(bigInt(testChainID), nil)
		n := testNetwork(t, evm, nil)

		opts, err := n.Connect("token")
		require.NoError(t, err)
		assert.Len(t, opts, 3)
	})

	t.Run("mismatched chain id is refused", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.ChainIDReturns(bigInt(1), nil)
		n := testNetwork(t, evm, nil)

		_, err := n.Connect("token")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chain id")
	})

	t.Run("unreachable node is refused", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.ChainIDReturns(nil, errors.New("dial tcp: connection refused"))
		n := testNetwork(t, evm, nil)

		_, err := n.Connect("token")
		require.Error(t, err)
	})
}

// --- RequestApproval -----------------------------------------------------------------------------

// TestRequestApprovalDoesNotTouchTheChain checks the separation the design relies on: collecting
// endorsements is free, and only Broadcast spends gas. A request that cannot gather a quorum must
// never have sent anything.
func TestRequestApprovalDoesNotTouchTheChain(t *testing.T) {
	evm := &mock.EVMClient{}
	stub := &stubEndorser{err: errors.New("no quorum")}
	n := testNetwork(t, evm, stub)

	_, err := n.RequestApproval(nil, &token2.ManagementService{}, []byte("request"), nil, driverTxID(), nil)
	require.Error(t, err)
	assert.Zero(t, evm.SendRawTransactionCallCount(), "a failed approval must not broadcast")
}

func TestRequestApprovalWithoutEndorsementService(t *testing.T) {
	n := testNetwork(t, nil, nil)
	_, err := n.RequestApproval(nil, &token2.ManagementService{}, []byte("request"), nil, driverTxID(), nil)
	require.Error(t, err)
}

// --- Broadcast -----------------------------------------------------------------------------------

func TestBroadcastRejectsBadInput(t *testing.T) {
	n := testNetwork(t, nil, nil)

	t.Run("wrong blob type", func(t *testing.T) {
		require.Error(t, n.Broadcast(t.Context(), "not an envelope"))
	})

	t.Run("no submitter configured", func(t *testing.T) {
		err := n.Broadcast(t.Context(), &Envelope{Anchor: "a", Delta: &statedelta.StateDelta{}})
		require.Error(t, err)
	})

	t.Run("envelope without a delta", func(t *testing.T) {
		err := n.Broadcast(t.Context(), &Envelope{Anchor: "a"})
		require.Error(t, err)
	})
}

// --- queries -------------------------------------------------------------------------------------

func TestQueryTokens(t *testing.T) {
	t.Run("returns the stored bytes", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.CallReturns(abiBytes([]byte("token-bytes")), nil)
		n := testNetwork(t, evm, nil)

		out, err := n.QueryTokens(t.Context(), "token", []*token.ID{{TxId: anchorHex(0x01), Index: 0}})
		require.NoError(t, err)
		require.Len(t, out, 1)
		assert.Equal(t, []byte("token-bytes"), out[0])
	})

	t.Run("a missing token is an error, not an empty entry", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.CallReturns(abiBytes(nil), nil)
		n := testNetwork(t, evm, nil)

		_, err := n.QueryTokens(t.Context(), "token", []*token.ID{{TxId: anchorHex(0x01), Index: 0}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not exist")
	})

	t.Run("the call targets the token's addressable id", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.CallReturns(abiBytes([]byte("x")), nil)
		n := testNetwork(t, evm, nil)

		txID := anchorHex(0xB1)
		_, err := n.QueryTokens(t.Context(), "token", []*token.ID{{TxId: txID, Index: 3}})
		require.NoError(t, err)

		_, to, data, tag := evm.CallArgsForCall(0)
		assert.Equal(t, n.tokenState, to)
		assert.Equal(t, client.BlockTagFinalized, tag)

		anchor, err := keys.AnchorFromTxID(txID)
		require.NoError(t, err)
		expected := keys.ComputeTokenID(anchor, 3)
		assert.Equal(t, expected[:], data[4:], "the call must address the computed token id")
	})
}

func TestAreTokensSpent(t *testing.T) {
	t.Run("returns aligned flags", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.CallReturns(abiBoolArray([]bool{true, false}), nil)
		n := testNetwork(t, evm, nil)

		out, err := n.AreTokensSpent(t.Context(), "token", []*token.ID{
			{TxId: anchorHex(0x01), Index: 0},
			{TxId: anchorHex(0x02), Index: 1},
		}, nil)
		require.NoError(t, err)
		assert.Equal(t, []bool{true, false}, out)
	})

	t.Run("an empty request needs no call", func(t *testing.T) {
		evm := &mock.EVMClient{}
		n := testNetwork(t, evm, nil)

		out, err := n.AreTokensSpent(t.Context(), "token", nil, nil)
		require.NoError(t, err)
		assert.Empty(t, out)
		assert.Zero(t, evm.CallCallCount())
	})

	t.Run("a length mismatch from the contract is an error", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.CallReturns(abiBoolArray([]bool{true}), nil)
		n := testNetwork(t, evm, nil)

		_, err := n.AreTokensSpent(t.Context(), "token", []*token.ID{
			{TxId: anchorHex(0x01), Index: 0},
			{TxId: anchorHex(0x02), Index: 1},
		}, nil)
		require.Error(t, err)
	})
}

func TestFetchPublicParameters(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.CallReturns(abiBytes([]byte("public-params")), nil)
	n := testNetwork(t, evm, nil)

	out, err := n.FetchPublicParameters("token")
	require.NoError(t, err)
	assert.Equal(t, []byte("public-params"), out)
}

// TestLookupTransferMetadataKeyTimesOut checks the polling contract: the value is written when the
// carrying transaction is applied, so a caller may ask before it exists and must get a timeout rather
// than a false empty answer.
func TestLookupTransferMetadataKeyTimesOut(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.CallReturns(abiBytes(nil), nil) // never appears
	n := testNetwork(t, evm, nil)
	n.config.Finality.PollInterval = 10 * time.Millisecond

	_, err := n.LookupTransferMetadataKey("token", "key", 50*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestLookupTransferMetadataKeyFound(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.CallReturns(abiBytes([]byte("value")), nil)
	n := testNetwork(t, evm, nil)

	out, err := n.LookupTransferMetadataKey("token", "key", time.Second)
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), out)
}

// bigInt is a small helper for chain ids in tests.
func bigInt(v int64) *big.Int { return big.NewInt(v) }

// driverTxID returns a TxID with a creator and an empty nonce, the shape callers pass in.
func driverTxID() driver.TxID {
	return driver.TxID{Creator: []byte("alice")}
}
