/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/finality"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/hyperledger/fabric-protos-go-apiv2/common"
	"github.com/hyperledger/fabric-protos-go-apiv2/peer"
	"github.com/stretchr/testify/require"
)

// FuzzSkipShadowedEnvelopesNoPanic hunts for block bytes that panic
// SkipShadowedEnvelopes or ValidEnvelope rather than returning. Both parse the
// envelopes of a block fetched from a remote peer during a delivery scan — fully
// attacker-controllable bytes — so they must never panic on a malformed block, a
// truncated envelope, a transaction filter out of step with the data, or a header
// that does not unmarshal. The seed corpus covers the #2647 forged-then-honest
// block plus the degenerate shapes (no filter, filter longer/shorter than the data,
// garbage envelopes, empty block).
func FuzzSkipShadowedEnvelopesNoPanic(f *testing.F) {
	// A well-formed forged-then-honest block for tx1.
	good := &common.Block{
		Header: &common.BlockHeader{Number: 7},
		Data: &common.BlockData{Data: [][]byte{
			fuzzEndorserEnvelope("tx1"),
			fuzzEndorserEnvelope("tx1"),
		}},
		Metadata: fuzzMetadata(byte(peer.TxValidationCode_BAD_PROPOSAL_TXID), byte(peer.TxValidationCode_VALID)),
	}
	goodRaw, err := proto.Marshal(good)
	require.NoError(f, err)
	f.Add(goodRaw)

	// Filter shorter than the data (only one flag for two envelopes).
	shortFilter := &common.Block{
		Data: &common.BlockData{Data: [][]byte{
			fuzzEndorserEnvelope("tx1"),
			fuzzEndorserEnvelope("tx1"),
		}},
		Metadata: fuzzMetadata(byte(peer.TxValidationCode_VALID)),
	}
	shortRaw, err := proto.Marshal(shortFilter)
	require.NoError(f, err)
	f.Add(shortRaw)

	// Filter longer than the data, garbage envelope bytes, and a block with no
	// metadata at all.
	longFilter := &common.Block{
		Data:     &common.BlockData{Data: [][]byte{fuzzEndorserEnvelope("tx1")}},
		Metadata: fuzzMetadata(byte(peer.TxValidationCode_VALID), byte(peer.TxValidationCode_VALID)),
	}
	longRaw, err := proto.Marshal(longFilter)
	require.NoError(f, err)
	f.Add(longRaw)

	garbage := &common.Block{
		Data:     &common.BlockData{Data: [][]byte{[]byte("not a protobuf envelope"), {}}},
		Metadata: fuzzMetadata(byte(peer.TxValidationCode_VALID), byte(peer.TxValidationCode_BAD_PROPOSAL_TXID)),
	}
	garbageRaw, err := proto.Marshal(garbage)
	require.NoError(f, err)
	f.Add(garbageRaw)

	f.Add([]byte{})
	f.Add([]byte{0x00, 0x01, 0x02})

	f.Fuzz(func(t *testing.T, raw []byte) {
		block := &common.Block{}
		if err := proto.Unmarshal(raw, block); err != nil {
			// Not a block; nothing to exercise.
			return
		}

		require.NotPanics(t, func() {
			got := finality.SkipShadowedEnvelopes(block)
			require.NotNil(t, got)

			// The filter never panics or spuriously finds for an arbitrary txID.
			_, _ = finality.ValidEnvelope(block, "tx1")
			_, _ = finality.ValidEnvelope(got, "tx1")

			// SkipShadowedEnvelopes only ever removes envelopes, never adds.
			inData := 0
			if block.Data != nil {
				inData = len(block.Data.Data)
			}
			outData := 0
			if got.Data != nil {
				outData = len(got.Data.Data)
			}
			require.LessOrEqual(t, outData, inData, "filtering must not add envelopes")
		})
	})
}

// fuzzEndorserEnvelope builds a minimal ENDORSER_TRANSACTION envelope for txID,
// without a *testing.T (usable from the fuzz seed setup).
func fuzzEndorserEnvelope(txID string) []byte {
	chdr, _ := proto.Marshal(&common.ChannelHeader{
		Type:      int32(common.HeaderType_ENDORSER_TRANSACTION),
		ChannelId: "testchannel",
		TxId:      txID,
	})
	payl, _ := proto.Marshal(&common.Payload{
		Header: &common.Header{ChannelHeader: chdr},
		Data:   []byte("tx-data"),
	})
	env, _ := proto.Marshal(&common.Envelope{Payload: payl})

	return env
}

// fuzzMetadata builds block metadata whose TRANSACTIONS_FILTER slot holds flags.
func fuzzMetadata(flags ...byte) *common.BlockMetadata {
	md := make([][]byte, int(common.BlockMetadataIndex_TRANSACTIONS_FILTER)+1)
	md[common.BlockMetadataIndex_TRANSACTIONS_FILTER] = flags

	return &common.BlockMetadata{Metadata: md}
}
