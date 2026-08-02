/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality_test

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/finality"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/hyperledger/fabric-protos-go-apiv2/common"
	"github.com/hyperledger/fabric-protos-go-apiv2/ledger/rwset"
	"github.com/hyperledger/fabric-protos-go-apiv2/ledger/rwset/kvrwset"
	"github.com/hyperledger/fabric-protos-go-apiv2/msp"
	"github.com/hyperledger/fabric-protos-go-apiv2/peer"
	"github.com/stretchr/testify/require"
)

// buildEndorserTx assembles a minimal but well-formed ENDORSER_TRANSACTION
// envelope that passes UnmarshalTx and rwset.Read, so that MapTxData proceeds
// to the transaction-filter checks that this test exercises.
func buildEndorserTx(t *testing.T) []byte {
	t.Helper()

	// Minimal but non-empty read-write set (a single namespace with no keys).
	// It must be non-empty so its bytes survive the proto round-trip and the
	// enclosing ProposalResponsePayload.Extension is not dropped as a zero value.
	nsRWSet, err := proto.Marshal(&kvrwset.KVRWSet{})
	require.NoError(t, err)
	rwsetBytes, err := proto.Marshal(&rwset.TxReadWriteSet{
		DataModel: rwset.TxReadWriteSet_KV,
		NsRwset: []*rwset.NsReadWriteSet{
			{Namespace: "tns", Rwset: nsRWSet},
		},
	})
	require.NoError(t, err)

	ccAction := &peer.ChaincodeAction{Results: rwsetBytes}
	ccActionBytes, err := proto.Marshal(ccAction)
	require.NoError(t, err)

	respPayload := &peer.ProposalResponsePayload{Extension: ccActionBytes}
	respPayloadBytes, err := proto.Marshal(respPayload)
	require.NoError(t, err)

	ccInvSpec := &peer.ChaincodeInvocationSpec{
		ChaincodeSpec: &peer.ChaincodeSpec{
			ChaincodeId: &peer.ChaincodeID{Name: "tcc", Version: "1.0"},
			Input:       &peer.ChaincodeInput{Args: [][]byte{[]byte("invoke")}},
		},
	}
	ccInvSpecBytes, err := proto.Marshal(ccInvSpec)
	require.NoError(t, err)

	ccProposalPayload := &peer.ChaincodeProposalPayload{Input: ccInvSpecBytes}
	ccProposalPayloadBytes, err := proto.Marshal(ccProposalPayload)
	require.NoError(t, err)

	ccActionPayload := &peer.ChaincodeActionPayload{
		ChaincodeProposalPayload: ccProposalPayloadBytes,
		Action: &peer.ChaincodeEndorsedAction{
			ProposalResponsePayload: respPayloadBytes,
			Endorsements:            []*peer.Endorsement{},
		},
	}
	ccActionPayloadBytes, err := proto.Marshal(ccActionPayload)
	require.NoError(t, err)

	sigHdr := &common.SignatureHeader{
		Creator: mustMarshal(t, &msp.SerializedIdentity{Mspid: "Org1MSP", IdBytes: []byte("cert")}),
		Nonce:   []byte("nonce"),
	}
	sigHdrBytes := mustMarshal(t, sigHdr)

	tx := &peer.Transaction{
		Actions: []*peer.TransactionAction{
			{Header: sigHdrBytes, Payload: ccActionPayloadBytes},
		},
	}
	txBytes, err := proto.Marshal(tx)
	require.NoError(t, err)

	chdr := &common.ChannelHeader{
		Type:      int32(common.HeaderType_ENDORSER_TRANSACTION),
		ChannelId: "testchannel",
		TxId:      "tx1",
	}
	payload := &common.Payload{
		Header: &common.Header{
			ChannelHeader:   mustMarshal(t, chdr),
			SignatureHeader: sigHdrBytes,
		},
		Data: txBytes,
	}
	envRaw, err := proto.Marshal(&common.Envelope{Payload: mustMarshal(t, payload)})
	require.NoError(t, err)

	return envRaw
}

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	require.NoError(t, err)

	return b
}

// TestMapTxData_MissingTransactionFilter verifies bug #1: when the block
// metadata does not include the TRANSACTIONS_FILTER slot (its length is exactly
// equal to the filter index, so the index is out of range), MapTxData returns an
// error instead of panicking with an index-out-of-range. This is the fix that
// changed the guard from `<` to `<=`.
func TestMapTxData_MissingTransactionFilter(t *testing.T) {
	mapper := &finality.EndorserTxInfoMapper{Network: "testnet"}
	tx := buildEndorserTx(t)

	// Metadata length is 2 (indices 0 and 1); TRANSACTIONS_FILTER is index 2, so it is absent.
	block := &common.BlockMetadata{Metadata: [][]byte{{}, {}}}

	_, err := mapper.MapTxData(context.Background(), tx, block, 0, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "block metadata lacks transaction filter")
}

// TestMapTxData_TxFilterTooShort verifies bug #2: when the TRANSACTIONS_FILTER
// is present but shorter than txNum, MapTxData returns an error instead of
// panicking with an index-out-of-range when indexing txFilter[txNum].
func TestMapTxData_TxFilterTooShort(t *testing.T) {
	mapper := &finality.EndorserTxInfoMapper{Network: "testnet"}
	tx := buildEndorserTx(t)

	// TRANSACTIONS_FILTER (index 2) present with a single byte, but txNum=5 is out of range.
	block := &common.BlockMetadata{Metadata: [][]byte{{}, {}, {0x00}}}

	_, err := mapper.MapTxData(context.Background(), tx, block, 0, 5)
	require.Error(t, err)
	require.Contains(t, err.Error(), "transaction filter too short")
}

// TestMapTxData_ValidFilter verifies the happy path: a well-formed block with a
// transaction filter long enough for txNum is mapped without error.
func TestMapTxData_ValidFilter(t *testing.T) {
	mapper := &finality.EndorserTxInfoMapper{Network: "testnet", KeyTranslator: &keys.Translator{}}
	tx := buildEndorserTx(t)

	// TRANSACTIONS_FILTER present, txNum=0 in range with a VALID (0x00) validation code.
	block := &common.BlockMetadata{Metadata: [][]byte{{}, {}, {0x00}}}

	_, err := mapper.MapTxData(context.Background(), tx, block, 0, 0)
	require.NoError(t, err)
}
