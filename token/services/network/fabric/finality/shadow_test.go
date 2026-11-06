/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/finality"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger/fabric-protos-go-apiv2/common"
	"github.com/hyperledger/fabric-protos-go-apiv2/msp"
	"github.com/hyperledger/fabric-protos-go-apiv2/peer"
	"github.com/stretchr/testify/require"
)

// endorserEnvelope assembles a minimal ENDORSER_TRANSACTION envelope carrying the
// given txID. It is enough for SkipShadowedEnvelopes/ValidEnvelope, which only read
// the channel header's type and txID.
func endorserEnvelope(t *testing.T, txID string) []byte {
	t.Helper()

	sigHdr := &common.SignatureHeader{
		Creator: mustMarshal(t, &msp.SerializedIdentity{Mspid: "Org1MSP", IdBytes: []byte("cert")}),
		Nonce:   []byte("nonce"),
	}
	chdr := &common.ChannelHeader{
		Type:      int32(common.HeaderType_ENDORSER_TRANSACTION),
		ChannelId: "testchannel",
		TxId:      txID,
	}
	payload := &common.Payload{
		Header: &common.Header{
			ChannelHeader:   mustMarshal(t, chdr),
			SignatureHeader: mustMarshal(t, sigHdr),
		},
		Data: []byte("tx-data"),
	}
	envRaw, err := proto.Marshal(&common.Envelope{Payload: mustMarshal(t, payload)})
	require.NoError(t, err)

	return envRaw
}

// configEnvelope assembles a minimal CONFIG envelope. A non-endorser envelope must
// never shadow another and must never be dropped.
func configEnvelope(t *testing.T, txID string) []byte {
	t.Helper()

	chdr := &common.ChannelHeader{
		Type:      int32(common.HeaderType_CONFIG),
		ChannelId: "testchannel",
		TxId:      txID,
	}
	payload := &common.Payload{
		Header: &common.Header{ChannelHeader: mustMarshal(t, chdr)},
		Data:   []byte("config-data"),
	}
	envRaw, err := proto.Marshal(&common.Envelope{Payload: mustMarshal(t, payload)})
	require.NoError(t, err)

	return envRaw
}

// block builds a block from the given envelopes and their validation codes.
func block(t *testing.T, number uint64, envs [][]byte, codes []peer.TxValidationCode) *common.Block {
	t.Helper()
	require.Len(t, codes, len(envs))

	flags := make([]byte, len(codes))
	for i, c := range codes {
		flags[i] = byte(c) //nolint:gosec // G115: Fabric validation codes are small constants that fit in a byte, which is exactly how the TRANSACTIONS_FILTER stores them
	}
	metadata := make([][]byte, int(common.BlockMetadataIndex_TRANSACTIONS_FILTER)+1)
	metadata[common.BlockMetadataIndex_TRANSACTIONS_FILTER] = flags

	return &common.Block{
		Header:   &common.BlockHeader{Number: number},
		Data:     &common.BlockData{Data: envs},
		Metadata: &common.BlockMetadata{Metadata: metadata},
	}
}

// txIDsOf returns the txID of every endorser envelope left in the block, in order.
func txIDsOf(t *testing.T, b *common.Block) []string {
	t.Helper()
	var ids []string
	for _, raw := range b.Data.Data {
		env := &common.Envelope{}
		require.NoError(t, proto.Unmarshal(raw, env))
		payl := &common.Payload{}
		require.NoError(t, proto.Unmarshal(env.Payload, payl))
		chdr := &common.ChannelHeader{}
		require.NoError(t, proto.Unmarshal(payl.Header.ChannelHeader, chdr))
		ids = append(ids, chdr.TxId)
	}

	return ids
}

// TestSkipShadowedEnvelopes_ForgedThenHonest is the reproduction from the issue: a
// forged BAD_PROPOSAL_TXID envelope for tx1 ordered ahead of the honest VALID one in
// the same block. The forgery must be dropped, leaving only the honest envelope and
// its VALID flag.
func TestSkipShadowedEnvelopes_ForgedThenHonest(t *testing.T) {
	b := block(t, 7,
		[][]byte{endorserEnvelope(t, "tx1"), endorserEnvelope(t, "tx1")},
		[]peer.TxValidationCode{peer.TxValidationCode_BAD_PROPOSAL_TXID, peer.TxValidationCode_VALID},
	)

	got := finality.SkipShadowedEnvelopes(b)

	require.Equal(t, []string{"tx1"}, txIDsOf(t, got))
	flags := got.Metadata.Metadata[common.BlockMetadataIndex_TRANSACTIONS_FILTER]
	require.Equal(t, []byte{byte(peer.TxValidationCode_VALID)}, flags,
		"the surviving envelope keeps its VALID flag, aligned at index 0")
	// The input block is left unmodified.
	require.Len(t, b.Data.Data, 2, "input block must not be mutated")
}

