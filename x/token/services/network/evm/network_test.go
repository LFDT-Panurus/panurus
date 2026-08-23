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

	ncommon "github.com/LFDT-Panurus/panurus/token/services/network/common"
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

	n, err := NewNetwork("evm-net", c, evmClient, endorser, nil, nil)
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

	t.Run("rejects another network", func(t *testing.T) {
		_, err := n.Normalize(&token2.ServiceOptions{Network: "some-other-network"})
		require.Error(t, err)
	})

	// Without a fetcher the token layer cannot find the public parameters on a fresh node, and the
	// TMS fails to build with an error that points nowhere near here.
	t.Run("installs the public parameters fetcher", func(t *testing.T) {
		opt, err := n.Normalize(&token2.ServiceOptions{Namespace: "token"})
		require.NoError(t, err)
		require.NotNil(t, opt.PublicParamsFetcher, "the token layer reads public parameters through this")
	})

	t.Run("keeps a fetcher the caller supplied", func(t *testing.T) {
		supplied := ncommon.NewPublicParamsFetcher(n, "token")
		opt, err := n.Normalize(&token2.ServiceOptions{Namespace: "token", PublicParamsFetcher: supplied})
		require.NoError(t, err)
		assert.Same(t, supplied, opt.PublicParamsFetcher)
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

// TestConnectStartsRecovery pins where transaction recovery is started, and why it has to be started
// at all.
//
// A node is told that a transaction it holds became final by a finality listener the ttx layer
// registers in memory when it stores that transaction. A restart destroys that registration, and
// nothing else ever revisits the row: it stays Pending, and every later wait on it runs to its
// timeout. Recovery is the sweep that re-asks the chain, and it is per TMS, so the namespace that
// Connect carries is the earliest point it can start.
func TestConnectStartsRecovery(t *testing.T) {
	t.Run("connect starts recovery for the namespace", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.ChainIDReturns(bigInt(testChainID), nil)
		n := testNetwork(t, evm, nil)

		var started []string
		n.SetRecoveryStarter(func(ns string) error {
			started = append(started, ns)

			return nil
		})

		_, err := n.Connect("token")
		require.NoError(t, err)
		assert.Equal(t, []string{"token"}, started, "recovery must be started for the connected namespace")
	})

	t.Run("a network without a starter still connects", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.ChainIDReturns(bigInt(testChainID), nil)
		n := testNetwork(t, evm, nil)

		_, err := n.Connect("token")
		require.NoError(t, err)
	})

	// Recovery completes transactions left over from a previous run. A node that cannot start it is
	// worse off, but it is not broken: refusing the connection would take out a working node over a
	// facility it only needs for transactions it may not even have.
	t.Run("a failure to start recovery does not refuse the connection", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.ChainIDReturns(bigInt(testChainID), nil)
		n := testNetwork(t, evm, nil)
		n.SetRecoveryStarter(func(string) error { return errors.New("no store for this tms") })

		opts, err := n.Connect("token")
		require.NoError(t, err)
		assert.Len(t, opts, 3)
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

// --- SetupPublicParams -----------------------------------------------------------------------------

// TestSetupPublicParamsWithoutEndorsementService checks the same failure mode RequestApproval has:
// nothing configured means a clear error, not a nil dereference further down.
func TestSetupPublicParamsWithoutEndorsementService(t *testing.T) {
	n := testNetwork(t, nil, nil)
	_, err := n.SetupPublicParams(nil, token2.TMSID{Network: "evm", Namespace: "token"}, []byte("pp"), nil, driverTxID())
	require.Error(t, err)
}

// TestSetupPublicParamsResolvesThroughTheIDBasedFactory is the fix for a real bug: SetupPublicParams
// checked only the endorsement field tests inject directly, never the per-TMS factory the driver
// actually installs in production (SetEndorsementFactory, keyed by a management service). Since
// SetupPublicParams only ever has a bare TMSID, not a management service, it could never reach that
// factory, and the call failed with "no endorsement service configured" on every real deployment
// regardless of how correctly everything else was wired: first-time setup of a namespace, the one
// thing this method exists to make possible, could not work at all.
func TestSetupPublicParamsResolvesThroughTheIDBasedFactory(t *testing.T) {
	n := testNetwork(t, nil, nil)
	tmsID := token2.TMSID{Network: "evm", Namespace: "token"}
	stub := &stubEndorser{result: &endorsement.Result{Anchor: "anchor", Endorsements: [][]byte{{0x01}}}}

	var seenID token2.TMSID
	n.SetEndorsementFactoryByID(func(id token2.TMSID) (EndorsementService, error) {
		seenID = id

		return stub, nil
	})

	env, err := n.SetupPublicParams(nil, tmsID, []byte("pp"), nil, driverTxID())
	require.NoError(t, err)
	assert.NotNil(t, env)
	assert.Equal(t, tmsID, seenID, "the factory must be asked to resolve exactly the TMS being set up")
	require.NotNil(t, stub.seen)
	assert.Equal(t, tmsID, stub.seen.TMSID)
}

// TestSetupPublicParamsDoesNotTouchTheChain mirrors TestRequestApprovalDoesNotTouchTheChain: a failed
// endorsement collection must not broadcast anything.
func TestSetupPublicParamsDoesNotTouchTheChain(t *testing.T) {
	evm := &mock.EVMClient{}
	n := testNetwork(t, evm, nil)
	n.SetEndorsementFactoryByID(func(token2.TMSID) (EndorsementService, error) {
		return &stubEndorser{err: errors.New("no quorum")}, nil
	})

	_, err := n.SetupPublicParams(nil, token2.TMSID{Network: "evm", Namespace: "token"}, []byte("pp"), nil, driverTxID())
	require.Error(t, err)
	assert.Zero(t, evm.SendRawTransactionCallCount(), "a failed setup must not broadcast")
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

// networkWithSubmitter returns a Network wired with a real submitter over a ready mock client, for
// Broadcast tests that need to get past the submitter/delta checks to exercise what follows them.
func networkWithSubmitter(t *testing.T, evm *mock.EVMClient) *Network {
	t.Helper()
	c := validConfig()
	c.applyDefaults()
	require.NoError(t, c.Validate())
	n, err := NewNetwork("evm-net", c, evm, nil, testSubmitter(t, evm, estimateGas()), nil)
	require.NoError(t, err)

	return n
}

// TestBroadcastRejectsMismatchedAnchor is the fix for a real gap: nothing checked that an envelope's
// anchor and its delta's own anchor actually agreed before spending gas on it. Under the normal flow
// they always do, since RequestApproval derives both from the same anchor, but Broadcast has no way
// to know how the envelope it was actually handed was built, and the local finality tracking is keyed
// on the envelope's anchor while the chain applies and emits StateCommitted for the delta's: a
// mismatch here would mean waiting on an anchor the chain never touches.
func TestBroadcastRejectsMismatchedAnchor(t *testing.T) {
	evm := readySubmitterClient()
	n := networkWithSubmitter(t, evm)

	elsewhere, err := keys.AnchorFromTxID(anchorHex(0xEE))
	require.NoError(t, err)
	delta := testDelta()
	delta.Anchor = elsewhere

	err = n.Broadcast(t.Context(), &Envelope{
		Anchor:       anchorHex(0x01),
		Delta:        delta,
		Endorsements: [][]byte{make([]byte, 65)},
	})
	require.Error(t, err)
	assert.Zero(t, evm.SendRawTransactionCallCount(), "a mismatched envelope must not be broadcast")
}

// TestBroadcastRejectsAnUnparsableAnchor checks the envelope's own anchor is validated before it is
// compared against anything, rather than producing a confusing failure further down.
func TestBroadcastRejectsAnUnparsableAnchor(t *testing.T) {
	evm := readySubmitterClient()
	n := networkWithSubmitter(t, evm)

	err := n.Broadcast(t.Context(), &Envelope{
		Anchor:       "not-hex",
		Delta:        testDelta(),
		Endorsements: [][]byte{make([]byte, 65)},
	})
	require.Error(t, err)
	assert.Zero(t, evm.SendRawTransactionCallCount())
}

// TestBroadcastAcceptsAConsistentEnvelope is the positive case: an envelope whose anchor and delta
// agree, as every normal caller produces, must still broadcast.
func TestBroadcastAcceptsAConsistentEnvelope(t *testing.T) {
	evm := readySubmitterClient()
	n := networkWithSubmitter(t, evm)

	delta := testDelta()
	err := n.Broadcast(t.Context(), &Envelope{
		Anchor:       hex.EncodeToString(delta.Anchor[:]),
		Delta:        delta,
		Endorsements: [][]byte{make([]byte, 65)},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, evm.SendRawTransactionCallCount())
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

// TestBroadcastKeepsARevertWhenTheAnchorIsNotApplied is the other half of the already-applied case:
// a genuine rejection must still be reported. The chain here has no record of the anchor, so the
// refusal is about a transaction that never landed and the caller has to hear about it.
func TestBroadcastKeepsARevertWhenTheAnchorIsNotApplied(t *testing.T) {
	evm := readySubmitterClient()
	evm.EstimateGasReturns(0, errors.Wrap(client.ErrExecutionReverted, "eth_estimateGas failed"))
	// getTokenRequestHash returns bytes32(0), which is how the contract says it has no such anchor.
	evm.CallReturns(make([]byte, 32), nil)
	n := networkWithSubmitter(t, evm)

	delta := testDelta()
	err := n.Broadcast(t.Context(), &Envelope{
		Anchor:       hex.EncodeToString(delta.Anchor[:]),
		Delta:        delta,
		Endorsements: [][]byte{make([]byte, 65)},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTransactionReverted,
		"a refusal of an anchor the chain has never recorded is exactly the permanent failure it looks like")
}

// TestBroadcastKeepsARevertWhenTheStatusCannotBeRead pins the fail-closed direction. A node that
// cannot be asked whether the anchor landed is no reason to convert a refusal into a success.
func TestBroadcastKeepsARevertWhenTheStatusCannotBeRead(t *testing.T) {
	evm := readySubmitterClient()
	evm.EstimateGasReturns(0, errors.Wrap(client.ErrExecutionReverted, "eth_estimateGas failed"))
	evm.CallReturns(nil, errors.New("node unreachable"))
	n := networkWithSubmitter(t, evm)

	delta := testDelta()
	err := n.Broadcast(t.Context(), &Envelope{
		Anchor:       hex.EncodeToString(delta.Anchor[:]),
		Delta:        delta,
		Endorsements: [][]byte{make([]byte, 65)},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTransactionReverted, "an unreadable status keeps the original rejection")
}