// TestSkipShadowedEnvelopes_HonestThenForged covers the honest VALID envelope coming
// first: the later forgery for the same txID is still dropped.
func TestSkipShadowedEnvelopes_HonestThenForged(t *testing.T) {
	b := block(t, 7,
		[][]byte{endorserEnvelope(t, "tx1"), endorserEnvelope(t, "tx1")},
		[]peer.TxValidationCode{peer.TxValidationCode_VALID, peer.TxValidationCode_BAD_PROPOSAL_TXID},
	)

	got := finality.SkipShadowedEnvelopes(b)

	require.Equal(t, []string{"tx1"}, txIDsOf(t, got))
	require.Equal(t, []byte{byte(peer.TxValidationCode_VALID)},
		got.Metadata.Metadata[common.BlockMetadataIndex_TRANSACTIONS_FILTER])
}

// TestSkipShadowedEnvelopes_NoValidEnvelope: a block whose only envelope for a txID
// is invalid is left unchanged — there is nothing to prefer it to.
func TestSkipShadowedEnvelopes_NoValidEnvelope(t *testing.T) {
	b := block(t, 7,
		[][]byte{endorserEnvelope(t, "tx1")},
		[]peer.TxValidationCode{peer.TxValidationCode_BAD_PROPOSAL_TXID},
	)

	got := finality.SkipShadowedEnvelopes(b)

	require.Same(t, b, got, "a block with no shadowed envelope is returned unchanged")
	require.Equal(t, []string{"tx1"}, txIDsOf(t, got))
}

// TestSkipShadowedEnvelopes_DistinctTxIDsUntouched: distinct txIDs, even invalid
// ones, are never dropped — only an envelope shadowed by a VALID one with the same
// txID is.
func TestSkipShadowedEnvelopes_DistinctTxIDsUntouched(t *testing.T) {
	b := block(t, 7,
		[][]byte{endorserEnvelope(t, "txA"), endorserEnvelope(t, "txB")},
		[]peer.TxValidationCode{peer.TxValidationCode_VALID, peer.TxValidationCode_MVCC_READ_CONFLICT},
	)

	got := finality.SkipShadowedEnvelopes(b)

	require.Same(t, b, got)
	require.Equal(t, []string{"txA", "txB"}, txIDsOf(t, got))
}

// TestSkipShadowedEnvelopes_MultipleShadowed: several forgeries for one txID are all
// dropped, and an unrelated txID is preserved at its correct flag.
func TestSkipShadowedEnvelopes_MultipleShadowed(t *testing.T) {
	b := block(t, 7,
		[][]byte{
			endorserEnvelope(t, "tx1"), // forged
			endorserEnvelope(t, "tx2"), // honest, unrelated
			endorserEnvelope(t, "tx1"), // forged
			endorserEnvelope(t, "tx1"), // honest
		},
		[]peer.TxValidationCode{
			peer.TxValidationCode_BAD_PROPOSAL_TXID,
			peer.TxValidationCode_VALID,
			peer.TxValidationCode_DUPLICATE_TXID,
			peer.TxValidationCode_VALID,
		},
	)

	got := finality.SkipShadowedEnvelopes(b)

	require.Equal(t, []string{"tx2", "tx1"}, txIDsOf(t, got))
	require.Equal(t,
		[]byte{byte(peer.TxValidationCode_VALID), byte(peer.TxValidationCode_VALID)},
		got.Metadata.Metadata[common.BlockMetadataIndex_TRANSACTIONS_FILTER])
}

// TestSkipShadowedEnvelopes_ConfigNeverShadows: a config transaction is never
// dropped and never shadows an endorser envelope, even with a matching txID.
func TestSkipShadowedEnvelopes_ConfigNeverShadows(t *testing.T) {
	b := block(t, 7,
		[][]byte{configEnvelope(t, "tx1"), endorserEnvelope(t, "tx1")},
		[]peer.TxValidationCode{peer.TxValidationCode_VALID, peer.TxValidationCode_BAD_PROPOSAL_TXID},
	)

	got := finality.SkipShadowedEnvelopes(b)

	// The config VALID envelope must not shadow the endorser envelope: both survive.
	require.Same(t, b, got)
	require.Equal(t, []string{"tx1", "tx1"}, txIDsOf(t, got))
}

// TestSkipShadowedEnvelopes_NoFilter: a block carrying no transaction filter is
// returned unchanged rather than panicking.
func TestSkipShadowedEnvelopes_NoFilter(t *testing.T) {
	b := &common.Block{
		Header:   &common.BlockHeader{Number: 7},
		Data:     &common.BlockData{Data: [][]byte{endorserEnvelope(t, "tx1")}},
		Metadata: &common.BlockMetadata{Metadata: [][]byte{{}, {}}},
	}

	got := finality.SkipShadowedEnvelopes(b)
	require.Same(t, b, got)
}

func TestSkipShadowedEnvelopes_NilSafe(t *testing.T) {
	require.Nil(t, finality.SkipShadowedEnvelopes(nil))
	empty := &common.Block{}
	require.Same(t, empty, finality.SkipShadowedEnvelopes(empty))
}

// TestValidEnvelope covers the point-lookup counterpart: the index of the VALID
// envelope carrying a txID, or found=false when none does.
func TestValidEnvelope(t *testing.T) {
	b := block(t, 7,
		[][]byte{endorserEnvelope(t, "tx1"), endorserEnvelope(t, "tx1"), endorserEnvelope(t, "tx2")},
		[]peer.TxValidationCode{
			peer.TxValidationCode_BAD_PROPOSAL_TXID,
			peer.TxValidationCode_VALID,
			peer.TxValidationCode_MVCC_READ_CONFLICT,
		},
	)

	idx, ok := finality.ValidEnvelope(b, "tx1")
	require.True(t, ok)
	require.Equal(t, uint64(1), idx, "the VALID envelope for tx1 is at index 1")

	_, ok = finality.ValidEnvelope(b, "tx2")
	require.False(t, ok, "tx2 has no VALID envelope")

	_, ok = finality.ValidEnvelope(b, "absent")
	require.False(t, ok)
}

// fakeBlockNumberByTxID resolves txID -> block number, or an error.
type fakeBlockNumberByTxID struct {
	nums map[string]uint64
	err  error
}

func (f *fakeBlockNumberByTxID) GetBlockNumberByTxID(txID string) (uint64, error) {
	if f.err != nil {
		return 0, f.err
	}

	return f.nums[txID], nil
}

// fakeBlockScanner replays a single scripted block to the callback on ScanBlockFrom,
// recording the block number it was asked to start from.
type fakeBlockScanner struct {
	byNumber map[uint64]*common.Block
	started  uint64
	calls    int
	err      error
}

func (f *fakeBlockScanner) ScanBlockFrom(ctx context.Context, number uint64, callback fabric.BlockCallback) error {
	f.started = number
	f.calls++
	if f.err != nil {
		return f.err
	}
	b, ok := f.byNumber[number]
	if !ok {
		return nil
	}
	_, err := callback(ctx, b)

	return err
}

// TestHonestEnvelope_ForgedThenHonest: a point lookup for tx1 returned the forged
// BAD_PROPOSAL_TXID envelope; HonestEnvelope scans tx1's block and resolves the VALID
// one, at its own index, so a mapper can read the real token request.
func TestHonestEnvelope_ForgedThenHonest(t *testing.T) {
	b := block(t, 7,
		[][]byte{endorserEnvelope(t, "tx1"), endorserEnvelope(t, "tx1")},
		[]peer.TxValidationCode{peer.TxValidationCode_BAD_PROPOSAL_TXID, peer.TxValidationCode_VALID},
	)
	ledger := &fakeBlockNumberByTxID{nums: map[string]uint64{"tx1": 7}}
	scanner := &fakeBlockScanner{byNumber: map[uint64]*common.Block{7: b}}

	gotBlock, txNum, found, err := finality.HonestEnvelope(context.Background(), ledger, scanner, "tx1")

	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(1), txNum, "the honest envelope is at index 1")
	require.Same(t, b, gotBlock)
	require.Equal(t, uint64(7), scanner.started, "the scan must start at the transaction's own block")
	require.Equal(t, 1, scanner.calls, "exactly one block is scanned")
}

// TestHonestEnvelope_NoValidEnvelope: a block with only the invalid envelope yields
// found=false, so the caller keeps the point-lookup (Invalid) verdict.
func TestHonestEnvelope_NoValidEnvelope(t *testing.T) {
	b := block(t, 7,
		[][]byte{endorserEnvelope(t, "tx1")},
		[]peer.TxValidationCode{peer.TxValidationCode_MVCC_READ_CONFLICT},
	)
	ledger := &fakeBlockNumberByTxID{nums: map[string]uint64{"tx1": 7}}
	scanner := &fakeBlockScanner{byNumber: map[uint64]*common.Block{7: b}}

	_, _, found, err := finality.HonestEnvelope(context.Background(), ledger, scanner, "tx1")

	require.NoError(t, err)
	require.False(t, found)
}

// TestHonestEnvelope_BlockNumberError: a failure to resolve the block number is
// returned, so the caller falls back to the point-lookup verdict.
func TestHonestEnvelope_BlockNumberError(t *testing.T) {
	ledger := &fakeBlockNumberByTxID{err: errors.New("no such txID")}
	scanner := &fakeBlockScanner{}

	_, _, found, err := finality.HonestEnvelope(context.Background(), ledger, scanner, "tx1")

	require.Error(t, err)
	require.False(t, found)
	require.Equal(t, 0, scanner.calls, "no scan is attempted when the block number is unknown")
}

// TestHonestEnvelope_ScanError: a scan failure is reported as an error (fall back to
// the point-lookup verdict), not swallowed as "not found".
func TestHonestEnvelope_ScanError(t *testing.T) {
	ledger := &fakeBlockNumberByTxID{nums: map[string]uint64{"tx1": 7}}
	scanner := &fakeBlockScanner{err: errors.New("delivery failed")}

	_, _, found, err := finality.HonestEnvelope(context.Background(), ledger, scanner, "tx1")

	require.Error(t, err)
	require.False(t, found)
}
